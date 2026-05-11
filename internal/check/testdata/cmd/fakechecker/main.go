// fakechecker is a flag-driven stand-in for osty-native-checker used by
// host_boundary_test.go to exercise the subprocess error-reporting paths.
//
// Each --mode emits a deterministic (stdout, stderr, exit code) combination
// so the tests can assert that nativeCheckerExec.run captures and truncates
// the streams correctly.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

func main() {
	mode := os.Getenv("FAKECHECKER_MODE")
	if mode == "" {
		mode = "ok"
	}

	_, _ = io.Copy(io.Discard, os.Stdin)

	switch mode {
	case "ok":
		// Minimal valid CheckResult JSON. Empty object decodes cleanly into
		// api.CheckResult — EnsureStableIDs handles the rest.
		fmt.Fprint(os.Stdout, `{}`)
	case "bad-json":
		fmt.Fprint(os.Stdout, "this is definitely not json {[")
	case "panic":
		fmt.Fprintln(os.Stderr, "panic: runtime error: index out of range [4] with length 2")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "goroutine 1 [running]:")
		fmt.Fprintln(os.Stderr, "main.checkThing(0x0, 0x0)")
		fmt.Fprintln(os.Stderr, "\tcheck.go:42 +0x1a3")
		os.Exit(2)
	case "stderr-noise":
		// Real-world bug we want to be robust against: subprocess writes a
		// debug line to stderr but still produces a valid JSON response on
		// stdout. The success path should ignore stderr entirely.
		fmt.Fprintln(os.Stderr, "warning: deprecated flag --foo")
		fmt.Fprint(os.Stdout, `{}`)
	case "huge-stderr":
		// 10KB of stderr, ending with a recognizable tail marker so the test
		// can verify tail-truncation kept the bottom.
		fmt.Fprint(os.Stderr, strings.Repeat("filler line\n", 1000))
		fmt.Fprintln(os.Stderr, "TAIL_MARKER_LAST_LINE")
		os.Exit(3)
	case "huge-stdout":
		// 10KB of stdout starting with a recognizable head marker, all
		// non-JSON so the decode error path fires.
		fmt.Fprint(os.Stdout, "HEAD_MARKER_FIRST_LINE\n")
		fmt.Fprint(os.Stdout, strings.Repeat("garbage line\n", 1000))
	default:
		fmt.Fprintf(os.Stderr, "fakechecker: unknown mode %q\n", mode)
		os.Exit(1)
	}
}
