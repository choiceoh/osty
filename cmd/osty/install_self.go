package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/osty/osty/internal/backend"
	"github.com/osty/osty/internal/backend/stage0"
	"github.com/osty/osty/internal/toolchain/selfhostcache"
)

// runInstallSelf orchestrates the bootstrap "build osty-self →
// promote into cache" sequence. It:
//
//  1. Locates the project root via `selfhostcache.LocateProjectRoot`.
//  2. Computes the canonical content-addressed Key from
//     `toolchain/*.osty`.
//  3. Skips the build when a fresh cache entry already exists, unless
//     `--force` is set.
//  4. Otherwise invokes `osty build --backend=llvm --emit binary
//     toolchain/` (uses the already-resolved host osty binary) to
//     produce `toolchain/.osty/out/.../osty-self`.
//  5. Calls `selfhostcache.Install` to copy the freshly built binary
//     into `.osty/cache/self-host/<key>/osty-self` for fast lookup.
//
// Outputs:
//
//   - `installed:   <cache-path>` on success.
//   - `up-to-date:  <cache-path>` when the entry was already present.
//
// Exit codes match the rest of the CLI: 0 success, 1 failure.
func runInstallSelf(args []string, _ cliFlags) {
	// Flush accumulated phase timings (including any recorded in the
	// forked `osty build` if it inherited the env gate) at exit when
	// `OSTY_BUILD_PHASE_TIMING=1`. No-op otherwise.
	defer backend.EmitPhaseTimings()
	fs := flag.NewFlagSet("install-self", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty install-self [--force] [--toolchain-dir DIR] [--osty-bin PATH]")
		fs.PrintDefaults()
	}
	var force bool
	var toolchainDir string
	var ostyBin string
	fs.BoolVar(&force, "force", false, "rebuild + reinstall even when the cache entry already exists")
	fs.StringVar(&toolchainDir, "toolchain-dir", "toolchain", "path to the toolchain source directory passed to `osty build`")
	fs.StringVar(&ostyBin, "osty-bin", "", "path to the host osty binary; defaults to the running executable")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	endLocate := backend.BeginPhase("install-self.locate-and-key")
	root, err := selfhostcache.LocateProjectRoot(".")
	if err != nil {
		endLocate()
		fmt.Fprintf(os.Stderr, "osty install-self: locate project root: %v\n", err)
		os.Exit(1)
	}
	key, err := selfhostcache.ComputeKey(root)
	endLocate()
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty install-self: compute key: %v\n", err)
		os.Exit(1)
	}
	cachedPath := selfhostcache.CachePath(root, key)
	if !force {
		if info, statErr := os.Stat(cachedPath); statErr == nil && !info.IsDir() {
			fmt.Printf("up-to-date:  %s\n", cachedPath)
			return
		}
	}

	hostOsty := ostyBin
	if hostOsty == "" {
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "osty install-self: locate host binary: %v\n", err)
			os.Exit(1)
		}
		hostOsty = exe
	}
	if _, err := os.Stat(hostOsty); err != nil {
		fmt.Fprintf(os.Stderr, "osty install-self: host binary %q: %v\n", hostOsty, err)
		os.Exit(1)
	}

	tcAbs := toolchainDir
	if !filepath.IsAbs(tcAbs) {
		tcAbs = filepath.Join(root, toolchainDir)
	}
	if _, err := os.Stat(tcAbs); err != nil {
		fmt.Fprintf(os.Stderr, "osty install-self: toolchain dir %q: %v\n", tcAbs, err)
		os.Exit(1)
	}

	resolvedSelf, _, resolveErr := selfhostcache.ResolveBinaryWithFetch(context.Background(), root, selfhostcache.EnvFetcher())
	if resolveErr != nil && force {
		if bootstrapSelf, err := findLocalSelfhostBootstrap(root); err == nil {
			resolvedSelf = bootstrapSelf
			resolveErr = nil
		}
	}
	if resolveErr == nil {
		if !force {
			if resolvedSelf != cachedPath {
				if err := selfhostcache.Install(root, key, resolvedSelf); err != nil {
					fmt.Fprintf(os.Stderr, "osty install-self: cache install from resolved osty-self: %v\n", err)
					os.Exit(1)
				}
				fmt.Printf("installed:   %s\n", cachedPath)
				return
			}
			fmt.Printf("up-to-date:  %s\n", cachedPath)
			return
		}
	} else {
		fmt.Fprintf(os.Stderr, "osty install-self: no prebuilt osty-self available: %v\n", resolveErr)
	}

	builtBin := ""
	var buildErr error
	if force && resolveErr == nil {
		endSelfhostBuild := backend.BeginPhase("install-self.build-via-selfhost")
		builtBin, buildErr = buildOstySelfWithSelfhost(context.Background(), resolvedSelf, hostOsty, root, tcAbs)
		endSelfhostBuild()
		if buildErr != nil {
			fmt.Fprintf(os.Stderr, "osty install-self: selfhost build failed; retrying source bootstrap: %v\n", buildErr)
		}
	}
	if builtBin == "" {
		// Stage0 source bootstrap is the chicken-and-egg fallback: it
		// rebuilds osty-self from `toolchain/*.osty` through the Go-side
		// stage0 emitter when no prebuilt osty-self can be resolved.
		// It is opt-in only — the default install-self path resolves
		// osty-self from the registry or local cache. A fresh clone with
		// no reachable registry must explicitly request the slower
		// stage0 bootstrap via OSTY_STAGE0_FALLBACK=1. Gating it here
		// keeps the stage0 emitter (and its decline-stub cascade for
		// stdlib-generic-method monomorph functions) off the default
		// path, which is the prerequisite for eventually retiring the
		// `--bootstrap-stage0` build mode entirely.
		if !stage0SourceBootstrapEnabled() {
			fmt.Fprintln(os.Stderr, "osty install-self: no prebuilt osty-self and stage0 source bootstrap is disabled")
			printInstallSelfRegistryHint()
			os.Exit(1)
		}
		endStage0Build := backend.BeginPhase("install-self.build-via-stage0")
		builtBin, buildErr = buildOstySelf(context.Background(), hostOsty, root, tcAbs)
		endStage0Build()
	}
	if buildErr != nil {
		fmt.Fprintf(os.Stderr, "osty install-self: build: %v\n", buildErr)
		printInstallSelfBootstrapHint()
		os.Exit(1)
	}
	endPromote := backend.BeginPhase("install-self.cache-promote")
	if err := selfhostcache.Install(root, key, builtBin); err != nil {
		endPromote()
		fmt.Fprintf(os.Stderr, "osty install-self: cache install: %v\n", err)
		os.Exit(1)
	}
	endPromote()
	fmt.Printf("installed:   %s\n", cachedPath)
}

