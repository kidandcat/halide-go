package halide

import (
	"context"
	"encoding/binary"
	"fmt"
	"unicode/utf8"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// wasmState runs one Halide module. Halide's wasm runtime imports a small libc
// from module "env"; those functions are implemented here.
type wasmState struct {
	ctx    context.Context
	rt     wazero.Runtime
	mod    api.Module
	heap   wasmHeap
	stderr []byte
}

type wasmHeap struct {
	brk   uint32
	ready bool
	spans []wasmSpan
}

type wasmSpan struct {
	addr uint32
	size uint32
}

func newWASM(module []byte) (*wasmState, error) {
	if len(module) == 0 {
		return nil, fmt.Errorf("halide: empty wasm module")
	}
	ctx := context.Background()
	rt := wazero.NewRuntime(ctx)
	st := &wasmState{ctx: ctx, rt: rt}
	if _, err := st.hostModule(); err != nil {
		rt.Close(ctx)
		return nil, err
	}
	mod, err := rt.Instantiate(ctx, module)
	if err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("halide: instantiate wasm: %w", err)
	}
	st.mod = mod
	st.heap.init(mod)
	return st, nil
}

func (w *wasmState) close() error {
	if w == nil || w.rt == nil {
		return nil
	}
	err := w.rt.Close(w.ctx)
	w.rt = nil
	w.mod = nil
	return err
}

func (w *wasmState) bind(lib *Library, name string) (*Func, error) {
	if w.mod.ExportedFunction(name+"_argv") == nil && w.mod.ExportedFunction(name) == nil {
		return nil, fmt.Errorf("halide: wasm export %s not found", name)
	}
	f := &Func{lib: lib, name: name}
	if sig, err := w.metadata(name); err == nil {
		f.sig = sig
	}
	return f, nil
}

func (w *wasmState) call(f *Func, args []any) error {
	w.stderr = w.stderr[:0]
	fn := w.mod.ExportedFunction(f.name + "_argv")
	if fn == nil {
		return fmt.Errorf("halide: wasm export %s_argv not found", f.name)
	}
	ptrs := make([]uint32, len(args))
	var temps []uint32
	defer func() {
		for _, p := range temps {
			w.heap.free(w.mod, p)
		}
	}()
	type copied struct {
		buf *Buffer
		at  uint32
		n   int
	}
	var outs []copied
	for i, arg := range args {
		switch v := arg.(type) {
		case *Buffer:
			p, host, n, extra, err := w.putBuffer(v)
			if err != nil {
				return err
			}
			temps = append(temps, extra...)
			ptrs[i] = p
			isOut := true
			if i < len(f.sig) {
				isOut = f.sig[i].Kind == KindOutputBuffer
			}
			if isOut {
				outs = append(outs, copied{buf: v, at: host, n: n})
			}
		default:
			var slot scalarSlot
			if err := encodeScalar(&slot, inferType(f, i, arg), arg); err != nil {
				return fmt.Errorf("halide: %s: %w", f.name, err)
			}
			p := w.heap.malloc(w.mod, 8)
			if p == 0 {
				return fmt.Errorf("halide: wasm malloc failed")
			}
			temps = append(temps, p)
			var raw [8]byte
			binary.LittleEndian.PutUint64(raw[:], slot.v)
			if !w.mod.Memory().Write(p, raw[:]) {
				return fmt.Errorf("halide: wasm write failed")
			}
			ptrs[i] = p
		}
	}
	argsPtr := w.heap.malloc(w.mod, uint32(4*len(ptrs)+4))
	if argsPtr == 0 {
		return fmt.Errorf("halide: wasm malloc failed")
	}
	temps = append(temps, argsPtr)
	raw := make([]byte, 4*len(ptrs))
	for i, p := range ptrs {
		binary.LittleEndian.PutUint32(raw[i*4:], p)
	}
	if len(raw) > 0 && !w.mod.Memory().Write(argsPtr, raw) {
		return fmt.Errorf("halide: wasm write failed")
	}
	res, err := fn.Call(w.ctx, uint64(argsPtr))
	if err != nil {
		if text := string(trimNL(w.stderr)); text != "" {
			return &PipelineError{Func: f.name, Code: -1, Text: text}
		}
		return fmt.Errorf("halide: %s: %w", f.name, err)
	}
	if len(res) != 1 {
		return fmt.Errorf("halide: %s: wasm returned %d values", f.name, len(res))
	}
	rc := int32(res[0])
	if rc != 0 {
		return &PipelineError{Func: f.name, Code: rc, Text: string(trimNL(w.stderr))}
	}
	for _, o := range outs {
		if o.n == 0 {
			continue
		}
		b, ok := w.mod.Memory().Read(o.at, uint32(o.n))
		if !ok {
			return fmt.Errorf("halide: wasm read of output failed")
		}
		copy(o.buf.bytes, b)
	}
	return nil
}

