package filters

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"testing"

	"github.com/kidandcat/halide-go"
	"github.com/kidandcat/halide-go/ref"
)

func TestBitExact(t *testing.T) {
	sizes := [][2]int{{1, 1}, {2, 3}, {7, 5}, {31, 32}, {64, 63}, {128, 128}}
	backends := []halide.Backend{halide.Native, halide.WASM}
	for _, backend := range backends {
		backend := backend
		t.Run(backend.String(), func(t *testing.T) {
			p, err := Load(halide.WithBackend(backend))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { p.Close() })
			for _, sz := range sizes {
				w, h := sz[0], sz[1]
				src := pattern(w, h)
				name := fmt.Sprintf("%dx%d", w, h)
				t.Run(name+"/blur", func(t *testing.T) {
					got := callPlane(t, src, w, h, func(in, out *halide.Buffer) error {
						return p.Blur(in, out)
					})
					want := make([]byte, w*h)
					ref.Blur(ref.Packed(src, w, h), ref.Packed(want, w, h))
					if !bytes.Equal(got, want) {
						t.Fatalf("%dx%d blur mismatch, first %d", w, h, firstDiff(got, want))
					}
				})
				t.Run(name+"/brightness", func(t *testing.T) {
					const brightness, contrast int32 = -20, 320
					got := callPlane(t, src, w, h, func(in, out *halide.Buffer) error {
						return p.Brightness(in, brightness, contrast, out)
					})
					want := make([]byte, w*h)
					ref.Brightness(ref.Packed(src, w, h), ref.Packed(want, w, h), brightness, contrast)
					if !bytes.Equal(got, want) {
						t.Fatalf("%dx%d brightness mismatch, first %d", w, h, firstDiff(got, want))
					}
				})
				t.Run(name+"/sobel", func(t *testing.T) {
					got := callPlane(t, src, w, h, func(in, out *halide.Buffer) error {
						return p.Sobel(in, out)
					})
					want := make([]byte, w*h)
					ref.Sobel(ref.Packed(src, w, h), ref.Packed(want, w, h))
					if !bytes.Equal(got, want) {
						t.Fatalf("%dx%d sobel mismatch, first %d", w, h, firstDiff(got, want))
					}
				})
				t.Run(name+"/resize", func(t *testing.T) {
					dw, dh := w+3, h*2+1
					if w == 1 && h == 1 {
						dw, dh = 4, 5
					}
					dst := make([]byte, dw*dh)
					in, err := halide.Slice(src, w, h)
					if err != nil {
						t.Fatal(err)
					}
					out, err := halide.Slice(dst, dw, dh)
					if err != nil {
						t.Fatal(err)
					}
					if err := p.Resize(in, out); err != nil {
						t.Fatal(err)
					}
					want := make([]byte, dw*dh)
					ref.Resize(ref.Packed(src, w, h), ref.Packed(want, dw, dh))
					if !bytes.Equal(dst, want) {
						t.Fatalf("%dx%d -> %dx%d resize mismatch, first %d", w, h, dw, dh, firstDiff(dst, want))
					}
				})
				t.Run(name+"/grayscale", func(t *testing.T) {
					stride := w*4 + 8
					rgba := make([]byte, stride*h)
					for y := 0; y < h; y++ {
						for x := 0; x < w; x++ {
							i := y*stride + x*4
							rgba[i] = byte(x * 13)
							rgba[i+1] = byte(y * 9)
							rgba[i+2] = byte(x + y*3)
							rgba[i+3] = 255
						}
					}
					img := &image.RGBA{
						Pix:    rgba,
						Stride: stride,
						Rect:   image.Rect(0, 0, w, h),
					}
					in, err := halide.FromRGBA(img)
					if err != nil {
						t.Fatal(err)
					}
					dst := make([]byte, w*h)
					out, err := halide.Slice(dst, w, h)
					if err != nil {
						t.Fatal(err)
					}
					if err := p.Grayscale(in, out); err != nil {
						t.Fatal(err)
					}
					want := make([]byte, w*h)
					ref.Grayscale(rgba, stride, w, h, ref.Packed(want, w, h))
					if !bytes.Equal(dst, want) {
						t.Fatalf("%dx%d grayscale mismatch, first %d", w, h, firstDiff(dst, want))
					}
				})
			}
		})
	}
}

