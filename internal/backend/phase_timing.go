package backend

import (
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"time"
)

// PhaseTimingEnv gates the build-phase timing instrumentation.
// `OSTY_BUILD_PHASE_TIMING=1` (or any truthy value) opts in. When unset
// every Begin/End pair is a no-op and the accumulator stays empty so the
// hot path pays only a single env-cached bool check per marker.
//
// The intended consumer is the install-self bootstrap critical path,
// where the wall-clock breakdown (front-end vs MIR/IR lowering vs
// stage0 emit vs LLVM toolchain link) decides which phase to attack
// next. The env gate keeps the noise out of production builds; CI and
// local profiling sessions opt in explicitly.
const PhaseTimingEnv = "OSTY_BUILD_PHASE_TIMING"

// phaseTimingEnabled is computed once at process start so each
// BeginPhase call does a single boolean load instead of re-parsing the
// env var. Tests that need to toggle the gate use SetPhaseTimingEnabled
// to update it deterministically.
var (
	phaseTimingMu       sync.Mutex
	phaseTimingEnabled  = phaseTimingOnFromEnv()
	phaseTimingMarkers  []PhaseMarker
	phaseTimingWriter   io.Writer = os.Stderr
	phaseTimingResetSeq uint64
)

// PhaseMarker is one completed phase's name + wall-clock duration.
// Exported so tests can assert against the accumulated set.
type PhaseMarker struct {
	Name     string
	Duration time.Duration
}

func phaseTimingOnFromEnv() bool {
	switch os.Getenv(PhaseTimingEnv) {
	case "1", "true", "TRUE", "True", "yes", "YES", "on", "ON":
		return true
	}
	return false
}

// SetPhaseTimingEnabled toggles the gate at runtime. Tests use this to
// flip the gate without mutating the process env. Returns the previous
// value so callers can restore it in a defer.
func SetPhaseTimingEnabled(on bool) bool {
	phaseTimingMu.Lock()
	prev := phaseTimingEnabled
	phaseTimingEnabled = on
	phaseTimingMu.Unlock()
	return prev
}

// PhaseTimingEnabled reports whether instrumentation is active. Most
// callers do not need this — they call BeginPhase directly and let it
// no-op. Surfaced for tests and for call sites that want to skip
// expensive bookkeeping (e.g. timestamping IR-substring matches) that
// would itself be dead under a no-op marker.
func PhaseTimingEnabled() bool {
	phaseTimingMu.Lock()
	defer phaseTimingMu.Unlock()
	return phaseTimingEnabled
}

// BeginPhase records the start of a build phase. The returned func
// must be invoked when the phase ends — `defer backend.BeginPhase("…")()`
// is the canonical shape. The cost is one boolean check + one time.Now
// call when enabled, and a single boolean load when disabled.
//
// Each completed phase is also streamed immediately to the configured
// writer in the form `phase-timing: <name> <duration>` so that even
// when the calling subcommand bails via `os.Exit` (which skips
// `EmitPhaseTimings`) the markers up to the failure point are still
// visible. The accumulator still collects every marker for the
// sorted summary at clean exits.
func BeginPhase(name string) func() {
	if !PhaseTimingEnabled() {
		return phaseTimingNoop
	}
	start := time.Now()
	return func() {
		d := time.Since(start)
		phaseTimingMu.Lock()
		phaseTimingMarkers = append(phaseTimingMarkers, PhaseMarker{Name: name, Duration: d})
		w := phaseTimingWriter
		phaseTimingMu.Unlock()
		fmt.Fprintf(w, "phase-timing: %-32s %12s\n", name, d.Round(time.Millisecond))
	}
}

func phaseTimingNoop() {}

// EmitPhaseTimings prints every accumulated marker to stderr (or the
// configured writer) sorted by duration descending, then clears the
// accumulator. No-op when the gate is off or nothing was recorded so
// callers can wire it unconditionally at process exit.
//
// The output layout is single-column human-readable — the consumer is
// a developer reading stderr, not a parser. Stable enough that grep /
// awk pipelines stay simple but not documented as a wire format.
func EmitPhaseTimings() {
	phaseTimingMu.Lock()
	if !phaseTimingEnabled || len(phaseTimingMarkers) == 0 {
		phaseTimingMu.Unlock()
		return
	}
	markers := append([]PhaseMarker(nil), phaseTimingMarkers...)
	phaseTimingMarkers = nil
	phaseTimingResetSeq++
	w := phaseTimingWriter
	phaseTimingMu.Unlock()

	sort.SliceStable(markers, func(i, j int) bool {
		return markers[i].Duration > markers[j].Duration
	})

	fmt.Fprintln(w, "-- build phase timing --")
	var total time.Duration
	for _, m := range markers {
		fmt.Fprintf(w, "  %-32s %12s\n", m.Name, m.Duration.Round(time.Millisecond))
		total += m.Duration
	}
	fmt.Fprintf(w, "  %-32s %12s\n", "(sum of phases)", total.Round(time.Millisecond))
}

// CollectPhaseTimings drains the accumulator and returns the markers
// in recorded order. Used by tests to assert on the captured set
// without touching the writer.
func CollectPhaseTimings() []PhaseMarker {
	phaseTimingMu.Lock()
	defer phaseTimingMu.Unlock()
	if len(phaseTimingMarkers) == 0 {
		return nil
	}
	out := append([]PhaseMarker(nil), phaseTimingMarkers...)
	phaseTimingMarkers = nil
	return out
}

// SetPhaseTimingWriter redirects EmitPhaseTimings output. Tests use this
// to capture into a buffer; production leaves it as os.Stderr.
func SetPhaseTimingWriter(w io.Writer) io.Writer {
	phaseTimingMu.Lock()
	prev := phaseTimingWriter
	if w == nil {
		phaseTimingWriter = os.Stderr
	} else {
		phaseTimingWriter = w
	}
	phaseTimingMu.Unlock()
	return prev
}
