package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRunEmitsStructuredCheckResult(t *testing.T) {
	var stdout bytes.Buffer
	stdin := strings.NewReader(`{"source":"fn id<T>(value: T) -> T { value }\nfn main() { let answer = id::<Int>(1) }\n"}`)
	if err := run(stdin, &stdout); err != nil {
		t.Fatalf("run error: %v", err)
	}
	var resp checkResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Summary.Errors != 0 {
		t.Fatalf("summary errors = %d, want 0", resp.Summary.Errors)
	}
	if len(resp.TypedNodes) == 0 {
		t.Fatalf("typedNodes = %#v, want non-empty result", resp.TypedNodes)
	}
	if len(resp.Bindings) == 0 {
		t.Fatalf("bindings = %#v, want binding facts", resp.Bindings)
	}
	if len(resp.Symbols) == 0 {
		t.Fatalf("symbols = %#v, want symbol facts", resp.Symbols)
	}
	if len(resp.Instantiations) == 0 {
		t.Fatalf("instantiations = %#v, want generic instantiation facts", resp.Instantiations)
	}
}

func TestRunRejectsInvalidJSON(t *testing.T) {
	var stdout bytes.Buffer
	err := run(strings.NewReader("{"), &stdout)
	if err == nil || !strings.Contains(err.Error(), "decode checker request") {
		t.Fatalf("run error = %v, want decode error", err)
	}
}
