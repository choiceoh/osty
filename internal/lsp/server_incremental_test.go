package lsp

import (
	"bytes"
	"testing"
)

// TestEngineBackedAnalyzeCachesRepeatedEdits verifies that
// analyzeSingleFileViaEngine actually reuses cached work: re-analyzing
// the same buffer should produce a metric delta with zero misses and
// zero reruns, just hits.
func TestEngineBackedAnalyzeCachesRepeatedEdits(t *testing.T) {
	s := NewServer(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})

	uri := "untitled:Scratch-1.osty"
	src := []byte("pub fn main() { }\n")

	// First analyze — everything cold.
	_ = s.analyzeSingleFileViaEngine(uri, src)

	before := s.engine.DB.Metrics()
	_ = s.analyzeSingleFileViaEngine(uri, src)
	after := s.engine.DB.Metrics().Sub(before)

	if after.Misses != 0 {
		t.Errorf("repeated same-source analyze should not miss: %+v", after)
	}
	if after.Reruns != 0 {
		t.Errorf("repeated same-source analyze should not rerun: %+v", after)
	}
	if after.Hits == 0 {
		t.Errorf("repeated same-source analyze should produce hits: %+v", after)
	}
}

// TestEngineBackedAnalyzeCutoffsOnWhitespaceEdit validates the
// cornerstone Salsa-style behavior: a byte-level change that doesn't
// alter the resolver's semantic output lets downstream queries
// (CheckFile, LintFile) stay cached via early cutoff.
func TestEngineBackedAnalyzeCutoffsOnWhitespaceEdit(t *testing.T) {
	s := NewServer(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})

	uri := "untitled:Scratch-2.osty"
	src := []byte("pub fn main() { }\n")
	_ = s.analyzeSingleFileViaEngine(uri, src)

	// Add a trailing newline. Source bytes differ → Parse re-runs.
	// The resolver's output (Refs/TypeRefs/PkgScope content) is
	// unchanged → ResolvePackage hash matches → CheckFile / LintFile
	// stay cached via cutoff.
	srcPlusNewline := append([]byte(nil), src...)
	srcPlusNewline = append(srcPlusNewline, '\n')

	before := s.engine.DB.Metrics()
	_ = s.analyzeSingleFileViaEngine(uri, srcPlusNewline)
	after := s.engine.DB.Metrics().Sub(before)

	if after.Cutoffs == 0 {
		t.Errorf("expected at least one cutoff on whitespace-only edit: %+v", after)
	}
	if after.Reruns == 0 {
		t.Errorf("expected at least Parse to re-run on byte diff: %+v", after)
	}
	t.Logf("whitespace-edit metrics: %+v", after)
}

// TestEngineBackedAnalyzeInvalidatesOnRealEdit proves the flip side:
// a semantic edit (new top-level decl) flushes the downstream cache
// and forces re-runs all the way through CheckFile.
func TestEngineBackedAnalyzeInvalidatesOnRealEdit(t *testing.T) {
	s := NewServer(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})

	uri := "untitled:Scratch-3.osty"
	_ = s.analyzeSingleFileViaEngine(uri, []byte("pub fn a() { }\n"))

	before := s.engine.DB.Metrics()
	_ = s.analyzeSingleFileViaEngine(uri, []byte("pub fn a() { }\npub fn b() { }\n"))
	after := s.engine.DB.Metrics().Sub(before)

	if after.Reruns == 0 {
		t.Errorf("expected reruns after adding a new decl: %+v", after)
	}
}
