package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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

	root, err := selfhostcache.LocateProjectRoot(".")
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty install-self: locate project root: %v\n", err)
		os.Exit(1)
	}
	key, err := selfhostcache.ComputeKey(root)
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

	builtBin, err := buildOstySelf(context.Background(), hostOsty, root, tcAbs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty install-self: build: %v\n", err)
		// The chicken-egg: a fresh clone has no osty-self in cache,
		// no in-tree build, and (by default) no registry URL. The
		// host osty's LLVM backend forks `osty-self lir-proto-lower`
		// for emit, which declines without an osty-self to fork.
		// `OSTY_STAGE0_FALLBACK=1` activates the bootstrap-only
		// emitter (`docs/osty_self_bootstrap_design.md`) which can
		// emit a small subset of MIR patterns — enough for the
		// emergency path but not yet for the full toolchain. Surface
		// the workflow options so the user does not have to hunt
		// through the resolver chain in the dark.
		if os.Getenv("OSTY_STAGE0_FALLBACK") == "" {
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "hint: bootstrap from a fresh clone needs an osty-self source. Pick one:")
			fmt.Fprintln(os.Stderr, "  - point OSTY_SELF_REGISTRY_URL at a registry serving a pre-built osty-self,")
			fmt.Fprintln(os.Stderr, "  - point OSTY_SELF_BIN at an existing osty-self binary, or")
			fmt.Fprintln(os.Stderr, "  - retry with OSTY_STAGE0_FALLBACK=1 to use the emergency bootstrap emitter")
			fmt.Fprintln(os.Stderr, "    (subset coverage; see docs/osty_self_bootstrap_design.md).")
		}
		os.Exit(1)
	}
	if err := selfhostcache.Install(root, key, builtBin); err != nil {
		fmt.Fprintf(os.Stderr, "osty install-self: cache install: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("installed:   %s\n", cachedPath)
}

// buildOstySelf invokes the host `osty` binary with the standard
// build flags used by the self-rebuild ratchet. Returns the absolute
// path to the produced osty-self binary.
func buildOstySelf(ctx context.Context, hostOsty, root, toolchainDir string) (string, error) {
	cmd := exec.CommandContext(ctx, hostOsty, "build", "--backend=llvm", "--emit", "binary", "--force", toolchainDir)
	cmd.Dir = root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("osty build toolchain/: %w", err)
	}
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