func TestGraySubImage(t *testing.T) {
	big := image.NewGray(image.Rect(0, 0, 40, 24))
	for i := range big.Pix {
		big.Pix[i] = byte(i)
	}
	sub := big.SubImage(image.Rect(3, 2, 34, 18)).(*image.Gray)
	w, h := sub.Rect.Dx(), sub.Rect.Dy()
	in, err := halide.FromGray(sub)
	if err != nil {
		t.Fatal(err)
	}
	dst := image.NewGray(image.Rect(0, 0, w, h))
	out, err := halide.FromGray(dst)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Load(halide.WithBackend(halide.Native))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.Blur(in, out); err != nil {
		t.Fatal(err)
	}
	off := sub.PixOffset(sub.Rect.Min.X, sub.Rect.Min.Y)
	src := ref.Plane{Pix: sub.Pix[off:], W: w, H: h, Stride: sub.Stride}
	want := image.NewGray(image.Rect(0, 0, w, h))
	ref.Blur(src, ref.Plane{Pix: want.Pix, W: w, H: h, Stride: want.Stride})
	if !bytes.Equal(dst.Pix, want.Pix) {
		t.Fatalf("gray subimage mismatch, first %d", firstDiff(dst.Pix, want.Pix))
	}
	img, err := out.GrayImage()
	if err != nil {
		t.Fatal(err)
	}
	if img.At(1, 1) != (color.Gray{Y: dst.GrayAt(1, 1).Y}) {
		t.Fatal("GrayImage copy differs")
	}
}

func TestSignatureAndBadArgs(t *testing.T) {
	p, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	sig := p.blur.Signature()
	if len(sig) != 2 || sig[0].Name != "in" || sig[1].Kind != halide.KindOutputBuffer {
		t.Fatalf("blur signature %+v", sig)
	}
	src := make([]byte, 4)
	in, err := halide.Slice(src, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]byte, 4)
	outb, err := halide.Slice(out, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Blur(in, outb); err != nil {
		t.Fatal(err)
	}
	bad, err := halide.Slice(src, 4)
	if err != nil {
		t.Fatal(err)
	}
	err = p.Blur(bad, in)
	if err == nil {
		t.Fatal("expected dimension error")
	}
	// A zero-extent input is outside the domain the filter reads. The call
	// must return rather than crash; the runtime does not always report it
	// as a bounds error (the output region can be compiled as unreachable).
	ein, err := halide.Slice([]byte{}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	eout := []byte{9, 9, 9, 9}
	ob, err := halide.Slice(eout, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Resize(ein, ob); err != nil {
		t.Fatal(err)
	}
}

func TestPackageFuncs(t *testing.T) {
	src := pattern(8, 6)
	dst := make([]byte, len(src))
	in, err := halide.Slice(src, 8, 6)
	if err != nil {
		t.Fatal(err)
	}
	out, err := halide.Slice(dst, 8, 6)
	if err != nil {
		t.Fatal(err)
	}
	if err := Blur(in, out); err != nil {
		t.Fatal(err)
	}
	want := make([]byte, len(src))
	ref.Blur(ref.Packed(src, 8, 6), ref.Packed(want, 8, 6))
	if !bytes.Equal(dst, want) {
		t.Fatal("package Blur mismatch")
	}
	wasm, err := Load(halide.WithBackend(halide.WASM))
	if err != nil {
		t.Fatal(err)
	}
	defer wasm.Close()
	for i := range dst {
		dst[i] = 0
	}
	if err := wasm.Blur(in, out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dst, want) {
		t.Fatal("second wasm blur mismatch")
	}
}

func TestBlurLarge(t *testing.T) {
	const w, h = 4096, 4096
	src := make([]byte, w*h)
	for i := range src {
		src[i] = byte(i * 17)
	}
	want := make([]byte, w*h)
	ref.Blur(ref.Packed(src, w, h), ref.Packed(want, w, h))
	for _, backend := range []halide.Backend{halide.Native, halide.WASM} {
		got := callPlane(t, src, w, h, func(in, out *halide.Buffer) error {
			p, err := Load(halide.WithBackend(backend))
			if err != nil {
				return err
			}
			defer p.Close()
			return p.Blur(in, out)
		})
		if !bytes.Equal(got, want) {
			t.Fatalf("%s 4096 blur mismatch at %d", backend, firstDiff(got, want))
		}
	}
}

func TestParallelRefMatches(t *testing.T) {
	w, h := 127, 96
	src := pattern(w, h)
	a := make([]byte, w*h)
	b := make([]byte, w*h)
	ref.Blur(ref.Packed(src, w, h), ref.Packed(a, w, h))
	ref.BlurParallel(ref.Packed(src, w, h), ref.Packed(b, w, h))
	if !bytes.Equal(a, b) {
		t.Fatal("parallel blur differs")
	}
}

func callPlane(t *testing.T, src []byte, w, h int, fn func(in, out *halide.Buffer) error) []byte {
	t.Helper()
	dst := make([]byte, w*h)
	in, err := halide.Slice(src, w, h)
	if err != nil {
		t.Fatal(err)
	}
	out, err := halide.Slice(dst, w, h)
	if err != nil {
		t.Fatal(err)
	}
	if err := fn(in, out); err != nil {
		t.Fatal(err)
	}
	return dst
}

func pattern(w, h int) []byte {
	pix := make([]byte, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			pix[y*w+x] = byte(x*17 + y*29)
		}
	}
	return pix
}

func firstDiff(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}
