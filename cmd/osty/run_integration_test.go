package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/osty/osty/internal/backend"
	"github.com/osty/osty/internal/profile"
)

var (
	ostyBinOnce   sync.Once
	ostyBinDir    string
	ostyBinPath   string
	ostyBinErr    error
	ostyBinOutput string
)

func TestMain(m *testing.M) {
	code := m.Run()
	if ostyBinDir != "" {
		_ = os.RemoveAll(ostyBinDir)
	}
	os.Exit(code)
}

func TestRunStdFSUsesInvocationCwd(t *testing.T) {
	if testing.Short() {
		t.Skip("CLI integration test (slow)")
	}
	dir := t.TempDir()
	writeStdFSProject(t, dir, "fsdemo")

	bin := buildOstyBinary(t)
	out, code := runBuiltOsty(t, bin, dir, "run")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d. output:\n%s", code, out)
	}
	want := strings.Join([]string{
		"false",
		"true",
		"roundtrip",
		"false",
		"true",
	}, "\n")
	if got := strings.TrimSpace(out); got != want {
		t.Fatalf("stdout = %q, want %q\nfull output:\n%s", got, want, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "data", "payload.txt")); !os.IsNotExist(err) {
		t.Fatalf("payload should have been removed from invocation cwd, stat err = %v", err)
	}
}

func TestBuildStdFSBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("CLI integration test (slow)")
	}
	dir := t.TempDir()
	writeStdFSProject(t, dir, "fsbuild")

	osty := buildOstyBinary(t)
	out, code := runBuiltOsty(t, osty, dir, "build")
	if code != 0 {
		t.Fatalf("expected build exit 0, got %d. output:\n%s", code, out)
	}
	exeName := "fsbuild"
	if runtime.GOOS == "windows" {
		exeName += ".exe"
	}
	exe := backend.Layout{
		Root:    dir,
		Profile: profile.NameDebug,
	}.Artifacts(backend.NameGo, exeName).Binary
	cmd := exec.Command(exe)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		t.Fatalf("run built binary: %v\n%s", err, buf.String())
	}
	want := strings.Join([]string{
		"false",
		"true",
		"roundtrip",
		"false",
		"true",
	}, "\n")
	if got := strings.TrimSpace(buf.String()); got != want {
		t.Fatalf("stdout = %q, want %q\nfull output:\n%s", got, want, buf.String())
	}
}

func TestBuildIgnoresLegacyGoOutputAndCache(t *testing.T) {
	if testing.Short() {
		t.Skip("CLI integration test (slow)")
	}
	dir := t.TempDir()
	mustWrite(t, dir, "osty.toml", `[package]
name = "legacycompat"
version = "0.1.0"
edition = "0.3"
`)
	mustWrite(t, dir, "main.osty", `fn main() {
    println(42)
}
`)

	legacyOut := profile.OutputDir(dir, profile.NameDebug, "")
	if err := os.MkdirAll(legacyOut, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyOut, "main.go"), []byte("package broken\nfunc nope(\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(profile.LegacyCachePath(dir, profile.NameDebug, "")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profile.LegacyCachePath(dir, profile.NameDebug, ""), []byte(`{"profile":"debug","tool_version":"osty-dev","sources":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	osty := buildOstyBinary(t)
	out, code := runBuiltOsty(t, osty, dir, "build")
	if code != 0 {
		t.Fatalf("expected build exit 0, got %d. output:\n%s", code, out)
	}
	if strings.Contains(out, "Build is up to date") {
		t.Fatalf("build reused legacy cache unexpectedly:\n%s", out)
	}

	exeName := "legacycompat"
	if runtime.GOOS == "windows" {
		exeName += ".exe"
	}
	artifacts := backend.Layout{
		Root:    dir,
		Profile: profile.NameDebug,
	}.Artifacts(backend.NameGo, exeName)
	if _, err := os.Stat(artifacts.GoSource); err != nil {
		t.Fatalf("backend-aware go source missing: %v", err)
	}
	if _, err := os.Stat(artifacts.Binary); err != nil {
		t.Fatalf("backend-aware binary missing: %v", err)
	}
	if _, err := os.Stat(profile.BackendCachePath(dir, profile.NameDebug, "", backend.NameGo.String())); err != nil {
		t.Fatalf("backend-aware cache missing: %v", err)
	}
	if _, err := os.Stat(profile.LegacyCachePath(dir, profile.NameDebug, "")); err != nil {
		t.Fatalf("legacy cache should be left alone until cache clean: %v", err)
	}
}

func TestBuildLLVMIRWritesBackendCache(t *testing.T) {
	if testing.Short() {
		t.Skip("CLI integration test (slow)")
	}
	dir := t.TempDir()
	mustWrite(t, dir, "osty.toml", `[package]
name = "llcache"
version = "0.1.0"
edition = "0.3"
`)
	mustWrite(t, dir, "main.osty", `fn main() {
    println(40 + 2)
}
`)

	osty := buildOstyBinary(t)
	out, code := runBuiltOsty(t, osty, dir, "build", "--backend", "llvm", "--emit", "llvm-ir")
	if code != 0 {
		t.Fatalf("expected llvm-ir build exit 0, got %d. output:\n%s", code, out)
	}
	artifacts := backend.Layout{
		Root:    dir,
		Profile: profile.NameDebug,
	}.Artifacts(backend.NameLLVM, "")
	if _, err := os.Stat(artifacts.LLVMIR); err != nil {
		t.Fatalf("llvm ir artifact missing: %v", err)
	}
	cachePath := profile.BackendCachePath(dir, profile.NameDebug, "", backend.NameLLVM.String())
	cacheBytes, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("llvm cache missing: %v", err)
	}
	cacheText := string(cacheBytes)
	for _, want := range []string{
		`"backend": "llvm"`,
		`"emit": "llvm-ir"`,
		`"llvm_ir": ".osty/out/debug/llvm/main.ll"`,
	} {
		if !strings.Contains(cacheText, want) {
			t.Fatalf("cache missing %q:\n%s", want, cacheText)
		}
	}

	out, code = runBuiltOsty(t, osty, dir, "build", "--backend", "llvm", "--emit", "llvm-ir")
	if code != 0 {
		t.Fatalf("expected cached llvm-ir build exit 0, got %d. output:\n%s", code, out)
	}
	if !strings.Contains(out, "Build is up to date") || !strings.Contains(out, cachePath) {
		t.Fatalf("second build did not report llvm cache hit:\n%s", out)
	}
}

