package check

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/osty/osty/internal/selfhost/api"
)

// fakeCheckerPath is set by TestMain once for the whole package; tests just
// instantiate nativeCheckerExec{path: fakeCheckerPath} and toggle behaviour
// through the FAKECHECKER_MODE env var.
var fakeCheckerPath string

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "osty-fakechecker-*")
	if err != nil {
		panic("create temp dir: " + err.Error())
	}
	defer os.RemoveAll(tmp)
	binName := "fakechecker"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	binPath := filepath.Join(tmp, binName)
	cmd := exec.Command("go", "build", "-o", binPath, "./testdata/cmd/fakechecker")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		panic("build fakechecker: " + err.Error())
	}
	fakeCheckerPath = binPath
	os.Exit(m.Run())
}

func TestSubprocessRun_OK(t *testing.T) {
	t.Setenv("FAKECHECKER_MODE", "ok")
	_, err := nativeCheckerExec{path: fakeCheckerPath}.run(api.CheckRequest{Source: ""})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSubprocessRun_StderrNoiseIgnoredOnSuccess(t *testing.T) {
	t.Setenv("FAKECHECKER_MODE", "stderr-noise")
	_, err := nativeCheckerExec{path: fakeCheckerPath}.run(api.CheckRequest{Source: ""})
	if err != nil {
		t.Fatalf("zero-exit + valid-JSON must succeed regardless of stderr; got: %v", err)
	}
}

func TestSubprocessRun_PanicExposesStderr(t *testing.T) {
	t.Setenv("FAKECHECKER_MODE", "panic")
	_, err := nativeCheckerExec{path: fakeCheckerPath}.run(api.CheckRequest{Source: ""})
	if err == nil {
		t.Fatal("expected error for panic mode")
	}
	var se *subprocessError
	if !errors.As(err, &se) {
		t.Fatalf("expected *subprocessError, got %T", err)
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
}

func TestSubprocessRun_BadJSONExposesStdoutPreview(t *testing.T) {
	t.Setenv("FAKECHECKER_MODE", "bad-json")
	_, err := nativeCheckerExec{path: fakeCheckerPath}.run(api.CheckRequest{Source: ""})
	if err == nil {
		t.Fatal("expected error for bad-json mode")
	}
	var se *subprocessError
	if !errors.As(err, &se) {
		t.Fatalf("expected *subprocessError, got %T", err)
	}
	if !bytes.Contains(se.Stdout, []byte("not json")) {
		t.Errorf("stdout preview should carry the offending bytes; got: %q", se.Stdout)
	}
	if !strings.Contains(se.Err.Error(), "decode") {
		t.Errorf("wrapped error should mention decode failure; got: %v", se.Err)
	}
}

func TestSubprocessRun_HugeStderrTruncatedAtTail(t *testing.T) {
	t.Setenv("FAKECHECKER_MODE", "huge-stderr")
	_, err := nativeCheckerExec{path: fakeCheckerPath}.run(api.CheckRequest{Source: ""})
	if err == nil {
		t.Fatal("expected error for huge-stderr mode")
	}
	var se *subprocessError
	if !errors.As(err, &se) {
		t.Fatalf("expected *subprocessError, got %T", err)
	}
	if len(se.Stderr) > subprocStderrPreviewBytes {
		t.Errorf("stderr preview %d > cap %d", len(se.Stderr), subprocStderrPreviewBytes)
	}
	if se.StderrLen <= len(se.Stderr) {
		t.Errorf("StderrLen %d should exceed truncated preview %d", se.StderrLen, len(se.Stderr))
	}
	if !bytes.Contains(se.Stderr, []byte("TAIL_MARKER_LAST_LINE")) {
		t.Errorf("tail truncation must preserve the bottom line; got tail: %q", se.Stderr)
	}
}

func TestSubprocessRun_HugeStdoutTruncatedAtHead(t *testing.T) {
	t.Setenv("FAKECHECKER_MODE", "huge-stdout")
	_, err := nativeCheckerExec{path: fakeCheckerPath}.run(api.CheckRequest{Source: ""})
	if err == nil {
		t.Fatal("expected error for huge-stdout mode")
	}
	var se *subprocessError
	if !errors.As(err, &se) {
		t.Fatalf("expected *subprocessError, got %T", err)
	}
	if len(se.Stdout) > subprocStdoutPreviewBytes {
		t.Errorf("stdout preview %d > cap %d", len(se.Stdout), subprocStdoutPreviewBytes)
	}
	if se.StdoutLen <= len(se.Stdout) {
		t.Errorf("StdoutLen %d should exceed truncated preview %d", se.StdoutLen, len(se.Stdout))
	}
	if !bytes.HasPrefix(se.Stdout, []byte("HEAD_MARKER_FIRST_LINE")) {
		t.Errorf("head truncation must preserve the top line; got head: %q", se.Stdout)
	}
}

func TestSubprocessRun_BinaryNotFound(t *testing.T) {
	_, err := nativeCheckerExec{path: "/does/not/exist/at/all"}.run(api.CheckRequest{Source: ""})
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
	var se *subprocessError
	if !errors.As(err, &se) {
		t.Fatalf("expected *subprocessError even for fork failure, got %T (%v)", err, err)
	}
	if len(se.Stdout) != 0 || len(se.Stderr) != 0 {
		t.Errorf("expected empty streams on fork failure; got stdout=%q stderr=%q", se.Stdout, se.Stderr)
	}
}

func TestSubprocessFailureNotes_StructuredSplit(t *testing.T) {
	se := &subprocessError{
		Path:      "/bin/fake",
		Err:       errors.New("exit status 2"),
		Stdout:    nil,
		Stderr:    []byte("panic: boom\ngoroutine 1\n"),
		StdoutLen: 0,
		StderrLen: 24,
	}
	notes := subprocessFailureNotes(se)
	if len(notes) != 3 {
		t.Fatalf("expected 3 notes (header + se.Error + stderr preview), got %d: %v", len(notes), notes)
	}
	if !strings.Contains(notes[2], "panic: boom") {
		t.Errorf("stderr note should contain panic content; got: %q", notes[2])
	}
	if !strings.Contains(notes[2], "stderr") {
		t.Errorf("stderr note should be labeled; got: %q", notes[2])
	}
}

func TestSubprocessFailureNotes_PlainErrorFallback(t *testing.T) {
	notes := subprocessFailureNotes(errors.New("some non-structured error"))
	if len(notes) != 2 {
		t.Fatalf("expected 2 notes for plain error, got %d: %v", len(notes), notes)
	}
	if !strings.Contains(notes[1], "some non-structured error") {
		t.Errorf("plain error should be preserved; got: %q", notes[1])
	}
}
