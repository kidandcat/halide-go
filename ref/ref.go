// Package ref is a pure Go implementation of the example Halide filters.
// The formulas match generators/filters.cpp so outputs can be compared bit for bit.
package ref

import "runtime"

// Plane is a 2-D uint8 image. Stride is the row step in bytes.
// Pix[0] is pixel (0, 0).
type Plane struct {
	Pix    []byte
	W, H   int
	Stride int
}

// Packed returns a plane with no row padding.
func Packed(pix []byte, w, h int) Plane {
	return Plane{Pix: pix, W: w, H: h, Stride: w}
}

// Blur is a 3x3 box filter with repeat-edge borders. Each output is the
// integer sum of the window divided by 9.
func Blur(src, dst Plane) {
	blurRows(src, dst, 0, dst.H)
}

// BlurParallel is Blur split across GOMAXPROCS row bands.
func BlurParallel(src, dst Plane) {
	parallelY(dst.H, func(y0, y1 int) { blurRows(src, dst, y0, y1) })
}

func blurRows(src, dst Plane, y0, y1 int) {
	w, h := src.W, src.H
	for y := y0; y < y1; y++ {
		row := dst.Pix[y*dst.Stride:]
		for x := 0; x < dst.W; x++ {
			var sum uint16
			for dy := -1; dy <= 1; dy++ {
				yy := clamp(y+dy, h)
				for dx := -1; dx <= 1; dx++ {
					xx := clamp(x+dx, w)
					sum += uint16(src.Pix[yy*src.Stride+xx])
				}
			}
			row[x] = byte(sum / 9)
		}
	}
}

// Brightness applies 8.8 fixed-point contrast about 128, then adds brightness.
// contrast 256 is 1.0. The result is clamped to 0..255.
// Division truncates toward zero.
func Brightness(src, dst Plane, brightness, contrast int32) {
	brightnessRows(src, dst, brightness, contrast, 0, dst.H)
}

// BrightnessParallel is Brightness split across GOMAXPROCS row bands.
func BrightnessParallel(src, dst Plane, brightness, contrast int32) {
	parallelY(dst.H, func(y0, y1 int) {
		brightnessRows(src, dst, brightness, contrast, y0, y1)
	})
}

func brightnessRows(src, dst Plane, brightness, contrast int32, y0, y1 int) {
	for y := y0; y < y1; y++ {
		srow := src.Pix[y*src.Stride:]
		drow := dst.Pix[y*dst.Stride:]
		for x := 0; x < dst.W; x++ {
			centered := int32(srow[x]) - 128
			v := (centered*contrast)/256 + 128 + brightness
			if v < 0 {
				v = 0
			}
			if v > 255 {
				v = 255
			}
			drow[x] = byte(v)
		}
	}
}

// Grayscale converts interleaved RGBA (R, G, B, A) into a plane.
// Y = (77*R + 150*G + 29*B) >> 8. srcStride is the RGBA row step in bytes.
func Grayscale(src []byte, srcStride, w, h int, dst Plane) {
	grayRows(src, srcStride, w, dst, 0, h)
}

// GrayscaleParallel is Grayscale split across GOMAXPROCS row bands.
func GrayscaleParallel(src []byte, srcStride, w, h int, dst Plane) {
	parallelY(h, func(y0, y1 int) { grayRows(src, srcStride, w, dst, y0, y1) })
}

func grayRows(src []byte, srcStride, w int, dst Plane, y0, y1 int) {
	for y := y0; y < y1; y++ {
		row := src[y*srcStride:]
		drow := dst.Pix[y*dst.Stride:]
		for x := 0; x < w; x++ {
			r := uint16(row[x*4])
			g := uint16(row[x*4+1])
			b := uint16(row[x*4+2])
			drow[x] = byte((77*r + 150*g + 29*b) >> 8)
		}
	}
}

// Sobel writes the saturated Sobel magnitude |gx| + |gy| with repeat-edge borders.
func Sobel(src, dst Plane) {
	sobelRows(src, dst, 0, dst.H)
}

// SobelParallel is Sobel split across GOMAXPROCS row bands.
func SobelParallel(src, dst Plane) {
	parallelY(dst.H, func(y0, y1 int) { sobelRows(src, dst, y0, y1) })
}

func sobelRows(src, dst Plane, y0, y1 int) {
	w, h := src.W, src.H
	for y := y0; y < y1; y++ {
		row := dst.Pix[y*dst.Stride:]
		for x := 0; x < dst.W; x++ {
			at := func(xx, yy int) int16 {
				return int16(src.Pix[clamp(yy, h)*src.Stride+clamp(xx, w)])
			}
			gx := -at(x-1, y-1) + at(x+1, y-1) - 2*at(x-1, y) + 2*at(x+1, y) - at(x-1, y+1) + at(x+1, y+1)
			gy := -at(x-1, y-1) - 2*at(x, y-1) - at(x+1, y-1) + at(x-1, y+1) + 2*at(x, y+1) + at(x+1, y+1)
			mag := abs16(gx) + abs16(gy)
			if mag > 255 {
				mag = 255
			}
			row[x] = byte(mag)
		}
	}
}

// Resize is nearest-neighbor. Source coordinates are
// (x * srcW) / dstW and (y * srcH) / dstH, truncating toward zero.
func Resize(src, dst Plane) {
	resizeRows(src, dst, 0, dst.H)
}

// ResizeParallel is Resize split across GOMAXPROCS row bands.
func ResizeParallel(src, dst Plane) {
	parallelY(dst.H, func(y0, y1 int) { resizeRows(src, dst, y0, y1) })
}

func resizeRows(src, dst Plane, y0, y1 int) {
	for y := y0; y < y1; y++ {
		sy := (y * src.H) / dst.H
		srow := src.Pix[sy*src.Stride:]
		drow := dst.Pix[y*dst.Stride:]
		for x := 0; x < dst.W; x++ {
			sx := (x * src.W) / dst.W
			drow[x] = srow[sx]
		}
	}
}

func clamp(v, n int) int {
	if v < 0 {
		return 0
	}
	if v >= n {
		return n - 1
	}
	return v
}

func abs16(v int16) int16 {
	if v < 0 {
		return -v
	}
	return v
}

func parallelY(h int, fn func(y0, y1 int)) {
	workers := runtime.GOMAXPROCS(0)
	if workers > h {
		workers = h
	}
	if workers <= 1 || h < 64 {
		fn(0, h)
		return
	}
	chunk := (h + workers - 1) / workers
	done := make(chan struct{}, workers)
	started := 0
	for i := 0; i < workers; i++ {
		y0 := i * chunk
		if y0 >= h {
			break
		}
		y1 := y0 + chunk
		if y1 > h {
			y1 = h
		}
		started++
		go func(y0, y1 int) {
			fn(y0, y1)
			done <- struct{}{}
		}(y0, y1)
	}
	for i := 0; i < started; i++ {
		<-done
	}
}
