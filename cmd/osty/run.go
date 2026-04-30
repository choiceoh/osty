package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/osty/osty/internal/backend"
	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/pkgmgr"
	"github.com/osty/osty/internal/profile"
	ostyquery "github.com/osty/osty/internal/query/osty"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/runner"
	"github.com/osty/osty/internal/stdlib"
)

// runRun implements `osty run [-- ARGS...]`.
//
// Flow:
//
//  1. Locate osty.toml + vendor deps via pkgmgr.
//  2. Confirm we have an entry point (manifest Bin target or default main.osty).
//  3. Seed the shared query graph and run resolve + check across it.
//  4. Emit the root package through the graph's backend Emit query.
//  5. Execute the native backend binary, passing through the
//     user-supplied arguments after `--`.
//
// Limitations:
//
//   - Registry / git dep code is vendored but NOT yet emitted
//     together with the entry file — the Workspace loader sees them
//     for resolution, but package-per-package emission still needs to
//     land before they contribute native code.
//
// Exit codes: the child native binary's exit code is propagated.
// A 1–5 from the wrapper indicates an error inside osty itself.
func runRun(args []string, cliF cliFlags) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty run [--offline | --locked | --frozen] [--profile NAME | --release] [--target TRIPLE] [--features LIST] [--no-default-features] [--backend NAME] [--emit MODE] [--airepair=false] [--airepair-mode MODE] [-- ARGS...]")
	}
	var offline, locked, frozen bool
	fs.BoolVar(&offline, "offline", false, "do not fetch dependencies; fail if caches are missing")
	fs.BoolVar(&locked, "locked", false, "fail if osty.lock would change")
	fs.BoolVar(&frozen, "frozen", false, "imply --locked --offline; require an existing osty.lock")
	var aiRepairModeName string
	registerAIRepairCommandFlags(fs, &cliF.aiRepair, &aiRepairModeName)
	var backendName string
	var emitName string
	fs.StringVar(&backendName, "backend", defaultBackendName(), "code generation backend (llvm, onb)")
	fs.StringVar(&emitName, "emit", "", "artifact mode to execute (binary)")
	var pf profileFlags
	pf.register(fs)
	_ = fs.Parse(args)
	mode, ok := parseAIRepairMode(aiRepairModeName)
	if !ok {
		fmt.Fprintf(os.Stderr, "osty run: unknown airepair mode %q (want auto, rewrite, parse, or frontend)\n", aiRepairModeName)
		os.Exit(2)
	}
	cliF.aiMode = mode
	backendID, emitMode := resolveBackendAndEmitFlags("run", backendName, emitName)
	runArgs := fs.Args()

	runDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty run: get cwd: %v\n", err)
		os.Exit(1)
	}

	m, root, abort := loadManifestWithDiag(".", cliF)
	if abort {
		os.Exit(2)
	}

	resolved, _, perr := pf.resolve(m, profile.NameDebug)
	if perr != nil {
		fmt.Fprintf(os.Stderr, "osty run: %v\n", perr)
		os.Exit(2)
	}
	// `osty run` is a build-and-execute shortcut. Policy for "may
	// we exec this on the host?" lives in toolchain/runner.osty via
	// runner.CrossCompileGuard so the same rule applies to any
	// future host that wraps the run command (e.g. `osty test`
	// running a native artifact).
	targetTriple := ""
	if resolved.Target != nil {
		targetTriple = resolved.Target.Triple
	}
	if guard := runner.CrossCompileGuard(targetTriple); guard.Blocked {
		fmt.Fprintln(os.Stderr, "osty run: "+guard.Diag.Message)
		fmt.Fprintln(os.Stderr, "hint: "+guard.Diag.Hint)
		os.Exit(2)
	}
	_ = filepath.Join(root, manifest.ManifestFile) // kept for future inline rewriting

	// Step 1: vendor deps (also runs resolve, computes the graph +
	// DepProvider we'll attach to the workspace).
	graph, env, err := resolveAndVendorEnvOpts(m, root, resolveOpts{
		Offline: offline, Locked: locked, Frozen: frozen,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty run: %v\n", err)
		os.Exit(3)
	}
	deps := pkgmgr.NewDepProvider(m, graph, env)

	// Turn on the native-checker cache so repeated `osty run` cycles
	// during development don't re-check packages that haven't changed.
	enableCheckerCacheForRoot(root)

	// Step 2: pick the entry file. A binary project uses main.osty
	// at the project root unless [bin].path overrides it. The rule
	// lives in toolchain/runner.osty; cross-platform separator is
	// the host's filepath.Separator.
	binPath := ""
	if m.Bin != nil {
		binPath = m.Bin.Path
	}
	entry := runner.EntryPathFor(root, binPath, string(filepath.Separator))
	if _, err := os.Stat(entry); err != nil {
		fmt.Fprintf(os.Stderr, "osty run: entry %s not found: %v\n", entry, err)
		fmt.Fprintln(os.Stderr, "hint: create main.osty or override with [bin].path in osty.toml")
		os.Exit(2)
	}

	// Step 3: front-end through the shared query graph. A Workspace is
	// still used for manifest/dependency discovery, then the loaded
	// packages are seeded into the graph so resolve/check/lower/emit
	// share the same incremental path as build and LSP.
	ws, err := resolve.NewWorkspace(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty run: %v\n", err)
		os.Exit(1)
	}
	ws.SourceTransform = aiRepairSourceTransform("osty run --airepair", os.Stderr, cliF)
	ws.Stdlib = stdlib.Load()
	ws.Deps = deps
	if _, err := ws.LoadPackageNative(""); err != nil {
		fmt.Fprintf(os.Stderr, "osty run: %v\n", err)
		os.Exit(1)
	}
	eng, seeded := seedBuildWorkspaceEngine(ws)
	printBuildWorkspaceQueryDiags(eng, seeded, cliF)
	rootDir := ostyquery.NormalizePath(root)
	rw := eng.Queries.ResolveWorkspace.Get(eng.DB, seeded.Root)
	if rw == nil || rw.PackageByDir(rootDir) == nil {
		fmt.Fprintf(os.Stderr, "osty run: root package not in query graph\n")
		os.Exit(1)
	}

	// Step 4: emit the selected backend. Per-profile/target/backend
	// subdirectories keep debug / release / cross-built artifacts from
	// clobbering each other.
	// Binary filename policy (base name + optional .exe suffix) is
	// authored in toolchain/runner.osty and snapshotted in
	// internal/runner. Keep this call site free of OS-shape logic.
	binBaseOverride := ""
	pkgName := ""
	if m != nil {
		if m.Bin != nil {
			binBaseOverride = m.Bin.Name
		}
		pkgName = m.Package.Name
	}
	binName := ""
	if emitMode == backend.EmitBinary {
		binName = runner.BinaryNameFor(binBaseOverride, pkgName, runtime.GOOS)
	}
	emitResult := emitViaQuery("run", root, m, eng, ostyquery.LowerKey{
		WorkspaceRoot: seeded.Root,
		Dir:           rootDir,
	}, resolved, featureSet(resolved), backendID, emitMode, binName)
	if emitResult == nil {
		fmt.Fprintf(os.Stderr, "osty run: backend did not produce a runnable artifact\n")
		os.Exit(1)
	}
	runNativeBinary(emitResult.Artifacts.Binary, runArgs, runDir)
}

func runNativeBinary(binPath string, args []string, dir string) {
	if binPath == "" {
		fmt.Fprintln(os.Stderr, "osty run: native backend did not produce a binary")
		os.Exit(1)
	}
	absBin, err := filepath.Abs(binPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty run: %v\n", err)
		os.Exit(1)
	}
	cmd := exec.Command(absBin, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "osty run: exec llvm binary: %v\n", err)
		os.Exit(1)
	}
}
