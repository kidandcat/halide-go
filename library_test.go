package halide

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestReuseAfterClose(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("runs the committed linux/amd64 library")
	}
	so := pipelineSO(t)
	blur := func(lib *Library) {
		t.Helper()
		fn, err := lib.Func("blur")
		if err != nil {
			t.Fatal(err)
		}
		const n = 64
		src := make([]byte, n*n)
		dst := make([]byte, n*n)
		for i := range src {
			src[i] = byte(i)
		}
		in, err := Slice(src, n, n)
		if err != nil {
			t.Fatal(err)
		}
		out, err := Slice(dst, n, n)
		if err != nil {
			t.Fatal(err)
		}
		if err := fn.Call(in, out); err != nil {
			t.Fatal(err)
		}
		if dst[0] == 0 && dst[n+1] == 0 {
			t.Fatal("blur wrote nothing")
		}
	}

	// Two live opens share the mapping. Closing one must not shut the
	// pool down underneath the other, and must not unload the file.
	a, err := Open(so)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open(so)
	if err != nil {
		t.Fatal(err)
	}
	if a.handle != b.handle {
		t.Fatalf("handles differ: %#x %#x", a.handle, b.handle)
	}
	blur(a)
	blur(b)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	blur(b)
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}

	// The last Close joins the workers and leaves the mapping in place.
	// Opening again must run, including after the pool has been shut down.
	for i := 0; i < 8; i++ {
		lib, err := Open(so)
		if err != nil {
			t.Fatal(err)
		}
		blur(lib)
		if err := lib.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func pipelineSO(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller")
	}
	return filepath.Join(filepath.Dir(file), "filters", "libs", "linux_amd64", "libfilters.so")
}

func TestRuntimeErrorHandler(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("runs the committed linux/amd64 library")
	}
	so := pipelineSO(t)
	root := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(so))))
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
