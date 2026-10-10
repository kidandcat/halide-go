package halide

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
)

// OpenEmbedded loads the library embedded for this process.
//
// fsys is typically an embed.FS. Files are laid out as:
//
//	libs/<goos>_<goarch>/lib<base>.so
//	libs/<goos>_<goarch>/lib<base>.dylib
//	libs/<goos>_<goarch>/<base>.dll
//	libs/wasm/<base>.wasm
//
// The native file is written to a content-addressed temp directory and opened
// with dlopen (LoadLibrary on Windows). The wasm file is instantiated in
// process. WithBackend selects which file is used; the halidewasm build tag
// selects wasm when no option is set.
func OpenEmbedded(fsys fs.FS, base string, opts ...Option) (*Library, error) {
	if base == "" {
		return nil, fmt.Errorf("halide: empty library name")
	}
	cfg := applyOptions(opts)
	rel, err := embeddedPath(base, cfg.backend)
	if err != nil {
		return nil, err
	}
	data, err := fs.ReadFile(fsys, rel)
	if err != nil {
		return nil, fmt.Errorf("halide: embedded library %s: %w", rel, err)
	}
	if cfg.backend == WASM {
		return OpenWASM(data)
	}
	return OpenBytes(path.Base(rel), data)
}

func embeddedPath(base string, backend Backend) (string, error) {
	switch backend {
	case WASM:
		return path.Join("libs", "wasm", base+".wasm"), nil
	case Native:
		file, err := sharedFilename(base)
		if err != nil {
			return "", err
		}
		dir := runtime.GOOS + "_" + runtime.GOARCH
		return path.Join("libs", dir, file), nil
	default:
		return "", fmt.Errorf("halide: unknown backend %d", backend)
	}
}

func sharedFilename(base string) (string, error) {
	switch runtime.GOOS {
	case "windows":
		return base + ".dll", nil
	case "darwin":
		return "lib" + base + ".dylib", nil
	case "linux":
		return "lib" + base + ".so", nil
	default:
		return "", fmt.Errorf("halide: no native shared-library convention for %s", runtime.GOOS)
	}
}

// OpenBytes writes a native shared library to a temp file and loads it.
// name is the file name (for example libfilters.so). The same bytes are not
// written twice: the path is content-addressed.
func OpenBytes(name string, data []byte) (*Library, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("halide: empty library %s", name)
	}
	path, err := materialize(name, data)
	if err != nil {
		return nil, err
	}
	return Open(path)
}

func materialize(name string, data []byte) (string, error) {
	if name == "" || filepath.Base(name) != name {
		return "", fmt.Errorf("halide: library name %q must be a single path element", name)
	}
	sum := sha256.Sum256(data)
	dir := filepath.Join(os.TempDir(), "halide-go", hex.EncodeToString(sum[:16]))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, name)
	if st, err := os.Stat(dst); err == nil && st.Size() == int64(len(data)) {
		return dst, nil
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return "", err
	}
	return dst, nil
}
