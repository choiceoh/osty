package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/airepair"
	"github.com/osty/osty/internal/backend"
	"github.com/osty/osty/internal/diag"
)

func TestLoadGenPackageEntryLoadsSiblingFiles(t *testing.T) {
	dir := t.TempDir()
	writeGenTestFile(t, dir, "a.osty", "pub fn helper() -> Int { 1 }\n")
	target := writeGenTestFile(t, dir, "b.osty", "fn main() -> Int { helper() }\n")

	entry, err := loadGenPackageEntry(target)
	if err != nil {
		t.Fatalf("loadGenPackageEntry() error = %v", err)
	}
	if entry.pkg == nil || len(entry.pkg.Files) != 2 {
		t.Fatalf("loaded %d package files, want 2", len(entry.pkg.Files))
	}
	if entry.file == nil {
		t.Fatalf("entry.file is nil")
	}
	if got := filepath.Base(entry.file.Path); got != "b.osty" {
		t.Fatalf("entry file = %q, want b.osty", got)
	}
	if entry.res == nil {
		t.Fatalf("entry.res is nil")
	}
}

func TestParseGenEmitFileMergesPackageSources(t *testing.T) {
	dir := t.TempDir()
	writeGenTestFile(t, dir, "a.osty", "pub fn helper() -> Int { 1 }\n")
	target := writeGenTestFile(t, dir, "b.osty", "fn main() -> Int { helper() }\n")

	entry, err := loadGenPackageEntry(target)
	if err != nil {
		t.Fatalf("loadGenPackageEntry() error = %v", err)
	}
	file, src, err := parseGenEmitFile(entry.pkg)
	if err != nil {
		t.Fatalf("parseGenEmitFile() error = %v", err)
	}
	if file == nil {
		t.Fatalf("merged file is nil")
	}
	text := string(src)
	if !strings.Contains(text, "helper") || !strings.Contains(text, "main") {
		t.Fatalf("merged source = %q, want both package files", text)
	}
}

func TestParseGenEmitFileReparsesMergedSource(t *testing.T) {
	dir := t.TempDir()
	writeGenTestFile(t, dir, "a.osty", "pub fn helper() -> Int { 1 }\n")
	target := writeGenTestFile(t, dir, "b.osty", "fn main() -> Int { helper() }\n")

	entry, err := loadGenPackageEntry(target)
	if err != nil {
		t.Fatalf("loadGenPackageEntry() error = %v", err)
	}
	file, _, err := parseGenEmitFile(entry.pkg)
	if err != nil {
		t.Fatalf("parseGenEmitFile() error = %v", err)
	}
	if file == nil {
		t.Fatalf("merged file is nil")
	}
	if got, want := len(file.Decls), 2; got != want {
		t.Fatalf("merged decl count = %d, want %d", got, want)
	}
	if got, want := file.Decls[0], entry.pkg.Files[0].File.Decls[0]; got == want {
		t.Fatalf("decl[0] reused the original AST node; want a reparsed merged AST")
	}
	if got, want := file.Decls[1], entry.pkg.Files[1].File.Decls[0]; got == want {
		t.Fatalf("decl[1] reused the original AST node; want a reparsed merged AST")
	}
}

func TestSplitGenCheckDiagsDefersUnavailableChecker(t *testing.T) {
	unavailable := diag.New(diag.Error, "type checking unavailable for package").Build()
	blocking := diag.New(diag.Error, "native checker reported type errors: 1 error(s)").Build()

	blockingDiags, deferredDiags := splitGenCheckDiags([]*diag.Diagnostic{unavailable, blocking})
	if genAllowsDeferredCheckerErrors() {
		if len(blockingDiags) != 0 {
			t.Fatalf("blocking diags = %#v, want none when deferred checker errors are enabled", blockingDiags)
		}
		if len(deferredDiags) != 2 {
			t.Fatalf("deferred diags = %#v, want both diagnostics when deferred checker errors are enabled", deferredDiags)
		}
		return
	}
	if len(blockingDiags) != 1 || blockingDiags[0] != blocking {
		t.Fatalf("blocking diags = %#v, want only the real checker error", blockingDiags)
	}
	if len(deferredDiags) != 1 || deferredDiags[0] != unavailable {
		t.Fatalf("deferred diags = %#v, want only the unavailable checker diag", deferredDiags)
	}
}

