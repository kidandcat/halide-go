package halide

import (
	"fmt"
	"math"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Library is a loaded Halide pipeline library, either a native shared library
// or a wasm module. Calls on one Library are serialized. Close shuts down
// Halide's thread pool (native) and releases the module.
type Library struct {
	name   string
	mu     sync.Mutex
	handle uintptr
	wasm   *wasmState

	shutdown func()
	callback uintptr
	errN     atomic.Int32
	errBuf   [2048]byte
}

// Func is one exported pipeline inside a Library.
type Func struct {
	lib  *Library
	name string
	sig  []Arg
	argv func(*uintptr) int32
}

// Open loads a native shared library from path.
func Open(path string) (*Library, error) {
	if unsafe.Sizeof(halideBuffer{}) != 56 {
		return nil, fmt.Errorf("halide: native ABI requires a 56-byte halide_buffer_t (64-bit), have %d", unsafe.Sizeof(halideBuffer{}))
	}
	h, err := loadShared(path)
	if err != nil {
		return nil, fmt.Errorf("halide: open %s: %w", path, err)
	}
	l := &Library{name: filepath.Base(path), handle: h}
	l.installHandler()
	return l, nil
}

// OpenWASM instantiates a Halide wasm module compiled for
// wasm-32-wasmrt-wasm_simd128.
func OpenWASM(module []byte) (*Library, error) {
	st, err := newWASM(module)
	if err != nil {
		return nil, err
	}
	return &Library{name: "wasm", wasm: st}, nil
}

// Close releases the library. It is safe to call more than once.
func (l *Library) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.wasm != nil {
		err := l.wasm.close()
		l.wasm = nil
		return err
	}
	if l.handle == 0 {
		return nil
	}
	if l.shutdown != nil {
		l.shutdown()
		l.shutdown = nil
	}
	err := closeShared(l.handle)
	l.handle = 0
	return err
}

// Func looks up name_argv (and name_metadata when it is present).
func (l *Library) Func(name string) (*Func, error) {
	if l == nil {
		return nil, fmt.Errorf("halide: nil library")
	}
	if name == "" {
		return nil, fmt.Errorf("halide: empty function name")
	}
	if l.wasm != nil {
		return l.wasm.bind(l, name)
	}
	if l.handle == 0 {
		return nil, fmt.Errorf("halide: library is closed")
	}
	sym, err := lookupShared(l.handle, name+"_argv")
	if err != nil {
		return nil, fmt.Errorf("halide: %s: %w", name, err)
	}
	var argv func(*uintptr) int32
	purego.RegisterFunc(&argv, sym)
	f := &Func{lib: l, name: name, argv: argv}
	if sig, err := l.nativeMetadata(name); err == nil {
		f.sig = sig
	}
	return f, nil
}

// Name is the Halide function name.
func (f *Func) Name() string {
	if f == nil {
		return ""
	}
	return f.name
}

// Signature is the filter metadata. It is nil when the library did not
// export name_metadata.
func (f *Func) Signature() []Arg {
	if f == nil || len(f.sig) == 0 {
		return nil
	}
	out := make([]Arg, len(f.sig))
	copy(out, f.sig)
	return out
}

// Call runs the pipeline. Arguments follow the Halide calling convention:
// a buffer argument is a [*Buffer], a scalar is a Go value of the metadata
// type (int32, float32, bool, and the other fixed-width numeric types).
//
// Every buffer's backing memory, shape, and halide_buffer_t are pinned until
// Call returns. On success, output buffers have been written in place.
func (f *Func) Call(args ...any) error {
	if f == nil || f.lib == nil {
		return fmt.Errorf("halide: nil function")
	}
	if len(f.sig) > 0 && len(args) != len(f.sig) {
		return fmt.Errorf("halide: %s: got %d arguments, metadata lists %d", f.name, len(args), len(f.sig))
	}
	for i, arg := range args {
		var spec *Arg
		if i < len(f.sig) {
			spec = &f.sig[i]
		}
		if err := checkArg(f.name, spec, arg); err != nil {
			return err
		}
	}
	if f.lib.wasm != nil {
		return f.lib.callWASM(f, args)
	}
	return f.lib.callNative(f, args)
}

