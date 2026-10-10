package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type platform struct {
	Key     string
	GOOS    string
	GOARCH  string
	Zig     string
	Halide  string
	Pattern string // fmt pattern for the shared library file name
	WASM    bool
	Win     bool
}

func allPlatforms() []platform {
	return []platform{
		{Key: "linux/amd64", GOOS: "linux", GOARCH: "amd64", Zig: "x86_64-linux-gnu", Halide: "x86-64-linux-sse41-avx-avx2-fma", Pattern: "lib%s.so"},
		{Key: "linux/arm64", GOOS: "linux", GOARCH: "arm64", Zig: "aarch64-linux-gnu", Halide: "arm-64-linux", Pattern: "lib%s.so"},
		{Key: "darwin/amd64", GOOS: "darwin", GOARCH: "amd64", Zig: "x86_64-macos", Halide: "x86-64-osx-sse41-avx-avx2-fma", Pattern: "lib%s.dylib"},
		{Key: "darwin/arm64", GOOS: "darwin", GOARCH: "arm64", Zig: "aarch64-macos", Halide: "arm-64-osx", Pattern: "lib%s.dylib"},
		{Key: "windows/amd64", GOOS: "windows", GOARCH: "amd64", Zig: "x86_64-windows-gnu", Halide: "x86-64-windows-sse41-avx-avx2-fma", Pattern: "%s.dll", Win: true},
		{Key: "wasm", GOOS: "wasm", GOARCH: "wasm", Halide: "wasm-32-wasmrt-wasm_simd128", Pattern: "%s.wasm", WASM: true},
	}
}

func selectPlatforms(spec string) ([]platform, error) {
	all := allPlatforms()
	if spec == "" || spec == "all" {
		return all, nil
	}
	by := map[string]platform{}
	for _, p := range all {
		by[p.Key] = p
	}
	var out []platform
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		p, ok := by[part]
		if !ok {
			return nil, fmt.Errorf("unknown target %q (want linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64, wasm)", part)
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no targets selected")
	}
	return out, nil
}

func (p platform) dir() string {
	if p.WASM {
		return "wasm"
	}
	return p.GOOS + "_" + p.GOARCH
}

func (p platform) filename(base string) string {
	return fmt.Sprintf(p.Pattern, base)
}

type halideSDK struct {
	Root    string
	Include string
	LibDir  string
	GenGen  string
}

func findHalide(flag string) (halideSDK, error) {
	root := flag
	if root == "" {
		root = os.Getenv("HALIDE_ROOT")
	}
	if root == "" {
		if p, err := pythonHalideDir(); err == nil {
			root = p
		}
	}
	if root == "" {
		for _, c := range []string{"/usr/local", "/opt/halide", "/usr"} {
			if fileExists(filepath.Join(c, "include", "Halide.h")) {
				root = c
				break
			}
		}
	}
	if root == "" {
		return halideSDK{}, fmt.Errorf("Halide SDK not found; set -halide or HALIDE_ROOT, or pip install halide")
	}
	sdk := halideSDK{Root: root, Include: filepath.Join(root, "include")}
	if !fileExists(filepath.Join(sdk.Include, "Halide.h")) {
		return halideSDK{}, fmt.Errorf("Halide.h not in %s", sdk.Include)
	}
	for _, lib := range []string{"lib", "lib64"} {
		dir := filepath.Join(root, lib)
		if fileExists(filepath.Join(dir, "libHalide_GenGen.a")) || fileExists(filepath.Join(dir, "libHalide.so")) || fileExists(filepath.Join(dir, "libHalide.dylib")) {
			sdk.LibDir = dir
			break
		}
	}
	if sdk.LibDir == "" {
		return halideSDK{}, fmt.Errorf("libHalide not found under %s", root)
	}
	sdk.GenGen = filepath.Join(sdk.LibDir, "libHalide_GenGen.a")
	return sdk, nil
}

