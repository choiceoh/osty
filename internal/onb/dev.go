package onb

import (
	"errors"
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
func StrictMode() bool { return EnvTrue(os.Getenv(EnvStrict)) }

// TimingEnabled mirrors StrictMode for the timing log. Two separate env vars
// keep the strict cross-validation flow independent from the dev-loop perf
// telemetry.
func TimingEnabled() bool { return EnvTrue(os.Getenv(EnvTiming)) }

// EnvTrue reports whether the env-var-style string s should be treated as
// "enabled". Exported so other backend toggles (`OSTY_BACKEND_TRACE`, future
// debug flags) can reuse the same parsing rules — keeping accepted spellings
// in lockstep across packages.
func EnvTrue(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// TimingPath names which build path actually ran for one ONB emit attempt.
// Typed instead of bare strings so a typo in a future call site is a compile
// error rather than a silent default-branch in LogTiming.
type TimingPath string

const (
	TimingPathNative       TimingPath = "native"
	TimingPathLLVMFallback TimingPath = "llvm-fallback"
	TimingPathError        TimingPath = "error"
)

// TimingEvent describes one ONB build path so callers can render a stable log
// line.
type TimingEvent struct {
	Path     TimingPath
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
	var suffix string
	switch ev.Path {
	case TimingPathNative:
		suffix = fmt.Sprintf("[native: %s]", target)
	case TimingPathLLVMFallback:
		reason := ev.Reason
		if reason == "" {
			reason = "shape unsupported"
		}
		suffix = fmt.Sprintf("[fallback to llvm: %s]", reason)
	case TimingPathError:
		reason := ev.Reason
		if reason == "" {
			reason = "rejected"
		}
		suffix = fmt.Sprintf("[error: %s]", reason)
	default:
		suffix = "[" + string(ev.Path) + "]"
	}
	fmt.Fprintf(w, "onb: emit %s %s\n", formatElapsed(ev.Elapsed), suffix)
}

// UnsupportedShapeReason peels the ErrUnsupportedShape sentinel off so callers
// can quote the specific MIR construct that triggered the fallback without
// also surfacing the generic sentinel message. Returns the input error's
// string verbatim when the sentinel is not present.
//
// Owning the prefix-strip here keeps the sentinel's textual format private to
// the onb package — backend/onb.go consumed to do this manually, which made
// the message text an implicit ABI between the two packages.
func UnsupportedShapeReason(err error) string {
	if err == nil {
		return ""
	}
	if !errors.Is(err, ErrUnsupportedShape) {
		return err.Error()
	}
	return strings.TrimPrefix(err.Error(), ErrUnsupportedShape.Error()+": ")
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
