package check

import (
	"path/filepath"
	"strings"

	"github.com/osty/osty/internal/resolve"
)

// Package privilege detection (LANG_SPEC §19.2). The actual gate walker
// lives in toolchain/check_gates.osty::runPrivilegeGate and fires from
// inside CheckPackageStructured. Host callers only need to decide
// whether to strip the emitted E0770 records, which is a function of
// the *package identity*, not the source shape — hence this file shrank
// from a full arena walker to just these two predicates after #770
// ("refactor(check): drop Go-side §19 gate duplicates — Osty authoritative").

// isPrivilegedPackage determines whether a resolver Package is
// privileged under §19.2. The decision prefers the package's declared
// path when available (via isPrivilegedPackagePath), then falls back
// to the directory heuristic — `.../std/runtime/<anything>` on disk.
// The manifest-capability path (`[capabilities] runtime = true`) is
// read by the manifest loader and surfaced through a future
// `Package.Capabilities` field; for now any `std/runtime` directory
// shape is treated as privileged so the gate stays exercised against
// the obvious fixtures.
func isPrivilegedPackage(pkg *resolve.Package) bool {
	if pkg == nil || pkg.Dir == "" {
		return false
	}
	// Normalize to forward slashes so the predicate is platform-agnostic.
	norm := filepath.ToSlash(pkg.Dir)
	if strings.Contains(norm, "/std/runtime/") || strings.HasSuffix(norm, "/std/runtime") {
		return true
	}
	return false
}

// isPrivilegedPackagePath reports whether a workspace-level package
// path (dotted, e.g. `std.runtime.raw`) is privileged under §19.2.
// Any subpath of `std.runtime` qualifies.
func isPrivilegedPackagePath(path string) bool {
	if path == "" {
		return false
	}
	return path == "std.runtime" || strings.HasPrefix(path, "std.runtime.")
}
