package halide

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRuntimeErrorHandler(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("runs the committed linux/amd64 library")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller")
	}
	root := filepath.Dir(file)
	so := filepath.Join(root, "filters", "libs", "linux_amd64", "libfilters.so")
	lib, err := Open(so)
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	fn, err := lib.Func("blur")
	if err != nil {
		t.Fatal(err)
	}
	src := []byte{1, 2, 3, 4}
	dst := []byte{0, 0, 0, 0}
	in, err := Slice(src, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Slice(dst, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	out.dims[0].Extent = -1
	err = fn.Call(in, out)
	pe, ok := err.(*PipelineError)
	if !ok {
		t.Fatalf("got %T %v", err, err)
	}
	if pe.Code != -28 {
		t.Fatalf("code %d: %v", pe.Code, pe)
	}
	if pe.Text == "" {
		t.Fatalf("error handler captured no text: %v", pe)
	}
	// The library still runs a valid call after an error.
	out.dims[0].Extent = 2
	for i := range dst {
		dst[i] = 0
	}
	if err := fn.Call(in, out); err != nil {
		t.Fatal(err)
	}

	wasm, err := os.ReadFile(filepath.Join(root, "filters", "libs", "wasm", "filters.wasm"))
	if err != nil {
		t.Fatal(err)
	}
	wl, err := OpenWASM(wasm)
	if err != nil {
		t.Fatal(err)
	}
	defer wl.Close()
	wfn, err := wl.Func("blur")
	if err != nil {
		t.Fatal(err)
	}
	out.dims[0].Extent = -1
	err = wfn.Call(in, out)
	pe, ok = err.(*PipelineError)
	if !ok {
		t.Fatalf("wasm got %T %v", err, err)
	}
	if pe.Code == 0 || pe.Text == "" {
		t.Fatalf("wasm error: %v", pe)
	}
}
