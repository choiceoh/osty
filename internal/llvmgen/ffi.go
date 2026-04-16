// ffi.go — foreign-function-interface entry points for the LLVM backend.
//
// Currently only owns `fileUnsupportedDiagnostic`, which rejects `use go
// "..."` (Go FFI — never supported in LLVM) and unknown `use runtime ...`
// paths at module entry.
//
// NOTE(osty-migration): this file is deliberately thin because the LLVM
// backend has no Go-FFI emission path. When Go FFI eventually needs an
// LLVM lowering, the emit routes would land here next to the detection.
// Runtime-FFI emission lives in runtime_ffi.go; these are separate concerns
// kept in separate files for clarity.
package llvmgen

import "github.com/osty/osty/internal/ast"

func fileUnsupportedDiagnostic(file *ast.File) (UnsupportedDiagnostic, bool) {
	for _, use := range file.Uses {
		if use != nil && use.IsGoFFI {
			return UnsupportedDiagnosticFor("go-ffi", use.GoPath), true
		}
		if use != nil && use.IsRuntimeFFI && !llvmIsKnownRuntimeFfiPath(use.RuntimePath) {
			return UnsupportedDiagnosticFor("runtime-ffi", use.RuntimePath), true
		}
	}
	return UnsupportedDiagnostic{}, false
}
