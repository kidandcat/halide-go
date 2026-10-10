// Package filters is a set of example Halide pipelines compiled ahead of time
// and called from Go without cgo.
//
// The shared libraries under libs/ are produced by:
//
//	go generate ./filters
//
// That command needs a Halide SDK (HALIDE_ROOT or `pip install halide`) and,
// for the non-host targets, zig. The committed libraries are what
// Load uses, so a caller does not need Halide installed.
package filters

//go:generate go run github.com/kidandcat/halide-go/cmd/halide-gen -o . -package filters -name filters ../generators/filters.cpp
