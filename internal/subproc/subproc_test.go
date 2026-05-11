package subproc

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var fakePath string

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "osty-fakesubproc-*")
	if err != nil {
		panic("create temp dir: " + err.Error())
	}
	defer os.RemoveAll(tmp)
	binName := "fakesubproc"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	binPath := filepath.Join(tmp, binName)
	cmd := exec.Command("go", "build", "-o", binPath, "./testdata/cmd/fakesubproc")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		panic("build fakesubproc: " + err.Error())
	}
	fakePath = binPath
	os.Exit(m.Run())
}

func TestRun_OK(t *testing.T) {
	t.Setenv("FAKESUBPROC_MODE", "ok")
	stdout, stderr, err := Run(fakePath, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(stdout) != "{}" {
		t.Errorf("stdout=%q, want %q", stdout, "{}")
	}
	if len(stderr) != 0 {
		t.Errorf("stderr should be empty on clean run; got %q", stderr)
	}
}

func TestRun_StderrNoiseDoesNotFailRun(t *testing.T) {
	t.Setenv("FAKESUBPROC_MODE", "stderr-noise")
	stdout, stderr, err := Run(fakePath, nil)
	if err != nil {
		t.Fatalf("zero-exit must return nil error regardless of stderr; got: %v", err)
	}
	if string(stdout) != "{}" {
		t.Errorf("stdout=%q, want %q", stdout, "{}")
	}
	if !bytes.Contains(stderr, []byte("deprecated flag")) {
		t.Errorf("stderr should still be captured for inspection; got %q", stderr)
	}
}

func TestRun_PanicReturnsStructuredError(t *testing.T) {
	t.Setenv("FAKESUBPROC_MODE", "panic")
	_, _, err := Run(fakePath, nil)
	if err == nil {
		t.Fatal("expected error for panic mode")
	}
	var se *Error
	if !errors.As(err, &se) {
		t.Fatalf("expected *Error, got %T", err)
	}
	if !bytes.Contains(se.Stderr, []byte("panic: runtime error")) {
		t.Errorf("stderr should contain panic header; got: %q", se.Stderr)
	}
	if !bytes.Contains(se.Stderr, []byte("goroutine 1")) {
		t.Errorf("stderr should contain goroutine dump; got: %q", se.Stderr)
	}
	if len(se.Stdout) != 0 {
		t.Errorf("expected empty stdout on panic; got: %q", se.Stdout)
	}
	if !strings.Contains(se.Error(), "stderr:") {
		t.Errorf("Error() should embed inline stderr snippet for fallback callers; got: %q", se.Error())
	}
}

func TestRun_HugeStderrTruncatedAtTail(t *testing.T) {
	t.Setenv("FAKESUBPROC_MODE", "huge-stderr")
	_, _, err := Run(fakePath, nil)
	if err == nil {
		t.Fatal("expected error for huge-stderr mode")
	}
	var se *Error
	if !errors.As(err, &se) {
		t.Fatalf("expected *Error, got %T", err)
	}
	if len(se.Stderr) > StderrPreviewBytes {
		t.Errorf("stderr preview %d > cap %d", len(se.Stderr), StderrPreviewBytes)
	}
	if se.StderrLen <= len(se.Stderr) {
		t.Errorf("StderrLen %d should exceed truncated preview %d", se.StderrLen, len(se.Stderr))
	}
	if !bytes.Contains(se.Stderr, []byte("TAIL_MARKER_LAST_LINE")) {
		t.Errorf("tail truncation must preserve the bottom line; got tail: %q", se.Stderr)
	}
}

func TestRun_BinaryNotFound(t *testing.T) {
	_, _, err := Run("/does/not/exist/at/all", nil)
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
	var se *Error
	if !errors.As(err, &se) {
		t.Fatalf("expected *Error even for fork failure, got %T (%v)", err, err)
	}
	if len(se.Stdout) != 0 || len(se.Stderr) != 0 {
		t.Errorf("expected empty streams on fork failure; got stdout=%q stderr=%q", se.Stdout, se.Stderr)
	}
}

func TestWrapResponseError_BadJSONPath(t *testing.T) {
	t.Setenv("FAKESUBPROC_MODE", "huge-stdout")
	stdout, stderr, err := Run(fakePath, nil)
	if err != nil {
		t.Fatalf("huge-stdout mode exits 0 — Run should succeed; got: %v", err)
	}
	var resp struct {
		Foo string `json:"foo"`
	}
	decodeErr := json.Unmarshal(stdout, &resp)
	if decodeErr == nil {
		t.Fatal("huge-stdout payload should not decode as JSON")
	}
	wrapped := WrapResponseError("/fake/bin", decodeErr, stdout, stderr)
	if len(wrapped.Stdout) > StdoutPreviewBytes {
		t.Errorf("wrapped stdout preview %d > cap %d", len(wrapped.Stdout), StdoutPreviewBytes)
	}
	if wrapped.StdoutLen <= len(wrapped.Stdout) {
		t.Errorf("StdoutLen %d should exceed truncated preview %d", wrapped.StdoutLen, len(wrapped.Stdout))
	}
	if !bytes.HasPrefix(wrapped.Stdout, []byte("HEAD_MARKER_FIRST_LINE")) {
		t.Errorf("head truncation must preserve the top line; got head: %q", wrapped.Stdout)
	}
}

func TestFailureNotes_StructuredSplit(t *testing.T) {
	se := &Error{
		Path:      "/bin/fake",
		Err:       errors.New("exit status 2"),
		Stdout:    nil,
		Stderr:    []byte("panic: boom\ngoroutine 1\n"),
		StdoutLen: 0,
		StderrLen: 24,
	}
	notes := FailureNotes("the subprocess failed", se)
	if len(notes) != 3 {
		t.Fatalf("expected 3 notes (header + se.Error + stderr preview), got %d: %v", len(notes), notes)
	}
	if notes[0] != "the subprocess failed" {
		t.Errorf("notes[0] = %q, want %q", notes[0], "the subprocess failed")
	}
	if !strings.Contains(notes[2], "panic: boom") {
		t.Errorf("stderr note should contain panic content; got: %q", notes[2])
	}
	if !strings.Contains(notes[2], "stderr") {
		t.Errorf("stderr note should be labeled; got: %q", notes[2])
	}
}

func TestFailureNotes_PlainErrorFallback(t *testing.T) {
	notes := FailureNotes("header", errors.New("some non-structured error"))
	if len(notes) != 2 {
		t.Fatalf("expected 2 notes for plain error, got %d: %v", len(notes), notes)
	}
	if !strings.Contains(notes[1], "some non-structured error") {
		t.Errorf("plain error should be preserved; got: %q", notes[1])
	}
}

func TestError_InlineSnippetCollapsesNewlines(t *testing.T) {
	se := &Error{
		Path:      "/bin/fake",
		Err:       errors.New("exit status 2"),
		Stderr:    []byte("line1\nline2\nline3\n"),
		StderrLen: 18,
	}
	msg := se.Error()
	if strings.Count(msg, "\n") != 0 {
		t.Errorf("Error() should be single-line for fallback callers; got: %q", msg)
	}
	if !strings.Contains(msg, "line1") || !strings.Contains(msg, "line3") {
		t.Errorf("inline snippet should contain stderr content; got: %q", msg)
	}
}
