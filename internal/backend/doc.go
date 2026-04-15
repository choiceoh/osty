// Package backend defines the small contract shared by concrete Osty
// code-generation backends.
//
// The current CLI still calls the Go transpiler directly. This package is the
// migration point for making that path backend-aware before adding LLVM output:
// it owns stable backend names, emit modes, artifact/cache layout helpers, and
// the minimal interface concrete backends will implement.
package backend
