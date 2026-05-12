// Package selfhostcache resolves the `osty-self` binary used by the
// LLVM backend's LIR Proto bridge. It replaces the implicit "look at
// toolchain/.osty/out/.../osty-self" search the older bridge code did
// with a content-addressed cache that survives across worktrees and
// tracks toolchain source changes.
//
// Lookup order (first hit wins):
//
//  1. `OSTY_SELF_BIN` env override.
//  2. `toolchain/.osty/out/{debug,release}/llvm/osty-self` — the
//     in-tree build path. Preferred because it always reflects the
//     current source state.
//  3. `.osty/cache/self-host/<sha256>-<triple>/osty-self` — the
//     content-addressed artifact cache. The key combines the
//     SHA-256 of every `toolchain/*.osty` source byte with the host
//     triple so a downloaded or copied binary is rejected when the
//     local toolchain source has drifted.
//
// Future phases will plug a network fetcher into the same lookup
// chain (after #3) so a fresh clone with no `osty-self` can pull a
// pre-built artifact from a release host instead of needing an
// in-process bootstrap.
package selfhostcache

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/osty/osty/internal/manifest"
)

// CacheDirName is the relative path inside the project root used for
// content-addressed osty-self artifacts.
const CacheDirName = ".osty/cache/self-host"

// SelfBinEnv mirrors `cmd/osty-native-lirproto/main.go: SelfBinEnv`.
// Re-declared here so the cache resolver can honour the same env
// variable without depending on the bridge command.
const SelfBinEnv = "OSTY_SELF_BIN"

// ErrNotCached signals that no usable osty-self could be located.
// Callers convert this into a "fall back to stage0" or "decline"
// signal upstream.
var ErrNotCached = errors.New("selfhostcache: no usable osty-self binary found")

// Key identifies a single osty-self build per (toolchain source
// state, host triple). The SHA-256 spans every `toolchain/*.osty`
// file (sorted by repo-relative path) so a stale cache entry is
// rejected when source changes.
type Key struct {
	ToolchainSHA string
	Triple       string
}

// String renders the canonical cache subdirectory name.
func (k Key) String() string {
	if k.ToolchainSHA == "" || k.Triple == "" {
		return ""
	}
	return k.ToolchainSHA + "-" + k.Triple
}

// HostTriple returns the canonical "<GOOS>-<GOARCH>" stamp for the
// running process. Cache entries built for other triples remain
// inert here.
func HostTriple() string {
	return runtime.GOOS + "-" + runtime.GOARCH
}

// BinaryName returns the `osty-self` filename appropriate for the
// host (`.exe` suffix on Windows).
func BinaryName() string {
	if runtime.GOOS == "windows" {
		return "osty-self.exe"
	}
	return "osty-self"
}

// CachePath returns the canonical absolute path the artifact cache
// would store the binary at for `key`.
func CachePath(projectRoot string, key Key) string {
	return filepath.Join(projectRoot, CacheDirName, key.String(), BinaryName())
}

// inTreeBuildPaths returns the legacy in-tree build paths the older
// `osty-native-lirproto` bridge searched. Lookup keeps these as the
// preferred source so an active development build always wins over
// any cached artifact.
func inTreeBuildPaths(projectRoot string) []string {
	return []string{
		filepath.Join(projectRoot, "toolchain", ".osty", "out", "debug", "llvm", BinaryName()),
		filepath.Join(projectRoot, "toolchain", ".osty", "out", "release", "llvm", BinaryName()),
	}
}

// ComputeKey walks `projectRoot/toolchain` and produces a content-
// addressed Key based on every `*.osty` source file under it
// (sorted by relative path). Generated artifacts under
// `toolchain/.osty/` are excluded so a fresh build does not mutate
// the cache key.
func ComputeKey(projectRoot string) (Key, error) {
	toolchainDir := filepath.Join(projectRoot, "toolchain")
	if info, err := os.Stat(toolchainDir); err != nil {
		return Key{}, fmt.Errorf("selfhostcache: stat %s: %w", toolchainDir, err)
	} else if !info.IsDir() {
		return Key{}, fmt.Errorf("selfhostcache: %s is not a directory", toolchainDir)
	}

	paths := []string{}
	err := filepath.WalkDir(toolchainDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			rel, relErr := filepath.Rel(toolchainDir, p)
			if relErr != nil {
				return relErr
			}
			// Skip generated build outputs to keep the key
			// deterministic across rebuilds.
			if rel != "." && (rel == ".osty" || strings.HasPrefix(rel, ".osty"+string(filepath.Separator))) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".osty") {
			return nil
		}
		paths = append(paths, p)
		return nil
	})
	if err != nil {
		return Key{}, err
	}
	if len(paths) == 0 {
		return Key{}, fmt.Errorf("selfhostcache: no .osty source files under %s", toolchainDir)
	}
	sort.Strings(paths)

	h := sha256.New()
	for _, p := range paths {
		rel, err := filepath.Rel(projectRoot, p)
		if err != nil {
			return Key{}, err
		}
		// Hash the path (in slash-form) before the contents so two
		// files with identical bytes but different names hash apart.
		fmt.Fprintf(h, "path:%s\n", filepath.ToSlash(rel))
		data, err := os.ReadFile(p)
		if err != nil {
			return Key{}, fmt.Errorf("selfhostcache: read %s: %w", p, err)
		}
		fmt.Fprintf(h, "size:%d\n", len(data))
		h.Write(data)
		h.Write([]byte{0})
	}
	return Key{
		ToolchainSHA: hex.EncodeToString(h.Sum(nil)),
		Triple:       HostTriple(),
	}, nil
}

