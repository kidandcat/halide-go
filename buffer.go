package halide

import (
	"fmt"
	"image"
	"unsafe"
)

// halide_buffer_flag_host_dirty. CPU pipelines ignore it. A later GPU
// pipeline treats host memory as the copy that must be uploaded.
const flagHostDirty uint64 = 1

// halideDim matches halide_dimension_t (16 bytes, same on every ABI).
type halideDim struct {
	Min    int32
	Extent int32
	Stride int32
	Flags  uint32
}

// halideBuffer matches halide_buffer_t on LP64 and LLP64 (56 bytes).
// The wasm backend writes the ILP32 layout itself and does not use this struct.
type halideBuffer struct {
	Device          uint64
	DeviceInterface uintptr
	Host            *byte
	Flags           uint64
	Code            uint8
	Bits            uint8
	Lanes           uint16
	Dimensions      int32
	Dim             *halideDim
	Padding         uintptr
}

// Dim is one axis of a buffer, innermost dimension first.
// Min/Extent/Stride use Halide's coordinate system: stride is in elements,
// and the host pointer addresses the element at the min of every dimension.
type Dim struct {
	Min    int32
	Extent int32
	Stride int32
}

// Buffer is a halide_buffer_t view of Go memory.
// The Buffer aliases its backing slice. Callers must not mutate the slice
// from another goroutine during [Func.Call]. Call pins the memory until it returns.
type Buffer struct {
	raw   halideBuffer
	dims  []halideDim
	bytes []byte
	typ   Type
}

// Type returns the element type.
func (b *Buffer) Type() Type {
	if b == nil {
		return Type{}
	}
	return b.typ
}

// Dims returns a copy of the shape, innermost dimension first.
func (b *Buffer) Dims() []Dim {
	if b == nil {
		return nil
	}
	out := make([]Dim, len(b.dims))
	for i, d := range b.dims {
		out[i] = Dim{Min: d.Min, Extent: d.Extent, Stride: d.Stride}
	}
	return out
}

// Bytes returns the host slice starting at the min coordinate.
// The slice aliases the memory the buffer was built on.
func (b *Buffer) Bytes() []byte {
	if b == nil {
		return nil
	}
	return b.bytes
}

// Bytes builds a buffer over data. elem is the element type. dims are
// innermost-first. data[0] is the element at the min coordinate of every
// dimension. Every element the shape can address must lie inside data.
func Bytes(data []byte, elem Type, dims ...Dim) (*Buffer, error) {
	if elem.Bytes() <= 0 || elem.Lanes == 0 {
		return nil, fmt.Errorf("halide: invalid element type %s", elem)
	}
	if err := checkCovered(dims, elem.Bytes(), len(data)); err != nil {
		return nil, err
	}
	hd := make([]halideDim, len(dims))
	for i, d := range dims {
		hd[i] = halideDim{Min: d.Min, Extent: d.Extent, Stride: d.Stride}
	}
	b := &Buffer{dims: hd, bytes: data, typ: elem}
	b.sync()
	return b, nil
}

// Scalar is a Go type that can back a dense Halide buffer.
type Scalar interface {
	~int8 | ~int16 | ~int32 | ~int64 |
		~uint8 | ~uint16 | ~uint32 | ~uint64 |
		~float32 | ~float64
}

// Slice builds a dense buffer (min 0, packed, dimension 0 innermost) over data.
// len(data) must equal the product of extents.
func Slice[T Scalar](data []T, extents ...int) (*Buffer, error) {
	elem, err := typeOf[T]()
	if err != nil {
		return nil, err
	}
	dims := make([]Dim, len(extents))
	stride := int32(1)
	n := 1
	for i, e := range extents {
		if e < 0 || int32(e) != int32(int(e)) {
			return nil, fmt.Errorf("halide: extent %d out of range", e)
		}
		dims[i] = Dim{Min: 0, Extent: int32(e), Stride: stride}
		if e == 0 {
			n = 0
		} else if n > 0 {
			n *= e
		}
		if e > 0 {
			stride *= int32(e)
		}
	}
	if n != len(data) {
		return nil, fmt.Errorf("halide: slice length %d, shape covers %d elements", len(data), n)
	}
	return Bytes(bytesOf(data), elem, dims...)
}

// FromGray wraps img's rectangle. The buffer aliases img.Pix.
// Dimension 0 is x (stride 1) and dimension 1 is y (stride img.Stride).
func FromGray(img *image.Gray) (*Buffer, error) {
	if img == nil {
		return nil, fmt.Errorf("halide: nil *image.Gray")
	}
	w, h := img.Rect.Dx(), img.Rect.Dy()
	if w < 0 || h < 0 {
		return nil, fmt.Errorf("halide: negative image rectangle")
	}
	var data []byte
	if w > 0 && h > 0 {
		off := img.PixOffset(img.Rect.Min.X, img.Rect.Min.Y)
		need := (h-1)*img.Stride + w
		if off < 0 || off+need > len(img.Pix) {
			return nil, fmt.Errorf("halide: gray image rectangle is outside Pix")
		}
		data = img.Pix[off:]
	}
	return Bytes(data, UInt8,
		Dim{Min: 0, Extent: int32(w), Stride: 1},
		Dim{Min: 0, Extent: int32(h), Stride: int32(img.Stride)},
	)
}

