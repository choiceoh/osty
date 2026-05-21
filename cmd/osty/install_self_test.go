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
	cmd.Env = append(os.Environ(), "OSTY_SELF_REGISTRY_OFFLINE=1")
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
	cmd.Env = append(os.Environ(),
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
	cmd.Env = append(os.Environ(),
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
	cmd.Env = append(os.Environ(),
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
