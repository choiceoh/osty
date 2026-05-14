// profile_features_policy.go is the Go snapshot of
// toolchain/profile_features.osty. Osty is the source of truth;
// the drift test in this package keeps the two in sync.
package runner

import (
	"sort"
	"strings"
)

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
