// Package halide calls ahead-of-time Halide pipelines from Go without cgo.
//
// A pipeline is an ordinary shared library (.so, .dylib, or .dll) produced by
// the Halide AOT compiler. This package loads it with
// github.com/ebitengine/purego and passes arguments as halide_buffer_t values
// backed by Go memory. The same Call API can run a WebAssembly build of the
// pipeline under wazero (build tag halidewasm, or [WithBackend] with [WASM]).
//
// Go memory that a call hands to native code is pinned for the duration of
// the call and kept alive until the call returns. Halide's thread pool runs
// inside the loaded library; [Library.Close] shuts that pool down.
//
// The usual way to produce a library is the halide-gen command:
//
//	go run github.com/kidandcat/halide-go/cmd/halide-gen -o ./filters generators/filters.cpp
//
// Prebuilt example pipelines live in the filters subpackage.
package halide
