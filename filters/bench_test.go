package filters

import (
	"testing"

	"github.com/kidandcat/halide-go"
	"github.com/kidandcat/halide-go/ref"
)

func BenchmarkBlurHalide4096(b *testing.B) {
	benchBlur(b, halide.Native, 4096, false)
}

func BenchmarkBlurWASM4096(b *testing.B) {
	benchBlur(b, halide.WASM, 4096, false)
}

func BenchmarkBlurGo4096(b *testing.B) {
	benchBlurGo(b, 4096, false)
}

func BenchmarkBlurGoParallel4096(b *testing.B) {
	benchBlurGo(b, 4096, true)
}

func BenchmarkBlurHalide512(b *testing.B) {
	benchBlur(b, halide.Native, 512, false)
}

func BenchmarkBrightnessHalide2048(b *testing.B) {
	benchPoint(b, halide.Native, 2048, "brightness")
}

func BenchmarkBrightnessGoParallel2048(b *testing.B) {
	w, h := 2048, 2048
	src := pattern(w, h)
	dst := make([]byte, w*h)
	s := ref.Packed(src, w, h)
	d := ref.Packed(dst, w, h)
	b.SetBytes(int64(w * h))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ref.BrightnessParallel(s, d, 12, 280)
	}
}

func BenchmarkGrayscaleHalide2048(b *testing.B) {
	benchPoint(b, halide.Native, 2048, "grayscale")
}

func BenchmarkGrayscaleGoParallel2048(b *testing.B) {
	n := 2048
	stride := n * 4
	rgba := make([]byte, stride*n)
	for i := range rgba {
		rgba[i] = byte(i)
	}
	dst := make([]byte, n*n)
	d := ref.Packed(dst, n, n)
	b.SetBytes(int64(n * n * 4))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ref.GrayscaleParallel(rgba, stride, n, n, d)
	}
}

func BenchmarkSobelHalide2048(b *testing.B) {
	benchPoint(b, halide.Native, 2048, "sobel")
}

func BenchmarkSobelGoParallel2048(b *testing.B) {
	w, h := 2048, 2048
	src := pattern(w, h)
	dst := make([]byte, w*h)
	s := ref.Packed(src, w, h)
	d := ref.Packed(dst, w, h)
	b.SetBytes(int64(w * h))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ref.SobelParallel(s, d)
	}
}

func BenchmarkResizeHalide2048(b *testing.B) {
	benchPoint(b, halide.Native, 2048, "resize")
}

func benchBlur(b *testing.B, backend halide.Backend, n int, _ bool) {
	b.Helper()
	p, err := Load(halide.WithBackend(backend))
	if err != nil {
		b.Fatal(err)
	}
	defer p.Close()
	src := pattern(n, n)
	dst := make([]byte, n*n)
	in, err := halide.Slice(src, n, n)
	if err != nil {
		b.Fatal(err)
	}
	out, err := halide.Slice(dst, n, n)
	if err != nil {
		b.Fatal(err)
	}
	if err := p.Blur(in, out); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(n * n))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := p.Blur(in, out); err != nil {
			b.Fatal(err)
		}
	}
}

func benchBlurGo(b *testing.B, n int, parallel bool) {
	src := pattern(n, n)
	dst := make([]byte, n*n)
	s := ref.Packed(src, n, n)
	d := ref.Packed(dst, n, n)
	if parallel {
		ref.BlurParallel(s, d)
	} else {
		ref.Blur(s, d)
	}
	b.SetBytes(int64(n * n))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if parallel {
			ref.BlurParallel(s, d)
		} else {
			ref.Blur(s, d)
		}
	}
}

func benchPoint(b *testing.B, backend halide.Backend, n int, kind string) {
	p, err := Load(halide.WithBackend(backend))
	if err != nil {
		b.Fatal(err)
	}
	defer p.Close()
	var run func() error
	switch kind {
	case "brightness":
		src := pattern(n, n)
		dst := make([]byte, n*n)
		in, err := halide.Slice(src, n, n)
		if err != nil {
			b.Fatal(err)
		}
		out, err := halide.Slice(dst, n, n)
		if err != nil {
			b.Fatal(err)
		}
		run = func() error { return p.Brightness(in, 12, 280, out) }
		b.SetBytes(int64(n * n))
	case "sobel":
		src := pattern(n, n)
		dst := make([]byte, n*n)
		in, err := halide.Slice(src, n, n)
		if err != nil {
			b.Fatal(err)
		}
		out, err := halide.Slice(dst, n, n)
		if err != nil {
			b.Fatal(err)
		}
		run = func() error { return p.Sobel(in, out) }
		b.SetBytes(int64(n * n))
	case "resize":
		src := pattern(n, n)
		dw, dh := n/2, n/2
		dst := make([]byte, dw*dh)
		in, err := halide.Slice(src, n, n)
		if err != nil {
			b.Fatal(err)
		}
		out, err := halide.Slice(dst, dw, dh)
		if err != nil {
			b.Fatal(err)
		}
		run = func() error { return p.Resize(in, out) }
		b.SetBytes(int64(n * n))
	case "grayscale":
		stride := n * 4
		rgba := make([]byte, stride*n)
		for i := range rgba {
			rgba[i] = byte(i)
		}
		img := &struct {
			pix    []byte
			stride int
		}{rgba, stride}
		_ = img
		in, err := halide.Bytes(rgba, halide.UInt8,
			halide.Dim{Extent: 4, Stride: 1},
			halide.Dim{Extent: int32(n), Stride: 4},
			halide.Dim{Extent: int32(n), Stride: int32(stride)},
		)
		if err != nil {
			b.Fatal(err)
		}
		dst := make([]byte, n*n)
		out, err := halide.Slice(dst, n, n)
		if err != nil {
			b.Fatal(err)
		}
		run = func() error { return p.Grayscale(in, out) }
		b.SetBytes(int64(n * n * 4))
	default:
		b.Fatalf("unknown %s", kind)
	}
	if err := run(); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := run(); err != nil {
			b.Fatal(err)
		}
	}
}
