package llvmgen

import (
	"errors"
	"strings"
	"testing"
)

func TestLIRProtoSelectedDefaultOff(t *testing.T) {
	t.Setenv(LIRProtoEnvVar, "")
	if LIRProtoSelected() {
		t.Fatal("LIRProtoSelected() = true with empty env — gate must default off")
	}
}

func TestLIRProtoSelectedRecognizesNegativeValues(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"0", "false", "FALSE", "False", "off", "OFF", "Off", "no", "NO", "No"} {
		if lirProtoEnvOn(value) {
			t.Fatalf("lirProtoEnvOn(%q) = true, want false", value)
		}
	}
}

func TestLIRProtoSelectedRecognizesPositiveValues(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"1", "true", "TRUE", "True", "on", "yes", "anything-else"} {
		if !lirProtoEnvOn(value) {
			t.Fatalf("lirProtoEnvOn(%q) = false, want true", value)
		}
	}
}

func TestLIRProtoSelectedReadsEnvVar(t *testing.T) {
	t.Setenv(LIRProtoEnvVar, "1")
	if !LIRProtoSelected() {
		t.Fatal("LIRProtoSelected() = false with env=1, want true")
	}
}

func TestErrLIRProtoNotWiredMentionsEnvVar(t *testing.T) {
	t.Parallel()
	if !errors.Is(ErrLIRProtoNotWired, ErrLIRProtoNotWired) {
		t.Fatal("ErrLIRProtoNotWired must satisfy errors.Is against itself")
	}
	msg := ErrLIRProtoNotWired.Error()
	if !strings.Contains(msg, LIRProtoEnvVar) {
		t.Fatalf("ErrLIRProtoNotWired message missing %q: %s", LIRProtoEnvVar, msg)
	}
	if !strings.Contains(msg, "fall") {
		t.Fatalf("ErrLIRProtoNotWired message should mention fallback: %s", msg)
	}
}
