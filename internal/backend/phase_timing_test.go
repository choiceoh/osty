package backend

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"
)

// withPhaseTimingEnabled toggles the gate for one test and restores the
// previous state on cleanup. Use this rather than mutating
// os.Setenv(PhaseTimingEnv) because phaseTimingEnabled is sampled once
// at package init.
func withPhaseTimingEnabled(t *testing.T, on bool) {
	t.Helper()
	prev := SetPhaseTimingEnabled(on)
	t.Cleanup(func() { SetPhaseTimingEnabled(prev) })
	_ = CollectPhaseTimings()
}

func TestBeginPhaseRecordsDurationWhenEnabled(t *testing.T) {
	withPhaseTimingEnabled(t, true)

	end := BeginPhase("stage0.emit")
	time.Sleep(2 * time.Millisecond)
	end()

	markers := CollectPhaseTimings()
	if len(markers) != 1 {
		t.Fatalf("CollectPhaseTimings returned %d markers, want 1: %#v", len(markers), markers)
	}
	if markers[0].Name != "stage0.emit" {
		t.Fatalf("marker name = %q, want %q", markers[0].Name, "stage0.emit")
	}
	if markers[0].Duration < 2*time.Millisecond {
		t.Fatalf("marker duration = %s, want >= 2ms", markers[0].Duration)
	}
}

func TestBeginPhaseNoopsWhenDisabled(t *testing.T) {
	withPhaseTimingEnabled(t, false)

	end := BeginPhase("backend.link")
	time.Sleep(time.Millisecond)
	end()

	if got := CollectPhaseTimings(); got != nil {
		t.Fatalf("CollectPhaseTimings returned %#v while disabled, want nil", got)
	}
}

func TestEmitPhaseTimingsSortsDescAndClears(t *testing.T) {
	withPhaseTimingEnabled(t, true)

	var buf bytes.Buffer
	prev := SetPhaseTimingWriter(&buf)
	t.Cleanup(func() { SetPhaseTimingWriter(prev) })

	fastEnd := BeginPhase("backend.link")
	time.Sleep(time.Millisecond)
	fastEnd()
	slowEnd := BeginPhase("stage0.emit")
	time.Sleep(8 * time.Millisecond)
	slowEnd()

	// Streaming output (the `phase-timing:` lines) fires as each
	// marker ends — both phases must appear in recorded order.
	out := buf.String()
	streamLink := strings.Index(out, "phase-timing: backend.link")
	streamStage := strings.Index(out, "phase-timing: stage0.emit")
	switch {
	case streamLink < 0:
		t.Fatalf("streaming row missing backend.link:\n%s", out)
	case streamStage < 0:
		t.Fatalf("streaming row missing stage0.emit:\n%s", out)
	case streamLink > streamStage:
		t.Fatalf("streaming rows not in record order — backend.link should precede stage0.emit:\n%s", out)
	}

	EmitPhaseTimings()

	out = buf.String()
	header := strings.Index(out, "-- build phase timing --")
	if header < 0 {
		t.Fatalf("output missing summary header:\n%s", out)
	}
	summary := out[header:]
	if !strings.Contains(summary, "(sum of phases)") {
		t.Fatalf("summary missing sum-of-phases row:\n%s", summary)
	}
	// Inside the summary block specifically, the slower phase must
	// come first (desc by duration).
	summStage := strings.Index(summary, "stage0.emit")
	summLink := strings.Index(summary, "backend.link")
	if summStage < 0 || summLink < 0 || summStage > summLink {
		t.Fatalf("summary not sorted desc by duration — stage0.emit should precede backend.link:\n%s", summary)
	}

	// Accumulator is cleared after emit so a second EmitPhaseTimings
	// call produces no new summary header.
	buf.Reset()
	EmitPhaseTimings()
	if strings.Contains(buf.String(), "-- build phase timing --") {
		t.Fatalf("EmitPhaseTimings re-emitted summary after clear:\n%s", buf.String())
	}
}

func TestEmitPhaseTimingsNoopsWhenDisabled(t *testing.T) {
	withPhaseTimingEnabled(t, true)
	// Record one marker, then disable before emitting.
	BeginPhase("disabled-before-emit")()
	withPhaseTimingEnabled(t, false)

	var buf bytes.Buffer
	prev := SetPhaseTimingWriter(&buf)
	t.Cleanup(func() { SetPhaseTimingWriter(prev) })

	EmitPhaseTimings()

	if buf.Len() != 0 {
		t.Fatalf("EmitPhaseTimings emitted output while disabled:\n%s", buf.String())
	}
}

func TestCollectPhaseTimingsPreservesRecordedOrder(t *testing.T) {
	withPhaseTimingEnabled(t, true)

	BeginPhase("first")()
	BeginPhase("second")()
	BeginPhase("third")()

	markers := CollectPhaseTimings()
	names := []string{markers[0].Name, markers[1].Name, markers[2].Name}
	want := []string{"first", "second", "third"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("recorded order = %v, want %v", names, want)
	}
}
