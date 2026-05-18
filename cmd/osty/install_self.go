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

	resolvedSelf, _, resolveErr := selfhostcache.ResolveBinaryWithFetch(context.Background(), root, selfhostcache.EnvFetcher())
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
		fmt.Fprintf(os.Stderr, "osty install-self: no prebuilt osty-self available; attempting source bootstrap: %v\n", resolveErr)
	}

	builtBin, err := buildOstySelf(context.Background(), hostOsty, root, tcAbs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty install-self: build: %v\n", err)
		printInstallSelfBootstrapHint()

		os.Exit(1)
	}
	if err := selfhostcache.Install(root, key, builtBin); err != nil {
		fmt.Fprintf(os.Stderr, "osty install-self: cache install: %v\n", err)
		os.Exit(1)
	}
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

// buildOstySelf invokes the host `osty` binary with the standard
// build flags used by the self-rebuild ratchet. Returns the absolute
// path to the produced osty-self binary.
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