// FromRGBA wraps img as an interleaved 3-D buffer: channel, x, y.
// Channel extent is 4 and its stride is 1. The buffer aliases img.Pix.
func FromRGBA(img *image.RGBA) (*Buffer, error) {
	if img == nil {
		return nil, fmt.Errorf("halide: nil *image.RGBA")
	}
	w, h := img.Rect.Dx(), img.Rect.Dy()
	if w < 0 || h < 0 {
		return nil, fmt.Errorf("halide: negative image rectangle")
	}
	var data []byte
	if w > 0 && h > 0 {
		off := img.PixOffset(img.Rect.Min.X, img.Rect.Min.Y)
		need := (h-1)*img.Stride + w*4
		if off < 0 || off+need > len(img.Pix) {
			return nil, fmt.Errorf("halide: RGBA image rectangle is outside Pix")
		}
		data = img.Pix[off:]
	}
	return Bytes(data, UInt8,
		Dim{Min: 0, Extent: 4, Stride: 1},
		Dim{Min: 0, Extent: int32(w), Stride: 4},
		Dim{Min: 0, Extent: int32(h), Stride: int32(img.Stride)},
	)
}

// GrayImage copies a 2-D uint8 buffer into a new image.Gray.
// Dimension 0 is x and dimension 1 is y. Stride may be wider than the width.
func (b *Buffer) GrayImage() (*image.Gray, error) {
	if b == nil {
		return nil, fmt.Errorf("halide: nil buffer")
	}
	if !b.typ.Equal(UInt8) || len(b.dims) != 2 {
		return nil, fmt.Errorf("halide: GrayImage wants a 2-D uint8 buffer, have %d-D %s", len(b.dims), b.typ)
	}
	w := int(b.dims[0].Extent)
	h := int(b.dims[1].Extent)
	if b.dims[0].Stride != 1 {
		return nil, fmt.Errorf("halide: GrayImage wants x stride 1, have %d", b.dims[0].Stride)
	}
	dst := image.NewGray(image.Rect(0, 0, w, h))
	if w == 0 || h == 0 {
		return dst, nil
	}
	stride := int(b.dims[1].Stride)
	for y := 0; y < h; y++ {
		src := b.bytes[y*stride : y*stride+w]
		copy(dst.Pix[y*dst.Stride:y*dst.Stride+w], src)
	}
	return dst, nil
}

func (b *Buffer) sync() {
	b.raw = halideBuffer{
		Flags:      flagHostDirty,
		Code:       uint8(b.typ.Code),
		Bits:       b.typ.Bits,
		Lanes:      b.typ.Lanes,
		Dimensions: int32(len(b.dims)),
	}
	if len(b.bytes) > 0 {
		b.raw.Host = &b.bytes[0]
	}
	if len(b.dims) > 0 {
		b.raw.Dim = &b.dims[0]
	}
}

func checkCovered(dims []Dim, elemSize, nBytes int) error {
	if elemSize <= 0 {
		return fmt.Errorf("halide: element size must be positive")
	}
	var minI, maxI int64
	touched := false
	for _, d := range dims {
		if d.Extent < 0 {
			return fmt.Errorf("halide: negative extent %d", d.Extent)
		}
		if d.Extent == 0 {
			return nil
		}
		delta := int64(d.Extent-1) * int64(d.Stride)
		lo, hi := int64(0), delta
		if hi < lo {
			lo, hi = hi, lo
		}
		minI += lo
		maxI += hi
		touched = true
	}
	if !touched {
		if len(dims) == 0 && nBytes < elemSize {
			return fmt.Errorf("halide: scalar buffer needs %d bytes, have %d", elemSize, nBytes)
		}
		return nil
	}
	if minI < 0 {
		return fmt.Errorf("halide: shape reaches %d elements before the host pointer; host must address the min coordinate", -minI)
	}
	need := (maxI + 1) * int64(elemSize)
	if need > int64(nBytes) {
		return fmt.Errorf("halide: buffer data is %d bytes, shape needs %d", nBytes, need)
	}
	return nil
}

func typeOf[T Scalar]() (Type, error) {
	var zero T
	switch any(zero).(type) {
	case int8:
		return Int8, nil
	case int16:
		return Int16, nil
	case int32:
		return Int32, nil
	case int64:
		return Int64, nil
	case uint8:
		return UInt8, nil
	case uint16:
		return UInt16, nil
	case uint32:
		return UInt32, nil
	case uint64:
		return UInt64, nil
	case float32:
		return Float32, nil
	case float64:
		return Float64, nil
	default:
		return Type{}, fmt.Errorf("halide: unsupported slice element")
	}
}

func bytesOf[T Scalar](data []T) []byte {
	if len(data) == 0 {
		return nil
	}
	n := len(data) * int(unsafe.Sizeof(data[0]))
	return unsafe.Slice((*byte)(unsafe.Pointer(&data[0])), n)
}