func (l *Library) callNative(f *Func, args []any) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.handle == 0 {
		return fmt.Errorf("halide: library is closed")
	}
	l.errN.Store(0)

	slots := make([]uintptr, len(args))
	boxes := make([]scalarSlot, len(args))
	var pinner runtime.Pinner
	defer pinner.Unpin()

	for i, arg := range args {
		switch v := arg.(type) {
		case *Buffer:
			v.sync()
			pinner.Pin(&v.raw)
			if len(v.dims) > 0 {
				pinner.Pin(&v.dims[0])
			}
			if len(v.bytes) > 0 {
				pinner.Pin(&v.bytes[0])
			}
			slots[i] = uintptr(unsafe.Pointer(&v.raw))
		default:
			t := inferType(f, i, arg)
			if err := encodeScalar(&boxes[i], t, arg); err != nil {
				return fmt.Errorf("halide: %s: %w", f.name, err)
			}
			slots[i] = uintptr(unsafe.Pointer(&boxes[i]))
		}
	}
	var rc int32
	if len(slots) == 0 {
		rc = f.argv(nil)
	} else {
		pinner.Pin(&slots[0])
		rc = f.argv(&slots[0])
	}
	runtime.KeepAlive(args)
	runtime.KeepAlive(boxes)
	runtime.KeepAlive(slots)
	if rc != 0 {
		return &PipelineError{Func: f.name, Code: rc, Text: l.errText()}
	}
	return nil
}

func (l *Library) callWASM(f *Func, args []any) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.wasm == nil {
		return fmt.Errorf("halide: library is closed")
	}
	return l.wasm.call(f, args)
}

type scalarSlot struct {
	v uint64
}

func (l *Library) installHandler() {
	if sym, err := lookupShared(l.handle, "halide_shutdown_thread_pool"); err == nil {
		var shutdown func()
		purego.RegisterFunc(&shutdown, sym)
		l.shutdown = shutdown
	}
	sym, err := lookupShared(l.handle, "halide_set_error_handler")
	if err != nil {
		return
	}
	var set func(uintptr)
	purego.RegisterFunc(&set, sym)
	l.callback = purego.NewCallback(func(user, msg uintptr) uintptr {
		l.capture(msg)
		return 0
	})
	set(l.callback)
}

func (l *Library) capture(msg uintptr) {
	if msg == 0 {
		l.errN.Store(0)
		return
	}
	n := 0
	for n < len(l.errBuf) {
		b := *(*byte)(unsafe.Pointer(msg + uintptr(n)))
		if b == 0 {
			break
		}
		l.errBuf[n] = b
		n++
	}
	l.errN.Store(int32(n))
}

func (l *Library) errText() string {
	n := int(l.errN.Load())
	if n <= 0 {
		return ""
	}
	if n > len(l.errBuf) {
		n = len(l.errBuf)
	}
	return string(l.errBuf[:n])
}

func (l *Library) nativeMetadata(name string) ([]Arg, error) {
	sym, err := lookupShared(l.handle, name+"_metadata")
	if err != nil {
		return nil, err
	}
	var fn func() uintptr
	purego.RegisterFunc(&fn, sym)
	return parseNativeMetadata(fn())
}

func parseNativeMetadata(ptr uintptr) ([]Arg, error) {
	if ptr == 0 {
		return nil, fmt.Errorf("halide: nil metadata")
	}
	version := *(*int32)(unsafe.Pointer(ptr))
	num := *(*int32)(unsafe.Pointer(ptr + 4))
	argsPtr := *(*uintptr)(unsafe.Pointer(ptr + 8))
	if version != 1 {
		return nil, fmt.Errorf("halide: metadata version %d", version)
	}
	if num < 0 || num > 64 {
		return nil, fmt.Errorf("halide: metadata has %d arguments", num)
	}
	if num > 0 && argsPtr == 0 {
		return nil, fmt.Errorf("halide: metadata arguments pointer is nil")
	}
	out := make([]Arg, num)
	const stride = 64
	for i := int32(0); i < num; i++ {
		base := argsPtr + uintptr(i)*stride
		namePtr := *(*uintptr)(unsafe.Pointer(base))
		kind := *(*int32)(unsafe.Pointer(base + 8))
		dims := *(*int32)(unsafe.Pointer(base + 12))
		code := *(*uint8)(unsafe.Pointer(base + 16))
		bits := *(*uint8)(unsafe.Pointer(base + 17))
		lanes := *(*uint16)(unsafe.Pointer(base + 18))
		out[i] = Arg{
			Name:       readCString(namePtr),
			Kind:       Kind(kind),
			Dimensions: int(dims),
			Type:       Type{Code: TypeCode(code), Bits: bits, Lanes: lanes},
		}
	}
	return out, nil
}

func readCString(ptr uintptr) string {
	if ptr == 0 {
		return ""
	}
	const max = 1024
	buf := make([]byte, 0, 32)
	for i := uintptr(0); i < max; i++ {
		b := *(*byte)(unsafe.Pointer(ptr + i))
		if b == 0 {
			break
		}
		buf = append(buf, b)
	}
	return string(buf)
}

