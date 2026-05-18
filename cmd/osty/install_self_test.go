package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/osty/osty/internal/toolchain/selfhostcache"
)

// TestInstallSelfUsageMessage exercises the helper that some future
// help-text generator will consult. Cheap correctness check that the
// usage string mentions the canonical flags.
func TestInstallSelfUsageMessage(t *testing.T) {
	got := installSelfUsage()
	for _, want := range []string{
		"osty install-self",
		"--force",
		"--toolchain-dir",
		"--osty-bin",
		"selfhostcache",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("usage missing %q:\n%s", want, got)
		}
	}
}

// fakeOstyBin builds a minimal Go shim that pretends to be the
// `osty` binary for the duration of the test. It accepts any
// arguments and writes a synthetic osty-self binary into the
// expected debug/ output path under the working directory.
func fakeOstyBin(t *testing.T, projectRoot, body string) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	prog := `package main
import (
    "fmt"
    "os"
    "path/filepath"
)
func main() {
    cwd, _ := os.Getwd()
    fmt.Fprintln(os.Stderr, "fake-osty:", os.Args[1:])
    out := filepath.Join(cwd, "toolchain", ".osty", "out", "debug", "llvm", ` + "\"" + selfhostcache.BinaryName() + "\"" + `)
    if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
    if err := os.WriteFile(out, []byte(` + "`" + body + "`" + `), 0o755); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatalf("write fake osty: %v", err)
	}
	binName := "fake-osty"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	bin := filepath.Join(dir, binName)
	cmd := exec.Command("go", "build", "-o", bin, src)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build fake osty: %v\n%s", err, out)
	}
	return bin
}

func fakeExePath(dir, base string) string {
	if runtime.GOOS == "windows" {
		base += ".exe"
	}
	return filepath.Join(dir, base)
}

// TestBuildOstySelfRunsHostBinary covers the lower-level helper
// directly without going through the CLI flag parser. Confirms the
// helper invokes the host binary in the right directory and finds the
// produced osty-self path.
func TestBuildOstySelfRunsHostBinary(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "toolchain"), 0o755); err != nil {
		t.Fatalf("mkdir toolchain: %v", err)
	}
	hostBin := fakeOstyBin(t, root, "osty-self body bytes")

	got, err := buildOstySelf(t.Context(), hostBin, root, filepath.Join(root, "toolchain"))
	if err != nil {
		t.Fatalf("buildOstySelf: %v", err)
	}
	want := filepath.Join(root, "toolchain", ".osty", "out", "debug", "llvm", selfhostcache.BinaryName())
	if got != want {
		t.Fatalf("got = %q, want %q", got, want)
	}
	body, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("read built bin: %v", err)
	}
	if string(body) != "osty-self body bytes" {
		t.Fatalf("body mismatch: %q", body)
	}
}

func TestBuildOstySelfFailsWhenHostExits(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "toolchain"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte(`package main
import "os"
func main() { os.Exit(7) }
`), 0o644); err != nil {
		t.Fatalf("write fake osty: %v", err)
	}
	bin := fakeExePath(dir, "fake-osty-fail")
	out, err := exec.Command("go", "build", "-o", bin, src).CombinedOutput()
	if err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	_, err = buildOstySelf(t.Context(), bin, root, filepath.Join(root, "toolchain"))
	if err == nil {
		t.Fatal("expected error when host exits non-zero")
	}
}

// TestRunInstallSelfPrintsBootstrapHintOnFailure exercises the
// fresh-clone diagnostic: when the internal source bootstrap fails, the
// user is pointed at the prebuilt osty-self options instead of env-var
// bootstrap toggles.
func TestRunInstallSelfPrintsBootstrapHintOnFailure(t *testing.T) {
	ostyBin := buildOstyBinaryForTest(t)

	// Stage a synthetic project root pointing at a host-osty stub
	// that exits non-zero — emulates the chicken-egg failure
	// without needing the real toolchain.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "osty.toml"), []byte("[package]\nname = \"fake\"\n"), 0o644); err != nil {
		t.Fatalf("osty.toml: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "toolchain"), 0o755); err != nil {
		t.Fatalf("toolchain mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "toolchain", "main.osty"), []byte("// stub\n"), 0o644); err != nil {
		t.Fatalf("main.osty: %v", err)
	}
	stubDir := t.TempDir()
	stub := fakeExePath(stubDir, "stub-osty")
	src := filepath.Join(stubDir, "stub-osty.go")
	if err := os.WriteFile(src, []byte(`package main
import "os"
func main() { os.Exit(1) }
`), 0o644); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	if out, err := exec.Command("go", "build", "-o", stub, src).CombinedOutput(); err != nil {
		t.Fatalf("build stub: %v\n%s", err, out)
	}

	cmd := exec.Command(ostyBin, "install-self", "--osty-bin", stub)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected install-self to fail\n%s", out)
	}
	got := string(out)
	for _, want := range []string{
		"hint: install-self tried the internal source bootstrap path",
		"OSTY_SELF_REGISTRY_URL",
		"OSTY_SELF_BIN",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "OSTY_STAGE0_FALLBACK") || strings.Contains(got, "OSTY_INSTALL_SELF_ALLOW_SOURCE_BOOTSTRAP") {
		t.Errorf("output should not mention removed bootstrap env vars:\n%s", got)
	}
}

func TestBuildOstySelfFailsWhenNoBinaryProduced(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "toolchain"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte(`package main
func main() {}
`), 0o644); err != nil {
		t.Fatalf("write fake osty: %v", err)
	}
	bin := fakeExePath(dir, "fake-osty-noop")
	out, err := exec.Command("go", "build", "-o", bin, src).CombinedOutput()
	if err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	_, err = buildOstySelf(t.Context(), bin, root, filepath.Join(root, "toolchain"))
	if err == nil {
		t.Fatal("expected error when host produces no osty-self binary")
	}
	if !strings.Contains(err.Error(), "did not produce") {
		t.Fatalf("err = %v, want missing-binary message", err)
	}
}
