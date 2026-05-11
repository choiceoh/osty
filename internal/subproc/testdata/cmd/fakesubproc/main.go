// fakesubproc is a flag-driven stand-in for the Osty-native subprocess
// binaries (osty-native-checker / lirproto / llvmgen) and the external
// toolchain (clang / lld). It emits deterministic (stdout, stderr, exit code)
// combinations keyed by FAKESUBPROC_MODE so the subproc package tests can
// assert capture + truncation + cancellation correctness.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

func main() {
	mode := os.Getenv("FAKESUBPROC_MODE")
	if mode == "" {
		mode = "ok"
	}

	_, _ = io.Copy(io.Discard, os.Stdin)

	switch mode {
	case "ok":
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
		fmt.Fprintln(os.Stderr, "warning: deprecated flag --foo")
		fmt.Fprint(os.Stdout, `{}`)
	case "huge-stderr":
		fmt.Fprint(os.Stderr, strings.Repeat("filler line\n", 1000))
		fmt.Fprintln(os.Stderr, "TAIL_MARKER_LAST_LINE")
		os.Exit(3)
	case "huge-stdout":
		fmt.Fprint(os.Stdout, "HEAD_MARKER_FIRST_LINE\n")
		fmt.Fprint(os.Stdout, strings.Repeat("garbage line\n", 1000))
	case "echo-args":
		fmt.Fprint(os.Stdout, strings.Join(os.Args[1:], "|"))
	case "sleep":
		time.Sleep(30 * time.Second)
	default:
		fmt.Fprintf(os.Stderr, "fakesubproc: unknown mode %q\n", mode)
		os.Exit(1)
	}
}
