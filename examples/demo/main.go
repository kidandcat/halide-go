// Command demo runs the embedded blur pipeline and checks it against the
// pure Go reference. It does not need a Halide installation.
//
//	CGO_ENABLED=0 go run ./examples/demo
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/kidandcat/halide-go"
	"github.com/kidandcat/halide-go/filters"
	"github.com/kidandcat/halide-go/ref"
)

func main() {
	const w, h = 1024, 1024
	src := make([]byte, w*h)
	for i := range src {
		src[i] = byte(i * 17)
	}
	dst := make([]byte, w*h)
	in, err := halide.Slice(src, w, h)
	if err != nil {
		fatal(err)
	}
	out, err := halide.Slice(dst, w, h)
	if err != nil {
		fatal(err)
	}
	pipe, err := filters.Load()
	if err != nil {
		fatal(err)
	}
	defer pipe.Close()

	if err := pipe.Blur(in, out); err != nil {
		fatal(err)
	}
	want := make([]byte, w*h)
	ref.Blur(ref.Packed(src, w, h), ref.Packed(want, w, h))
	diffs := 0
	var sum uint64
	for i := range dst {
		sum += uint64(dst[i])
		if dst[i] != want[i] {
			diffs++
		}
	}
	if diffs != 0 {
		fatal(fmt.Errorf("blur is not bit-exact: %d differing bytes", diffs))
	}

	start := time.Now()
	const rounds = 5
	for i := 0; i < rounds; i++ {
		if err := pipe.Blur(in, out); err != nil {
			fatal(err)
		}
	}
	elapsed := time.Since(start) / rounds
	fmt.Printf("blur %dx%d  halide %s  checksum %d  diffs 0\n", w, h, elapsed, sum)

	goStart := time.Now()
	for i := 0; i < rounds; i++ {
		ref.BlurParallel(ref.Packed(src, w, h), ref.Packed(want, w, h))
	}
	fmt.Printf("blur %dx%d  go     %s\n", w, h, time.Since(goStart)/rounds)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
