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

// Stage0FallbackEnv mirrors `cmd/osty/install_self.go::stage0SourceBootstrapEnv`
// so `buildNativeChecker` can detect the chicken-and-egg bootstrap mode
// without importing the cmd package. When the user has opted into the
// stage0 source bootstrap (`OSTY_STAGE0_FALLBACK=1`), no `osty-self` is
// available — by definition, that's what install-self is trying to
// produce — so the LLVM-built native checker path (which gates on a
// resolvable `osty-self`) cannot run. In that mode `buildNativeChecker`
// falls back to `go build ./cmd/osty-native-checker`, producing the
// Go-side shell (which delegates to `internal/selfhost/generated.go`)
// instead. The slot is populated once and subsequent `osty build`
// invocations short-circuit on the cached artifact.
//
// Kept in lockstep with the install-self gate: the truthy spellings
// must accept the same set (1 / true / yes / on) the install-self CLI
// recognises, so `OSTY_STAGE0_FALLBACK=1 osty install-self` works
// end-to-end on a fresh clone.
const Stage0FallbackEnv = "OSTY_STAGE0_FALLBACK"

var (
	nativeCheckerBuildMu sync.Mutex

	managedProjectRootFunc = defaultManagedProjectRoot
	sourceRepoRootFunc     = defaultSourceRepoRoot
	installNativeChecker   = buildNativeChecker
	renameManagedFile      = os.Rename
	removeManagedFile      = os.Remove
	verifyOstySelfCached   = defaultVerifyOstySelfCached
	goBuildNativeChecker   = defaultGoBuildNativeChecker
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

// NativeCheckerLLVMBinEnv is the env var override that points at a
// prebuilt LLVM-target native checker binary. When set,
// `ResolveNativeCheckerLLVM` returns the env value directly without
// looking at the in-tree build output. Mirrors the
// `OSTY_NATIVE_CHECKER_BIN` precedent for the Go-built variant.
//
// Distinct from `RecursionGuardEnv`: that flag is set by
// `buildNativeChecker` on the subprocess to abort nested build
// re-entries. This one is a *user-facing* opt-in that lets CI /
// install-self / cross-worktree developers stage a prebuilt binary
// and skip the build entirely.
//
// Use cases:
//
//   - CI: stage a prebuilt binary outside the repo and point this
//     env var at it so the install-self ratchet does not need to
//     bootstrap `osty-self` first.
//   - Worktree-shared cache: a developer with `osty-self` already
//     built can run `osty build --backend llvm cmd/osty-native-checker/`
//     once, then export this env var from their shell profile to
//     reuse the LLVM-built binary across other worktrees without
//     rebuilding (mirrors the OSTY_NATIVE_CHECKER_BIN convention
//     documented in CLAUDE.md).
//   - Tests: an integration test wanting byte-parity diff between
//     the Go-built and LLVM-built variants supplies its own
//     prebuilt path here.
//
// CLAUDE.md note still applies: prefer not pinning this in a global
// shell profile across worktrees — stale references silently
// propagate.
const NativeCheckerLLVMBinEnv = "OSTY_NATIVE_CHECKER_LLVM_BIN"

func ManagedNativeCheckerPath(projectRoot string) string {
	return filepath.Join(projectRoot, toolchainDirName, Version(), NativeCheckerBinaryName())
}

// recursionFallbackCheckerPath returns the slot the recursion-guard
// detour writes its Go-built shell to during a nested
// `osty build --backend llvm cmd/osty-native-checker/` invocation.
//
// The recursion case (`OSTY_BUILDING_NATIVE_CHECKER=1`) needs a
// checker so the subprocess's own frontend can typecheck the checker
// source — but writing the Go shell to the canonical managed slot
// makes it persist past the subprocess: if the outer LLVM build
// fails, the next `EnsureNativeChecker` call finds the cached Go
// shell, short-circuits on `fileExists(path)`, and silently downgrades
// the production checker to the Go fallback. Routing the fallback to
// a sibling path keeps the canonical slot empty until a real LLVM
// build promotes its artifact there, so failed LLVM builds surface
// as the expected "managed slot still empty → retry the build"
// behavior on the next call instead of an undetectable downgrade.
func recursionFallbackCheckerPath(projectRoot string) string {
	return filepath.Join(projectRoot, toolchainDirName, Version(), NativeCheckerBinaryName()+".recursion-fallback")
}

// InvalidateManagedNativeChecker deletes the cached managed checker
// artifact (if present) so the next `EnsureNativeChecker` call rebuilds
// it from scratch. Used by `osty install-self` to retire a Go-built
// fallback (placed by the stage0 bootstrap path) once `osty-self`
// becomes resolvable — the next `osty build` will then re-enter
// `buildNativeChecker` and produce the LLVM-built variant from live
// `toolchain/*.osty` sources, which is the steady-state production
// configuration described in `cmd/osty-native-checker/README.md`.
//
// A missing file is not an error. Any other error (e.g. permission
// denied) is returned so the caller can decide whether to surface a
// warning.
func InvalidateManagedNativeChecker(projectRoot string) error {
	path := ManagedNativeCheckerPath(projectRoot)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("invalidate managed native checker %s: %w", path, err)
	}
	return nil
}

