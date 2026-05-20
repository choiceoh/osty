package toolchain

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/toolchain/selfhostcache"
)

const toolchainDirName = ".osty/toolchain"

// RecursionGuardEnv prevents EnsureNativeChecker from looping when the
// `osty build --backend llvm cmd/osty-native-checker/` subprocess spawned
// by buildNativeChecker re-enters via `check.UseManagedSubprocessChecker`.
// The subprocess inherits this env from its parent; when EnsureNativeChecker
// sees it, the call returns a deterministic error instead of forking another
// nested build.
const RecursionGuardEnv = "OSTY_BUILDING_NATIVE_CHECKER"

var (
	nativeCheckerBuildMu sync.Mutex

	managedProjectRootFunc = defaultManagedProjectRoot
	sourceRepoRootFunc     = defaultSourceRepoRoot
	installNativeChecker   = buildNativeChecker
	renameManagedFile      = os.Rename
	removeManagedFile      = os.Remove
	verifyOstySelfCached   = defaultVerifyOstySelfCached
)

// Version returns the toolchain version stamp used to scope managed artifacts.
// Release builds can override this variable via -ldflags.
var VersionStamp = "osty-dev"

func Version() string {
	return VersionStamp
}

func NativeCheckerBinaryName() string {
	name := "osty-native-checker"
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// LLVMCheckerArtifactName returns the filename of the LLVM-built
// `[bin]` artifact produced by `osty build --backend llvm
// cmd/osty-native-checker/`. The leaf differs from the managed-slot
// name because the package's `osty.toml` declares
// `name = "osty-native-checker-llvm"` to keep the dual-target
// `main.go` / `main.osty` build outputs visually distinct on disk.
func LLVMCheckerArtifactName() string {
	name := "osty-native-checker-llvm"
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func ManagedNativeCheckerPath(projectRoot string) string {
	return filepath.Join(projectRoot, toolchainDirName, Version(), NativeCheckerBinaryName())
}

// EnsureNativeChecker returns the managed checker artifact for the current
// project/worktree, building it into .osty/toolchain/<version>/ on first use.
//
// Build path (post LLVM-flip): the managed artifact is the LLVM-built
// `cmd/osty-native-checker/` package. The Go-built shell at
// `cmd/osty-native-checker/main.go` is no longer reachable from production —
// it survives only for `internal/check/testsupport.BuildSharedNativeCheckerForTests`
// and out-of-band `OSTY_NATIVE_CHECKER_BIN` overrides. See
// `docs/llvm-selfhost-plan.md` for the trajectory that drove this flip.
func EnsureNativeChecker(start string) (string, error) {
	if os.Getenv(RecursionGuardEnv) == "1" {
		return "", fmt.Errorf(
			"managed osty-native-checker build re-entered EnsureNativeChecker via %s=1; "+
				"the LLVM build subprocess (`osty build --backend llvm cmd/osty-native-checker/`) "+
				"depends on the same managed checker it is trying to produce. "+
				"Pre-build the artifact or set OSTY_NATIVE_CHECKER_BIN to an existing binary "+
				"before triggering the managed path",
			RecursionGuardEnv,
		)
	}
	root, err := managedProjectRootFunc(start)
	if err != nil {
		return "", err
	}
	path := ManagedNativeCheckerPath(root)
	if fileExists(path) {
		return path, nil
	}

	nativeCheckerBuildMu.Lock()
	defer nativeCheckerBuildMu.Unlock()

	if fileExists(path) {
		return path, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create managed toolchain dir: %w", err)
	}
	if err := installNativeChecker(path); err != nil {
		return "", err
	}
	return path, nil
}

func defaultManagedProjectRoot(start string) (string, error) {
	if root, err := manifest.FindRoot(start); err == nil {
		return root, nil
	}
	root, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("resolve managed toolchain root: %w", err)
	}
	return root, nil
}

func defaultSourceRepoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("locate native checker source root: runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return "", fmt.Errorf("locate native checker source root: %w", err)
	}
	return root, nil
}