func (w *wasmState) putBuffer(b *Buffer) (bufPtr, host uint32, n int, temps []uint32, err error) {
	b.sync()
	n = len(b.bytes)
	if n > 0 {
		host = w.heap.malloc(w.mod, uint32(n))
		if host == 0 {
			return 0, 0, 0, nil, fmt.Errorf("halide: wasm malloc failed")
		}
		temps = append(temps, host)
		if !w.mod.Memory().Write(host, b.bytes) {
			return 0, 0, 0, temps, fmt.Errorf("halide: wasm write of buffer failed")
		}
	}
	dimBytes := 16 * len(b.dims)
	var dimPtr uint32
	if dimBytes > 0 {
		dimPtr = w.heap.malloc(w.mod, uint32(dimBytes))
		if dimPtr == 0 {
			return 0, 0, 0, temps, fmt.Errorf("halide: wasm malloc failed")
		}
		temps = append(temps, dimPtr)
		raw := make([]byte, dimBytes)
		for i, d := range b.dims {
			off := i * 16
			binary.LittleEndian.PutUint32(raw[off:], uint32(d.Min))
			binary.LittleEndian.PutUint32(raw[off+4:], uint32(d.Extent))
			binary.LittleEndian.PutUint32(raw[off+8:], uint32(d.Stride))
			binary.LittleEndian.PutUint32(raw[off+12:], d.Flags)
		}
		if !w.mod.Memory().Write(dimPtr, raw) {
			return 0, 0, 0, temps, fmt.Errorf("halide: wasm write of shape failed")
		}
	}
	bufPtr = w.heap.malloc(w.mod, 40)
	if bufPtr == 0 {
		return 0, 0, 0, temps, fmt.Errorf("halide: wasm malloc failed")
	}
	temps = append(temps, bufPtr)
	raw := make([]byte, 40)
	binary.LittleEndian.PutUint32(raw[12:], host)
	binary.LittleEndian.PutUint64(raw[16:], flagHostDirty)
	raw[24] = uint8(b.typ.Code)
	raw[25] = b.typ.Bits
	binary.LittleEndian.PutUint16(raw[26:], b.typ.Lanes)
	binary.LittleEndian.PutUint32(raw[28:], uint32(len(b.dims)))
	binary.LittleEndian.PutUint32(raw[32:], dimPtr)
	if !w.mod.Memory().Write(bufPtr, raw) {
		return 0, 0, 0, temps, fmt.Errorf("halide: wasm write of halide_buffer_t failed")
	}
	return bufPtr, host, n, temps, nil
}

func (w *wasmState) metadata(name string) ([]Arg, error) {
	fn := w.mod.ExportedFunction(name + "_metadata")
	if fn == nil {
		return nil, fmt.Errorf("halide: no %s_metadata", name)
	}
	res, err := fn.Call(w.ctx)
	if err != nil {
		return nil, err
	}
	if len(res) != 1 || res[0] == 0 {
		return nil, fmt.Errorf("halide: nil wasm metadata")
	}
	p := uint32(res[0])
	mem := w.mod.Memory()
	ver, ok := mem.ReadUint32Le(p)
	if !ok {
		return nil, fmt.Errorf("halide: short wasm metadata")
	}
	if ver != 1 {
		return nil, fmt.Errorf("halide: wasm metadata version %d", ver)
	}
	num, _ := mem.ReadUint32Le(p + 4)
	args, _ := mem.ReadUint32Le(p + 8)
	if num > 64 {
		return nil, fmt.Errorf("halide: wasm metadata has %d arguments", num)
	}
	out := make([]Arg, num)
	const stride = 36
	for i := uint32(0); i < num; i++ {
		base := args + i*stride
		namePtr, _ := mem.ReadUint32Le(base)
		kind, _ := mem.ReadUint32Le(base + 4)
		dims, _ := mem.ReadUint32Le(base + 8)
		tb, ok := mem.Read(base+12, 4)
		if !ok {
			return nil, fmt.Errorf("halide: short wasm argument")
		}
		out[i] = Arg{
			Name:       w.cstr(namePtr),
			Kind:       Kind(kind),
			Dimensions: int(dims),
			Type:       Type{Code: TypeCode(tb[0]), Bits: tb[1], Lanes: binary.LittleEndian.Uint16(tb[2:])},
		}
	}
	return out, nil
}