// printInstallSelfBootstrapHint points users at prebuilt recovery options
// after the internal source bootstrap path has already failed.
func printInstallSelfBootstrapHint() {
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "hint: install-self tried the internal source bootstrap path.")
	fmt.Fprintln(os.Stderr, "      To avoid source bootstrap, provide a prebuilt osty-self:")
	fmt.Fprintln(os.Stderr, "  - point OSTY_SELF_REGISTRY_URL at a registry serving a pre-built osty-self,")
	fmt.Fprintln(os.Stderr, "  - or point OSTY_SELF_BIN at an existing osty-self binary.")
}

// stage0SourceBootstrapEnv gates the chicken-and-egg stage0 source
// bootstrap. When unset, `osty install-self` resolves osty-self from the
// registry or local cache only; when truthy it additionally allows the
// slower Go-side stage0 emitter to rebuild osty-self from toolchain
// source. See docs/osty_self_bootstrap_design.md.
const stage0SourceBootstrapEnv = "OSTY_STAGE0_FALLBACK"

// stage0SourceBootstrapEnabled reports whether the user explicitly
// opted into the stage0 source bootstrap. The accepted truthy spellings
// match installSelfTryDirectBuildRequested so the two install-self env
// gates parse consistently.
func stage0SourceBootstrapEnabled() bool {
	raw := strings.TrimSpace(os.Getenv(stage0SourceBootstrapEnv))
	return raw == "1" || strings.EqualFold(raw, "true") || strings.EqualFold(raw, "yes") || strings.EqualFold(raw, "on")
}

// printInstallSelfRegistryHint guides the user toward a prebuilt
// osty-self after registry/cache resolution came up empty and stage0
// source bootstrap was not opted into.
func printInstallSelfRegistryHint() {
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "hint: install-self resolves a prebuilt osty-self from the registry or local cache.")
	fmt.Fprintln(os.Stderr, "      To provide one:")
	fmt.Fprintln(os.Stderr, "  - point OSTY_SELF_REGISTRY_URL at a registry serving a pre-built osty-self,")
	fmt.Fprintln(os.Stderr, "  - or point OSTY_SELF_BIN at an existing osty-self binary,")
	fmt.Fprintln(os.Stderr, "  - or set OSTY_STAGE0_FALLBACK=1 to bootstrap from toolchain source (slower).")
}