// ManagedNativeCheckerLLVMPath returns the conventional location
// `osty build --backend llvm cmd/osty-native-checker/` writes its
// artifact to (`<project>/cmd/osty-native-checker/.osty/out/{debug,release}/llvm/osty-native-checker-llvm`).
// Returned for the debug profile because the manual build command
// from `cmd/osty-native-checker/README.md` uses the default profile.
// Release-profile callers should resolve the binary path themselves.
// Same leaf as `buildNativeChecker` resolves at line ~271
// (`LLVMCheckerArtifactName`) — kept in lockstep so the in-tree
// fallback in `ResolveNativeCheckerLLVM` hits the file `buildNativeChecker`
// just produced.
func ManagedNativeCheckerLLVMPath(projectRoot string) string {
	return filepath.Join(projectRoot, "cmd", "osty-native-checker", ".osty", "out", "debug", "llvm", LLVMCheckerArtifactName())
}

// ResolveNativeCheckerLLVM returns the first usable LLVM-target
// native checker binary, searching:
//
//  1. `$OSTY_NATIVE_CHECKER_LLVM_BIN` env override.
//  2. The in-tree build output at
//     `<project>/cmd/osty-native-checker/.osty/out/debug/llvm/`.
//
// Returns an empty string when neither location yields a binary so
// callers can branch into "build it" or "use the Go-built variant"
// without parsing error types. No build is attempted here — this
// helper is a lookup, not a managed builder, because the LLVM build
// path is heavyweight (`osty-self` recursion) and tests should opt
// into building explicitly. Differs from `EnsureNativeChecker`,
// which manages a separate slot at
// `.osty/toolchain/<version>/osty-native-checker` and DOES drive a
// build on miss.
func ResolveNativeCheckerLLVM(projectRoot string) string {
	if envPath := strings.TrimSpace(os.Getenv(NativeCheckerLLVMBinEnv)); envPath != "" {
		if fileExists(envPath) {
			return envPath
		}
	}
	inTree := ManagedNativeCheckerLLVMPath(projectRoot)
	if fileExists(inTree) {
		return inTree
	}
	return ""
}

