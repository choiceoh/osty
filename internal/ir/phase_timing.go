package ir

import (
	"fmt"
	"os"
	"time"
)

// beginIRPhase mirrors the `OSTY_BUILD_PHASE_TIMING=1` gate that
// `internal/backend/phase_timing.go` and
// `internal/resolve/phase_timing.go` use. The `ir` package can't
// import `internal/backend` (backend imports ir), so the gate logic
// is duplicated here. Output shape matches the others exactly so
// the same grep pipeline catches every phase line.
func beginIRPhase(name string) func() {
	if !irPhaseTimingEnabled() {
		return irPhaseTimingNoop
	}
	start := time.Now()
	return func() {
		d := time.Since(start)
		fmt.Fprintf(os.Stderr, "phase-timing: %-32s %12s\n", name, d.Round(time.Millisecond))
	}
}

func irPhaseTimingEnabled() bool {
	switch os.Getenv("OSTY_BUILD_PHASE_TIMING") {
	case "1", "true", "TRUE", "True", "yes", "YES", "on", "ON":
		return true
	}
	return false
}

func irPhaseTimingNoop() {}
