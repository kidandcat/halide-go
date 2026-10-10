//go:build darwin || freebsd || linux

package halide

import "github.com/ebitengine/purego"

func loadShared(path string) (uintptr, error) {
	return purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_LOCAL)
}

func lookupShared(handle uintptr, name string) (uintptr, error) {
	return purego.Dlsym(handle, name)
}

func closeShared(handle uintptr) error {
	return purego.Dlclose(handle)
}
