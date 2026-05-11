// Package subproc is the shared host-boundary helper for invoking Osty-native
// subprocesses (osty-native-checker, osty-native-lirproto, osty-native-llvmgen).
//
// It captures stdout/stderr on separate buffers so a checker panic or stray
// stderr write can't corrupt the JSON response stream, and surfaces structured
// failure context (path, underlying error, capped stdout/stderr previews) for
// diagnostic rendering.
package subproc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// Preview caps. stderr keeps the tail (panic stacks are most informative at
// the bottom); stdout keeps the head (JSON parse errors fire near the start,
// so a 256B prefix is enough to recognize what shape the response had).
const (
	StdoutPreviewBytes = 256
	StderrPreviewBytes = 2048
	// inlineErrPreviewBytes is the single-line stderr snippet embedded in
	// Error() for callers that only render err.Error(). Full preview is on
	// the structured fields.
	inlineErrPreviewBytes = 200
)

// Error is the structured failure of a native-subprocess invocation. Callers
// can errors.As to split stdout/stderr into separate diagnostic notes; those
// that only render err.Error() still get the path + underlying error + a short
// stderr snippet.
type Error struct {
	Path      string
	Err       error
	Stdout    []byte // truncated head, up to StdoutPreviewBytes
	Stderr    []byte // truncated tail, up to StderrPreviewBytes
	StdoutLen int    // original stdout length before truncation
	StderrLen int    // original stderr length before truncation
}

func (e *Error) Error() string {
	if len(e.Stderr) == 0 {
		return fmt.Sprintf("native subprocess %s: %v", e.Path, e.Err)
	}
	return fmt.Sprintf("native subprocess %s: %v (stderr: %s)", e.Path, e.Err, singleLineSnippet(e.Stderr, inlineErrPreviewBytes))
}

func (e *Error) Unwrap() error { return e.Err }

// Run spawns `path` and pipes `stdin` to it, capturing stdout and stderr on
// separate buffers. Returns *Error wrapping the captured streams if the
// subprocess fails to start or exits non-zero; otherwise returns the full
// captured streams and nil error.
//
// Callers that need to surface post-exec decode/validation failures (e.g.
// json.Unmarshal returning an error on the captured stdout) should call
// WrapResponseError with the underlying error and the same streams.
func Run(path string, stdin []byte) (stdout, stderr []byte, err error) {
	return RunCommand(context.Background(), path, nil, stdin)
}

// RunCommand is the general-purpose form of Run: it accepts a context (for
// cancellation), command-line args, and optional stdin bytes. Stream capture,
// truncation policy, and structured-error semantics match Run.
//
// When `stdin` is nil the subprocess inherits no input (suitable for tools
// that read args/files rather than stdin, like clang or lld).
func RunCommand(ctx context.Context, path string, args []string, stdin []byte) (stdout, stderr []byte, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, path, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	runErr := cmd.Run()
	stdout = stdoutBuf.Bytes()
	stderr = stderrBuf.Bytes()
	if runErr != nil {
		return stdout, stderr, newError(path, runErr, stdout, stderr)
	}
	return stdout, stderr, nil
}

// WrapResponseError builds an *Error for a post-Run failure (typically a JSON
// decode error on the captured stdout). Truncation policy matches Run.
func WrapResponseError(path string, baseErr error, stdout, stderr []byte) *Error {
	return newError(path, baseErr, stdout, stderr)
}

// FailureNotes expands an error into diagnostic notes. A *Error is split into
// header + summary + stderr-preview + stdout-preview; any other error becomes
// header + err.Error().
func FailureNotes(header string, err error) []string {
	var se *Error
	if !errors.As(err, &se) {
		return []string{header, err.Error()}
	}
	notes := []string{header, se.Error()}
	if se.StderrLen > 0 {
		notes = append(notes, formatStreamPreview("stderr", se.Stderr, se.StderrLen, "tail"))
	}
	if se.StdoutLen > 0 {
		notes = append(notes, formatStreamPreview("stdout", se.Stdout, se.StdoutLen, "head"))
	}
	return notes
}

func newError(path string, err error, stdout, stderr []byte) *Error {
	return &Error{
		Path:      path,
		Err:       err,
		Stdout:    headBytes(stdout, StdoutPreviewBytes),
		Stderr:    tailBytes(stderr, StderrPreviewBytes),
		StdoutLen: len(stdout),
		StderrLen: len(stderr),
	}
}

func headBytes(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return b[:n]
}

func tailBytes(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return b[len(b)-n:]
}

func formatStreamPreview(label string, preview []byte, total int, side string) string {
	if total <= len(preview) {
		return fmt.Sprintf("%s (%d bytes):\n%s", label, total, preview)
	}
	return fmt.Sprintf("%s (%s %d of %d bytes):\n%s", label, side, len(preview), total, preview)
}

// singleLineSnippet returns up to `n` bytes of `b`, replacing newlines with
// spaces and adding an ellipsis when truncated. Used to embed a short stderr
// preview inline in Error() output for callers that only see err.Error().
func singleLineSnippet(b []byte, n int) string {
	trim := b
	if len(trim) > n {
		trim = trim[:n]
	}
	flat := bytes.ReplaceAll(trim, []byte("\n"), []byte(" "))
	flat = bytes.TrimSpace(flat)
	if len(b) > n {
		return string(flat) + "..."
	}
	return string(flat)
}
