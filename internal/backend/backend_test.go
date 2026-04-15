package backend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
)

func TestParseName(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Name
	}{
		{"go", NameGo},
		{"llvm", NameLLVM},
	} {
		got, err := ParseName(tc.in)
		if err != nil {
			t.Fatalf("ParseName(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("ParseName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if _, err := ParseName("wasm"); err == nil {
		t.Fatal("ParseName(wasm) succeeded; want error")
	}
}

func TestValidateEmit(t *testing.T) {
	valid := []struct {
		name Name
		mode EmitMode
	}{
		{NameGo, EmitGoSource},
		{NameGo, EmitBinary},
		{NameLLVM, EmitLLVMIR},
		{NameLLVM, EmitObject},
		{NameLLVM, EmitBinary},
	}
	for _, tc := range valid {
		if err := ValidateEmit(tc.name, tc.mode); err != nil {
			t.Fatalf("ValidateEmit(%q, %q): %v", tc.name, tc.mode, err)
		}
	}
	invalid := []struct {
		name Name
		mode EmitMode
	}{
		{NameGo, EmitLLVMIR},
		{NameGo, EmitObject},
		{NameLLVM, EmitGoSource},
		{Name("bad"), EmitBinary},
		{NameGo, EmitMode("bad")},
	}
	for _, tc := range invalid {
		if err := ValidateEmit(tc.name, tc.mode); err == nil {
			t.Fatalf("ValidateEmit(%q, %q) succeeded; want error", tc.name, tc.mode)
		}
	}
}

func TestLayoutPaths(t *testing.T) {
	root := filepath.Join("tmp", "project")
	l := Layout{Root: root, Profile: "debug"}

	if got, want := l.Key(), "debug"; got != want {
		t.Fatalf("Key() = %q, want %q", got, want)
	}
	if got, want := l.OutputRoot(), filepath.Join(root, ".osty", "out", "debug"); got != want {
		t.Fatalf("OutputRoot() = %q, want %q", got, want)
	}
	if got, want := l.OutputDir(NameGo), filepath.Join(root, ".osty", "out", "debug", "go"); got != want {
		t.Fatalf("OutputDir(go) = %q, want %q", got, want)
	}
	if got, want := l.CachePath(NameLLVM), filepath.Join(root, ".osty", "cache", "debug", "llvm.json"); got != want {
		t.Fatalf("CachePath(llvm) = %q, want %q", got, want)
	}
	if got, want := l.LegacyCachePath(), filepath.Join(root, ".osty", "cache", "debug.json"); got != want {
		t.Fatalf("LegacyCachePath() = %q, want %q", got, want)
	}
}

func TestLayoutTargetPaths(t *testing.T) {
	root := filepath.Join("tmp", "project")
	l := Layout{Root: root, Profile: "release", Target: "amd64-linux"}

	if got, want := l.Key(), "release-amd64-linux"; got != want {
		t.Fatalf("Key() = %q, want %q", got, want)
	}
	if got, want := l.OutputDir(NameLLVM), filepath.Join(root, ".osty", "out", "release-amd64-linux", "llvm"); got != want {
		t.Fatalf("OutputDir(llvm) = %q, want %q", got, want)
	}
	if got, want := l.CachePath(NameGo), filepath.Join(root, ".osty", "cache", "release-amd64-linux", "go.json"); got != want {
		t.Fatalf("CachePath(go) = %q, want %q", got, want)
	}
}

func TestArtifacts(t *testing.T) {
	root := filepath.Join("tmp", "project")
	l := Layout{Root: root, Profile: "debug"}

	goArtifacts := l.Artifacts(NameGo, "app")
	if got, want := goArtifacts.GoSource, filepath.Join(root, ".osty", "out", "debug", "go", "main.go"); got != want {
		t.Fatalf("GoSource = %q, want %q", got, want)
	}
	if goArtifacts.LLVMIR != "" || goArtifacts.Object != "" || goArtifacts.RuntimeDir != "" {
		t.Fatalf("Go artifacts populated LLVM fields: %+v", goArtifacts)
	}
	if got, want := goArtifacts.Binary, filepath.Join(root, ".osty", "out", "debug", "go", "app"); got != want {
		t.Fatalf("Go Binary = %q, want %q", got, want)
	}

	llvmArtifacts := l.Artifacts(NameLLVM, "app")
	if got, want := llvmArtifacts.LLVMIR, filepath.Join(root, ".osty", "out", "debug", "llvm", "main.ll"); got != want {
		t.Fatalf("LLVMIR = %q, want %q", got, want)
	}
	if got, want := llvmArtifacts.Object, filepath.Join(root, ".osty", "out", "debug", "llvm", "main.o"); got != want {
		t.Fatalf("Object = %q, want %q", got, want)
	}
	if got, want := llvmArtifacts.RuntimeDir, filepath.Join(root, ".osty", "out", "debug", "llvm", "runtime"); got != want {
		t.Fatalf("RuntimeDir = %q, want %q", got, want)
	}
	if llvmArtifacts.GoSource != "" {
		t.Fatalf("LLVM artifacts populated GoSource: %+v", llvmArtifacts)
	}
}

func TestGoBackendEmitWritesSource(t *testing.T) {
	src := []byte(`fn main() {
    println(42)
}
`)
	file, diags := parser.ParseDiagnostics(src)
	if len(diags) != 0 {
		t.Fatalf("parse diagnostics: %v", diags)
	}
	res := resolve.File(file, resolve.NewPrelude())
	chk := check.File(file, res)

	root := t.TempDir()
	req := Request{
		Layout: Layout{Root: root, Profile: "debug"},
		Emit:   EmitGoSource,
		Entry: Entry{
			PackageName: "main",
			SourcePath:  filepath.Join(root, "main.osty"),
			File:        file,
			Resolve:     res,
			Check:       chk,
		},
		Features: []string{"alpha"},
	}
	result, err := GoBackend{}.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	wantPath := filepath.Join(root, ".osty", "out", "debug", "go", "main.go")
	if result.Artifacts.GoSource != wantPath {
		t.Fatalf("GoSource = %q, want %q", result.Artifacts.GoSource, wantPath)
	}
	data, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read emitted source: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"//go:build feat_alpha",
		"package main",
		"// Osty: " + filepath.Join(root, "main.osty") + ":1:1",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("emitted source missing %q:\n%s", want, text)
		}
	}
}
