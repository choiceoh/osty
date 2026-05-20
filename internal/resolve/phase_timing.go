package resolve

import (
	"fmt"
	"os"
	"time"
)

// beginResolvePhase wraps the same env gate that `internal/backend` uses
// (`OSTY_BUILD_PHASE_TIMING=1`) for resolve-side instrumentation. The
// resolve package can't import `internal/backend` (backend imports
// resolve), so the gate check is duplicated here. Both sides write to
// stderr in the same `phase-timing: <name> <duration>` shape so the
// caller can grep the combined stream.
//
// Returns a no-op closer when the gate is off; cost is one env load
// per BeginPhase call. The hot path is the call site (`defer
// beginResolvePhase("…")()`); production builds with the gate off pay
// only that boolean check.
func beginResolvePhase(name string) func() {
	if !phaseTimingEnabled() {
		return phaseTimingNoop
	}
	start := time.Now()
	return func() {
		d := time.Since(start)
		fmt.Fprintf(os.Stderr, "phase-timing: %-32s %12s\n", name, d.Round(time.Millisecond))
	}
}

// logResolveWorkspaceCounts is a one-shot diagnostic line that pairs
// with the `resolve.workspace.iter` phase marker so the reader can
// tell at a glance whether the 100+ packages are stdlib pre-resolved
// shells (cheap) or real `ResolvePackage` calls (expensive). Surfaced
// only when phase timing is enabled.
func logResolveWorkspaceCounts(resolved, preResolved, total int) {
	if !phaseTimingEnabled() {
		return
	}
	fmt.Fprintf(os.Stderr, "phase-timing: resolve.workspace.counts            resolved=%d preResolved=%d total=%d\n",
		resolved, preResolved, total)
}

func phaseTimingEnabled() bool {
	switch os.Getenv("OSTY_BUILD_PHASE_TIMING") {
	case "1", "true", "TRUE", "True", "yes", "YES", "on", "ON":
		return true
	}
	return false
}

func phaseTimingNoop() {}