// buildOstySelf invokes the host `osty` binary with the standard
// build flags used by the self-rebuild ratchet. Returns the absolute
// path to the produced osty-self binary.
//
// This is the stage0 source-bootstrap path. It is reached only when no
// prebuilt osty-self could be resolved AND the user opted in via
// OSTY_STAGE0_FALLBACK=1 (see stage0SourceBootstrapEnabled). The
// `--bootstrap-stage0` flag below makes the forked build skip the
// native-owned LIR Proto subprocess (guaranteed to decline because
// osty-self does not exist yet) and emit through the Go-side stage0
// emitter instead.
func buildOstySelf(ctx context.Context, hostOsty, root, toolchainDir string) (string, error) {
	cmd := exec.CommandContext(ctx, hostOsty, "build", "--bootstrap-stage0", "--backend=llvm", "--emit", "binary", "--force", toolchainDir)
	// Forward user env vars and enable LIST_ALL_DECLINES for the
	// install-self bootstrap build. The cascade
	// of stdlib-generic-method monomorph functions (Result.unwrapOr,
	// List.pop, ...) that stage0 can't fully match are diagnostic-only
	// in the install-self critical path — emitting them as `unreachable`
	// decline stubs lets the binary link without changing the runtime
	// behaviour of the covered code paths.
	cmd.Env = os.Environ()
	if os.Getenv(stage0.ListAllDeclinesEnv) == "" {
		cmd.Env = append(cmd.Env, stage0.ListAllDeclinesEnv+"=1")
	}
	cmd.Dir = root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("osty build toolchain/: %w", err)
	}
	return findBuiltOstySelf(root, toolchainDir)
}

func findLocalSelfhostBootstrap(root string) (string, error) {
	cacheRoot := filepath.Join(root, selfhostcache.CacheDirName)
	entries, err := os.ReadDir(cacheRoot)
	if err != nil {
		return "", err
	}
	type candidate struct {
		path    string
		modTime int64
	}
	candidates := []candidate{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(cacheRoot, entry.Name(), selfhostcache.BinaryName())
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		candidates = append(candidates, candidate{
			path:    path,
			modTime: info.ModTime().UnixNano(),
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].modTime > candidates[j].modTime
	})
	if len(candidates) == 0 {
		return "", selfhostcache.ErrNotCached
	}
	return candidates[0].path, nil
}

const installSelfTryDirectBuildEnv = "OSTY_INSTALL_SELF_TRY_DIRECT_BUILD"

func buildOstySelfWithSelfhost(ctx context.Context, selfOsty, hostOsty, root, toolchainDir string) (string, error) {
	if installSelfTryDirectBuildRequested() {
		return buildOstySelfWithSelfhostCommand(ctx, selfOsty, root, toolchainDir)
	}
	return buildOstySelfWithSelfhostMIRDriver(ctx, selfOsty, hostOsty, root, toolchainDir)
}

func installSelfTryDirectBuildRequested() bool {
	raw := strings.TrimSpace(os.Getenv(installSelfTryDirectBuildEnv))
	return raw == "1" || strings.EqualFold(raw, "true") || strings.EqualFold(raw, "yes") || strings.EqualFold(raw, "on")
}

func buildOstySelfWithSelfhostCommand(ctx context.Context, selfOsty, root, toolchainDir string) (string, error) {
	cmd := exec.CommandContext(ctx, selfOsty, "build", "--backend", "llvm", "--emit", "binary", "--force", toolchainDir)
	cmd.Env = os.Environ()
	cmd.Dir = root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("osty-self build toolchain/: %w", err)
	}
	return findBuiltOstySelf(root, toolchainDir)
}

func buildOstySelfWithSelfhostMIRDriver(ctx context.Context, selfOsty, hostOsty, root, toolchainDir string) (string, error) {
	if hostOsty == "" {
		return "", errors.New("host osty binary is required to build the selfhost MIR driver")
	}
	outDir := filepath.Join(toolchainDir, ".osty", "out", "debug", "llvm")
	workDir := filepath.Join(outDir, "selfhost-mir-driver-pkg")
	bundlePath := filepath.Join(workDir, "main.osty")
	manifestPath := filepath.Join(workDir, "osty.toml")
	binaryPath := filepath.Join(outDir, selfhostcache.BinaryName())

	if err := os.RemoveAll(workDir); err != nil {
		return "", fmt.Errorf("clean selfhost MIR driver work dir: %w", err)
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir selfhost MIR driver work dir: %w", err)
	}
	source, err := selfhostMIRDriverBundleSource(root)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(bundlePath, []byte(source), 0o644); err != nil {
		return "", fmt.Errorf("write selfhost MIR driver bundle: %w", err)
	}
	if err := os.WriteFile(manifestPath, []byte(selfhostMIRDriverManifest()), 0o644); err != nil {
		return "", fmt.Errorf("write selfhost MIR driver manifest: %w", err)
	}

	cmd := exec.CommandContext(ctx, hostOsty, "build", "--backend=llvm", "--emit", "binary", "--force", workDir)
	cmd.Env = installSelfEnvWith(os.Environ(),
		selfhostcache.SelfBinEnv, selfOsty,
		"OSTY_SELF_REGISTRY_OFFLINE", "1",
	)
	cmd.Dir = root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("host build selfhost MIR driver: %w", err)
	}
	candidate, err := findBuiltOstySelf(root, workDir)
	if err != nil {
		return "", fmt.Errorf("host build selfhost MIR driver: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(binaryPath), 0o755); err != nil {
		return "", fmt.Errorf("mkdir selfhost binary dir: %w", err)
	}
	if err := copySelfhostBinary(candidate, binaryPath); err != nil {
		return "", err
	}
	return findBuiltOstySelf(root, toolchainDir)
}