// buildNativeChecker drives `osty build --backend llvm
// cmd/osty-native-checker/` and promotes the produced LLVM artifact
// into the managed slot.
//
// Prerequisites (caller surfaces failures as a single focused error):
//
//  1. `osty-self` is resolvable via `selfhostcache.ResolveBinary` —
//     stage0 fallback is not honoured by the production build path,
//     so a fresh worktree must run `osty install-self` once first.
//  2. The host osty executable that called us (`os.Executable()`) can
//     reinvoke itself with `build --backend llvm <pkg>` — i.e. we are
//     running inside an `osty` CLI process, not a test binary that
//     happens to import this package without exposing a build mode.
//
// The chicken-and-egg case (managed-checker build needs the checker
// to typecheck the package source) is handled by setting
// `RecursionGuardEnv` on the subprocess; the nested EnsureNativeChecker
// call returns a deterministic error rather than forking again. Closing
// the bootstrap UX cleanly is a separate follow-up (see
// `docs/llvm-selfhost-plan.md` PR3-G+ trajectory).
func buildNativeChecker(dest string) error {
	root, err := sourceRepoRootFunc()
	if err != nil {
		return err
	}
	if err := verifyOstySelfCached(root); err != nil {
		return err
	}

	hostOsty, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate host osty executable for native checker build: %w", err)
	}

	pkgDir := filepath.Join(root, "cmd", "osty-native-checker")
	cmd := exec.Command(hostOsty, "build", "--backend", "llvm", pkgDir)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), RecursionGuardEnv+"=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = "<no output>"
		}
		return fmt.Errorf("build LLVM osty-native-checker: %w (%s)", err, msg)
	}

	artifact := filepath.Join(pkgDir, ".osty", "out", "debug", "llvm", LLVMCheckerArtifactName())
	if !fileExists(artifact) {
		return fmt.Errorf("LLVM build of osty-native-checker did not produce expected artifact at %s", artifact)
	}

	tmpDir, err := os.MkdirTemp(filepath.Dir(dest), "osty-native-checker-*")
	if err != nil {
		return fmt.Errorf("create managed native checker temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	tmpPath := filepath.Join(tmpDir, NativeCheckerBinaryName())
	if err := copyExecutable(artifact, tmpPath); err != nil {
		return fmt.Errorf("stage LLVM native checker artifact: %w", err)
	}
	return installManagedBinary(tmpPath, dest, "osty-native-checker")
}

func defaultVerifyOstySelfCached(root string) error {
	if _, _, err := selfhostcache.ResolveBinary(root); err != nil {
		return fmt.Errorf(
			"LLVM-built osty-native-checker requires a cached osty-self; "+
				"run `osty install-self` to populate %s: %w",
			selfhostcache.CacheDirName, err,
		)
	}
	return nil
}

// copyExecutable streams src to dst with 0755 perm. Unlike os.Rename it
// does not consume the source, so the in-tree build output stays in place
// for follow-up commands while a sibling copy lands in the managed slot.
func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(dst, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func installManagedBinary(tmpPath, dest, label string) error {
	if err := renameManagedFile(tmpPath, dest); err == nil {
		return nil
	} else {
		firstErr := err
		if !shouldRetryManagedReplace(tmpPath, dest, firstErr) {
			return fmt.Errorf("install managed %s: %w", label, firstErr)
		}
		if err := removeManagedFile(dest); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("replace managed %s: remove stale artifact: %w (initial rename failed: %v)", label, err, firstErr)
		}
		if err := renameManagedFile(tmpPath, dest); err != nil {
			return fmt.Errorf("install managed %s: %w (initial rename failed: %v)", label, err, firstErr)
		}
	}
	return nil
}

func shouldRetryManagedReplace(tmpPath, dest string, err error) bool {
	if !errors.Is(err, os.ErrExist) {
		return false
	}
	return fileExists(tmpPath) && fileExists(dest)
}