// EnsureNativeChecker returns the managed checker artifact for the current
// project/worktree, building it into .osty/toolchain/<version>/ on first use.
//
// Build path (post LLVM-flip): the managed artifact is the LLVM-built
// `cmd/osty-native-checker/` package. The Go-built shell at
// `cmd/osty-native-checker/main.go` is no longer reachable from production —
// it survives only for `internal/check/testsupport.BuildSharedNativeCheckerForTests`,
// out-of-band `OSTY_NATIVE_CHECKER_BIN` overrides, and the chicken-and-egg
// `OSTY_STAGE0_FALLBACK=1` bootstrap path described below. See
// `docs/llvm-selfhost-plan.md` for the trajectory that drove this flip.
//
// Stage0 fallback: when `OSTY_STAGE0_FALLBACK=1` is set,
// `buildNativeChecker` instead drives `go build ./cmd/osty-native-checker`
// because the LLVM build path requires an `osty-self` binary that fresh
// clones do not yet have. This keeps `osty install-self` working
// end-to-end on a fresh clone without forcing the user to manually
// pre-stage a checker via `OSTY_NATIVE_CHECKER_BIN`.
//
// Recursion detour: when `OSTY_BUILDING_NATIVE_CHECKER=1` is set (we are
// the `osty build --backend llvm cmd/osty-native-checker/` subprocess
// trying to typecheck the checker's own source), `buildNativeChecker`
// ALSO detours to the Go-build path. Without this the inner subprocess
// errored out with a recursion-guard message and the post-install
// chicken-and-egg "first `osty build` after `install-self` triggers a
// fresh LLVM build of the native checker" path was broken (see PR #1995
// CI gate failure). The detour produces a Go-built checker in the
// managed slot just long enough for the inner subprocess to typecheck;
// the outer process then overwrites the slot with the LLVM artifact it
// just built, so the steady-state remains LLVM-built.
func EnsureNativeChecker(start string) (string, error) {
	root, err := managedProjectRootFunc(start)
	if err != nil {
		return "", err
	}
	// Recursion-guard detour: when we are the LLVM-build subprocess
	// spawned by an outer EnsureNativeChecker, prefer the sibling
	// "recursion-fallback" slot the subprocess wrote into. This keeps
	// the canonical managed slot untouched until the outer LLVM build
	// produces a real artifact, so a failed LLVM build does not leak
	// the Go shell into the persistent cache.
	if os.Getenv(RecursionGuardEnv) == "1" {
		fallback := recursionFallbackCheckerPath(root)
		if fileExists(fallback) {
			return fallback, nil
		}
	}
	path := ManagedNativeCheckerPath(root)
	if fileExists(path) {
		return path, nil
	}

	nativeCheckerBuildMu.Lock()
	defer nativeCheckerBuildMu.Unlock()

	if os.Getenv(RecursionGuardEnv) == "1" {
		if fallback := recursionFallbackCheckerPath(root); fileExists(fallback) {
			return fallback, nil
		}
	}
	if fileExists(path) {
		return path, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create managed toolchain dir: %w", err)
	}
	if err := installNativeChecker(path); err != nil {
		return "", err
	}
	if os.Getenv(RecursionGuardEnv) == "1" {
		// In the recursion case installNativeChecker wrote the Go
		// shell to the recursion-fallback slot, not `path`. Return
		// that slot so the caller's frontend has a usable checker.
		if fallback := recursionFallbackCheckerPath(root); fileExists(fallback) {
			return fallback, nil
		}
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

// ProbeManagedNativeChecker returns the path of the managed
// native-checker artifact if it already exists, or empty if none is
// cached. Probe-only: does NOT trigger a build. Use this from CLI
// entry points that need to propagate `OSTY_NATIVE_CHECKER_BIN` to
// subprocesses without paying for an LLVM build on cold caches —
// `EnsureNativeChecker` fires the full build path and is wrong for
// cheap commands like `osty fmt` / `osty --help`.
func ProbeManagedNativeChecker(start string) string {
	root, err := managedProjectRootFunc(start)
	if err != nil {
		return ""
	}
	path := ManagedNativeCheckerPath(root)
	if path == "" {
		return ""
	}
	if !fileExists(path) {
		return ""
	}
	return path
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
//     a fresh worktree must run `osty install-self` once first
//     (or set `OSTY_STAGE0_FALLBACK=1` to bootstrap via the Go-side
//     checker shell; see `Stage0FallbackEnv` for the trajectory).
//  2. The host osty executable that called us (`os.Executable()`) can
//     reinvoke itself with `build --backend llvm <pkg>` — i.e. we are
//     running inside an `osty` CLI process, not a test binary that
//     happens to import this package without exposing a build mode.
//
// Stage0 fallback short-circuit: when `OSTY_STAGE0_FALLBACK=1` is set
// the function detours to `buildNativeCheckerViaGo` BEFORE the
// `osty-self` requirement is enforced. This is the fresh-clone
// bootstrap path — by definition `osty-self` is still missing in that
// scenario, so the LLVM build cannot proceed.
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
	// Two flavours of "LLVM path is unavailable, build the Go-side shell":
	//
	// 1. Stage0 source bootstrap (`OSTY_STAGE0_FALLBACK=1`): the user
	//    explicitly opted into the install-self recovery path. No
	//    `osty-self` exists yet — that's literally what install-self is
	//    currently producing — so the LLVM path cannot run.
	//
	// 2. Recursion detour (`OSTY_BUILDING_NATIVE_CHECKER=1`): we are
	//    the LLVM-build subprocess spawned by `buildNativeChecker`
	//    itself, and now we need a checker to typecheck the checker's
	//    own source. Spawning another LLVM-build subprocess would
	//    loop forever. Build the Go-side shell into the managed slot
	//    just long enough for our outer caller to read; that outer
	//    caller (the original `osty check` / `osty build` that
	//    triggered our existence) then overwrites the slot with the
	//    LLVM artifact it produces from us, so the steady state remains
	//    LLVM-built.
	//
	// Either flavour produces a Go-built shell that delegates straight
	// to `internal/selfhost/generated.go` and has no `osty-self`
	// dependency — sufficient to typecheck `toolchain/*.osty` and
	// `cmd/osty-native-checker/main.osty` so the build can complete.
	if stage0FallbackEnabled() {
		// Stage0 opt-in: caller explicitly wants the Go shell in the
		// production slot for the duration of the install-self
		// bootstrap. The install-self flow invalidates the slot via
		// `InvalidateManagedNativeChecker` once `osty-self` becomes
		// resolvable, so this Go shell is intentionally transient.
		return buildNativeCheckerViaGo(root, dest)
	}
	if os.Getenv(RecursionGuardEnv) == "1" {
		// Recursion detour: route the Go shell to a sibling
		// "recursion-fallback" slot instead of the canonical managed
		// slot. The outer LLVM build that spawned us has not finished
		// yet — if it later fails, the Go shell here would otherwise
		// remain in the canonical slot and silently downgrade every
		// subsequent `osty check`/`osty build` to the Go fallback,
		// hiding the LLVM build failure. The fallback slot is only
		// visible to subprocesses running under `RecursionGuardEnv`
		// (see `EnsureNativeChecker`); outside the recursion the
		// canonical slot stays empty until a real LLVM artifact is
		// promoted into it.
		mgrRoot, err := managedProjectRootFunc(".")
		if err == nil {
			fallback := recursionFallbackCheckerPath(mgrRoot)
			if dirErr := os.MkdirAll(filepath.Dir(fallback), 0o755); dirErr == nil {
				return buildNativeCheckerViaGo(root, fallback)
			}
		}
		// On the rare path where we can't compute the managed root,
		// fall back to the original behavior — the recursion guard
		// still prevents an infinite loop, and the failure mode is
		// the pre-fix one rather than a worse mis-resolution.
		return buildNativeCheckerViaGo(root, dest)
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
	// Strip any pre-existing OSTY_BUILDING_NATIVE_CHECKER from the parent
	// env before appending the guard. `os.Environ()` reflects whatever the
	// user exported (including `=0`); the child's `os.Getenv` returns the
	// first match, so an unfiltered append would let an inherited `=0`
	// shadow our `=1` and re-enable the recursion.
	cmd.Env = append(filterEnv(os.Environ(), RecursionGuardEnv), RecursionGuardEnv+"=1")
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

// stage0FallbackEnabled reports whether the caller has opted into the
// chicken-and-egg source bootstrap. The accepted truthy spellings match
// `cmd/osty/install_self.go::stage0SourceBootstrapEnabled` so the two
// install-self env gates parse consistently.
func stage0FallbackEnabled() bool {
	raw := strings.TrimSpace(os.Getenv(Stage0FallbackEnv))
	return raw == "1" || strings.EqualFold(raw, "true") || strings.EqualFold(raw, "yes") || strings.EqualFold(raw, "on")
}

// buildNativeCheckerViaGo compiles `cmd/osty-native-checker/main.go` with
// `go build` and promotes the resulting binary into the managed slot.
// This is the bootstrap fallback path used when the LLVM-built variant
// is unreachable because `osty-self` does not yet exist (i.e. the user
// is running `osty install-self` on a fresh clone with
// `OSTY_STAGE0_FALLBACK=1`).
//
// Identical in spirit to `internal/check/testsupport.go`'s
// `BuildSharedNativeCheckerForTests` — both go through `go build` and
// both link `internal/selfhost/generated.go` as the checker core — but
// `internal/check` imports `internal/toolchain`, so this package can't
// share that helper without a cycle. Keep the two invocations in sync.
func buildNativeCheckerViaGo(root, dest string) error {
	tmpDir, err := os.MkdirTemp(filepath.Dir(dest), "osty-native-checker-go-*")
	if err != nil {
		return fmt.Errorf("create managed native checker temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	tmpPath := filepath.Join(tmpDir, NativeCheckerBinaryName())
	if err := goBuildNativeChecker(root, tmpPath); err != nil {
		return err
	}
	if !fileExists(tmpPath) {
		return fmt.Errorf("go build of osty-native-checker did not produce expected artifact at %s", tmpPath)
	}
	return installManagedBinary(tmpPath, dest, "osty-native-checker")
}

// defaultGoBuildNativeChecker invokes the host Go toolchain to compile
// `cmd/osty-native-checker` into outPath. Substitutable as a package var
// (`goBuildNativeChecker`) so tests can swap in a synthetic binary
// producer without spawning a real Go build.
func defaultGoBuildNativeChecker(root, outPath string) error {
	cmd := exec.Command("go", "build", "-o", outPath, "github.com/osty/osty/cmd/osty-native-checker")
	cmd.Dir = root
	// Strip RecursionGuardEnv if inherited — `go build` does not spawn
	// `osty build`, so the guard is irrelevant here, but leaving an
	// inherited `=1` in env would surprise anyone reading the build's
	// environment dump for debugging.
	cmd.Env = filterEnv(os.Environ(), RecursionGuardEnv)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = "<no output>"
		}
		return fmt.Errorf("go build osty-native-checker (stage0 fallback): %w (%s)", err, msg)
	}
	return nil
}

func defaultVerifyOstySelfCached(root string) error {
	if _, _, err := selfhostcache.ResolveBinary(root); err != nil {
		// `selfhostcache.ResolveBinary` accepts OSTY_SELF_BIN, in-tree
		// toolchain/.osty/out/{debug,release}/llvm/osty-self builds, AND the
		// content-addressed `.osty/cache/self-host/` entries — so phrase the
		// failure as a generic "no resolvable osty-self" rather than naming
		// only the cache, and point at the install-self recipe as the
		// canonical fix.
		return fmt.Errorf(
			"LLVM-built osty-native-checker requires a resolvable osty-self "+
				"(OSTY_SELF_BIN override, toolchain/.osty/out/{debug,release}/llvm/osty-self, "+
				"or `.osty/cache/self-host/`); run `osty install-self` to populate %s: %w",
			selfhostcache.CacheDirName, err,
		)
	}
	return nil
}

// filterEnv returns a copy of env with all `<key>=...` entries removed.
// Used to prevent inherited assignments from shadowing a value we are
// about to append.
func filterEnv(env []string, key string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			continue
		}
		out = append(out, kv)
	}
	return out
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
