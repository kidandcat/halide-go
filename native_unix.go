//go:build darwin || freebsd || linux

package halide

import "github.com/ebitengine/purego"

func loadShared(path string) (uintptr, error) {
	return purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_LOCAL)
}

func lookupShared(handle uintptr, name string) (uintptr, error) {
	return purego.Dlsym(handle, name)
}

// closeShared unloads a native library. Library.Close does not call it.
// dlclose runs Halide's thread-pool destructor and, on macOS, dyld's
// thread-local teardown. That segfaults inside Dlclose when a worker is
// still leaving the image (arm64 CI, sigcode 2, PC == fault address).
func closeShared(handle uintptr) error {
	return purego.Dlclose(handle)
}
