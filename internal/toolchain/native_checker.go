package toolchain

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/osty/osty/internal/manifest"
)

const toolchainDirName = ".osty/toolchain"

var (
	nativeCheckerBuildMu sync.Mutex

	managedProjectRootFunc = defaultManagedProjectRoot
	sourceRepoRootFunc     = defaultSourceRepoRoot
	installNativeChecker   = buildNativeChecker
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

func ManagedNativeCheckerPath(projectRoot string) string {
	return filepath.Join(projectRoot, toolchainDirName, Version(), NativeCheckerBinaryName())
}

// EnsureNativeChecker returns the managed checker artifact for the current
// project/worktree, building it into .osty/toolchain/<version>/ on first use.
func EnsureNativeChecker(start string) (string, error) {
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

func buildNativeChecker(dest string) error {
	root, err := sourceRepoRootFunc()
	if err != nil {
		return err
	}
	tmpDir, err := os.MkdirTemp(filepath.Dir(dest), "osty-native-checker-*")
	if err != nil {
		return fmt.Errorf("create native checker temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	tmpPath := filepath.Join(tmpDir, NativeCheckerBinaryName())
	cmd := exec.Command("go", "build", "-o", tmpPath, "./cmd/osty-native-checker")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = "<no output>"
		}
		return fmt.Errorf("build managed osty-native-checker: %w (%s)", err, msg)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(tmpPath, 0o755); err != nil {
			return fmt.Errorf("chmod managed osty-native-checker: %w", err)
		}
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		if fileExists(dest) {
			return nil
		}
		return fmt.Errorf("install managed osty-native-checker: %w", err)
	}
	return nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