func TestLoadGenPackageEntryWithTransformRepairsForeignSyntax(t *testing.T) {
	dir := t.TempDir()
	target := writeGenTestFile(t, dir, "main.osty", "func main() -> Int { 1 }\n")

	flags := cliFlags{aiRepair: true, aiMode: airepair.ModeAutoAssist}
	entry, err := loadGenPackageEntryWithTransform(target, aiRepairSourceTransform("osty gen --airepair", io.Discard, flags))
	if err != nil {
		t.Fatalf("loadGenPackageEntryWithTransform() error = %v", err)
	}
	if entry == nil || entry.file == nil {
		t.Fatal("expected a loaded gen package entry")
	}
	if got, want := string(entry.file.Source), "fn main() -> Int { 1 }\n"; got != want {
		t.Fatalf("entry source = %q, want %q", got, want)
	}
	if hasError(entry.res.Diags) {
		t.Fatalf("resolve diags = %#v, want no blocking front-end errors after airepair", entry.res.Diags)
	}
}

func TestPrepareGenBackendEntryUsesPackageLoweringForSiblingFiles(t *testing.T) {
	dir := t.TempDir()
	writeGenTestFile(t, dir, "a.osty", "pub fn helper() -> Int { 1 }\n")
	target := writeGenTestFile(t, dir, "b.osty", "fn main() -> Int { helper() }\n")

	entry, err := loadGenPackageEntry(target)
	if err != nil {
		t.Fatalf("loadGenPackageEntry() error = %v", err)
	}
	backendEntry, err := prepareGenBackendEntry("main", entry)
	if err != nil {
		t.Fatalf("prepareGenBackendEntry() error = %v", err)
	}
	if backendEntry.File != entry.file.File {
		t.Fatal("backend entry did not keep the original package entry AST")
	}
	if backendEntry.IR == nil || len(backendEntry.IR.Decls) != 2 {
		t.Fatalf("backend entry decl count = %d, want 2", len(backendEntry.IR.Decls))
	}
}

func TestPrepareGenBackendEntryUsesPackageLoweringForSingleFile(t *testing.T) {
	dir := t.TempDir()
	target := writeGenTestFile(t, dir, "main.osty", "fn main() { println(1) }\n")

	entry, err := loadGenPackageEntry(target)
	if err != nil {
		t.Fatalf("loadGenPackageEntry() error = %v", err)
	}
	backendEntry, err := prepareGenBackendEntry("main", entry)
	if err != nil {
		t.Fatalf("prepareGenBackendEntry() error = %v", err)
	}
	if backendEntry.File != entry.file.File {
		t.Fatal("backend entry did not keep the original single-file package AST")
	}
	if got := len(backendEntry.IR.Decls); got != 1 {
		t.Fatalf("backend entry decl count = %d, want 1", got)
	}
}

