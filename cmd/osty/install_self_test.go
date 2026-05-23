package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/osty/osty/internal/toolchain"
	"github.com/osty/osty/internal/toolchain/selfhostcache"
)

// installSelfTestEnv returns the parent environment with the
// caller-listed keys stripped, plus the overrides appended. Use
// instead of `append(os.Environ(), "KEY=VAL")` for subprocess
// invocations that need to *override* env values: duplicate keys are
// allowed in `os.Environ()` form and child `getenv` typically returns
// the FIRST match, so an unfiltered append silently loses the
// override when the parent shell already exported the same key. Tests
// that depend on `OSTY_STAGE0_FALLBACK` / `OSTY_SELF_BIN` semantics
// MUST go through this helper so the harness is hermetic across the
// dev shells engineers actually run them in.
func installSelfTestEnv(t *testing.T, overrides ...string) []string {
	t.Helper()
	strip := map[string]bool{}
	for _, kv := range overrides {
		if eq := strings.IndexByte(kv, '='); eq > 0 {
			strip[kv[:eq]] = true
		}
	}
	parent := os.Environ()
	out := make([]string, 0, len(parent)+len(overrides))
	for _, kv := range parent {
		if eq := strings.IndexByte(kv, '='); eq > 0 && strip[kv[:eq]] {
			continue
		}
		out = append(out, kv)
	}
	return append(out, overrides...)
}

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
	bin := filepath.Join(dir, fakeOstyBinName("fake-osty"))
	cmd := exec.Command("go", "build", "-o", bin, src)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build fake osty: %v\n%s", err, out)
	}
	return bin
}

func fakeOstyBinName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

func fakeExePath(dir, base string) string {
	return filepath.Join(dir, fakeOstyBinName(base))
}

func fakeSelfhostBin(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	prog := `package main
import (
    "fmt"
    "os"
    "path/filepath"
    "strings"
)
func main() {
    cwd, _ := os.Getwd()
    if err := os.WriteFile(filepath.Join(cwd, "selfhost-args.txt"), []byte(strings.Join(os.Args[1:], "\n")), 0o644); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
    want := []string{"build", "--backend", "llvm", "--emit", "binary", "--force"}
    if len(os.Args) != len(want)+2 {
        fmt.Fprintf(os.Stderr, "args length = %d, want %d: %v\n", len(os.Args)-1, len(want)+1, os.Args[1:])
        os.Exit(1)
    }
    for i, arg := range want {
        if os.Args[i+1] != arg {
            fmt.Fprintf(os.Stderr, "arg %d = %q, want %q\n", i, os.Args[i+1], arg)
            os.Exit(1)
        }
    }
    toolchainDir := os.Args[len(os.Args)-1]
    if !filepath.IsAbs(toolchainDir) {
        toolchainDir = filepath.Join(cwd, toolchainDir)
    }
    out := filepath.Join(toolchainDir, ".osty", "out", "debug", "llvm", ` + "\"" + selfhostcache.BinaryName() + "\"" + `)
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
		t.Fatalf("write fake osty-self: %v", err)
	}
	bin := filepath.Join(dir, fakeOstyBinName("fake-osty-self"))
	cmd := exec.Command("go", "build", "-o", bin, src)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build fake osty-self: %v\n%s", err, out)
	}
	return bin
}

func fakeMIRDriverHostBin(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	prog := `package main
import (
    "fmt"
    "os"
    "path/filepath"
    "strings"
)
func main() {
    cwd, _ := os.Getwd()
    if err := os.WriteFile(filepath.Join(cwd, "host-self-bin.txt"), []byte(os.Getenv("OSTY_SELF_BIN")), 0o644); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
    if len(os.Args) < 6 {
        fmt.Fprintf(os.Stderr, "args too short: %v\n", os.Args[1:])
        os.Exit(1)
    }
    got := strings.Join(os.Args[1:len(os.Args)-1], "\n")
    want := "build\n--backend=llvm\n--emit\nbinary\n--force"
    if got != want {
        fmt.Fprintf(os.Stderr, "args = %q, want %q\n", got, want)
        os.Exit(1)
    }
    workDir := os.Args[len(os.Args)-1]
    out := filepath.Join(workDir, ".osty", "out", "debug", "llvm", ` + "\"" + selfhostcache.BinaryName() + "\"" + `)
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
		t.Fatalf("write fake MIR driver host: %v", err)
	}
	bin := filepath.Join(dir, fakeOstyBinName("fake-mir-driver-host"))
	cmd := exec.Command("go", "build", "-o", bin, src)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build fake MIR driver host: %v\n%s", err, out)
	}
	return bin
}

