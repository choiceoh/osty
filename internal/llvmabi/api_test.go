package llvmabi

import (
	"errors"
	"strings"
	"testing"

	"github.com/osty/osty/internal/profile"
)

func TestUnsupportedDiagnosticForKnownKinds(t *testing.T) {
	cases := []struct {
		kind     string
		detail   string
		wantCode string
		wantKind string
	}{
		{"go-ffi", "net/http.Get", "LLVM001", "foreign-ffi"},
		{"runtime-ffi", "runtime.cabi.fopen", "LLVM002", "runtime-ffi"},
		{"source-layout", "nil MIR module", "LLVM010", "source-layout"},
		{"type-system", "i256", "LLVM011", "type-system"},
		{"statement", "for-each over closure", "LLVM012", "statement"},
		{"expression", "user-defined ?? overload", "LLVM013", "expression"},
		{"control-flow", "labeled break", "LLVM014", "control-flow"},
		{"call", "unbounded recursion", "LLVM015", "call"},
		{"name", "shadowed import alias", "LLVM016", "name"},
		{"function-signature", "variadic", "LLVM017", "function-signature"},
		{"stdlib-body", "std.crypto.aes_init", "LLVM018", "stdlib-body"},
		{"unsupported-source", "", "LLVM000", "unsupported-source"},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			got := UnsupportedDiagnosticFor(tc.kind, tc.detail)
			if got.Code != tc.wantCode {
				t.Errorf("Code = %q, want %q", got.Code, tc.wantCode)
			}
			if got.Kind != tc.wantKind {
				t.Errorf("Kind = %q, want %q", got.Kind, tc.wantKind)
			}
			if got.Hint == "" {
				t.Errorf("Hint should be populated, got empty")
			}
		})
	}
}

func TestUnsupportedDiagnosticForFillsBlankDetail(t *testing.T) {
	got := UnsupportedDiagnosticFor("go-ffi", "")
	if !strings.Contains(got.Message, "<unknown>") {
		t.Errorf("Message should fill blank detail with <unknown>, got %q", got.Message)
	}
}

func TestUnsupportedDiagnosticForUnknownKindFallsBackToLLVM000(t *testing.T) {
	got := UnsupportedDiagnosticFor("brand-new-kind", "details")
	if got.Code != "LLVM000" {
		t.Errorf("Code = %q, want LLVM000 (fallback)", got.Code)
	}
	if got.Kind != "unsupported-source" {
		t.Errorf("Kind = %q, want unsupported-source (fallback)", got.Kind)
	}
}

func TestUnsupportedErrorWrapsErrUnsupported(t *testing.T) {
	err := Unsupported("statement", "loop-on-channel")
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("errors.Is(err, ErrUnsupported) = false, want true")
	}
	var ue *UnsupportedError
	if !errors.As(err, &ue) {
		t.Fatalf("errors.As(err, *UnsupportedError) = false, want true")
	}
	if ue.Diagnostic.Code != "LLVM012" {
		t.Errorf("Diagnostic.Code = %q, want LLVM012", ue.Diagnostic.Code)
	}
}

func TestUnsupportedDiagnosticForErrorRecoversStructuredDiagnostic(t *testing.T) {
	wrapped := Unsupported("call", "missing-impl")
	got := UnsupportedDiagnosticForError(wrapped)
	if got.Code != "LLVM015" {
		t.Errorf("Code = %q, want LLVM015 from wrapped error", got.Code)
	}
}

func TestUnsupportedDiagnosticForErrorWrapsRawError(t *testing.T) {
	got := UnsupportedDiagnosticForError(errors.New("plain error"))
	if got.Code != "LLVM000" {
		t.Errorf("Code = %q, want LLVM000 for raw error", got.Code)
	}
	if !strings.Contains(got.Message, "plain error") {
		t.Errorf("Message = %q should contain raw error text", got.Message)
	}
}

func TestUnsupportedSummaryFormat(t *testing.T) {
	diag := UnsupportedDiagnostic{
		Code:    "LLVM999",
		Kind:    "fake-kind",
		Message: "fake message",
		Hint:    "fake hint",
	}
	got := UnsupportedSummary(diag)
	for _, want := range []string{"LLVM999", "fake-kind", "fake message", "fake hint"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing %q: %s", want, got)
		}
	}
}

