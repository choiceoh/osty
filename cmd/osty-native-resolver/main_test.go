package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/selfhost"
)

func TestRunEmitsStructuredResolveResult(t *testing.T) {
	var stdout bytes.Buffer
	stdin := strings.NewReader(`{"source":"fn helper(x: Int) -> Int { x }\nfn main() { let value = helper(1) }\n"}`)
	if err := run(stdin, &stdout); err != nil {
		t.Fatalf("run error: %v", err)
	}
	var resp resolveResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Summary.Diagnostics != 0 {
		t.Fatalf("diagnostics = %d, want 0", resp.Summary.Diagnostics)
	}
	if len(resp.Symbols) == 0 {
		t.Fatalf("symbols = %#v, want non-empty result", resp.Symbols)
	}
	if len(resp.Refs) == 0 {
		t.Fatalf("refs = %#v, want non-empty result", resp.Refs)
	}
	if resp.Summary.SymbolsByKind["fn"] == 0 {
		t.Fatalf("symbolsByKind = %#v, want fn bucket", resp.Summary.SymbolsByKind)
	}
}

func TestRunRejectsInvalidJSON(t *testing.T) {
	var stdout bytes.Buffer
	err := run(strings.NewReader("{"), &stdout)
	if err == nil || !strings.Contains(err.Error(), "decode resolver request") {
		t.Fatalf("run error = %v, want decode error", err)
	}
}

func TestRunResolvesPackageStructuredRequest(t *testing.T) {
	dir := t.TempDir()
	fileA := []byte("pub fn helper() -> Int { 1 }\n")
	fileB := []byte("fn main() { let value = helper() }\n")
	reqBody, err := json.Marshal(resolveRequest{
		Package: &selfhost.PackageResolveInput{
			Files: []selfhost.PackageResolveFile{
				{Source: fileA, Base: 0, Name: "a.osty", Path: filepath.Join(dir, "a.osty")},
				{Source: fileB, Base: len(fileA) + 1, Name: "b.osty", Path: filepath.Join(dir, "b.osty")},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	var stdout bytes.Buffer
	if err := run(bytes.NewReader(reqBody), &stdout); err != nil {
		t.Fatalf("run error: %v", err)
	}
	var resp resolveResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Summary.Diagnostics != 0 {
		t.Fatalf("diagnostics = %d, want 0", resp.Summary.Diagnostics)
	}
	if len(resp.Refs) == 0 {
		t.Fatalf("refs = %#v, want package ref data", resp.Refs)
	}
	found := false
	for _, ref := range resp.Refs {
		if ref.Name == "helper" && strings.HasSuffix(ref.File, "b.osty") && strings.HasSuffix(ref.TargetFile, "a.osty") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("refs = %#v, want cross-file helper attribution", resp.Refs)
	}
}
