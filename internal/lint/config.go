package lint

import (
	"path/filepath"
	"strings"

	"github.com/osty/osty/internal/diag"
)

// Config is the project-level configuration for the lint pass. It is
// orthogonal to #[allow(...)] annotations: Config applies uniformly
// across every file in scope, annotations apply only to the decl they
// attach to.
//
// Both Allow and Deny accept the same symbolic names as #[allow(...)]:
//
//   - A concrete code: `L0001`, `L0040`, ...
//   - A category alias: `unused`, `shadow`, `dead_code`, `naming`,
//     `simplify`
//   - A rule alias: `unused_let`, `unused_param`, `redundant_bool`, …
//   - The wildcards `lint` or `all`
//
// When Allow and Deny both list the same code, Deny wins — the code is
// elevated to Error. When a code appears in Allow only, matching lint
// diagnostics are removed from the result. When in Deny only, matching
// diagnostics have their Severity promoted to Error.
//
// Typical source: the project's `osty.toml` `[lint]` section:
//
//	[lint]
//	allow = ["naming_value", "L0003"]
//	deny  = ["dead_code", "self_assign"]
type Config struct {
	Allow []string
	Deny  []string
	// Exclude is a list of path globs. `osty lint` skips any file whose
	// path matches one. `**` acts as a cross-segment wildcard:
	//
	//   "vendor/**"        → everything under vendor/
	//   "gen/**/*.osty"    → any .osty in gen/ or its subtree
	//   "third_party/**/*" → any file under third_party/
	//
	// Paths are matched after being made relative to the config's base
	// directory (the directory of the osty.toml it came from). If the
	// file lives outside that tree, the absolute path is tried instead.
	Exclude []string
}

// ShouldExclude reports whether `path` matches any Exclude glob. Call
// sites should use this to skip files before parsing. `base` is the
// directory of the osty.toml that produced this Config; `path` may be
// absolute or relative to CWD.
func (c Config) ShouldExclude(path, base string) bool {
	if len(c.Exclude) == 0 {
		return false
	}
	rel := path
	if base != "" {
		absBase, errBase := filepath.Abs(base)
		absPath, errPath := filepath.Abs(path)
		if errBase == nil && errPath == nil && strings.HasPrefix(absPath, absBase+string(filepath.Separator)) {
			rel = strings.TrimPrefix(absPath[len(absBase):], string(filepath.Separator))
		}
	}
	rel = filepath.ToSlash(rel)
	for _, pat := range c.Exclude {
		if matchGlob(pat, rel) {
			return true
		}
	}
	return false
}

// matchGlob implements filepath.Match-style globbing with the
// extension that `**` matches zero or more path segments (including
// slashes). The implementation converts `**` to a single-segment
// wildcard by splitting and matching piecewise — simpler than a
// dedicated state machine and adequate for lint exclusions.
func matchGlob(pattern, path string) bool {
	pattern = filepath.ToSlash(pattern)
	path = filepath.ToSlash(path)
	return globParts(strings.Split(pattern, "/"), strings.Split(path, "/"))
}

func globParts(pat, p []string) bool {
	for len(pat) > 0 && len(p) > 0 {
		if pat[0] == "**" {
			// `**` at the end swallows everything.
			if len(pat) == 1 {
				return true
			}
			// Try to match the rest at every suffix of p.
			for i := 0; i <= len(p); i++ {
				if globParts(pat[1:], p[i:]) {
					return true
				}
			}
			return false
		}
		ok, err := filepath.Match(pat[0], p[0])
		if err != nil || !ok {
			return false
		}
		pat = pat[1:]
		p = p[1:]
	}
	// Trailing `**` patterns on pat side may match zero segments on p.
	for len(pat) > 0 && pat[0] == "**" {
		pat = pat[1:]
	}
	return len(pat) == 0 && len(p) == 0
}

// Apply returns a filtered / mutated copy of r's Diags according to c.
// The input r is not mutated. Safe to call with a nil or empty Config —
// it returns r unchanged in that case.
func (c Config) Apply(r *Result) *Result {
	if r == nil {
		return &Result{}
	}
	if len(c.Allow) == 0 && len(c.Deny) == 0 {
		return r
	}
	allowSet := expandCodeSet(c.Allow)
	denySet := expandCodeSet(c.Deny)

	out := &Result{}
	for _, d := range r.Diags {
		if !isLintCode(d.Code) {
			out.Diags = append(out.Diags, d)
			continue
		}
		// Deny wins over Allow — codes in both are promoted.
		if denySet != nil && (denySet[d.Code] || denySet["*"]) {
			copy := *d
			copy.Severity = diag.Error
			out.Diags = append(out.Diags, &copy)
			continue
		}
		if allowSet != nil && (allowSet[d.Code] || allowSet["*"]) {
			continue // suppressed
		}
		out.Diags = append(out.Diags, d)
	}
	return out
}

// expandCodeSet takes a list of user-facing names (codes / aliases /
// wildcards) and returns the concrete L-code set. "*" means "every lint
// code"; it's emitted when any wildcard alias (`lint` / `all`) appears.
func expandCodeSet(names []string) map[string]bool {
	if len(names) == 0 {
		return nil
	}
	out := map[string]bool{}
	for _, name := range names {
		if name == "lint" || name == "all" {
			return map[string]bool{"*": true}
		}
		for _, code := range resolveAllowName(name) {
			out[code] = true
		}
	}
	if len(out) == 0 {
		return nil // nothing matched — caller treats as no-op
	}
	return out
}