func (h *wasmHeap) init(m api.Module) {
	if h.ready {
		return
	}
	if g := m.ExportedGlobal("__heap_base"); g != nil {
		h.brk = uint32(g.Get())
	}
	h.ready = true
}

func (h *wasmHeap) malloc(m api.Module, n uint32) uint32 {
	h.init(m)
	if n == 0 {
		n = 1
	}
	n = (n + 127) &^ 127
	for i, b := range h.spans {
		if b.size >= n {
			h.spans = append(h.spans[:i], h.spans[i+1:]...)
			return b.addr
		}
	}
	user := ((h.brk + 127) &^ 127) + 128
	end := user + n
	mem := m.Memory()
	if end > mem.Size() {
		pages := (end - mem.Size() + 65535) / 65536
		if pages < 1 {
			pages = 1
		}
		if _, ok := mem.Grow(pages); !ok {
			return 0
		}
	}
	var sz [4]byte
	binary.LittleEndian.PutUint32(sz[:], n)
	if !mem.Write(user-4, sz[:]) {
		return 0
	}
	h.brk = end
	return user
}

func (h *wasmHeap) free(m api.Module, p uint32) {
	if p < 4 {
		return
	}
	b, ok := m.Memory().Read(p-4, 4)
	if !ok {
		return
	}
	n := binary.LittleEndian.Uint32(b)
	if n == 0 {
		return
	}
	h.spans = append(h.spans, wasmSpan{addr: p, size: n})
}