func selfhostMIRDriverManifest() string {
	return strings.TrimLeft(`
[package]
name = "selfhost-mir-driver"
version = "0.1.0"
edition = "0.3"

[bin]
name = "osty-self"
path = "main.osty"

[capabilities]
runtime = true
`, "\n")
}

func selfhostMIRDriverBundleSource(root string) (string, error) {
	parts := []string{"use std.env", "use std.fs", "use std.os", "use std.process", "use std.strings", ""}
	for _, rel := range selfhostMIRDriverSourceFiles() {
		path := filepath.Join(root, filepath.FromSlash(rel))
		src, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read selfhost source %s: %w", path, err)
		}
		parts = append(parts, "// ---- "+filepath.ToSlash(rel)+" ----")
		parts = append(parts, selfhostStripDriverImports(string(src)))
		parts = append(parts, "")
	}
	parts = append(parts, "fn main() {", "    selfhostMirDriverMain()", "}", "")
	return strings.Join(parts, "\n"), nil
}

func selfhostMIRDriverSourceFiles() []string {
	return []string{
		"toolchain/mir.osty",
		"toolchain/mir_validator.osty",
		"toolchain/mir_json.osty",
		"scripts/selfhost_mir_support.osty",
		"toolchain/lir_proto.osty",
		"toolchain/selfhost_mir_driver.osty",
	}
}

func selfhostStripDriverImports(src string) string {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	src = strings.ReplaceAll(src, "\r", "\n")
	lines := strings.Split(src, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		switch line {
		case "use std.env", "use std.fs", "use std.os", "use std.process", "use std.strings", "use std.strings as strings":
			continue
		default:
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func installSelfEnvWith(env []string, pairs ...string) []string {
	out := append([]string{}, env...)
	for i := 0; i+1 < len(pairs); i += 2 {
		key, value := pairs[i], pairs[i+1]
		prefix := key + "="
		replaced := false
		for j, item := range out {
			eq := strings.IndexByte(item, '=')
			if eq < 0 {
				continue
			}
			existing := item[:eq]
			if existing == key || (os.PathSeparator == '\\' && strings.EqualFold(existing, key)) {
				out[j] = prefix + value
				replaced = true
			}
		}
		if !replaced {
			out = append(out, prefix+value)
		}
	}
	return out
}

func copySelfhostBinary(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open selfhost MIR driver binary: %w", err)
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("create selfhost MIR driver binary: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("copy selfhost MIR driver binary: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close selfhost MIR driver binary: %w", err)
	}
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("replace old selfhost MIR driver binary: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		return fmt.Errorf("install selfhost MIR driver binary: %w", err)
	}
	return nil
}

func findBuiltOstySelf(root, toolchainDir string) (string, error) {
	for _, candidate := range []string{
		filepath.Join(toolchainDir, ".osty", "out", "debug", "llvm", selfhostcache.BinaryName()),
		filepath.Join(toolchainDir, ".osty", "out", "release", "llvm", selfhostcache.BinaryName()),
	} {
		abs := candidate
		if !filepath.IsAbs(candidate) {
			abs = filepath.Join(root, candidate)
		}
		if info, err := os.Stat(abs); err == nil && !info.IsDir() {
			return abs, nil
		}
	}
	return "", errors.New("osty build did not produce a recognised osty-self binary; checked debug/ and release/ paths")
}

// installSelfUsage formats the subcommand entry for the top-level
// usage banner. Currently unused outside of unit tests but kept
// importable so future help-text generation can pick it up without
// reaching into package-private state.
func installSelfUsage() string {
	return strings.TrimSpace(`
osty install-self [--force] [--toolchain-dir DIR] [--osty-bin PATH]
    Build the self-host osty-self binary and promote it into the
    content-addressed selfhostcache so subsequent compile invocations
    skip the slow toolchain rebuild.
`)
}
