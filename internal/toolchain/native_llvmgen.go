package toolchain

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

var (
	nativeLLVMGenBuildMu sync.Mutex
	installNativeLLVMGen = buildNativeLLVMGen
)

var nativeLLVMGenSourceInputs = []string{
	"cmd/osty-native-llvmgen",
	"internal/backend",
	"internal/llvmgen",
	"internal/nativellvmgen",
	"go.mod",
	"go.sum",
}

func NativeLLVMGenBinaryName() string {
	name := "osty-native-llvmgen"
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func ManagedNativeLLVMGenPath(projectRoot string) string {
	return filepath.Join(projectRoot, toolchainDirName, Version(), NativeLLVMGenBinaryName())
}

// EnsureNativeLLVMGen returns the managed llvmgen artifact for the current
// project/worktree, building it into .osty/toolchain/<version>/ on first use.
func EnsureNativeLLVMGen(start string) (string, error) {
	root, err := managedProjectRootFunc(start)
	if err != nil {
		return "", err
	}
	path := ManagedNativeLLVMGenPath(root)
	stale, err := nativeLLVMGenNeedsRebuild(path)
	if err != nil {
		return "", err
	}
	if !stale {
		return path, nil
	}

	nativeLLVMGenBuildMu.Lock()
	defer nativeLLVMGenBuildMu.Unlock()

	stale, err = nativeLLVMGenNeedsRebuild(path)
	if err != nil {
		return "", err
	}
	if !stale {
		return path, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create managed llvmgen dir: %w", err)
	}
	if err := installNativeLLVMGen(path); err != nil {
		return "", err
	}
	return path, nil
}

func nativeLLVMGenNeedsRebuild(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, fmt.Errorf("stat managed llvmgen: %w", err)
	}
	if info.IsDir() {
		return true, nil
	}
	root, err := sourceRepoRootFunc()
	if err != nil {
		return false, err
	}
	managedModTime := info.ModTime()
	for _, rel := range nativeLLVMGenSourceInputs {
		sourcePath := filepath.Join(root, rel)
		sourceInfo, err := os.Stat(sourcePath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return false, fmt.Errorf("stat native llvmgen source %q: %w", sourcePath, err)
		}
		if !sourceInfo.IsDir() {
			if sourceInfo.ModTime().After(managedModTime) {
				return true, nil
			}
			continue
		}
		var newer bool
		walkErr := filepath.WalkDir(sourcePath, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			fileInfo, err := d.Info()
			if err != nil {
				return err
			}
			if fileInfo.ModTime().After(managedModTime) {
				newer = true
				return fs.SkipAll
			}
			return nil
		})
		if walkErr != nil && walkErr != fs.SkipAll {
			return false, fmt.Errorf("walk native llvmgen sources %q: %w", sourcePath, walkErr)
		}
		if newer {
			return true, nil
		}
	}
	return false, nil
}

func buildNativeLLVMGen(dest string) error {
	root, err := sourceRepoRootFunc()
	if err != nil {
		return err
	}
	tmpDir, err := os.MkdirTemp(filepath.Dir(dest), "osty-native-llvmgen-*")
	if err != nil {
		return fmt.Errorf("create native llvmgen temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	tmpPath := filepath.Join(tmpDir, NativeLLVMGenBinaryName())
	cmd := exec.Command("go", "build", "-o", tmpPath, "./cmd/osty-native-llvmgen")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = "<no output>"
		}
		return fmt.Errorf("build managed osty-native-llvmgen: %w (%s)", err, msg)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(tmpPath, 0o755); err != nil {
			return fmt.Errorf("chmod managed osty-native-llvmgen: %w", err)
		}
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		if fileExists(dest) {
			return nil
		}
		return fmt.Errorf("install managed osty-native-llvmgen: %w", err)
	}
	return nil
}
