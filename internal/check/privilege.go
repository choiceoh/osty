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

// isPrivilegedPackage determines whether a resolver Package is privileged under
// §19.2. Toolchain packages can opt in with `[capabilities] runtime = true`;
// std.runtime packages stay privileged by path.
func isPrivilegedPackage(pkg *resolve.Package) bool {
	if pkg == nil {
		return false
	}
	if pkg.RuntimeCapability {
		return true
	}
	if pkg.Dir == "" {
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
