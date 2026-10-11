//go:build windows

package halide

import "syscall"

func loadShared(path string) (uintptr, error) {
	h, err := syscall.LoadLibrary(path)
	return uintptr(h), err
}

func lookupShared(handle uintptr, name string) (uintptr, error) {
	addr, err := syscall.GetProcAddress(syscall.Handle(handle), name)
	return uintptr(addr), err
}

// closeShared unloads a native library. Library.Close does not call it.
// FreeLibrary has the same thread-pool teardown hazard as dlclose.
func closeShared(handle uintptr) error {
	return syscall.FreeLibrary(syscall.Handle(handle))
}