func TestEmitGenArtifactUsesNativeOwnedFastPathWhenCovered(t *testing.T) {
	dir := t.TempDir()
	target := writeGenTestFile(t, dir, "main.osty", `fn pick(flag: Bool) -> Int {
    if flag {
        42
    } else {
        0
    }
}

fn main() {
    let mut i = 0
    let mut sum = 0
    for i < 3 {
        sum = sum + pick(i == 2)
        i = i + 1
    }
    println(sum)
}
`)

	entry, err := loadGenPackageEntry(target)
	if err != nil {
		t.Fatalf("loadGenPackageEntry() error = %v", err)
	}
	oldTry := tryExternalGenLLVMIR
	tryExternalGenLLVMIR = func(*genPackageEntry) ([]byte, bool, []error, error) {
		return nil, false, nil, nil
	}
	t.Cleanup(func() { tryExternalGenLLVMIR = oldTry })
	backendEntry, err := prepareGenBackendEntry("main", entry)
	if err != nil {
		t.Fatalf("prepareGenBackendEntry() error = %v", err)
	}
	want, ok, warnings, err := backend.TryEmitNativeOwnedLLVMIRText(backendEntry, "")
	if err != nil {
		t.Fatalf("TryEmitNativeOwnedLLVMIRText() error = %v", err)
	}
	if !ok {
		t.Fatal("TryEmitNativeOwnedLLVMIRText() reported not covered for primitive slice")
	}

	got, result, err := emitGenArtifact(backend.NameLLVM, backend.EmitLLVMIR, "main", entry)
	if err != nil {
		t.Fatalf("emitGenArtifact() error = %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("gen llvm-ir did not use native fast path output\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if result == nil {
		t.Fatal("emitGenArtifact() result is nil")
	}
	if len(result.Warnings) != len(warnings) {
		t.Fatalf("warning count = %d, want %d", len(result.Warnings), len(warnings))
	}
}

func TestEmitGenArtifactUsesManagedNativeLLVMGenWhenCovered(t *testing.T) {
	dir := t.TempDir()
	target := writeGenTestFile(t, dir, "main.osty", "fn main() { println(1) }\n")

	entry, err := loadGenPackageEntry(target)
	if err != nil {
		t.Fatalf("loadGenPackageEntry() error = %v", err)
	}

	oldTry := tryExternalGenLLVMIR
	tryExternalGenLLVMIR = func(*genPackageEntry) ([]byte, bool, []error, error) {
		return []byte("; external llvm ir"), true, []error{errors.New("external warning")}, nil
	}
	t.Cleanup(func() { tryExternalGenLLVMIR = oldTry })

	got, result, err := emitGenArtifact(backend.NameLLVM, backend.EmitLLVMIR, "main", entry)
	if err != nil {
		t.Fatalf("emitGenArtifact() error = %v", err)
	}
	if string(got) != "; external llvm ir" {
		t.Fatalf("llvm ir = %q, want external output", got)
	}
	if result == nil || len(result.Warnings) != 1 || result.Warnings[0].Error() != "external warning" {
		t.Fatalf("warnings = %#v, want external warning", result)
	}
}

func TestEmitGenArtifactUsesNativeOwnedFastPathForStructFieldAssign(t *testing.T) {
	dir := t.TempDir()
	target := writeGenTestFile(t, dir, "main.osty", `struct Pair { left: Int, right: Int }

fn main() {
    let mut pair = Pair { left: 1, right: 2 }
    pair.left = 3
    println(pair.left)
}
`)

	entry, err := loadGenPackageEntry(target)
	if err != nil {
		t.Fatalf("loadGenPackageEntry() error = %v", err)
	}
	oldTry := tryExternalGenLLVMIR
	tryExternalGenLLVMIR = func(*genPackageEntry) ([]byte, bool, []error, error) {
		return nil, false, nil, nil
	}
	t.Cleanup(func() { tryExternalGenLLVMIR = oldTry })
	backendEntry, err := prepareGenBackendEntry("main", entry)
	if err != nil {
		t.Fatalf("prepareGenBackendEntry() error = %v", err)
	}
	want, ok, warnings, err := backend.TryEmitNativeOwnedLLVMIRText(backendEntry, "")
	if err != nil {
		t.Fatalf("TryEmitNativeOwnedLLVMIRText() error = %v", err)
	}
	if !ok {
		t.Fatal("TryEmitNativeOwnedLLVMIRText() reported not covered for struct field assignment")
	}

	got, result, err := emitGenArtifact(backend.NameLLVM, backend.EmitLLVMIR, "main", entry)
	if err != nil {
		t.Fatalf("emitGenArtifact() error = %v", err)
	}
	if result == nil {
		t.Fatal("emitGenArtifact() result is nil")
	}
	if string(got) != string(want) {
		t.Fatalf("gen llvm-ir did not use native struct-field fast path output\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if len(result.Warnings) != len(warnings) {
		t.Fatalf("warning count = %d, want %d", len(result.Warnings), len(warnings))
	}
}

func TestEmitGenArtifactUsesNativeOwnedFastPathForListIndex(t *testing.T) {
	dir := t.TempDir()
	target := writeGenTestFile(t, dir, "main.osty", `fn main() {
    let xs = [1, 2]
    println(xs[0])
}
`)

	entry, err := loadGenPackageEntry(target)
	if err != nil {
		t.Fatalf("loadGenPackageEntry() error = %v", err)
	}
	oldTry := tryExternalGenLLVMIR
	tryExternalGenLLVMIR = func(*genPackageEntry) ([]byte, bool, []error, error) {
		return nil, false, nil, nil
	}
	t.Cleanup(func() { tryExternalGenLLVMIR = oldTry })
	backendEntry, err := prepareGenBackendEntry("main", entry)
	if err != nil {
		t.Fatalf("prepareGenBackendEntry() error = %v", err)
	}
	want, ok, warnings, err := backend.TryEmitNativeOwnedLLVMIRText(backendEntry, "")
	if err != nil {
		t.Fatalf("TryEmitNativeOwnedLLVMIRText() error = %v", err)
	}
	if !ok {
		t.Fatal("TryEmitNativeOwnedLLVMIRText() reported not covered for list index")
	}

	got, result, err := emitGenArtifact(backend.NameLLVM, backend.EmitLLVMIR, "main", entry)
	if err != nil {
		t.Fatalf("emitGenArtifact() error = %v", err)
	}
	if result == nil {
		t.Fatal("emitGenArtifact() result is nil")
	}
	if string(got) != string(want) {
		t.Fatalf("gen llvm-ir did not use native list fast path output\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if len(result.Warnings) != len(warnings) {
		t.Fatalf("warning count = %d, want %d", len(result.Warnings), len(warnings))
	}
}

func TestEmitGenArtifactFallsBackForUncoveredNativeOwnedModule(t *testing.T) {
	dir := t.TempDir()
	target := writeGenTestFile(t, dir, "main.osty", `fn resolve(name: String?) -> String {
    name ?? "anonymous"
}

fn main() {
}
`)

	entry, err := loadGenPackageEntry(target)
	if err != nil {
		t.Fatalf("loadGenPackageEntry() error = %v", err)
	}
	oldTry := tryExternalGenLLVMIR
	tryExternalGenLLVMIR = func(*genPackageEntry) ([]byte, bool, []error, error) {
		return nil, false, nil, nil
	}
	t.Cleanup(func() { tryExternalGenLLVMIR = oldTry })
	backendEntry, err := prepareGenBackendEntry("main", entry)
	if err != nil {
		t.Fatalf("prepareGenBackendEntry() error = %v", err)
	}
	if _, ok, _, err := backend.TryEmitNativeOwnedLLVMIRText(backendEntry, ""); err != nil {
		t.Fatalf("TryEmitNativeOwnedLLVMIRText() error = %v", err)
	} else if ok {
		t.Fatal("TryEmitNativeOwnedLLVMIRText() unexpectedly covered coalesce fallback")
	}

	got, result, err := emitGenArtifact(backend.NameLLVM, backend.EmitLLVMIR, "main", entry)
	if err != nil {
		t.Fatalf("emitGenArtifact() error = %v", err)
	}
	if result == nil {
		t.Fatal("emitGenArtifact() result is nil")
	}
	if !strings.Contains(string(got), "coalesce.none") || !strings.Contains(string(got), "phi ptr") {
		t.Fatalf("fallback llvm-ir missing coalesce shape:\n%s", got)
	}
}

func TestGenCLIMultiFilePackageEmitsLLVMIR(t *testing.T) {
	dir := t.TempDir()
	writeGenTestFile(t, dir, "a.osty", "pub fn helper() -> Int { 1 }\n")
	target := writeGenTestFile(t, dir, "b.osty", "fn main() -> Int { helper() }\n")

	got := runOstyCLI(t, "gen", "--emit", "llvm-ir", target)
	if got.exit != 0 {
		t.Fatalf("osty gen exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stdout, "@helper") {
		t.Fatalf("stdout missing helper symbol:\n%s", got.stdout)
	}
	if !strings.Contains(got.stdout, "@main") {
		t.Fatalf("stdout missing main symbol:\n%s", got.stdout)
	}
}

func writeGenTestFile(t *testing.T, dir, name, contents string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}
