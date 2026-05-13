// format_policy.go is the Go snapshot of
// toolchain/format_policy.osty. Osty is the source of truth;
// the drift test in this package keeps the two in sync.
package runner

import "strings"

// CSurfaceLib mirrors toolchain/format_policy.osty's CSurfaceLib.
// Ok=false leaves Lib empty and signals the canonical runtime
// printer should run.
//
// Osty: toolchain/format_policy.osty:22
type CSurfaceLib struct {
	Lib string
	Ok  bool
}

// ChainSegLines mirrors toolchain/format_policy.osty's
// ChainSegLines. Decoupling NameEndLine from EndLine matters
// because a multi-line argument block in a chain segment must
// not feedback-loop the break decision.
//
// Osty: toolchain/format_policy.osty:39
type ChainSegLines struct {
	NameEndLine int
	EndLine     int
}

// UseGroupOrder bins a use decl into the canonical group order:
// 0 = stdlib (`std.*`), 1 = external (everything else that's not
// FFI), 2 = FFI.
//
// Osty: toolchain/format_policy.osty:50
func UseGroupOrder(isFFI bool, firstPathSegment string) int {
	if isFFI {
		return 2
	}
	if firstPathSegment == "std" {
		return 0
	}
	return 1
}

// UseSortKey is the intra-group sort key for `use` decls.
// Precedence: FFI path -> raw path -> dotted path.
//
// Osty: toolchain/format_policy.osty:65
func UseSortKey(isFFI bool, ffiPath, rawPath, dottedPath string) string {
	if isFFI {
		return ffiPath
	}
	if rawPath != "" {
		return rawPath
	}
	return dottedPath
}

// UseCSurfaceLibName recognises a runtime FFI path of the form
// `runtime.cabi.<lib>` (single trailing segment) and returns the
// library name. Multi-segment cabi paths and ones that smuggle
// a `/` through the segment fall through to the canonical
// runtime printer.
//
// Osty: toolchain/format_policy.osty:80
func UseCSurfaceLibName(runtimePath string) CSurfaceLib {
	const prefix = "runtime.cabi."
	if !strings.HasPrefix(runtimePath, prefix) {
		return CSurfaceLib{}
	}
	lib := runtimePath[len(prefix):]
	if lib == "" || strings.Contains(lib, ".") || strings.Contains(lib, "/") {
		return CSurfaceLib{}
	}
	return CSurfaceLib{Lib: lib, Ok: true}
}

// ShouldBreakChain reports whether a method chain should render
// in the leading-dot multi-line form. Triggers: 3+ segments, or
// any segment whose `.name` sits on a line below the previous
// segment's end.
//
// Osty: toolchain/format_policy.osty:101
func ShouldBreakChain(baseEndLine int, segs []ChainSegLines) bool {
	if len(segs) == 0 {
		return false
	}
	if len(segs) >= 3 {
		return true
	}
	prev := baseEndLine
	for _, s := range segs {
		if s.NameEndLine != prev {
			return true
		}
		prev = s.EndLine
	}
	return false
}
