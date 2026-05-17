package backend

import (
	"context"
	"path/filepath"
	"testing"
)

// TestLLVMBackendEmitPrebuiltIncludesExtraObjectsInLink verifies that
// `Request.ExtraObjects` paths flow into the `LinkBinary` call between
// the main package object and the runtime object — the slot the
// cross-package dependency link path needs to satisfy main's
// `declare`d symbols (PR-G1 wiring scaffold).
//
// The check is order-sensitive: deps after main so dep `define`s
// resolve main `declare`s, runtime last so it gets the last shot at
// unresolved symbols. Anyone reordering the slice without auditing
// the existing OS-specific link library ordering (e.g. macOS
// `-framework Security`) will trip here.
func TestLLVMBackendEmitPrebuiltIncludesExtraObjectsInLink(t *testing.T) {
	tc := &fakeLLVMToolchain{}
	b := LLVMBackend{toolchain: tc}

	root := t.TempDir()
	extraA := filepath.Join(root, "dep_a.o")
	extraB := filepath.Join(root, "dep_b.o")

	req := Request{
		Layout: Layout{
			Root:    root,
			Profile: "debug",
		},
		Emit:         EmitBinary,
		BinaryName:   "test_extra_objects",
		ExtraObjects: []string{extraA, extraB},
	}

	_, err := b.emitPrebuiltIR(context.Background(), req, []byte("; minimal placeholder IR\n"), nil)
	if err != nil {
		t.Fatalf("emitPrebuiltIR returned error: %v", err)
	}
	if len(tc.links) != 1 {
		t.Fatalf("link calls = %d, want 1", len(tc.links))
	}
	got := tc.links[0].objectPaths
	if len(got) < 3 {
		t.Fatalf("linkObjects = %#v, want at least [main, dep_a.o, dep_b.o] (3 entries)", got)
	}
	// Main object first, then deps in order, then runtime.
	if got[1] != extraA {
		t.Errorf("linkObjects[1] = %q, want %q (first ExtraObject)", got[1], extraA)
	}
	if got[2] != extraB {
		t.Errorf("linkObjects[2] = %q, want %q (second ExtraObject)", got[2], extraB)
	}
}

// TestLLVMBackendEmitPrebuiltSkipsEmptyExtraObjects verifies the
// baseline shape (main + runtime only) is unchanged when
// ExtraObjects is nil — the common case for packages without
// cross-pkg deps. Without this, the PR-G1 plumbing could silently
// introduce a stray empty-string entry that breaks the link command.
func TestLLVMBackendEmitPrebuiltSkipsEmptyExtraObjects(t *testing.T) {
	tc := &fakeLLVMToolchain{}
	b := LLVMBackend{toolchain: tc}

	req := Request{
		Layout: Layout{
			Root:    t.TempDir(),
			Profile: "debug",
		},
		Emit:       EmitBinary,
		BinaryName: "test_no_extras",
	}

	_, err := b.emitPrebuiltIR(context.Background(), req, []byte("; minimal placeholder IR\n"), nil)
	if err != nil {
		t.Fatalf("emitPrebuiltIR returned error: %v", err)
	}
	if len(tc.links) != 1 {
		t.Fatalf("link calls = %d, want 1", len(tc.links))
	}
	for i, p := range tc.links[0].objectPaths {
		if p == "" {
			t.Errorf("linkObjects[%d] = empty string, want non-empty path", i)
		}
	}
}