func pythonHalideDir() (string, error) {
	cmd := exec.Command("python3", "-c", "import halide, os; print(os.path.dirname(halide.__file__))")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func findZig(flag string) string {
	if flag != "" {
		return flag
	}
	if z := os.Getenv("ZIG"); z != "" {
		return z
	}
	if p, err := exec.LookPath("zig"); err == nil {
		return p
	}
	return ""
}

func findCXX() (string, error) {
	for _, name := range []string{"g++", "c++", "clang++"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("need g++ or clang++ to compile a C++ Halide generator")
}

func compileCppGenerator(sdk halideSDK, sources []string, out string) error {
	cxx, err := findCXX()
	if err != nil {
		return err
	}
	if !fileExists(sdk.GenGen) {
		return fmt.Errorf("libHalide_GenGen.a not found in %s", sdk.LibDir)
	}
	args := []string{
		"-std=c++17", "-fno-rtti",
		"-I", sdk.Include,
	}
	args = append(args, sources...)
	args = append(args,
		sdk.GenGen,
		"-o", out,
		"-L", sdk.LibDir,
		"-lHalide", "-lz", "-ldl", "-lpthread",
		"-Wl,-rpath,"+sdk.LibDir,
	)
	return runCmd(cxx, args...)
}

func runGenerator(bin string, genName, outDir, target string, extraEmit string, env []string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	args := []string{
		"-g", genName,
		"-f", genName,
		"-n", genName,
		"-e", extraEmit,
		"-o", outDir,
		"target=" + target,
	}
	return runCmdEnv(bin, env, args...)
}

func runRuntime(bin, outDir, target string, env []string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	args := []string{
		"-r", "runtime",
		"-n", "runtime",
		"-e", "object",
		"-o", outDir,
		"target=" + target,
	}
	return runCmdEnv(bin, env, args...)
}

func pythonEnv() []string { return os.Environ() }

func sdkEnv(sdk halideSDK) []string {
	env := os.Environ()
	return append(env, "LD_LIBRARY_PATH="+sdk.LibDir)
}

func writePythonDriver(user string) (string, error) {
	f, err := os.CreateTemp("", "halide-gen-*.py")
	if err != nil {
		return "", err
	}
	defer f.Close()
	fmt.Fprintf(f, `import importlib.util
import halide as hl
spec = importlib.util.spec_from_file_location("halide_user_gen", %q)
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)
raise SystemExit(hl.main())
`, user)
	return f.Name(), nil
}

func runPythonGenerator(driver, genName, outDir, target string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	args := []string{
		driver,
		"-g", genName,
		"-f", genName,
		"-n", genName,
		"-e", "object,c_header,function_info_header",
		"-o", outDir,
		"target=" + target,
	}
	return runCmd("python3", args...)
}

func runPythonRuntime(driver, outDir, target string) error {
	args := []string{
		driver,
		"-r", "runtime",
		"-n", "runtime",
		"-e", "object",
		"-o", outDir,
		"target=" + target,
	}
	return runCmd("python3", args...)
}

func linkPlatform(p platform, zig, base string, objs []string, outPath string) error {
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	if p.WASM {
		if zig == "" {
			return fmt.Errorf("linking wasm needs zig (https://ziglang.org); pass -zig or set ZIG")
		}
		args := []string{
			"wasm-ld", "--no-entry", "--allow-undefined", "--export-dynamic",
			"--export=__heap_base",
		}
		// objs are filter objects plus runtime. Export each filter symbol.
		for _, obj := range objs {
			base := strings.TrimSuffix(filepath.Base(obj), filepath.Ext(obj))
			if base == "runtime" || strings.HasPrefix(base, "chkstk") || base == "fltused" {
				continue
			}
			args = append(args, "--export="+base, "--export="+base+"_argv", "--export="+base+"_metadata")
		}
		args = append(args, "-o", outPath)
		args = append(args, objs...)
		return runCmd(zig, args...)
	}
	if p.Win {
		if zig == "" {
			return fmt.Errorf("linking windows/amd64 needs zig; pass -zig or set ZIG")
		}
		extra, err := windowsSupportObjs(zig)
		if err != nil {
			return err
		}
		args := []string{"cc", "-target", p.Zig, "-shared", "-o", outPath}
		args = append(args, objs...)
		args = append(args, extra...)
		if err := runCmd(zig, args...); err != nil {
			return err
		}
		// zig's mingw linker writes an import library and a PDB beside the DLL.
		// Neither belongs in the embedded tree.
		for _, ext := range []string{".pdb", ".lib", ".exp"} {
			os.Remove(strings.TrimSuffix(outPath, filepath.Ext(outPath)) + ext)
		}
		return nil
	}
	if isHost(p) {
		if cxx, err := findCXX(); err == nil {
			args := []string{"-shared", "-o", outPath}
			args = append(args, objs...)
			if err := runCmd(cxx, args...); err == nil {
				return nil
			}
		}
	}
	if zig == "" {
		return fmt.Errorf("linking %s needs zig, or a host C++ compiler when building for %s/%s", p.Key, runtime.GOOS, runtime.GOARCH)
	}
	args := []string{"cc", "-target", p.Zig, "-shared", "-o", outPath}
	args = append(args, objs...)
	return runCmd(zig, args...)
}

func isHost(p platform) bool {
	return !p.WASM && !p.Win && runtime.GOOS == p.GOOS && runtime.GOARCH == p.GOARCH
}

func windowsSupportObjs(zig string) ([]string, error) {
	dir, err := os.MkdirTemp("", "halide-gen-win-*")
	if err != nil {
		return nil, err
	}
	asm := filepath.Join(dir, "chkstk.S")
	if err := os.WriteFile(asm, []byte(chkstkAsm), 0o644); err != nil {
		return nil, err
	}
	cfile := filepath.Join(dir, "fltused.c")
	if err := os.WriteFile(cfile, []byte("int _fltused = 0;\n"), 0o644); err != nil {
		return nil, err
	}
	obj1 := filepath.Join(dir, "chkstk.obj")
	obj2 := filepath.Join(dir, "fltused.obj")
	if err := runCmd(zig, "cc", "-target", "x86_64-windows-gnu", "-c", asm, "-o", obj1); err != nil {
		return nil, err
	}
	if err := runCmd(zig, "cc", "-target", "x86_64-windows-gnu", "-c", cfile, "-o", obj2); err != nil {
		return nil, err
	}
	return []string{obj1, obj2}, nil
}

const chkstkAsm = `
    .text
    .globl __chkstk
    .def __chkstk; .scl 2; .type 32; .endef
__chkstk:
    push %rcx
    push %rax
    cmp $0x1000, %rax
    lea 24(%rsp), %rcx
    jb 1f
2:
    sub $0x1000, %rcx
    test %rcx, (%rcx)
    sub $0x1000, %rax
    cmp $0x1000, %rax
    ja 2b
1:
    sub %rax, %rcx
    test %rcx, (%rcx)
    pop %rax
    pop %rcx
    ret
`

func findObject(dir, name string) (string, error) {
	for _, ext := range []string{".o", ".obj"} {
		p := filepath.Join(dir, name+ext)
		if fileExists(p) {
			return p, nil
		}
	}
	return "", fmt.Errorf("Halide did not write %s.o or %s.obj in %s", name, name, dir)
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func runCmd(name string, args ...string) error {
	return runCmdEnv(name, nil, args...)
}

func runCmdEnv(name string, env []string, args ...string) error {
	cmd := exec.Command(name, args...)
	if env != nil {
		cmd.Env = env
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return fmt.Errorf("%s: %w", name, err)
		}
		return fmt.Errorf("%s: %w\n%s", name, err, msg)
	}
	return nil
}
