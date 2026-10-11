package halide

import (
	"image"
	"testing"
	"unsafe"
)

func TestBufferABI(t *testing.T) {
	if unsafe.Sizeof(halideBuffer{}) != 56 {
		t.Fatalf("halide_buffer_t size = %d, want 56", unsafe.Sizeof(halideBuffer{}))
	}
	if unsafe.Sizeof(halideDim{}) != 16 {
		t.Fatalf("halide_dimension_t size = %d, want 16", unsafe.Sizeof(halideDim{}))
	}
	if unsafe.Offsetof(halideBuffer{}.Host) != 16 {
		t.Fatalf("host offset = %d", unsafe.Offsetof(halideBuffer{}.Host))
	}
	if unsafe.Offsetof(halideBuffer{}.Dim) != 40 {
		t.Fatalf("dim offset = %d", unsafe.Offsetof(halideBuffer{}.Dim))
	}
}

func TestSlicePacked(t *testing.T) {
	data := make([]byte, 6)
	b, err := Slice(data, 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	dims := b.Dims()
	if dims[0].Stride != 1 || dims[1].Stride != 3 || dims[0].Extent != 3 || dims[1].Extent != 2 {
		t.Fatalf("dims %+v", dims)
	}
	if _, err := Slice(data, 4, 2); err == nil {
		t.Fatal("expected length mismatch")
	}
	if _, err := Slice([]byte{}, 0, 4); err != nil {
		t.Fatal(err)
	}
}

func TestBytesRejectsShortBuffer(t *testing.T) {
	_, err := Bytes(make([]byte, 3), UInt8, Dim{Extent: 2, Stride: 1}, Dim{Extent: 2, Stride: 2})
	if err == nil {
		t.Fatal("expected coverage error")
	}
}

func TestNegativeStrideBeforeHost(t *testing.T) {
	_, err := Bytes(make([]byte, 8), UInt8, Dim{Extent: 4, Stride: -1})
	if err == nil {
		t.Fatal("expected host-before-min error")
	}
}

func TestFromGrayStride(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 8, 2))
	img.Stride = 16
	img.Pix = make([]byte, 32)
	for i := range img.Pix {
		img.Pix[i] = byte(i)
	}
	// Rebuild with a real rectangle that uses the wide stride.
	img.Rect = image.Rect(1, 0, 4, 2)
	b, err := FromGray(img)
	if err != nil {
		t.Fatal(err)
	}
	dims := b.Dims()
	if dims[0].Extent != 3 || dims[1].Extent != 2 || dims[1].Stride != 16 {
		t.Fatalf("dims %+v", dims)
	}
	if b.Bytes()[0] != img.Pix[img.PixOffset(1, 0)] {
		t.Fatal("host pointer is not the min pixel")
	}
	out, err := b.GrayImage()
	if err != nil {
		t.Fatal(err)
	}
	if out.Bounds().Dx() != 3 || out.Pix[0] != img.Pix[img.PixOffset(1, 0)] {
		t.Fatalf("copy mismatch %+v", out.Pix[:out.Stride])
	}
}

func TestFromRGBA(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	img.Pix[0], img.Pix[1], img.Pix[2], img.Pix[3] = 1, 2, 3, 4
	b, err := FromRGBA(img)
	if err != nil {
		t.Fatal(err)
	}
	d := b.Dims()
	if len(d) != 3 || d[0].Extent != 4 || d[0].Stride != 1 || d[1].Stride != 4 {
		t.Fatalf("dims %+v", d)
	}
}

func TestUint16Slice(t *testing.T) {
	data := []uint16{1, 2, 3, 4}
	b, err := Slice(data, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !b.Type().Equal(UInt16) {
		t.Fatal(b.Type())
	}
	if len(b.Bytes()) != 8 {
		t.Fatal(len(b.Bytes()))
	}
}
