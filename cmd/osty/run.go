package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/osty/osty/internal/backend"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/pkgmgr"
	"github.com/osty/osty/internal/profile"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/runner"
	"github.com/osty/osty/internal/stdlib"
)

// runRun implements `osty run [-- ARGS...]`.
//
// Flow:
//
//  1. Locate osty.toml + vendor deps via pkgmgr.
//  2. Resolve the project as a package; confirm we have an entry
//     point (manifest Bin target or default main.osty with fn main).
//  3. Run the front-end (parse + resolve + type check).
//  4. Emit the entry file via internal/backend into .osty/out.
//  5. Execute the native backend binary, passing through the
//     user-supplied arguments after `--`.
//
// Limitations (current emitter):
//
//   - Multi-file packages aren't fully emitted by the backend yet; run executes
//     the selected entry file. Complex unsupported lowering shapes may
//     still produce an unsupported-backend diagnostic.
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
	fs.StringVar(&backendName, "backend", defaultBackendName(), "code generation backend (llvm)")
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

	// Step 3: front-end through a Workspace so `use <dep>` resolves
	// against vendored packages via the DepProvider.
	ws, err := resolve.NewWorkspace(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty run: %v\n", err)
		os.Exit(1)
	}
	ws.SourceTransform = aiRepairSourceTransform("osty run --airepair", os.Stderr, cliF)
	ws.Stdlib = stdlib.Load()
	ws.Deps = deps
	rootPkg, err := ws.LoadPackage("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty run: %v\n", err)
		os.Exit(1)
	}
	results := ws.ResolveAll()
	checks := check.Workspace(ws, results, checkOpts())
	// Aggregate diagnostics across every loaded package so front-end
	// errors in a vendored dep also surface.
	var all []*diag.Diagnostic
	for key, pkg := range ws.Packages {
		r := results[key]
		if r == nil || pkg == nil {
			continue
		}
		ds := append([]*diag.Diagnostic{}, r.Diags...)
		if cr, ok := checks[key]; ok && cr != nil {
			ds = append(ds, cr.Diags...)
		}
		printPackageDiags(pkg, ds, cliF)
		all = append(all, ds...)
	}
	if hasError(all) {
		fmt.Fprintf(os.Stderr, "osty run: front-end errors in %s\n", entry)
		os.Exit(1)
	}

	// Locate the AST + resolve/check results for the entry file so we
	// can pass them to gen. The entry is in the root package; find
	// the matching PackageFile.
	var entryFile *resolve.PackageFile
	entryAbs, _ := filepath.Abs(entry)
	for _, pf := range rootPkg.Files {
		if abs, _ := filepath.Abs(pf.Path); abs == entryAbs {
			entryFile = pf
			break
		}
	}
	if entryFile == nil {
		fmt.Fprintf(os.Stderr, "osty run: entry %s not part of the root package\n", entry)
		os.Exit(1)
	}
	res := &resolve.Result{
		Refs:      entryFile.Refs,
		TypeRefs:  entryFile.TypeRefs,
		FileScope: entryFile.FileScope,
	}
	file := entryFile.File
	chk := checks[""]
	if chk == nil {
		chk = &check.Result{}
	}

	// Step 4: emit the selected backend. Per-profile/target/backend
	// subdirectories keep debug / release / cross-built artifacts from
	// clobbering each other.
	triple := ""
	if resolved.Target != nil {
		triple = resolved.Target.Triple
	}
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
	binName := runner.BinaryNameFor(binBaseOverride, pkgName, runtime.GOOS)
	selectedBackend := backendFromCLI("run", backendID)
	// Multi-file packages must merge every sibling .osty file into one
	// ir.Module before the backend runs; single-file packages keep the
	// historical PrepareEntry path. countLowerableFiles lives in
	// build.go alongside the analogous build-side dispatch.
	var (
		backendEntry backend.Entry
		entryErr     error
	)
	if rootPkg != nil && countLowerableFiles(rootPkg) > 1 {
		backendEntry, entryErr = backend.PreparePackage("main", entryAbs, rootPkg, entryFile, chk)
	} else {
		backendEntry, entryErr = backend.PrepareEntry("main", entryAbs, file, res, chk)
	}
	if err := entryErr; err != nil {
		fmt.Fprintf(os.Stderr, "osty run: %v\n", err)
		os.Exit(1)
	}
	emitResult, err := selectedBackend.Emit(context.Background(), backend.Request{
		Layout: backend.Layout{
			Root:    root,
			Profile: resolved.Profile.Name,
			Target:  triple,
		},
		Emit:       emitMode,
		Entry:      backendEntry,
		BinaryName: binName,
		Features:   resolved.Features,
	})
	if err != nil {
		exitBackendEmitError("run", emitResult, err)
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