func checkArg(fn string, spec *Arg, arg any) error {
	if spec == nil {
		switch arg.(type) {
		case *Buffer, bool,
			int, int8, int16, int32, int64,
			uint, uint8, uint16, uint32, uint64,
			float32, float64:
			if _, ok := arg.(*Buffer); ok && arg.(*Buffer) == nil {
				return fmt.Errorf("halide: %s: nil buffer", fn)
			}
			return nil
		default:
			return fmt.Errorf("halide: %s: unsupported argument %T", fn, arg)
		}
	}
	label := spec.Name
	if label == "" {
		label = spec.Kind.String()
	}
	switch spec.Kind {
	case KindInputBuffer, KindOutputBuffer:
		b, ok := arg.(*Buffer)
		if !ok {
			return fmt.Errorf("halide: %s: argument %s is a buffer, got %T", fn, label, arg)
		}
		if b == nil {
			return fmt.Errorf("halide: %s: argument %s is a nil buffer", fn, label)
		}
		if spec.Dimensions > 0 && len(b.dims) != spec.Dimensions {
			return fmt.Errorf("halide: %s: argument %s wants %d dimensions, have %d", fn, label, spec.Dimensions, len(b.dims))
		}
		if spec.Type.Bits != 0 && !b.typ.Equal(spec.Type) {
			return fmt.Errorf("halide: %s: argument %s wants %s, have %s", fn, label, spec.Type, b.typ)
		}
		return nil
	case KindInputScalar:
		if !scalarMatches(spec.Type, arg) {
			return fmt.Errorf("halide: %s: argument %s wants %s, got %T", fn, label, spec.Type, arg)
		}
		return nil
	default:
		return fmt.Errorf("halide: %s: argument %s has unknown kind %d", fn, label, spec.Kind)
	}
}

func scalarMatches(t Type, v any) bool {
	switch t {
	case Int8:
		_, ok := v.(int8)
		return ok
	case Int16:
		_, ok := v.(int16)
		return ok
	case Int32:
		_, ok := v.(int32)
		return ok
	case Int64:
		_, ok := v.(int64)
		return ok
	case UInt8:
		_, ok := v.(uint8)
		return ok
	case UInt16:
		_, ok := v.(uint16)
		return ok
	case UInt32:
		_, ok := v.(uint32)
		return ok
	case UInt64:
		_, ok := v.(uint64)
		return ok
	case Float32:
		_, ok := v.(float32)
		return ok
	case Float64:
		_, ok := v.(float64)
		return ok
	case Bool:
		_, ok := v.(bool)
		return ok
	default:
		return false
	}
}

func inferType(f *Func, i int, arg any) Type {
	if f != nil && i < len(f.sig) && f.sig[i].Kind == KindInputScalar {
		return f.sig[i].Type
	}
	switch arg.(type) {
	case int8:
		return Int8
	case int16:
		return Int16
	case int32:
		return Int32
	case int64:
		return Int64
	case uint8:
		return UInt8
	case uint16:
		return UInt16
	case uint32:
		return UInt32
	case uint64:
		return UInt64
	case float32:
		return Float32
	case float64:
		return Float64
	case bool:
		return Bool
	case int:
		if unsafe.Sizeof(int(0)) == 8 {
			return Int64
		}
		return Int32
	case uint:
		if unsafe.Sizeof(uint(0)) == 8 {
			return UInt64
		}
		return UInt32
	default:
		return Type{}
	}
}

func encodeScalar(dst *scalarSlot, t Type, v any) error {
	dst.v = 0
	switch t {
	case Int8:
		dst.v = uint64(uint8(v.(int8)))
	case Int16:
		dst.v = uint64(uint16(v.(int16)))
	case Int32:
		dst.v = uint64(uint32(v.(int32)))
	case Int64:
		dst.v = uint64(v.(int64))
	case UInt8:
		dst.v = uint64(v.(uint8))
	case UInt16:
		dst.v = uint64(v.(uint16))
	case UInt32:
		dst.v = uint64(v.(uint32))
	case UInt64:
		dst.v = v.(uint64)
	case Float32:
		dst.v = uint64(math.Float32bits(v.(float32)))
	case Float64:
		dst.v = math.Float64bits(v.(float64))
	case Bool:
		if v.(bool) {
			dst.v = 1
		}
	default:
		return fmt.Errorf("cannot encode scalar %s from %T", t, v)
	}
	return nil
}
