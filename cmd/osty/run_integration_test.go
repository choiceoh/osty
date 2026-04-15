package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/osty/osty/internal/profile"
)

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
	exe := filepath.Join(profile.OutputDir(dir, profile.NameDebug, ""), exeName)
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
	name := "osty"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = repoRoot(t)
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build osty: %v\n%s", err, buf.String())
	}
	return bin
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
