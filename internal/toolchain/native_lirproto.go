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
	nativeLIRProtoBuildMu sync.Mutex
	installNativeLIRProto = buildNativeLIRProto
)

// nativeLIRProtoSourceInputs lists the directories whose mtimes
// trigger a rebuild of the managed `osty-native-lirproto` artifact.
// Same shape as `nativeLLVMGenSourceInputs` — the binary's body
// is the managed wrapper around the Osty-owned LIR Proto subprocess boundary.
var nativeLIRProtoSourceInputs = []string{
	"cmd/osty-native-lirproto",
	"internal/backend",
	"internal/ir",
	"internal/llvmabi",
	"internal/mir",
	"internal/mirjson",
	"internal/nativelirproto",
	"internal/toolchain/selfhostcache",
	"go.mod",
	"go.sum",
}

func NativeLIRProtoBinaryName() string {
	name := "osty-native-lirproto"
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func ManagedNativeLIRProtoPath(projectRoot string) string {
	return filepath.Join(projectRoot, toolchainDirName, Version(), NativeLIRProtoBinaryName())
}

// EnsureNativeLIRProto returns the managed lirproto artifact for the
// current project/worktree, building it into
// `.osty/toolchain/<version>/` on first use. Mirrors
// `EnsureNativeLLVMGen` — same lazy-build cache + mtime stamp.
func EnsureNativeLIRProto(start string) (string, error) {
	root, err := managedProjectRootFunc(start)
	if err != nil {
		return "", err
	}
	path := ManagedNativeLIRProtoPath(root)
	stale, err := nativeLIRProtoNeedsRebuild(path)
	if err != nil {
		return "", err
	}
	if !stale {
		return path, nil
	}

	nativeLIRProtoBuildMu.Lock()
	defer nativeLIRProtoBuildMu.Unlock()

	stale, err = nativeLIRProtoNeedsRebuild(path)
	if err != nil {
		return "", err
	}
	if !stale {
		return path, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create managed lirproto dir: %w", err)
	}
	if err := installNativeLIRProto(path); err != nil {
		return "", err
	}
	return path, nil
}

func nativeLIRProtoNeedsRebuild(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, fmt.Errorf("stat managed lirproto: %w", err)
	}
	if info.IsDir() {
		return true, nil
	}
	root, err := sourceRepoRootFunc()
	if err != nil {
		return false, err
	}
	managedModTime := info.ModTime()
	for _, rel := range nativeLIRProtoSourceInputs {
		sourcePath := filepath.Join(root, rel)
		sourceInfo, err := os.Stat(sourcePath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return false, fmt.Errorf("stat native lirproto source %q: %w", sourcePath, err)
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
			return false, fmt.Errorf("walk native lirproto sources %q: %w", sourcePath, walkErr)
		}
		if newer {
			return true, nil
		}
	}
	return false, nil
}

func buildNativeLIRProto(dest string) error {
	root, err := sourceRepoRootFunc()
	if err != nil {
		return err
	}
	tmpDir, err := os.MkdirTemp(filepath.Dir(dest), "osty-native-lirproto-*")
	if err != nil {
		return fmt.Errorf("create native lirproto temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	tmpPath := filepath.Join(tmpDir, NativeLIRProtoBinaryName())
	cmd := exec.Command("go", "build", "-o", tmpPath, "./cmd/osty-native-lirproto")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = "<no output>"
		}
		return fmt.Errorf("build managed osty-native-lirproto: %w (%s)", err, msg)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(tmpPath, 0o755); err != nil {
			return fmt.Errorf("chmod managed osty-native-lirproto: %w", err)
		}
	}
	return installManagedBinary(tmpPath, dest, "osty-native-lirproto")
}
