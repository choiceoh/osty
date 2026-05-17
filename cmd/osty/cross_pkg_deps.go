package main

import (
	"context"

	"github.com/osty/osty/internal/backend"
	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/profile"
	ostyquery "github.com/osty/osty/internal/query/osty"
)

// buildCrossPkgDepObjects compiles each cross-package dependency the
// main entry directly or transitively reaches into a standalone `.o`
// artifact and returns the absolute paths so the link step can
// resolve `declare`d symbols. Returns nil when there are no deps to
// compile.
//
// PR-G1 ships only the wiring scaffold — this function returns nil
// while PR-G2 fills in actual dep compilation. Until then, packages
// with cross-pkg `use` declarations still build IR successfully (the
// declares land in `main.ll`) but fail at link time as before. That
// failure mode is unchanged by this commit; the field plumbing just
// lets PR-G2 land without touching every caller again.
func buildCrossPkgDepObjects(_ context.Context, _ string, _ *manifest.Manifest, _ *ostyquery.Engine, _ ostyquery.LowerKey, _ *profile.Resolved, _ map[string]bool, _ backend.Layout) []string {
	return nil
}