// ResolveBinary returns the first usable osty-self path according
// to the lookup order documented at the package level. The returned
// `Key` is non-zero when the cache layer (step 3) hits; the in-tree
// build paths return a zero key because they are not content-
// addressed.
//
// All "no usable binary" outcomes — env override missing, no in-tree
// build, no cache entry, or even a missing `toolchain/` directory —
// surface as `ErrNotCached`. Callers convert that single sentinel
// into their preferred decline message (the lirproto bridge keeps
// the canonical "osty-self not found" wording so
// `backend.IsOstySelfMissing` continues matching).
func ResolveBinary(projectRoot string) (string, Key, error) {
	if env := strings.TrimSpace(os.Getenv(SelfBinEnv)); env != "" {
		if info, err := os.Stat(env); err == nil && !info.IsDir() {
			return env, Key{}, nil
		}
	}
	for _, p := range inTreeBuildPaths(projectRoot) {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, Key{}, nil
		}
	}
	key, err := ComputeKey(projectRoot)
	if err != nil {
		// Treat key-derivation failures (missing toolchain dir,
		// unreadable files, …) as a "no cache entry available"
		// signal rather than a hard error. The cache cannot help
		// here; upstream detection drops through to its own
		// fallback chain.
		return "", Key{}, ErrNotCached
	}
	cached := CachePath(projectRoot, key)
	if info, err := os.Stat(cached); err == nil && !info.IsDir() {
		return cached, key, nil
	}
	return "", key, ErrNotCached
}

// LocateProjectRoot walks up from `start` looking for `osty.toml`
// (the canonical Osty manifest). Falls back to the absolute path of
// `start` when no manifest is found, matching the behaviour of
// `internal/toolchain.defaultManagedProjectRoot`. Callers thread the
// result into `ResolveBinary`.
func LocateProjectRoot(start string) (string, error) {
	if start == "" {
		start = "."
	}
	if root, err := manifest.FindRoot(start); err == nil {
		return root, nil
	}
	// Fallback path: this repo lays out `toolchain/osty.toml` as the
	// only manifest, with no top-level `osty.toml`. `manifest.FindRoot`
	// therefore fails when callers (tests, in particular) invoke from
	// a subdir like `internal/backend/` — walking up never finds a
	// manifest in any ancestor. Without this branch, the fallback
	// `abs(start)` becomes a garbage "project root" and downstream
	// `ComputeKey` chokes on the missing `toolchain/` directory.
	//
	// Walk up looking for a directory that contains `toolchain/osty.toml`
	// (or, equivalently, a `toolchain/` subdir on the bootstrap path)
	// and treat that as the project root.
	if root, ok := findToolchainRoot(start); ok {
		return root, nil
	}
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("selfhostcache: locate project root: %w", err)
	}
	return abs, nil
}

func findToolchainRoot(start string) (string, bool) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", false
	}
	for {
		candidate := filepath.Join(dir, "toolchain", "osty.toml")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// Install copies `binPath` into the cache for `key`. It is
// idempotent — repeated calls with the same content overwrite atomic-
// renaming via a temp file. Callers typically obtain `key` from
// `ComputeKey` immediately before / after running `osty build
// toolchain/`, then promote the freshly built binary into the cache.
func Install(projectRoot string, key Key, binPath string) error {
	if key.String() == "" {
		return errors.New("selfhostcache: install: zero key")
	}
	if _, err := os.Stat(binPath); err != nil {
		return fmt.Errorf("selfhostcache: install: %w", err)
	}
	target := CachePath(projectRoot, key)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("selfhostcache: mkdir cache: %w", err)
	}

	src, err := os.Open(binPath)
	if err != nil {
		return fmt.Errorf("selfhostcache: open source: %w", err)
	}
	defer src.Close()

	tmp, err := os.CreateTemp(filepath.Dir(target), ".osty-self-*.tmp")
	if err != nil {
		return fmt.Errorf("selfhostcache: create tmp: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		// Clean up tmp on failure paths. Successful path renames
		// the file out from under us so Remove is a no-op.
		os.Remove(tmpPath)
	}()
	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		return fmt.Errorf("selfhostcache: copy: %w", err)
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return fmt.Errorf("selfhostcache: chmod: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("selfhostcache: close tmp: %w", err)
	}
	if err := os.Rename(tmpPath, target); err != nil {
		return fmt.Errorf("selfhostcache: rename: %w", err)
	}
	return nil
}