func writeSelfhostMIRDriverSourceStubs(t *testing.T, root string) {
	t.Helper()
	for _, rel := range selfhostMIRDriverSourceFiles() {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		body := "// " + filepath.ToSlash(rel) + "\n"
		if strings.HasSuffix(rel, "selfhost_mir_driver.osty") {
			body += "fn selfhostMirDriverMain() {}\n"
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
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

func TestBuildOstySelfWithSelfhostRunsResolvedBinary(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "toolchain"), 0o755); err != nil {
		t.Fatalf("mkdir toolchain: %v", err)
	}
	selfBin := fakeSelfhostBin(t, "resolved osty-self body")
	t.Setenv(installSelfTryDirectBuildEnv, "1")

	got, err := buildOstySelfWithSelfhost(t.Context(), selfBin, "", root, filepath.Join(root, "toolchain"))
	if err != nil {
		t.Fatalf("buildOstySelfWithSelfhost: %v", err)
	}
	want := filepath.Join(root, "toolchain", ".osty", "out", "debug", "llvm", selfhostcache.BinaryName())
	if got != want {
		t.Fatalf("got = %q, want %q", got, want)
	}
	body, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("read built bin: %v", err)
	}
	if string(body) != "resolved osty-self body" {
		t.Fatalf("body mismatch: %q", body)
	}
	args, err := os.ReadFile(filepath.Join(root, "selfhost-args.txt"))
	if err != nil {
		t.Fatalf("read selfhost args: %v", err)
	}
	if gotArgs := string(args); !strings.Contains(gotArgs, "build\n--backend\nllvm\n--emit\nbinary\n--force\n") {
		t.Fatalf("selfhost args = %q", gotArgs)
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
	// Opt into stage0 source bootstrap so the run actually reaches
	// buildOstySelf and fails inside it — the path this test covers.
	cmd.Env = installSelfTestEnv(t,
		"OSTY_SELF_REGISTRY_OFFLINE=1",
		"OSTY_STAGE0_FALLBACK=1",
	)
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
	if strings.Contains(got, "OSTY_INSTALL_SELF_ALLOW_SOURCE_BOOTSTRAP") {
		t.Errorf("output should not mention retired bootstrap env var OSTY_INSTALL_SELF_ALLOW_SOURCE_BOOTSTRAP:\n%s", got)
	}
}

// TestRunInstallSelfWithoutStage0FallbackErrorsCleanly verifies that a
// fresh project with no reachable registry and no OSTY_STAGE0_FALLBACK
// opt-in does NOT silently fall back to the stage0 source bootstrap.
// Instead it exits non-zero with a hint that names the three ways to
// supply a prebuilt osty-self (registry / OSTY_SELF_BIN / the explicit
// stage0 opt-in). This pins the gate that keeps the stage0 emitter off
// the default install-self path.
func TestRunInstallSelfWithoutStage0FallbackErrorsCleanly(t *testing.T) {
	ostyBin := buildOstyBinaryForTest(t)

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
	hostBin := fakeOstyBin(t, root, "should-not-be-built")

	cmd := exec.Command(ostyBin, "install-self", "--osty-bin", hostBin)
	cmd.Dir = root
	cmd.Env = installSelfTestEnv(t,
		"OSTY_SELF_REGISTRY_OFFLINE=1",
		"OSTY_STAGE0_FALLBACK=",
		"OSTY_SELF_BIN=",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected install-self to fail without stage0 opt-in\n%s", out)
	}
	got := string(out)
	for _, want := range []string{
		"stage0 source bootstrap is disabled",
		"OSTY_SELF_REGISTRY_URL",
		"OSTY_SELF_BIN",
		"OSTY_STAGE0_FALLBACK=1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	// The stage0 source bootstrap must not have run: its banner would
	// only appear if buildOstySelf forked the host build.
	if strings.Contains(got, "install-self tried the internal source bootstrap path") {
		t.Errorf("stage0 bootstrap hint leaked despite opt-in being unset:\n%s", got)
	}
	// The cache entry must not exist — nothing was built.
	key, err := selfhostcache.ComputeKey(root)
	if err != nil {
		t.Fatalf("compute key: %v", err)
	}
	if _, err := os.Stat(selfhostcache.CachePath(root, key)); err == nil {
		t.Fatalf("cache entry created despite the run erroring out")
	}
}

func TestRunInstallSelfStage0FallbackAttemptsSourceBootstrap(t *testing.T) {
	ostyBin := buildOstyBinaryForTest(t)

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
	hostBin := fakeOstyBin(t, root, "fresh stage0 osty-self")

	cmd := exec.Command(ostyBin, "install-self", "--osty-bin", hostBin)
	cmd.Dir = root
	cmd.Env = installSelfTestEnv(t,
		"OSTY_STAGE0_FALLBACK=1",
		"OSTY_SELF_REGISTRY_OFFLINE=1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install-self should source-bootstrap when stage0 fallback is set: %v\n%s", err, out)
	}
	got := string(out)
	if !strings.Contains(got, "installed:") {
		t.Fatalf("install-self did not report cache install:\n%s", got)
	}
	key, err := selfhostcache.ComputeKey(root)
	if err != nil {
		t.Fatalf("compute key: %v", err)
	}
	cachedPath := selfhostcache.CachePath(root, key)
	body, err := os.ReadFile(cachedPath)
	if err != nil {
		t.Fatalf("read cached osty-self: %v", err)
	}
	if string(body) != "fresh stage0 osty-self" {
		t.Fatalf("cached body = %q", body)
	}
}

// TestRunInstallSelfStage0FallbackInvalidatesNativeCheckerSlot pins the
// post-bootstrap LLVM-checker upgrade path: after stage0 fallback
// populates osty-self into the cache, the Go-built native checker
// dropped into `.osty/toolchain/<ver>/` by the sub-osty build is
// retired so the next `osty build` produces the LLVM variant. Without
// this, fresh-clone users would be permanently stuck on the frozen
// `internal/selfhost/generated.go` seed even after osty-self exists.
func TestRunInstallSelfStage0FallbackInvalidatesNativeCheckerSlot(t *testing.T) {
	ostyBin := buildOstyBinaryForTest(t)

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
	// Pre-seed the managed native checker slot to mimic what the sub-osty
	// build's `buildNativeCheckerViaGo` path would have written. The
	// install-self post-hook must delete this file.
	managedCheckerPath := toolchain.ManagedNativeCheckerPath(root)
	if err := os.MkdirAll(filepath.Dir(managedCheckerPath), 0o755); err != nil {
		t.Fatalf("mkdir managed checker dir: %v", err)
	}
	if err := os.WriteFile(managedCheckerPath, []byte("stale go-built checker"), 0o755); err != nil {
		t.Fatalf("seed managed checker: %v", err)
	}
	hostBin := fakeOstyBin(t, root, "fresh stage0 osty-self")

	cmd := exec.Command(ostyBin, "install-self", "--osty-bin", hostBin)
	cmd.Dir = root
	cmd.Env = installSelfTestEnv(t,
		"OSTY_STAGE0_FALLBACK=1",
		"OSTY_SELF_REGISTRY_OFFLINE=1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install-self stage0 fallback: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "installed:") {
		t.Fatalf("install-self did not report cache install:\n%s", out)
	}
	if _, statErr := os.Stat(managedCheckerPath); !os.IsNotExist(statErr) {
		t.Fatalf("managed checker slot %q must be invalidated after stage0 fallback (stat err: %v)", managedCheckerPath, statErr)
	}
}

// TestRunInstallSelfWithResolvedSelfDoesNotInvalidateNativeCheckerSlot
// guards the non-stage0 path: when install-self resolved osty-self from
// the registry/cache without entering the stage0 source bootstrap, the
// managed native checker slot is whatever it was (presumably the
// LLVM-built artifact) and must not be wiped.
func TestRunInstallSelfWithResolvedSelfDoesNotInvalidateNativeCheckerSlot(t *testing.T) {
	ostyBin := buildOstyBinaryForTest(t)

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
	// Pre-seed a managed checker we want preserved.
	managedCheckerPath := toolchain.ManagedNativeCheckerPath(root)
	if err := os.MkdirAll(filepath.Dir(managedCheckerPath), 0o755); err != nil {
		t.Fatalf("mkdir managed checker dir: %v", err)
	}
	if err := os.WriteFile(managedCheckerPath, []byte("good LLVM-built checker"), 0o755); err != nil {
		t.Fatalf("seed managed checker: %v", err)
	}
	// Pre-seed an OSTY_SELF_BIN so install-self resolves osty-self
	// without ever entering the stage0 path.
	prebuilt := filepath.Join(t.TempDir(), "prebuilt-self")
	if err := os.WriteFile(prebuilt, []byte("prebuilt osty-self"), 0o755); err != nil {
		t.Fatalf("seed prebuilt self: %v", err)
	}

	cmd := exec.Command(ostyBin, "install-self")
	cmd.Dir = root
	cmd.Env = installSelfTestEnv(t,
		"OSTY_SELF_BIN="+prebuilt,
		"OSTY_SELF_REGISTRY_OFFLINE=1",
		"OSTY_STAGE0_FALLBACK=",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install-self with prebuilt self: %v\n%s", err, out)
	}
	body, err := os.ReadFile(managedCheckerPath)
	if err != nil {
		t.Fatalf("managed checker must still exist (was invalidated unexpectedly): %v", err)
	}
	if string(body) != "good LLVM-built checker" {
		t.Fatalf("managed checker body = %q, want preserved 'good LLVM-built checker'", body)
	}
}

func TestRunInstallSelfForceUsesResolvedSelfhostWithoutStage0Fallback(t *testing.T) {
	ostyBin := buildOstyBinaryForTest(t)

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
	writeSelfhostMIRDriverSourceStubs(t, root)
	hostBin := fakeMIRDriverHostBin(t, "force selfhost osty-self")
	selfBin := fakeSelfhostBin(t, "force selfhost osty-self")

	cmd := exec.Command(ostyBin, "install-self", "--force", "--osty-bin", hostBin)
	cmd.Dir = root
	cmd.Env = installSelfTestEnv(t,
		"OSTY_SELF_BIN="+selfBin,
		"OSTY_STAGE0_FALLBACK=",
		"OSTY_STAGE0_LIST_ALL_DECLINES=",
		"OSTY_SELF_REGISTRY_OFFLINE=1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install-self should force-rebuild with resolved osty-self: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "installed:") {
		t.Fatalf("install-self did not report cache install:\n%s", out)
	}
	key, err := selfhostcache.ComputeKey(root)
	if err != nil {
		t.Fatalf("compute key: %v", err)
	}
	cachedPath := selfhostcache.CachePath(root, key)
	body, err := os.ReadFile(cachedPath)
	if err != nil {
		t.Fatalf("read cached osty-self: %v", err)
	}
	if string(body) != "force selfhost osty-self" {
		t.Fatalf("cached body = %q", body)
	}
	hostSelfBin, err := os.ReadFile(filepath.Join(root, "host-self-bin.txt"))
	if err != nil {
		t.Fatalf("host build was not invoked: %v", err)
	}
	if string(hostSelfBin) != selfBin {
		t.Fatalf("host OSTY_SELF_BIN = %q, want %q", hostSelfBin, selfBin)
	}
}

func TestRunInstallSelfForceUsesStaleLocalSelfhostWithoutStage0Fallback(t *testing.T) {
	ostyBin := buildOstyBinaryForTest(t)

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "osty.toml"), []byte("[package]\nname = \"fake\"\n"), 0o644); err != nil {
		t.Fatalf("osty.toml: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "toolchain"), 0o755); err != nil {
		t.Fatalf("toolchain mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "toolchain", "main.osty"), []byte("// new source key\n"), 0o644); err != nil {
		t.Fatalf("main.osty: %v", err)
	}
	writeSelfhostMIRDriverSourceStubs(t, root)
	hostBin := fakeMIRDriverHostBin(t, "stale cache osty-self")
	selfBin := fakeSelfhostBin(t, "stale cache osty-self")
	staleDir := filepath.Join(root, selfhostcache.CacheDirName, "stale-key-"+selfhostcache.HostTriple())
	if err := os.MkdirAll(staleDir, 0o755); err != nil {
		t.Fatalf("mkdir stale cache: %v", err)
	}
	selfBytes, err := os.ReadFile(selfBin)
	if err != nil {
		t.Fatalf("read fake selfhost: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staleDir, selfhostcache.BinaryName()), selfBytes, 0o755); err != nil {
		t.Fatalf("write stale selfhost: %v", err)
	}

	cmd := exec.Command(ostyBin, "install-self", "--force", "--osty-bin", hostBin)
	cmd.Dir = root
	cmd.Env = installSelfTestEnv(t,
		"OSTY_STAGE0_FALLBACK=",
		"OSTY_STAGE0_LIST_ALL_DECLINES=",
		"OSTY_SELF_BIN=",
		"OSTY_SELF_REGISTRY_OFFLINE=1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install-self should force-rebuild with stale local osty-self: %v\n%s", err, out)
	}
	key, err := selfhostcache.ComputeKey(root)
	if err != nil {
		t.Fatalf("compute key: %v", err)
	}
	body, err := os.ReadFile(selfhostcache.CachePath(root, key))
	if err != nil {
		t.Fatalf("read cached osty-self: %v", err)
	}
	if string(body) != "stale cache osty-self" {
		t.Fatalf("cached body = %q", body)
	}
	hostSelfBin, err := os.ReadFile(filepath.Join(root, "host-self-bin.txt"))
	if err != nil {
		t.Fatalf("host build was not invoked: %v", err)
	}
	if !strings.Contains(string(hostSelfBin), filepath.Join("stale-key-"+selfhostcache.HostTriple(), selfhostcache.BinaryName())) {
		t.Fatalf("host OSTY_SELF_BIN = %q, want stale cache binary", hostSelfBin)
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
