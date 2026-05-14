// profile_features_policy.go is the Go snapshot of
// toolchain/profile_features.osty. Osty is the source of truth;
// the drift test in this package keeps the two in sync.
package runner

import (
	"sort"
	"strings"
)

// FeatureCheckResult mirrors toolchain/profile_features.osty's
// FeatureCheckResult — the structured outcome of fileNeedsFeatures.
// Ok=false leaves Missing populated with the first missing feature
// name so the host can blame it in a diagnostic.
//
// Osty: toolchain/profile_features.osty:130
type FeatureCheckResult struct {
	Ok      bool
	Missing string
}

// OptLevelFlags returns the `-gcflags` argv chunk for an integer
// optimisation level. Levels outside {0, 1} return nil so the
// user's profile-level `GoFlags` controls alone.
//
// Osty: toolchain/profile_features.osty:26
func OptLevelFlags(level int) []string {
	switch level {
	case 0:
		return []string{"-gcflags=all=-N -l"}
	case 1:
		return []string{"-gcflags=all=-l"}
	}
	return nil
}

// ExpandFeatures computes the deterministic transitive closure of
// `requested` (and, when useDefaults is true, `defaults`) through
// the feature graph `features`. Result is sorted by name for
// stable downstream consumers.
//
// Cross-package tokens (`<dep>/<feat>`) reached as children are
// filtered out so they never join the local closure — downstream
// `Resolved.GoFlags()` would otherwise emit `feat_dep/feat` build
// tags, which Go rejects. Top-level `<dep>/<feat>` tokens passed
// in via requested/defaults still land in the output.
//
// Osty: toolchain/profile_features.osty:60
func ExpandFeatures(features map[string][]string, defaults, requested []string, useDefaults bool) []string {
	seen := map[string]bool{}
	queue := make([]string, 0, len(defaults)+len(requested))
	if useDefaults {
		for _, f := range defaults {
			if !seen[f] {
				seen[f] = true
				queue = append(queue, f)
			}
		}
	}
	for _, f := range requested {
		if !seen[f] {
			seen[f] = true
			queue = append(queue, f)
		}
	}
	visited := map[string]bool{}
	var out []string
	for head := 0; head < len(queue); head++ {
		f := queue[head]
		if visited[f] {
			continue
		}
		visited[f] = true
		out = append(out, f)
		for _, child := range features[f] {
			// Drop transitive cross-package refs at the enqueue
			// site — they must NOT enter the local closure
			// (prevents "feat_dep/feat" Go build tags downstream).
			if strings.Contains(child, "/") {
				continue
			}
			if !seen[child] {
				seen[child] = true
				queue = append(queue, child)
			}
		}
	}
	sort.Strings(out)
	return out
}

// GoFlags assembles the flat `go build` flag list for a resolved
// profile/target/feature config. Order:
//
//  1. OptLevelFlags(optLevel) — debug/light switches.
//  2. profileGoFlags from the merged manifest profile, deduped
//     against (1).
//  3. `-ldflags=-s -w` when strip is true, deduped.
//  4. `-tags=feat_<name>,...` when features non-empty; tags sorted.
//
// Feature names map 1:1 to Go build tags so generated code can
// gate via `//go:build feat_<name>`.
//
// Osty: toolchain/profile_features.osty:80
func GoFlags(optLevel int, profileGoFlags []string, strip bool, features []string) []string {
	var flags []string
	seen := map[string]bool{}
	for _, f := range OptLevelFlags(optLevel) {
		if !seen[f] {
			flags = append(flags, f)
			seen[f] = true
		}
	}
	for _, f := range profileGoFlags {
		if !seen[f] {
			flags = append(flags, f)
			seen[f] = true
		}
	}
	const stripFlag = "-ldflags=-s -w"
	if strip && !seen[stripFlag] {
		flags = append(flags, stripFlag)
		seen[stripFlag] = true
	}
	if len(features) > 0 {
		tags := make([]string, 0, len(features))
		for _, f := range features {
			tags = append(tags, "feat_"+f)
		}
		sort.Strings(tags)
		flags = append(flags, "-tags="+strings.Join(tags, ","))
	}
	return flags
}

// FileNeedsFeatures reads the file-header `// @feature:` pragma
// from `src` and checks every required feature against the
// `active` map. Returns Ok=true with empty Missing when every
// required feature is active (or when no pragma is present).
//
// Osty: toolchain/profile_features.osty:139
func FileNeedsFeatures(src []byte, active map[string]bool) FeatureCheckResult {
	for _, f := range ReadFeaturePragma(src) {
		if !active[f] {
			return FeatureCheckResult{Ok: false, Missing: f}
		}
	}
	return FeatureCheckResult{Ok: true}
}
