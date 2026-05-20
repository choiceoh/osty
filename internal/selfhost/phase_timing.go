package selfhost

import (
	"fmt"
	"os"
	"time"
)

// beginSelfhostPhase mirrors the `OSTY_BUILD_PHASE_TIMING=1` gate used
// by `internal/backend/phase_timing.go`,
// `internal/resolve/phase_timing.go`, and
// `internal/ir/phase_timing.go`. The `selfhost` package sits at the
// bottom of the import graph (none of the others import it) so the
// gate logic is duplicated to avoid forcing a cycle. Output shape
// matches the others exactly so the same grep pipeline catches every
// phase line.
func beginSelfhostPhase(name string) func() {
	if !selfhostPhaseTimingEnabled() {
		return selfhostPhaseTimingNoop
	}
	start := time.Now()
	return func() {
		d := time.Since(start)
		fmt.Fprintf(os.Stderr, "phase-timing: %-32s %12s\n", name, d.Round(time.Millisecond))
	}
}

func selfhostPhaseTimingEnabled() bool {
	switch os.Getenv("OSTY_BUILD_PHASE_TIMING") {
	case "1", "true", "TRUE", "True", "yes", "YES", "on", "ON":
		return true
	}
	return false
}

func selfhostPhaseTimingNoop() {}