func (w *wasmState) hostModule() (api.Module, error) {
	return w.rt.NewHostModuleBuilder("env").
		NewFunctionBuilder().WithFunc(func(ctx context.Context, m api.Module, n uint32) uint32 {
		return w.heap.malloc(m, n)
	}).Export("malloc").
		NewFunctionBuilder().WithFunc(func(ctx context.Context, m api.Module, p uint32) {
		w.heap.free(m, p)
	}).Export("free").
		NewFunctionBuilder().WithFunc(func(ctx context.Context, m api.Module, p uint32) uint32 {
		return uint32(len(cstrBytes(m, p)))
	}).Export("strlen").
		NewFunctionBuilder().WithFunc(func() {
		panic("halide wasm abort")
	}).Export("abort").
		NewFunctionBuilder().WithFunc(func(ctx context.Context, m api.Module, tv, tz uint32) int32 {
		m.Memory().Write(tv, make([]byte, 8))
		return 0
	}).Export("gettimeofday").
		NewFunctionBuilder().WithFunc(func(uint32) int32 { return 0 }).Export("usleep").
		NewFunctionBuilder().WithFunc(func(ctx context.Context, m api.Module, fd, buf, n uint32) int32 {
		b, ok := m.Memory().Read(buf, n)
		if !ok {
			return -1
		}
		if fd == 1 || fd == 2 {
			w.stderr = append(w.stderr, b...)
		}
		return int32(n)
	}).Export("write").
		NewFunctionBuilder().WithFunc(func(int32) int32 { return 1 }).Export("sysconf").
		NewFunctionBuilder().WithFunc(func() int32 { return 0 }).Export("sched_yield").
		NewFunctionBuilder().WithFunc(func(uint32) uint32 { return 0 }).Export("getenv").
		NewFunctionBuilder().WithFunc(func(ctx context.Context, m api.Module, p uint32) int32 {
		return atoi(cstrBytes(m, p))
	}).Export("atoi").
		NewFunctionBuilder().WithFunc(func(ctx context.Context, m api.Module, dst, src, n uint32) uint32 {
		if n == 0 {
			return dst
		}
		b, ok := m.Memory().Read(src, n)
		if !ok {
			return dst
		}
		cp := append([]byte(nil), b...)
		m.Memory().Write(dst, cp)
		return dst
	}).Export("memcpy").
		NewFunctionBuilder().WithFunc(func(ctx context.Context, m api.Module, dst, val, n uint32) uint32 {
		if n == 0 {
			return dst
		}
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(val)
		}
		m.Memory().Write(dst, b)
		return dst
	}).Export("memset").
		NewFunctionBuilder().WithFunc(func(ctx context.Context, m api.Module, a, b, n uint32) int32 {
		x, ok1 := m.Memory().Read(a, n)
		y, ok2 := m.Memory().Read(b, n)
		if !ok1 || !ok2 {
			return -1
		}
		x = append([]byte(nil), x...)
		for i := range x {
			if x[i] != y[i] {
				return int32(x[i]) - int32(y[i])
			}
		}
		return 0
	}).Export("memcmp").
		NewFunctionBuilder().WithFunc(func(ctx context.Context, m api.Module, a, b uint32) int32 {
		return strcmp(cstrBytes(m, a), cstrBytes(m, b))
	}).Export("strcmp").
		NewFunctionBuilder().WithFunc(func(ctx context.Context, m api.Module, h, n uint32) uint32 {
		hs, ns := cstrBytes(m, h), cstrBytes(m, n)
		if len(ns) == 0 {
			return h
		}
		for i := 0; i+len(ns) <= len(hs); i++ {
			if string(hs[i:i+len(ns)]) == string(ns) {
				return h + uint32(i)
			}
		}
		return 0
	}).Export("strstr").
		NewFunctionBuilder().WithFunc(func(uint32, uint32) uint32 { return 0 }).Export("fopen").
		NewFunctionBuilder().WithFunc(func(uint32) int32 { return -1 }).Export("fileno").
		NewFunctionBuilder().WithFunc(func(uint32) int32 { return 0 }).Export("fclose").
		NewFunctionBuilder().WithFunc(func(uint32, uint32, uint32, uint32) uint32 { return 0 }).Export("fwrite").
		NewFunctionBuilder().WithFunc(func(uint32, uint32, uint32) int32 { return 0 }).Export("__cxa_atexit").
		Instantiate(w.ctx)
}

func (w *wasmState) cstr(p uint32) string {
	return string(cstrBytes(w.mod, p))
}

func cstrBytes(m api.Module, p uint32) []byte {
	if p == 0 || m == nil || m.Memory() == nil {
		return nil
	}
	mem := m.Memory()
	size := mem.Size()
	if p >= size {
		return nil
	}
	n := size - p
	if n > 4096 {
		n = 4096
	}
	b, ok := mem.Read(p, n)
	if !ok {
		return nil
	}
	for i, c := range b {
		if c == 0 {
			return append([]byte(nil), b[:i]...)
		}
		if i == 4096 {
			return append([]byte(nil), b[:i]...)
		}
	}
	return nil
}

func strcmp(a, b []byte) int32 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return int32(a[i]) - int32(b[i])
		}
	}
	return int32(len(a) - len(b))
}

func atoi(b []byte) int32 {
	i := 0
	for i < len(b) && (b[i] == ' ' || b[i] == '\t') {
		i++
	}
	sign := int32(1)
	if i < len(b) && (b[i] == '-' || b[i] == '+') {
		if b[i] == '-' {
			sign = -1
		}
		i++
	}
	var n int32
	for i < len(b) && b[i] >= '0' && b[i] <= '9' {
		n = n*10 + int32(b[i]-'0')
		i++
	}
	return sign * n
}

func trimNL(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r' || b[len(b)-1] == ' ') {
		b = b[:len(b)-1]
	}
	if !utf8.Valid(b) {
		return []byte(fmt.Sprintf("%q", b))
	}
	return b
}
