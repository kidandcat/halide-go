package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kidandcat/halide-go"
	"github.com/kidandcat/halide-go/ref"
)

func TestGenerateHostBlur(t *testing.T) {
	if _, err := findHalide(""); err != nil {
		t.Skip(err)
	}
	root := repoRoot(t)
	dir := t.TempDir()
	err := run([]string{
		"-o", dir,
		"-package", "filt",
		"-name", "filt",
		"-targets", "linux/amd64",
		filepath.Join(root, "generators", "filters.cpp"),
	})
	if err != nil {
		t.Fatal(err)
	}
	lib, err := halide.Open(filepath.Join(dir, "libs", "linux_amd64", "libfilt.so"))
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	fn, err := lib.Func("blur")
	if err != nil {
		t.Fatal(err)
	}
	const w, h = 48, 36
	src := make([]byte, w*h)
	for i := range src {
		src[i] = byte(i * 3)
	}
	dst := make([]byte, w*h)
	in, err := halide.Slice(src, w, h)
	if err != nil {
		t.Fatal(err)
	}
	out, err := halide.Slice(dst, w, h)
	if err != nil {
		t.Fatal(err)
	}
	if err := fn.Call(in, out); err != nil {
		t.Fatal(err)
	}
	want := make([]byte, w*h)
	ref.Blur(ref.Packed(src, w, h), ref.Packed(want, w, h))
	if !bytes.Equal(dst, want) {
		t.Fatal("generated blur is not bit-exact")
	}
	gen, err := os.ReadFile(filepath.Join(dir, "halide_pipelines.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(gen, []byte("func (p *Pipeline) Brightness(in *halide.Buffer, brightness int32, contrast int32, out *halide.Buffer) error")) {
		t.Fatal("generated wrapper missing Brightness")
	}
}

func TestGenerateFloatScalar(t *testing.T) {
	sdk, err := findHalide("")
	if err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "addf.cpp")
	if err := os.WriteFile(src, []byte(addfSource), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	if err := run([]string{"-o", out, "-package", "addf", "-name", "addf", "-targets", "linux/amd64", "-halide", sdk.Root, src}); err != nil {
		t.Fatal(err)
	}
	lib, err := halide.Open(filepath.Join(out, "libs", "linux_amd64", "libaddf.so"))
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	fn, err := lib.Func("addf")
	if err != nil {
		t.Fatal(err)
	}
	inPix := []byte{10, 20, 250, 0}
	outPix := make([]byte, 4)
	in, err := halide.Slice(inPix, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	ob, err := halide.Slice(outPix, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := fn.Call(in, float32(1.9), ob); err != nil {
		t.Fatal(err)
	}
	want := []byte{11, 21, 251, 1}
	if !bytes.Equal(outPix, want) {
		t.Fatalf("float scalar got %v want %v", outPix, want)
	}
}

func TestGeneratePythonBlur(t *testing.T) {
	if _, err := exec.Command("python3", "-c", "import halide").CombinedOutput(); err != nil {
		t.Skip("python halide is not installed")
	}
	root := repoRoot(t)
	dir := t.TempDir()
	err := run([]string{
		"-o", dir,
		"-package", "pyblur",
		"-name", "pyblur",
		"-targets", "linux/amd64",
		filepath.Join(root, "generators", "blur.py"),
	})
	if err != nil {
		t.Fatal(err)
	}
	lib, err := halide.Open(filepath.Join(dir, "libs", "linux_amd64", "libpyblur.so"))
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	fn, err := lib.Func("blur")
	if err != nil {
		t.Fatal(err)
	}
	const w, h = 17, 13
	src := make([]byte, w*h)
	for i := range src {
		src[i] = byte(255 - i)
	}
	dst := make([]byte, w*h)
	in, err := halide.Slice(src, w, h)
	if err != nil {
		t.Fatal(err)
	}
	out, err := halide.Slice(dst, w, h)
	if err != nil {
		t.Fatal(err)
	}
	if err := fn.Call(in, out); err != nil {
		t.Fatal(err)
	}
	want := make([]byte, w*h)
	ref.Blur(ref.Packed(src, w, h), ref.Packed(want, w, h))
	if !bytes.Equal(dst, want) {
		t.Fatal("python blur is not bit-exact")
	}
}

func TestGPUObjectGeneration(t *testing.T) {
	sdk, err := findHalide("")
	if err != nil {
		t.Skip(err)
	}
	root := repoRoot(t)
	work := t.TempDir()
	bin := filepath.Join(work, "generator")
	if err := compileCppGenerator(sdk, []string{filepath.Join(root, "generators", "filters.cpp")}, bin); err != nil {
		t.Fatal(err)
	}
	env := sdkEnv(sdk)
	for _, target := range []string{
		"x86-64-linux-sse41-cuda-no_runtime",
		"arm-64-osx-metal-no_runtime",
	} {
		dir := filepath.Join(work, target)
		if err := runGenerator(bin, "blur", dir, target, "object", env); err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		if _, err := findObject(dir, "blur"); err != nil {
			t.Fatal(err)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "../.."))
}

const addfSource = `#include "Halide.h"
using namespace Halide;
class AddF : public Generator<AddF> {
 public:
    Input<Buffer<uint8_t, 2>> in{"in"};
    Input<float> delta{"delta"};
    Output<Buffer<uint8_t, 2>> out{"out"};
    void generate() {
        Var x("x"), y("y");
        Expr v = cast<int32_t>(in(x, y)) + cast<int32_t>(delta);
        out(x, y) = cast<uint8_t>(clamp(v, 0, 255));
    }
};
HALIDE_REGISTER_GENERATOR(AddF, addf)
`
