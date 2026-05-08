package specvalidate

import "testing"

func TestDiagnosticsAcceptsExistingSpecSection(t *testing.T) {
	src := []byte("#[spec(\"§3.10\")]\nfn linked() -> Int { 0 }\n")
	if got := Diagnostics(src, "linked.osty"); len(got) != 0 {
		t.Fatalf("Diagnostics() = %#v, want none", got)
	}
}

func TestDiagnosticsRejectsMissingSpecSection(t *testing.T) {
	src := []byte("#[spec(\"§99.99\")]\nfn linked() -> Int { 0 }\n")
	got := Diagnostics(src, "linked.osty")
	if len(got) != 1 {
		t.Fatalf("Diagnostics() len = %d, want 1 (%#v)", len(got), got)
	}
	if got[0].Code != "E0790" {
		t.Fatalf("code = %q, want E0790", got[0].Code)
	}
	if got[0].File != "linked.osty" {
		t.Fatalf("file = %q, want linked.osty", got[0].File)
	}
}

func TestDiagnosticsSkipsCommentedSpecAnnotation(t *testing.T) {
	src := []byte("// #[spec(\"§99.99\")]\nfn linked() -> Int { 0 }\n")
	if got := Diagnostics(src, "linked.osty"); len(got) != 0 {
		t.Fatalf("Diagnostics() = %#v, want none", got)
	}
}
