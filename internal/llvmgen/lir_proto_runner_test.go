package llvmgen

import (
	"errors"
	"testing"
)

func TestDefaultLIRProtoRunnerReturnsNotWired(t *testing.T) {
	t.Parallel()
	out, err := InvokeLIRProtoRunner(LIRProtoRequest{
		PackageName: "main",
		SourcePath:  "/tmp/x.osty",
		Source:      []byte("fn main() {}"),
	})
	if out != nil {
		t.Fatalf("default runner returned non-nil output %q", out)
	}
	if !errors.Is(err, ErrLIRProtoNotWired) {
		t.Fatalf("default runner err = %v, want ErrLIRProtoNotWired", err)
	}
}

type fakeLIRProtoRunner struct {
	out []byte
	err error
}

func (f *fakeLIRProtoRunner) Run(req LIRProtoRequest) ([]byte, error) {
	return f.out, f.err
}

func TestSetLIRProtoRunnerSwapsImpl(t *testing.T) {
	prev := CurrentLIRProtoRunner()
	defer SetLIRProtoRunner(prev)

	fake := &fakeLIRProtoRunner{out: []byte("; fake LLVM IR\n")}
	SetLIRProtoRunner(fake)

	out, err := InvokeLIRProtoRunner(LIRProtoRequest{PackageName: "main", SourcePath: "/tmp/y.osty"})
	if err != nil {
		t.Fatalf("fake runner unexpectedly errored: %v", err)
	}
	if string(out) != "; fake LLVM IR\n" {
		t.Fatalf("fake runner returned wrong bytes: %q", out)
	}
}

func TestSetLIRProtoRunnerNilRevertsToDefault(t *testing.T) {
	prev := CurrentLIRProtoRunner()
	defer SetLIRProtoRunner(prev)

	SetLIRProtoRunner(&fakeLIRProtoRunner{out: []byte("x")})
	SetLIRProtoRunner(nil)

	if _, err := InvokeLIRProtoRunner(LIRProtoRequest{}); !errors.Is(err, ErrLIRProtoNotWired) {
		t.Fatalf("after SetLIRProtoRunner(nil) expected ErrLIRProtoNotWired, got %v", err)
	}
}

func TestLIRProtoRunnerErrorTreatedAsFallback(t *testing.T) {
	prev := CurrentLIRProtoRunner()
	defer SetLIRProtoRunner(prev)

	custom := errors.New("self-host bridge unreachable")
	SetLIRProtoRunner(&fakeLIRProtoRunner{err: custom})

	_, err := InvokeLIRProtoRunner(LIRProtoRequest{PackageName: "main"})
	if !errors.Is(err, custom) {
		t.Fatalf("custom runner err not propagated: %v", err)
	}
}