func TestRunLLVMBackendWithClang(t *testing.T) {
	if testing.Short() {
		t.Skip("CLI integration test (slow)")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not on PATH; skipping LLVM executable smoke")
	}
	dir := t.TempDir()
	mustWrite(t, dir, "osty.toml", `[package]
name = "llvmrun"
version = "0.1.0"
edition = "0.3"
`)
	mustWrite(t, dir, "main.osty", `fn main() {
    println(40 + 2)
}
`)

	osty := buildOstyBinary(t)
	out, code := runBuiltOsty(t, osty, dir, "run", "--backend", "llvm")
	if code != 0 {
		t.Fatalf("expected llvm run exit 0, got %d. output:\n%s", code, out)
	}
	if !strings.Contains(out, "42") {
		t.Fatalf("llvm run output missing 42:\n%s", out)
	}
	artifacts := backend.Layout{
		Root:    dir,
		Profile: profile.NameDebug,
	}.Artifacts(backend.NameLLVM, "llvmrun")
	for name, path := range map[string]string{
		"llvm ir": artifacts.LLVMIR,
		"object":  artifacts.Object,
		"binary":  artifacts.Binary,
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s artifact missing at %s: %v", name, path, err)
		}
	}
}

func writeStdFSProject(t *testing.T, dir, name string) {
	t.Helper()
	mustWrite(t, dir, "osty.toml", `[package]
name = "`+name+`"
version = "0.1.0"
edition = "0.3"
`)
	if err := os.Mkdir(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, dir, "main.osty", `use std.fs

fn main() {
    let p = "data/payload.txt"
    println("{fs.exists(p)}")
    fs.writeString(p, "roundtrip").unwrap()
    println("{fs.exists(p)}")
    println(fs.readToString(p).unwrap())
    fs.remove(p).unwrap()
    println("{fs.exists(p)}")
    println("{fs.remove(p).isErr()}")
}
`)
}

func buildOstyBinary(t *testing.T) string {
	t.Helper()
	ostyBinOnce.Do(func() {
		name := "osty"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		ostyBinDir, ostyBinErr = os.MkdirTemp("", "osty-test-bin-*")
		if ostyBinErr != nil {
			return
		}
		ostyBinPath = filepath.Join(ostyBinDir, name)
		cmd := exec.Command("go", "build", "-o", ostyBinPath, ".")
		cmd.Dir = repoRoot(t)
		cmd.Env = append(os.Environ(), "GOFLAGS=")
		var buf bytes.Buffer
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		ostyBinErr = cmd.Run()
		ostyBinOutput = buf.String()
	})
	if ostyBinErr != nil {
		t.Fatalf("go build osty: %v\n%s", ostyBinErr, ostyBinOutput)
	}
	return ostyBinPath
}

func runBuiltOsty(t *testing.T, bin, dir string, args ...string) (combined string, exitCode int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := buf.String()
	if exitErr, ok := err.(*exec.ExitError); ok {
		return out, exitErr.ExitCode()
	}
	if err != nil {
		t.Fatalf("run osty binary: %v\n%s", err, out)
	}
	return out, 0
}