func TestNeedsArtifactGates(t *testing.T) {
	cases := []struct {
		emit       string
		wantObject bool
		wantBinary bool
	}{
		{"llvm-ir", false, false},
		{"object", true, false},
		{"binary", true, true},
		{"", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.emit, func(t *testing.T) {
			if got := NeedsObjectArtifact(tc.emit); got != tc.wantObject {
				t.Errorf("NeedsObjectArtifact(%q) = %v, want %v", tc.emit, got, tc.wantObject)
			}
			if got := NeedsBinaryArtifact(tc.emit); got != tc.wantBinary {
				t.Errorf("NeedsBinaryArtifact(%q) = %v, want %v", tc.emit, got, tc.wantBinary)
			}
		})
	}
}

func TestCanonicalLLVMTargetFormatsHostTriple(t *testing.T) {
	got := CanonicalLLVMTarget("")
	if !strings.Contains(got, "-") {
		t.Errorf("host triple should contain dashes, got %q", got)
	}
}

func TestCanonicalLLVMTargetExpandsShortForm(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"amd64-linux", "x86_64-unknown-linux-gnu"},
		{"arm64-darwin", "arm64-apple-darwin"},
		{"amd64-windows", "x86_64-pc-windows-msvc"},
		{"amd64-js", "wasm32-unknown-emscripten"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := CanonicalLLVMTarget(tc.in)
			if got != tc.want {
				t.Errorf("CanonicalLLVMTarget(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestCanonicalLLVMTargetPassesThroughExplicitTriple(t *testing.T) {
	in := "x86_64-unknown-linux-musl"
	if got := CanonicalLLVMTarget(in); got != in {
		t.Errorf("CanonicalLLVMTarget(%q) = %q, want unchanged", in, got)
	}
}

func TestClangCompileObjectArgsIncludesTarget(t *testing.T) {
	args := ClangCompileObjectArgs("amd64-linux", "in.ll", "out.o")
	if got := strings.Join(args, " "); !strings.Contains(got, "-target x86_64-unknown-linux-gnu") {
		t.Errorf("args missing -target: %v", args)
	}
	if !contains(args, "-c") || !contains(args, "in.ll") || !contains(args, "out.o") {
		t.Errorf("args missing canonical compile flags: %v", args)
	}
}

func TestClangCompileObjectArgsAddsX86V3OnAmd64(t *testing.T) {
	got := strings.Join(ClangCompileObjectArgs("amd64-linux", "in.ll", "out.o"), " ")
	if !strings.Contains(got, "-march=x86-64-v3") {
		t.Errorf("args missing -march=x86-64-v3 for amd64: %s", got)
	}
}

func TestClangCompileObjectArgsOmitsX86V3OnArm64(t *testing.T) {
	got := strings.Join(ClangCompileObjectArgs("arm64-linux", "in.ll", "out.o"), " ")
	if strings.Contains(got, "x86-64-v3") {
		t.Errorf("args should not include x86-64-v3 on arm64: %s", got)
	}
}

func TestClangLinkBinaryArgsAddsPthreadOnLinux(t *testing.T) {
	got := strings.Join(ClangLinkBinaryArgs("amd64-linux", []string{"a.o"}, "bin"), " ")
	if !strings.Contains(got, "-pthread") {
		t.Errorf("link args missing -pthread on linux: %s", got)
	}
}

func TestClangLinkBinaryArgsOmitsPthreadOnWindows(t *testing.T) {
	got := strings.Join(ClangLinkBinaryArgs("amd64-windows", []string{"a.o"}, "bin"), " ")
	if strings.Contains(got, "-pthread") {
		t.Errorf("link args should not include -pthread on windows: %s", got)
	}
}

func TestClangCompileObjectArgsNoLTOProfilesDropLTO(t *testing.T) {
	for _, profile := range []string{"debug", "test", "profile"} {
		got := strings.Join(ClangCompileObjectArgsForProfile("amd64-linux", profile, "in.ll", "out.o"), " ")
		if strings.Contains(got, "-flto") {
			t.Errorf("profile %q compile args should not include -flto: %s", profile, got)
		}
		if strings.Contains(got, "-O3") {
			t.Errorf("profile %q compile args should not include -O3: %s", profile, got)
		}
		if !strings.Contains(got, "-O0") {
			t.Errorf("profile %q compile args should include -O0: %s", profile, got)
		}
	}
}

func TestClangCompileObjectArgsReleaseProfileKeepsLTO(t *testing.T) {
	for _, profile := range []string{"", "release", "unknown-custom"} {
		got := strings.Join(ClangCompileObjectArgsForProfile("amd64-linux", profile, "in.ll", "out.o"), " ")
		if !strings.Contains(got, "-flto=thin") {
			t.Errorf("profile %q compile args missing -flto=thin: %s", profile, got)
		}
		if !strings.Contains(got, "-O3") {
			t.Errorf("profile %q compile args missing -O3: %s", profile, got)
		}
	}
}

func TestClangLinkBinaryArgsNoLTOProfilesDropLTO(t *testing.T) {
	for _, profile := range []string{"debug", "test", "profile"} {
		got := strings.Join(ClangLinkBinaryArgsForProfile("amd64-linux", profile, []string{"a.o"}, "bin"), " ")
		if strings.Contains(got, "-flto") {
			t.Errorf("profile %q link args should not include -flto: %s", profile, got)
		}
		if strings.Contains(got, "import-instr-limit") {
			t.Errorf("profile %q link args should not include import-instr-limit tuning: %s", profile, got)
		}
		if !strings.Contains(got, "-O0") {
			t.Errorf("profile %q link args should include -O0: %s", profile, got)
		}
	}
}

func TestClangLinkBinaryArgsReleaseProfileKeepsLTO(t *testing.T) {
	for _, profile := range []string{"", "release", "unknown-custom"} {
		got := strings.Join(ClangLinkBinaryArgsForProfile("amd64-linux", profile, []string{"a.o"}, "bin"), " ")
		if !strings.Contains(got, "-flto=thin") {
			t.Errorf("profile %q link args missing -flto=thin: %s", profile, got)
		}
		if !strings.Contains(got, "-O3") {
			t.Errorf("profile %q link args missing -O3: %s", profile, got)
		}
	}
}

// TestClangLinkBinaryArgsCompatWrapperUsesReleaseDefaults locks the
// expectation that direct callers of the legacy ClangLinkBinaryArgs
// (no profile) keep the aggressive `-O3 -flto=thin` pipeline.
// install-self / osty test are the new callers that want the no-LTO
// fast path; they route through the profile-aware variant with the
// matching profile name.
func TestClangLinkBinaryArgsCompatWrapperUsesReleaseDefaults(t *testing.T) {
	got := strings.Join(ClangLinkBinaryArgs("amd64-linux", []string{"a.o"}, "bin"), " ")
	if !strings.Contains(got, "-flto=thin") {
		t.Errorf("legacy wrapper should default to -flto=thin: %s", got)
	}
}

// TestProfileSkipsLTOMatchesProfileDefaults locks the ProfileSkipsLTO
// name list against profile.Defaults() — every built-in profile whose
// `LTO` field is false MUST be in the no-LTO clang-args path, and
// every profile whose `LTO` field is true MUST stay on the release-tier
// path. If a new built-in profile lands in profile.go's Defaults(),
// this test fails loudly so the clang-args helper stays in sync
// instead of silently inheriting whichever default the unknown-name
// fallback (release) picks.
func TestProfileSkipsLTOMatchesProfileDefaults(t *testing.T) {
	for _, p := range profile.Defaults().Profiles {
		if got, want := ProfileSkipsLTO(p.Name), !p.LTO; got != want {
			t.Errorf("ProfileSkipsLTO(%q) = %v, want %v (profile.Defaults() has LTO=%v)",
				p.Name, got, want, p.LTO)
		}
	}
}

func TestRenderSkeletonContainsDiagnosticAndTarget(t *testing.T) {
	out := RenderSkeleton("mypkg", "main.osty", "binary", "amd64-linux", errors.New("test reason"))
	got := string(out)
	for _, want := range []string{
		"; package: mypkg",
		"; source: main.osty",
		"; emit: binary",
		"; target: x86_64-unknown-linux-gnu",
		"; unsupported: test reason",
		"target triple = \"x86_64-unknown-linux-gnu\"",
		"target datalayout =",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("skeleton missing %q:\n%s", want, got)
		}
	}
}

func TestRenderSkeletonDefaultsBlankInputs(t *testing.T) {
	out := RenderSkeleton("", "", "llvm-ir", "", nil)
	got := string(out)
	if !strings.Contains(got, "; package: main") {
		t.Errorf("blank package should default to main: %s", got)
	}
	if !strings.Contains(got, "; source: <unknown>") {
		t.Errorf("blank source should default to <unknown>: %s", got)
	}
}

func TestIsKnownRuntimeFFIPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"runtime.package.foo", true},
		{"runtime.cabi.fopen", true},
		{"runtime.cabi", true},
		{"runtime.strings", true},
		{"runtime.path.filepath", true},
		{"runtime.cihost", true},
		{"runtime.unknown", false},
		{"net/http", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			if got := IsKnownRuntimeFFIPath(tc.path); got != tc.want {
				t.Errorf("IsKnownRuntimeFFIPath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestLIRProtoSelectedHonorsEnv(t *testing.T) {
	old := getenv
	t.Cleanup(func() { getenv = old })
	cases := []struct {
		env  string
		want bool
	}{
		{"", false},
		{"0", false},
		{"false", false},
		{"FALSE", false},
		{"off", false},
		{"no", false},
		{"1", true},
		{"true", true},
		{"on", true},
		{"yes", true},
		{"anything-else", true},
	}
	for _, tc := range cases {
		t.Run(tc.env, func(t *testing.T) {
			getenv = func(string) string { return tc.env }
			if got := LIRProtoSelected(); got != tc.want {
				t.Errorf("LIRProtoSelected() = %v with env=%q, want %v", got, tc.env, tc.want)
			}
		})
	}
}

func TestSetLIRProtoRunnerRegistersAndReverts(t *testing.T) {
	stub := stubLIRProtoRunner{out: []byte("stub-bytes")}
	prev := registeredLIRProtoRunner
	t.Cleanup(func() { registeredLIRProtoRunner = prev })

	SetLIRProtoRunner(stub)
	got, err := InvokeLIRProtoRunner(LIRProtoRequest{})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if string(got) != "stub-bytes" {
		t.Errorf("Invoke returned %q, want stub-bytes", got)
	}

	// Setting nil reverts to the default not-wired stub.
	SetLIRProtoRunner(nil)
	_, err = InvokeLIRProtoRunner(LIRProtoRequest{})
	if !errors.Is(err, ErrLIRProtoNotWired) {
		t.Errorf("nil-revert should restore not-wired error, got %v", err)
	}
}

func TestUnsupportedBackendErrorMessage(t *testing.T) {
	if got := UnsupportedBackendErrorMessage(); got == "" {
		t.Errorf("UnsupportedBackendErrorMessage returned empty")
	}
}

func TestMissingClangAndBinaryMessages(t *testing.T) {
	if got := MissingClangMessage(); !strings.Contains(got, "clang") {
		t.Errorf("MissingClangMessage = %q, should mention clang", got)
	}
	if got := MissingBinaryArtifactMessage(); got == "" {
		t.Errorf("MissingBinaryArtifactMessage returned empty")
	}
}

func TestClangFailureMessageEmbedsAllParts(t *testing.T) {
	got := ClangFailureMessage("compile", "clang -c foo", "permission denied")
	for _, want := range []string{"compile", "clang -c foo", "permission denied"} {
		if !strings.Contains(got, want) {
			t.Errorf("ClangFailureMessage missing %q: %s", want, got)
		}
	}
}

type stubLIRProtoRunner struct {
	out []byte
	err error
}

func (s stubLIRProtoRunner) Run(LIRProtoRequest) ([]byte, error) { return s.out, s.err }

func contains(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}
