package onb

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// EnvStrict is the env var that forces ONB to surface its own shape-rejection
// errors instead of letting the backend layer fall back to LLVM. Cross-validation
// harnesses and self-host bring-up flip this on so they fail loudly when ONB
// disagrees with LLVM. Day-to-day dev loops keep it unset.
const EnvStrict = "OSTY_ONB_STRICT"

// EnvTiming, when set to a truthy value, causes the backend layer to write a
// one-line wall-clock summary to stderr after each ONB emit. The summary names
// the path that actually ran (native vs llvm fallback) so the developer can
// tell at a glance whether the dev backend covered their MIR shape.
const EnvTiming = "OSTY_ONB_TIMING"

// StrictMode reports whether the environment is requesting that ONB never
// silently delegate to the LLVM backend. Reading the env on every call keeps
// the toggle responsive to inline `OSTY_ONB_STRICT=1 osty run ...` invocations
// without process restart.
func StrictMode() bool { return envTrue(os.Getenv(EnvStrict)) }

// TimingEnabled mirrors StrictMode for the timing log. Two separate env vars
// keep the strict cross-validation flow independent from the dev-loop perf
// telemetry.
func TimingEnabled() bool { return envTrue(os.Getenv(EnvTiming)) }

func envTrue(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// TimingEvent describes one ONB build path so callers can render a stable log
// line. Path is "native" when ONB lowered the MIR end-to-end, "llvm-fallback"
// when the backend layer delegated to LLVM after a shape rejection, or
// "error" when neither path produced an artifact.
type TimingEvent struct {
	Path     string
	Target   string
	Elapsed  time.Duration
	Reason   string
	BinaryAt string
}

// LogTiming writes a single human-readable line describing the build path
// taken. It is only meant to run when TimingEnabled() is true. Output goes to
// the supplied writer; the backend layer wires it to os.Stderr.
func LogTiming(w io.Writer, ev TimingEvent) {
	if w == nil {
		return
	}
	target := ev.Target
	if target == "" {
		target = "host"
	}
	suffix := ""
	switch ev.Path {
	case "native":
		suffix = fmt.Sprintf("[native: %s]", target)
	case "llvm-fallback":
		reason := ev.Reason
		if reason == "" {
			reason = "shape unsupported"
		}
		suffix = fmt.Sprintf("[fallback to llvm: %s]", reason)
	case "error":
		reason := ev.Reason
		if reason == "" {
			reason = "rejected"
		}
		suffix = fmt.Sprintf("[error: %s]", reason)
	default:
		suffix = "[" + ev.Path + "]"
	}
	fmt.Fprintf(w, "onb: emit %s %s\n", formatElapsed(ev.Elapsed), suffix)
}

func formatElapsed(d time.Duration) string {
	switch {
	case d <= 0:
		return "0ms"
	case d < time.Millisecond:
		return fmt.Sprintf("%.2fms", float64(d.Microseconds())/1000.0)
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	default:
		return fmt.Sprintf("%.2fs", d.Seconds())
	}
}
