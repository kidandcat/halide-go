# halide-go

Call [Halide](https://halide-lang.org) pipelines from Go without cgo.

A pipeline is compiled ahead of time to a shared library (`.so`, `.dylib`, `.dll`) or to WebAssembly. This module loads the native library with [purego](https://github.com/ebitengine/purego) and the wasm module with [wazero](https://github.com/tetratelabs/wazero). Both paths use `CGO_ENABLED=0`. Go slices and `image.Gray` / `image.RGBA` values are wrapped as `halide_buffer_t` and passed straight through; outputs are written back into that memory.

`cmd/halide-gen` compiles a C++ or Python Halide generator, links one library per platform, and writes a Go wrapper that embeds those libraries with `go:embed`. A program that imports the wrapper is a single binary. The example filters in [`filters`](filters) are already built, so using them does not require a Halide installation.

## Quick start

```bash
git clone https://github.com/kidandcat/halide-go
cd halide-go
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go run ./examples/demo
```

The demo runs a 1024×1024 box blur, checks it against the pure Go reference, and prints both times. A passing run ends with `diffs 0`.

Use the embedded filters from your own program:

```go
package main

import (
	"fmt"

	"github.com/kidandcat/halide-go"
	"github.com/kidandcat/halide-go/filters"
)

func main() {
	src := make([]byte, 64*48)
	dst := make([]byte, len(src))
	in, err := halide.Slice(src, 64, 48)
	if err != nil {
		panic(err)
	}
	out, err := halide.Slice(dst, 64, 48)
	if err != nil {
		panic(err)
	}
	if err := filters.Blur(in, out); err != nil {
		panic(err)
	}
	fmt.Println(dst[0])
}
```

`filters.Blur` loads the embedded library once. `Load` returns a `*Pipeline` when you want an explicit lifetime (`Close` shuts down Halide's thread pool):

```go
pipe, err := filters.Load()
if err != nil {
	panic(err)
}
defer pipe.Close()
err = pipe.Blur(in, out)
```

Force the wasm backend for one call, or for the whole build:

```go
pipe, err := filters.Load(halide.WithBackend(halide.WASM))
```

```bash
CGO_ENABLED=0 go test -tags halidewasm ./...
```

## Pipelines

| Filter | Arguments | Definition |
| --- | --- | --- |
| `Blur` | in, out | 3×3 box, repeat-edge, integer sum / 9 |
| `Brightness` | in, brightness `int32`, contrast `int32`, out | `((p-128)*contrast)/256 + 128 + brightness`, clamped to 0..255. Contrast is 8.8 fixed point: 256 means 1.0. Division truncates toward zero. |
| `Grayscale` | in, out | in is interleaved `(channel, x, y)` with channel extent 4. `Y = (77*R + 150*G + 29*B) >> 8` |
| `Sobel` | in, out | repeat-edge Sobel magnitude `min(255, abs(gx)+abs(gy))` |
| `Resize` | in, out | nearest neighbor. Output extent is the destination size. `sx = (x * inWidth) / outWidth` |

`halide.Slice` builds a packed buffer (dimension 0 is innermost). `halide.FromGray` and `halide.FromRGBA` alias `image.Image` pixel storage, including a sub-rectangle whose stride is wider than the width. `(*Buffer).GrayImage` copies a 2-D uint8 buffer out into a new `image.Gray`.

Pure Go copies of the same formulas live in [`ref`](ref) and are what the tests compare against.

## Generate a pipeline

`halide-gen` accepts a C++ file that uses `HALIDE_REGISTER_GENERATOR`, or a Python file that decorates a class with `@hl.generator` (`pip install halide`).

```bash
# C++ SDK (include/ and lib/ or lib64/). A `pip install halide` tree is detected too.
export HALIDE_ROOT=/opt/Halide

# Host-only library. Needs g++ (or clang++) and the Halide SDK.
go run github.com/kidandcat/halide-go/cmd/halide-gen \
  -o ./mypipe -package mypipe -targets linux/amd64 \
  generators/filters.cpp

# Every shipped target. Cross-linking uses zig (https://ziglang.org).
go run github.com/kidandcat/halide-go/cmd/halide-gen \
  -o ./mypipe -package mypipe \
  generators/filters.cpp
```

From a package that should regenerate itself:

```go
//go:generate go run github.com/kidandcat/halide-go/cmd/halide-gen -o . -package filters -name filters ../generators/filters.cpp
```

```bash
go generate ./filters
```

Targets (`-targets`, default `all`):

| `-targets` | File | Halide target |
| --- | --- | --- |
| `linux/amd64` | `libs/linux_amd64/lib<name>.so` | `x86-64-linux-sse41-avx-avx2-fma` |
| `linux/arm64` | `libs/linux_arm64/lib<name>.so` | `arm-64-linux` (NEON) |
| `darwin/amd64` | `libs/darwin_amd64/lib<name>.dylib` | `x86-64-osx-sse41-avx-avx2-fma` |
| `darwin/arm64` | `libs/darwin_arm64/lib<name>.dylib` | `arm-64-osx` (NEON) |
| `windows/amd64` | `libs/windows_amd64/<name>.dll` | `x86-64-windows-sse41-avx-avx2-fma` |
| `wasm` | `libs/wasm/<name>.wasm` | `wasm-32-wasmrt-wasm_simd128` |

x86-64 is AVX2 + FMA + SSE4.1 (Haswell and newer). The host used to build the committed libraries also has AVX-512; that is intentionally not in the target, so the `.so` loads on machines without AVX-512. Each library contains one Halide runtime plus every filter, so there is one thread pool per process per loaded library.

The tool compiles each filter with `target=<triple>-no_runtime` and links those objects with a single runtime object. At runtime the native file is extracted to a content-addressed directory under the temp dir and opened with `dlopen` (`LoadLibrary` on Windows). The same path shares one mapping. `Close` joins Halide's worker threads when the last user of that file closes, and leaves the mapping loaded for the rest of the process. Wasm is instantiated in process.

Python example (one generator per file):

```bash
pip install halide
go run github.com/kidandcat/halide-go/cmd/halide-gen \
  -o ./out -package pyblur -targets linux/amd64 \
  generators/blur.py
```

## API

- `Open(path)` loads a native shared library. `OpenWASM(module)` instantiates a wasm module. `OpenEmbedded(fsys, base, opts...)` picks the file out of an `embed.FS` with the layout above.
- `Library.Func(name)` resolves `name_argv` and, when it is exported, `name_metadata`.
- `Func.Call(args...)` checks the metadata (rank, element type, scalar Go type) and runs the pipeline. A non-zero Halide status is a `*PipelineError` with the runtime's error text.
- Buffer memory, the shape slice, and the `halide_buffer_t` are pinned with `runtime.Pinner` for the native call. Calls on one `Library` are serialized.

`halide_buffer_t` is the 56-byte LP64/LLP64 layout (host at offset 16, `halide_dimension_t*` at offset 40). Strides are in elements. The host pointer addresses the min coordinate of every dimension. The wasm backend writes the 40-byte wasm32 layout itself.

## GPU

Halide can AOT-compile the same generator for CUDA and Metal. `cmd/halide-gen`'s integration test, when a Halide SDK is present, emits a CUDA object (`x86-64-linux-sse41-cuda-no_runtime`) and a Metal object (`arm-64-osx-metal-no_runtime`) and checks that the object file exists.

Those objects are not linked into the embedded libraries, and nothing in CI executes them. A runnable CUDA library needs the CUDA driver at load time. A Metal library runs only on Apple hardware. The Go call path (buffer struct, `name_argv`, error codes) is the same once such a library is linked; that last step is not done here.

## Benchmarks

Measured with `CGO_ENABLED=0 go test -bench=. -benchtime=1s -count=1 ./filters/` on the machine that produced the committed libraries:

- 4 cores, Intel Xeon, AVX2 and AVX-512 present
- Go 1.22.2, `linux/amd64`
- the native library is the AVX2 build, not AVX-512

| Benchmark | ns/op | What it is |
| --- | ---: | --- |
| `BenchmarkBlurHalide4096` | 533,032 | 3×3 blur, 4096², native |
| `BenchmarkBlurWASM4096` | 39,665,085 | same, wasm-simd (includes copy into wasm memory) |
| `BenchmarkBlurGoParallel4096` | 57,595,566 | same, pure Go, `GOMAXPROCS` goroutines |
| `BenchmarkBlurGo4096` | 216,708,559 | same, pure Go, one goroutine |
| `BenchmarkBlurHalide512` | 22,012 | native blur, 512² |
| `BenchmarkBrightnessHalide2048` | 1,418,727 | native brightness/contrast, 2048² |
| `BenchmarkBrightnessGoParallel2048` | 1,159,475 | pure Go parallel, same |
| `BenchmarkGrayscaleHalide2048` | 1,072,911 | native grayscale, 2048² RGBA |
| `BenchmarkGrayscaleGoParallel2048` | 1,366,759 | pure Go parallel, same |
| `BenchmarkSobelHalide2048` | 1,164,809 | native Sobel, 2048² |
| `BenchmarkSobelGoParallel2048` | 15,255,358 | pure Go parallel, same |
| `BenchmarkResizeHalide2048` | 552,792 | native nearest resize 2048² → 1024² |

On this box the native 4096² blur is about 0.53 ms, the 4-goroutine Go blur is about 58 ms, and wasm is about 40 ms. Brightness is a single arithmetic expression; the parallel Go loop is in the same range as Halide there. Sobel, which reads a 3×3 neighborhood, is where the compiled pipeline pulls ahead. `examples/demo` on the same machine reported about 85 µs (Halide) and 5.1 ms (parallel Go) for a 1024² blur, with zero byte differences.

Numbers move with core count and with the AVX level. Re-run the benchmark command on the machine you care about.

## Testing

`CGO_ENABLED=0 go test ./...` compares every filter to `ref` on native and on wasm, for sizes 1×1, 2×3, 7×5, 31×32, 64×63, 128×128, and 4096×4096, plus an `image.Gray` sub-rectangle. Outputs must match bit for bit.

GitHub Actions (`.github/workflows/ci.yml`) runs that suite, and the suite again with `-tags halidewasm`, on `ubuntu-latest`, `macos-latest`, and `ubuntu-24.04-arm`. A separate job installs Halide with pip and runs `cmd/halide-gen`'s integration tests (C++ generator, Python generator, float scalar ABI, CUDA/Metal object emission) for `linux/amd64`.

What that does not execute:

- `darwin/amd64` and `windows/amd64` libraries are cross-compiled and committed. `go test` typechecks those GOOS/GOARCH combinations. The Windows DLL depends on the Universal CRT (`api-ms-win-crt-*.dll`, `KERNEL32.dll`) and was not loaded on a Windows machine. `darwin/amd64` was not loaded on Intel macOS; `macos-latest` in CI is Apple silicon and loads `darwin/arm64`.
- `linux/arm64` is loaded by the `ubuntu-24.04-arm` job when that runner is available. It was not executed on the x86-64 machine that built it. The arm64 `.so` needs glibc 2.17 or newer.
- CUDA and Metal, as described above.

## Design notes

Halide's AOT entry point is `int name_argv(void **args)`. Buffer arguments are pointers to `halide_buffer_t`. Scalar arguments are pointers to the scalar. `name_metadata` describes the signature (argument kind, dimensions, type) and `Call` rejects a Go value that does not match.

The native error handler is a purego callback. It copies the C string into a fixed buffer (no allocation on the Halide thread) and `*PipelineError` includes that text. Wasm has no function-pointer callback into Go; the runtime's default handler writes the message and then calls `abort`. The wasm host records that write and returns it as a `*PipelineError`.

Wasm is single-threaded (`parallel` is not scheduled on that target). Each call copies buffer contents into the module's linear memory and copies outputs back. Native calls do not copy the pixels.

## Limitations

- Little-endian only. Every target this module builds is little-endian.
- One `Library` runs one call at a time. Two libraries can run at the same time.
- Each native library installs one `purego.NewCallback`, which purego does not release. Load a handful of libraries, not one per request.
- `Close` does not call `dlclose` or `FreeLibrary`. Halide's runtime registers a destructor that joins the thread pool, and macOS runs dyld thread-local teardown as part of `dlclose`. Unloading while a worker is still inside the image segfaults (`purego.Dlclose`, arm64, fault address equal to the PC). The last `Close` calls `halide_shutdown_thread_pool` instead, which joins the workers while the file is still mapped.
- The Go slice must stay still for the duration of `Call`. Do not reslice or mutate it from another goroutine during the call. `Call` pins it so the garbage collector will not move it.
- A blur (or Sobel) that uses the same buffer for input and output reads neighbors while it writes them. Use two buffers.
- Wasm copies, so a 4096² frame is tens of milliseconds even when the compiled loop is short. Use it when you want one portable binary, and the native library when you want the speed.
- Generators are compiled with bounds checks left on, so a bad buffer returns an error instead of writing off the end. That costs a little compared to `-no_asserts`.
- Big images and odd sizes are covered by the tests. The x86 and arm schedules use `TailStrategy::GuardWithIf` so a width or height that is not a multiple of the vector length still matches the scalar formula.

## License

[MIT](LICENSE)
