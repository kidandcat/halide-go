// Command halide-gen AOT-compiles Halide generators and writes a Go package
// that embeds the shared libraries.
//
//	go run github.com/kidandcat/halide-go/cmd/halide-gen -o ./filters generators/filters.cpp
//
// C++ generators are compiled against a Halide SDK (HALIDE_ROOT, -halide, or
// the directory of a `pip install halide` package). Python generators are
// classes decorated with @hl.generator; the file must be importable and may
// call hl.main() under __name__ == "__main__".
//
// Cross-linking linux/arm64, darwin, windows, and wasm uses zig (https://ziglang.org).
// The host platform can be linked with the system C++ compiler.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "halide-gen: %s\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("halide-gen", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	outDir := fs.String("o", "", "output directory for the Go package")
	pkg := fs.String("package", "", "Go package name (default: output directory name)")
	base := fs.String("name", "", "embedded library base name (default: package name)")
	gens := fs.String("g", "", "comma-separated generator names (default: scan sources)")
	targets := fs.String("targets", "all", "comma-separated targets: linux/amd64,linux/arm64,darwin/amd64,darwin/arm64,windows/amd64,wasm")
	halide := fs.String("halide", "", "Halide SDK root (include/ and lib/)")
	zig := fs.String("zig", "", "path to the zig binary used to cross-link")
	if err := fs.Parse(args); err != nil {
		return err
	}
	sources := fs.Args()
	if len(sources) == 0 {
		return fmt.Errorf("give a C++ or Python generator file")
	}
	if *outDir == "" {
		return fmt.Errorf("-o output directory is required")
	}
	kind, err := sourceKind(sources)
	if err != nil {
		return err
	}
	names := splitComma(*gens)
	if len(names) == 0 {
		names, err = discover(kind, sources)
		if err != nil {
			return err
		}
	}
	if len(names) == 0 {
		return fmt.Errorf("no generators found; pass -g name")
	}
	plats, err := selectPlatforms(*targets)
	if err != nil {
		return err
	}
	packageName := *pkg
	if packageName == "" {
		packageName = filepath.Base(*outDir)
	}
	libBase := *base
	if libBase == "" {
		libBase = packageName
	}
	zigBin := findZig(*zig)

	work, err := os.MkdirTemp("", "halide-gen-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)

	var genBin, pyDriver string
	var sdk halideSDK
	switch kind {
	case kindCpp:
		sdk, err = findHalide(*halide)
		if err != nil {
			return err
		}
		genBin = filepath.Join(work, "generator")
		abs := make([]string, len(sources))
		for i, s := range sources {
			abs[i], err = filepath.Abs(s)
			if err != nil {
				return err
			}
		}
		if err := compileCppGenerator(sdk, abs, genBin); err != nil {
			return err
		}
	case kindPython:
		if len(sources) != 1 {
			return fmt.Errorf("pass one Python generator file")
		}
		abs, err := filepath.Abs(sources[0])
		if err != nil {
			return err
		}
		pyDriver, err = writePythonDriver(abs)
		if err != nil {
			return err
		}
		defer os.Remove(pyDriver)
	}

	if err := os.MkdirAll(filepath.Join(*outDir, "libs"), 0o755); err != nil {
		return err
	}

	var parsed []filter
	for i, p := range plats {
		if err := buildOne(kind, genBin, pyDriver, sdk, sdkEnv(sdk), names, p, zigBin, libBase, *outDir, work); err != nil {
			return fmt.Errorf("%s: %w", p.Key, err)
		}
		if i == 0 {
			parsed, err = readFilters(work, p, names)
			if err != nil {
				return err
			}
		}
	}
	src, err := emitGo(packageName, libBase, parsed)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(*outDir, "halide_pipelines.go"), src, 0o644)
}

type kind int

const (
	kindCpp kind = iota
	kindPython
)

func sourceKind(paths []string) (kind, error) {
	var k kind
	set := false
	for _, p := range paths {
		ext := strings.ToLower(filepath.Ext(p))
		var one kind
		switch ext {
		case ".cpp", ".cc", ".cxx":
			one = kindCpp
		case ".py":
			one = kindPython
		default:
			return 0, fmt.Errorf("%s: want a .cpp or .py generator", p)
		}
		if set && one != k {
			return 0, fmt.Errorf("do not mix C++ and Python generators in one run")
		}
		k = one
		set = true
	}
	return k, nil
}

func discover(k kind, paths []string) ([]string, error) {
	var names []string
	seen := map[string]bool{}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var found []string
		if k == kindCpp {
			found = registeredGenerators(string(b))
		} else {
			found = pythonGeneratorNames(string(b))
		}
		for _, n := range found {
			if !seen[n] {
				seen[n] = true
				names = append(names, n)
			}
		}
	}
	return names, nil
}

func buildOne(k kind, genBin, pyDriver string, sdk halideSDK, env []string, names []string, p platform, zig, libBase, outDir, work string) error {
	dir := filepath.Join(work, strings.ReplaceAll(p.Key, "/", "_"))
	target := p.Halide + "-no_runtime"
	for _, name := range names {
		switch k {
		case kindCpp:
			if err := runGenerator(genBin, name, dir, target, "object,c_header,function_info_header", env); err != nil {
				return err
			}
		case kindPython:
			if err := runPythonGenerator(pyDriver, name, dir, target); err != nil {
				return err
			}
		}
	}
	rtTarget := p.Halide
	switch k {
	case kindCpp:
		if err := runRuntime(genBin, dir, rtTarget, env); err != nil {
			return err
		}
	case kindPython:
		if err := runPythonRuntime(pyDriver, dir, rtTarget); err != nil {
			return err
		}
	}
	var objs []string
	rt, err := findObject(dir, "runtime")
	if err != nil {
		return err
	}
	objs = append(objs, rt)
	for _, name := range names {
		obj, err := findObject(dir, name)
		if err != nil {
			return err
		}
		objs = append(objs, obj)
	}
	dest := filepath.Join(outDir, "libs", p.dir(), p.filename(libBase))
	return linkPlatform(p, zig, libBase, objs, dest)
}

func readFilters(work string, p platform, names []string) ([]filter, error) {
	dir := filepath.Join(work, strings.ReplaceAll(p.Key, "/", "_"))
	var out []filter
	for _, name := range names {
		info, err := os.ReadFile(filepath.Join(dir, name+".function_info.h"))
		if err != nil {
			return nil, fmt.Errorf("read %s function info: %w", name, err)
		}
		f, err := parseFunctionInfo(string(info), name)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		header, err := os.ReadFile(filepath.Join(dir, name+".h"))
		if err != nil {
			return nil, err
		}
		params, err := parseCSignature(string(header), name)
		if err != nil {
			return nil, err
		}
		if len(params) != len(f.Args) {
			return nil, fmt.Errorf("%s: C signature has %d parameters, metadata has %d", name, len(params), len(f.Args))
		}
		out = append(out, f)
	}
	return out, nil
}

func splitComma(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
