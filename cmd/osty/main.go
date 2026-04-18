// Command osty is the CLI entry point for the Osty toolchain.
//
// Subcommands:
//
//	osty new <name>              Scaffold a new project directory (spec §13.1).
//	osty init                    Scaffold into the current directory.
//	osty build [dir]             Resolve deps, run the front-end, emit/build artifacts.
//	osty run [-- args...]        Build and execute the host binary.
//	osty test [path|filter...]   Discover, build, and run *_test.osty tests.
//	osty add/update/remove/fetch Manage manifest deps, vendoring, and osty.lock.
//	osty publish/search/info     Interact with package registries.
//	osty registry serve          Run a file-backed registry for local/private use.
//	osty doc <path>              Emit markdown or HTML API docs.
//	osty ci [path]               Run quality checks; `ci snapshot` captures API.
//	osty profiles/targets/features/cache
//	                             Inspect build profiles, target presets, features, cache.
//	osty parse/tokens/resolve/check/typecheck/lint/fmt/airepair
//	                             Single-file/package front-end and source tools.
//	osty gen <file.osty>         Emit a single file through the llvm backend.
//	osty lsp                     Run the Language Server Protocol server on stdio.
//	osty explain [CODE]          Describe a diagnostic code; with no arg, list every code.
//	osty pipeline <file|dir>     Run every front-end phase and print per-stage timing.
//
// Global flags (may precede the subcommand):
//
//	--no-color     disable ANSI escapes even when stderr is a TTY
//	--color        force ANSI escapes even when stderr is not a TTY
//	--max-errors N stop printing after N diagnostics (0 = unlimited)
//	--json         emit diagnostics as NDJSON on stderr, one per line
//
// `fmt` subcommand flags (after the subcommand name):
//
//	--check        exit 1 if the file is not already formatted; print diff to stderr
//	--engine NAME  formatter engine: go (default) or osty
//	--no-airepair disable the default pre-format AI repair pass
//	--write        overwrite the file in place instead of printing to stdout
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/osty/osty/internal/airepair"
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/backend"
	"github.com/osty/osty/internal/canonical"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/format"
	"github.com/osty/osty/internal/lexer"
	"github.com/osty/osty/internal/lint"
	"github.com/osty/osty/internal/lsp"
	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/pipeline"
	"github.com/osty/osty/internal/repair"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/runner"
	"github.com/osty/osty/internal/scaffold"
	"github.com/osty/osty/internal/stdlib"
	"github.com/osty/osty/internal/token"
	"github.com/osty/osty/internal/types"
	"golang.org/x/term"
)

type cliFlags struct {
	noColor    bool
	forceColor bool
	maxErrors  int
	jsonOutput bool
	strict     bool // lint: exit 1 on any warning
	fix        bool // lint: apply machine-applicable suggestions in place
	fixDryRun  bool // lint: compute fixes but print diff instead of writing
	showScopes bool // resolve: also print the nested scope tree
	trace      bool // global: stream per-phase timing to stderr
	explain    bool // global: append `osty explain CODE` text per unique code
	inspect    bool // check: emit one InspectRecord per expression (stdout)
	aiRepair   bool // front-end: adapt AI-authored foreign syntax in memory
	aiMode     airepair.Mode
	// dumpNativeDiags prints the native checker's per-context error
	// histogram (assignments/accepted/errors + breakdown) to stderr after
	// a `check` / `typecheck` run. Populated from `internal/check.Result`
	// once the native-checker boundary has been invoked. Off by default;
	// nil-safe when the native checker was unavailable.
	dumpNativeDiags bool
}

func main() {
	flags := parseFlags()

	args := flag.Args()
	if len(args) < 1 {
		usage()
		os.Exit(2)
	}
	cmd := args[0]
	// fmt has its own flag parser because --check/--write only make
	// sense in that subcommand. Most front-end subcommands take exactly
	// one file path as their second positional arg.
	if cmd == "fmt" {
		runFmt(args[1:])
		return
	}
	// airepair has its own flag parser for --check/--write. It runs
	// before parse so it can fix syntax slips that would otherwise
	// block fmt/lint. `repair` remains as a legacy alias.
	if cmd == "airepair" || cmd == "repair" {
		runAIRepair(args[1:])
		return
	}
	// gen also has its own flag parser for --out/-o.
	if cmd == "gen" {
		runGen(args[1:], flags)
		return
	}
	// lsp takes no positional args: it speaks JSON-RPC on stdio and
	// is driven entirely by the client's Initialize handshake.
	if cmd == "lsp" {
		server := lsp.NewStdioServer()
		if err := server.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "osty lsp: %v\n", err)
			os.Exit(1)
		}
		os.Exit(server.ExitCode())
	}
	// query-check runs a single file through the incremental query
	// engine and optionally prints cache metrics. Primarily a demo /
	// measurement tool; LSP and future long-lived tooling are the
	// main engine consumers.
	if cmd == "query-check" {
		runQueryCheck(args[1:])
		return
	}
	// new takes a project name and optional --lib/--bin flags — not a
	// file path — so it has its own flag parser and dispatch.
	if cmd == "new" {
		runNew(args[1:])
		return
	}
	// init takes no positional argument (it scaffolds into the cwd).
	if cmd == "init" {
		runInit(args[1:])
		return
	}
	// scaffold is the umbrella command for one-off code generators
	// that aren't whole-project scaffolds. Sub-subcommands so far:
	// fixture (table-test starter), schema (JSON sample → struct),
	// and ffi (C header → Osty wrapper stubs).
	if cmd == "scaffold" {
		runScaffold(args[1:])
		return
	}
	// build is the manifest-driven project pipeline: load osty.toml,
	// resolve deps, run the front-end, and ask the selected backend to
	// emit/build artifacts under the profile/target output tree.
	if cmd == "build" {
		runBuild(args[1:], flags)
		return
	}
	// `osty lint --explain CODE` prints the rule's description and
	// exits. `osty lint --list` prints every rule. Both short-circuit
	// before the normal file-arg handling below.
	if cmd == "lint" {
		if rest, matched := takeFlag(args[1:], "--explain"); matched != "" {
			runLintExplain(matched)
			_ = rest
			return
		}
		if _, listed := takeFlag(args[1:], "--list"); listed == "__present__" {
			runLintList()
			return
		}
		// Allow lint-only flags after the subcommand, e.g.
		// `osty lint --fix FILE`
		// (subcommand-local flag placement). Strip them from args so
		// downstream dispatch keeps seeing positional-only input.
		if rest, present := takeBoolFlag(args[1:], "--fix"); present {
			flags.fix = true
			args = append([]string{"lint"}, rest...)
		}
		if rest, present := takeBoolFlag(args[1:], "--fix-dry-run"); present {
			flags.fixDryRun = true
			args = append([]string{"lint"}, rest...)
		}
		if _, present := takeBoolFlag(args[1:], "--selfhost"); present {
			fmt.Fprintln(os.Stderr, "osty lint: --selfhost has been removed")
			os.Exit(2)
		}
		if rest, present := takeBoolFlag(args[1:], "--strict"); present {
			flags.strict = true
			args = append([]string{"lint"}, rest...)
		}
	}
	if usesFrontEndAIRepair(cmd) {
		rest, updatedFlags, err := consumeFrontEndAIRepairFlags(args[1:], flags)
		if err != nil {
			fmt.Fprintf(os.Stderr, "osty %s: %v\n", cmd, err)
			os.Exit(2)
		}
		flags = updatedFlags
		args = append([]string{cmd}, rest...)
	}
	// add / update are manifest-mutating commands: they edit osty.toml
	// (and osty.lock). They have their own flag parsers in add.go / update.go.
	if cmd == "add" {
		runAdd(args[1:], flags)
		return
	}
	if cmd == "update" {
		runUpdate(args[1:], flags)
		return
	}
	// run builds the project through the selected backend and executes
	// the host binary; test walks *_test.osty files and runs the Go
	// harness; publish packs a tarball and uploads it to a registry.
	if cmd == "run" {
		runRun(args[1:], flags)
		return
	}
	if cmd == "test" {
		runTest(args[1:], flags)
		return
	}
	if cmd == "publish" {
		runPublish(args[1:], flags)
		return
	}
	// Registry-lifecycle commands. search hits the registry's
	// full-text index; yank / unyank flag a specific (name, version)
	// without deleting it; login / logout manage the on-disk
	// credential store; remove (alias rm) drops a dep from the
	// manifest and re-resolves.
	if cmd == "search" {
		runSearch(args[1:], flags)
		return
	}
	if cmd == "yank" {
		runYank(args[1:], flags)
		return
	}
	if cmd == "unyank" {
		runUnyank(args[1:], flags)
		return
	}
	if cmd == "login" {
		runLogin(args[1:], flags)
		return
	}
	if cmd == "logout" {
		runLogout(args[1:], flags)
		return
	}
	if cmd == "remove" || cmd == "rm" {
		runRemove(args[1:], flags)
		return
	}
	if cmd == "fetch" {
		runFetch(args[1:], flags)
		return
	}
	if cmd == "info" {
		runInfo(args[1:], flags)
		return
	}
	if cmd == "registry" {
		runRegistry(args[1:], flags)
		return
	}
	// doc parses a file or directory and emits markdown/HTML API docs.
	// It has its own flag parser for --out / --title / --format /
	// --check / --verify-examples.
	if cmd == "doc" {
		runDoc(args[1:], flags)
		return
	}
	if cmd == "ci" {
		runCi(args[1:], flags)
		return
	}
	// `osty check --ci` is an alias for `osty ci` — kept so the
	// roadmap-canonical spelling works alongside the dedicated
	// `ci` subcommand. Falls through to the normal `check` flow
	// below when --ci is absent.
	if cmd == "check" {
		if rest, present := takeBoolFlag(args[1:], "--ci"); present {
			runCi(rest, flags)
			return
		}
	}
	// explain looks up a diagnostic or lint code and prints its doc.
	// Handled before the generic "file required" check because it
	// takes a code (or nothing, to list every code) — never a path.
	if cmd == "explain" {
		runExplain(args[1:])
		return
	}
	// pipeline runs every front-end phase and prints a per-stage
	// timing/output table (or JSON). Has its own flag parser for
	// --json and --trace.
	if cmd == "pipeline" {
		runPipeline(args[1:])
		return
	}
	// Build-profile / target / feature / cache inspection commands.
	// None of these take a file path — they operate on the project
	// rooted at cwd (or report built-in defaults when outside one).
	if cmd == "profiles" {
		runProfiles(args[1:], flags)
		return
	}
	if cmd == "targets" {
		runTargets(args[1:], flags)
		return
	}
	if cmd == "features" {
		runFeatures(args[1:], flags)
		return
	}
	if cmd == "cache" {
		runCache(args[1:], flags)
		return
	}
	if len(args) < 2 {
		usage()
		os.Exit(2)
	}
	path := args[1]

	// Directory mode: `osty check DIR` / `osty resolve DIR` run the full
	// pipeline over every .osty file in the directory as one package.
	// The file-only subcommands (`parse`, `tokens`, `fmt`, `typecheck`)
	// keep their single-file contract.
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		switch cmd {
		case "check":
			runCheckPackage(path, flags)
			return
		case "resolve":
			runResolvePackage(path, flags)
			return
		case "lint":
			runLintPackage(path, flags)
			return
		default:
			fmt.Fprintf(os.Stderr,
				"osty: %s does not accept a directory (expected a file)\n", cmd)
			os.Exit(2)
		}
	}

	if cmd == "check" || cmd == "typecheck" || cmd == "resolve" {
		selected, handled, err := loadSelectedPackageEntryWithTransform(path, aiRepairSourceTransform(aiRepairPrefix(cmd), os.Stderr, flags))
		if err != nil {
			fmt.Fprintf(os.Stderr, "osty: %v\n", err)
			os.Exit(1)
		}
		if handled {
			if flags.trace {
				pipeline.RunLoadedPackage(selected.pkg, os.Stderr, pipeline.Config{})
			}
			switch cmd {
			case "check":
				diags := append(append([]*diag.Diagnostic{}, selected.res.Diags...), selected.chk.Diags...)
				printPackageDiags(selected.pkg, diags, flags)
				if flags.inspect && selected.file != nil && selected.file.File != nil {
					if !flags.jsonOutput {
						fmt.Printf("# %s\n", selected.file.Path)
					}
					runInspect(selected.file.File, selected.chk, flags)
				}
				if hasError(diags) {
					os.Exit(1)
				}
				return
			case "typecheck":
				diags := append(append([]*diag.Diagnostic{}, selected.res.Diags...), selected.chk.Diags...)
				printPackageDiags(selected.pkg, diags, flags)
				printTypes(selected.chk)
				if hasError(diags) {
					os.Exit(1)
				}
				return
			case "resolve":
				printPackageDiags(selected.pkg, selected.res.Diags, flags)
				if len(selected.file.Refs) > 0 {
					fmt.Printf("# %s\n", selected.file.Path)
					printResolutionRefs(selected.file.Refs)
				}
				if flags.showScopes {
					if pkgScope := selected.file.FileScope.Parent(); pkgScope != nil {
						printScopeTree(pkgScope)
					} else {
						printScopeTree(selected.file.FileScope)
					}
				}
				if hasError(selected.res.Diags) {
					os.Exit(1)
				}
				return
			}
		}
	}

	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty: %v\n", err)
		os.Exit(1)
	}
	src = maybeAIRepairSource(path, src, aiRepairPrefix(cmd), os.Stderr, flags)
	formatter := newFormatter(path, src, flags)

	// --trace: run the full front-end once with streaming timing
	// output before the subcommand's own work. Restricted to commands
	// whose pipeline is a strict prefix of pipeline.Run — anything
	// else (fmt, gen, build, …) has its own subcommand-local timing.
	if flags.trace && isTraceableSingleFileCmd(cmd) {
		pipeline.Run(src, os.Stderr)
	}

	switch cmd {
	case "parse":
		parsed := parser.ParseDetailed(src)
		file, diags := parsed.File, parsed.Diagnostics
		printDiags(formatter, diags, flags)
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(file); err != nil {
			fmt.Fprintf(os.Stderr, "osty: encode: %v\n", err)
			os.Exit(1)
		}
		if hasError(diags) {
			os.Exit(1)
		}
	case "tokens":
		l := lexer.New(src)
		toks := l.Lex()
		for _, t := range toks {
			fmt.Println(t.String())
		}
		printDiags(formatter, l.Errors(), flags)
		if hasError(l.Errors()) {
			os.Exit(1)
		}
	case "check":
		parsed := parser.ParseDetailed(src)
		file, diags := parsed.File, parsed.Diagnostics
		res := resolveFile(file)
		chk := check.File(file, res, checkOptsForSource(canonical.Source(src, file)))
		all := append(append(append([]*diag.Diagnostic{}, diags...), res.Diags...), chk.Diags...)
		printDiags(formatter, all, flags)
		if flags.inspect {
			runInspect(file, chk, flags)
		}
		if flags.dumpNativeDiags {
			dumpNativeDiagsFor(path, chk)
		}
		if hasError(all) {
			os.Exit(1)
		}
	case "typecheck":
		parsed := parser.ParseDetailed(src)
		file, diags := parsed.File, parsed.Diagnostics
		res := resolveFile(file)
		chk := check.File(file, res, checkOptsForSource(canonical.Source(src, file)))
		all := append(append(append([]*diag.Diagnostic{}, diags...), res.Diags...), chk.Diags...)
		printDiags(formatter, all, flags)
		printTypes(chk)
		if hasError(all) {
			os.Exit(1)
		}
	case "resolve":
		parsed := parser.ParseDetailed(src)
		file, parseDiags := parsed.File, parsed.Diagnostics
		res := resolveFile(file)
		all := append(append([]*diag.Diagnostic{}, parseDiags...), res.Diags...)
		printDiags(formatter, all, flags)
		printResolution(file, res)
		if flags.showScopes {
			// File scope's parent is the package scope (a child of the
			// prelude). Rooting the dump at the package scope hides
			// noisy prelude builtins while still showing every
			// user-declared symbol.
			if pkgScope := res.FileScope.Parent(); pkgScope != nil {
				printScopeTree(pkgScope)
			} else {
				printScopeTree(res.FileScope)
			}
		}
		if hasError(all) {
			os.Exit(1)
		}
	case "lint":
		// Respect [lint] exclude before parsing. loadLintConfigWithBase
		// looks upward from the file for `osty.toml`, returning the
		// config + the manifest's directory so globs can be resolved
		// relative to the project root.
		if cfg, base, ok := loadLintConfigWithBase(path); ok && cfg.ShouldExclude(path, base) {
			return
		}
		parsed := parser.ParseDetailed(src)
		file, parseDiags := parsed.File, parsed.Diagnostics
		res := resolveFile(file)
		chk := check.File(file, res, checkOptsForSource(canonical.Source(src, file)))
		lr := runLintEngine(file, res, chk)
		if cfg, ok := loadLintConfigNear(path); ok {
			lr = cfg.Apply(lr)
		}
		all := append(append(append([]*diag.Diagnostic{}, parseDiags...), res.Diags...), chk.Diags...)
		all = append(all, lr.Diags...)
		printDiags(formatter, all, flags)
		if flags.fix || flags.fixDryRun {
			newSrc, applied, skipped := lint.ApplyFixes(src, lr.Diags)
			mode := "osty lint"
			switch {
			case flags.fixDryRun:
				// Write the would-be-applied source to stdout so users
				// can pipe it through `diff` / `less` before committing
				// to a real --fix pass. The file on disk is untouched.
				if _, err := os.Stdout.Write(newSrc); err != nil {
					fmt.Fprintf(os.Stderr, "%s --fix-dry-run: %v\n", mode, err)
					os.Exit(1)
				}
				fmt.Fprintf(os.Stderr, "%s --fix-dry-run: %d fix(es) would apply, %d overlap(s) would be skipped\n", mode, applied, skipped)
			case flags.fix:
				if applied > 0 {
					if err := os.WriteFile(path, newSrc, 0o644); err != nil {
						fmt.Fprintf(os.Stderr, "%s --fix: %v\n", mode, err)
						os.Exit(1)
					}
				}
				fmt.Fprintf(os.Stderr, "%s --fix: applied %d fix(es), skipped %d overlap(s)\n", mode, applied, skipped)
			}
		}
		if hasError(all) || (flags.strict && hasWarning(all)) {
			os.Exit(1)
		}
	default:
		usage()
		os.Exit(2)
	}
}

// parseFlags parses global flags that may precede the subcommand. Uses
// the standard library's flag package but tolerates an unknown first arg
// by stopping at the first non-flag token (the subcommand).
func parseFlags() cliFlags {
	var f cliFlags
	flag.BoolVar(&f.noColor, "no-color", false, "disable ANSI escapes")
	flag.BoolVar(&f.forceColor, "color", false, "force ANSI escapes")
	flag.IntVar(&f.maxErrors, "max-errors", 0, "stop after N diagnostics (0 = unlimited)")
	flag.BoolVar(&f.jsonOutput, "json", false, "emit diagnostics as NDJSON on stderr")
	flag.BoolVar(&f.strict, "strict", false, "exit non-zero on lint warnings (lint subcommand only)")
	flag.BoolVar(&f.fix, "fix", false, "apply machine-applicable lint suggestions in place (lint subcommand only)")
	flag.BoolVar(&f.fixDryRun, "fix-dry-run", false, "show the result of --fix on stdout without modifying files (lint subcommand only)")
	flag.BoolVar(&f.showScopes, "scopes", false, "resolve: also dump the nested scope tree")
	flag.BoolVar(&f.trace, "trace", false, "stream per-phase timing to stderr (single-file front-end commands)")
	flag.BoolVar(&f.explain, "explain", false, "after diagnostics, print the `osty explain CODE` text for each unique code")
	flag.BoolVar(&f.inspect, "inspect", false, "check: emit one record per expression showing the inference rule, type, and hint (see LANG_SPEC_v0.4/02a-type-inference.md)")
	flag.BoolVar(&f.dumpNativeDiags, "dump-native-diags", false, "check: after the run, print the native checker's per-context error histogram to stderr")
	flag.Usage = usage
	flag.Parse()
	return f
}

// newFormatter builds a Formatter that uses ANSI colors only when the
// caller's stderr is a terminal — pipes and CI logs get plain text unless
// --color is forced.
// runCheckPackage runs lex + parse + resolve over dir. Two modes:
//
//   - **Single-package**: dir contains `.osty` files directly. Loaded
//     as one Package via resolve.LoadPackage.
//   - **Workspace**: dir has no top-level `.osty` files but one or
//     more subdirectories do. The whole tree is loaded via Workspace
//     so cross-package `use` declarations resolve.
//
// Diagnostics are rendered with each file's own formatter so source
// snippets point at the right lines even when spanning packages.
func runCheckPackage(dir string, flags cliFlags) {
	// When dir (or any ancestor) contains osty.toml, validate it first:
	// manifest errors (bad edition, empty workspace, etc.) surface
	// before we descend into source files. A workspace manifest also
	// promotes dir to workspace-mode even when the directory layout
	// alone wouldn't trigger it.
	if _, _, err := manifestLookupNear(dir); err == nil {
		m, _, abort := loadManifestWithDiag(dir, flags)
		if abort {
			os.Exit(2)
		}
		if m != nil && m.Workspace != nil {
			runCheckWorkspace(dir, flags)
			return
		}
	}
	if isWorkspace(dir) {
		runCheckWorkspace(dir, flags)
		return
	}
	pkg, err := resolve.LoadPackageWithTransform(dir, aiRepairSourceTransform(aiRepairPrefix("check"), os.Stderr, flags))
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty: %v\n", err)
		os.Exit(1)
	}
	res := resolve.ResolvePackage(pkg, resolve.NewPrelude())
	chk := check.Package(pkg, res, checkOpts())
	diags := append(append([]*diag.Diagnostic{}, res.Diags...), chk.Diags...)
	printPackageDiags(pkg, diags, flags)
	if flags.inspect {
		runInspectPackage(pkg, chk, flags)
	}
	if flags.dumpNativeDiags {
		dumpNativeDiagsFor(dir, chk)
	}
	if hasError(diags) {
		os.Exit(1)
	}
}

// manifestLookupNear reports whether an osty.toml is reachable from
// dir (walking up). Used by runCheckPackage to decide whether to
// apply manifest-level validation before source-file processing.
// Returns the discovered root + "found" as (string, bool) via error
// semantics so the helper composes with the existing err-returning
// FindRoot.
func manifestLookupNear(dir string) (string, bool, error) {
	root, err := manifest.FindRoot(dir)
	if err != nil {
		return "", false, err
	}
	return root, true, nil
}

// isWorkspace is a thin wrapper around resolve.IsWorkspaceRoot so the
// call sites below stay readable. Kept local because the CLI uses the
// "no skip" variant exclusively.
func isWorkspace(dir string) bool {
	return resolve.IsWorkspaceRoot(dir, "")
}

// runCheckWorkspace loads every package (one subdirectory each) rooted
// at dir, runs cross-package resolution, and prints diagnostics per
// package — so `auth/` diagnostics use `auth/` sources, `db/` uses
// `db/` sources, etc.
func runCheckWorkspace(dir string, flags cliFlags) {
	ws, err := resolve.NewWorkspace(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty: %v\n", err)
		os.Exit(1)
	}
	ws.SourceTransform = aiRepairSourceTransform(aiRepairPrefix("check"), os.Stderr, flags)
	ws.Stdlib = stdlib.LoadCached()
	// Seed the loader with the root package (if any) plus every
	// immediate subdirectory that contains .osty files. LoadPackage
	// chases `use` edges from there, so deeper nested packages are
	// pulled in lazily.
	for _, p := range resolve.WorkspacePackagePaths(dir) {
		_, _ = ws.LoadPackage(p)
	}
	results := ws.ResolveAll()
	// Run the type checker over every package, producing one Result per
	// package keyed by import path. Diagnostics from both phases are
	// merged per-package for unified reporting.
	checks := check.Workspace(ws, results, checkOpts())

	anyErr := false
	paths := make([]string, 0, len(ws.Packages))
	for p := range ws.Packages {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		pkg := ws.Packages[p]
		r, ok := results[p]
		if !ok || pkg == nil {
			continue
		}
		diags := append([]*diag.Diagnostic{}, r.Diags...)
		cr := checks[p]
		if cr != nil {
			diags = append(diags, cr.Diags...)
		}
		printPackageDiags(pkg, diags, flags)
		if flags.inspect && cr != nil {
			runInspectPackage(pkg, cr, flags)
		}
		if flags.dumpNativeDiags && cr != nil {
			dumpNativeDiagsFor(p, cr)
		}
		if hasError(diags) {
			anyErr = true
		}
	}
	if anyErr {
		os.Exit(1)
	}
}

// runLintPackage runs the lint pass over every .osty file in dir as a
// single package so cross-file uses of `use` aliases and top-level
// declarations don't trigger false "unused" warnings. Workspace mode
// (dir-of-packages) runs lint per contained package via
// runLintWorkspace.
func runLintPackage(dir string, flags cliFlags) {
	if isWorkspace(dir) {
		runLintWorkspace(dir, flags)
		return
	}
	pkg, err := resolve.LoadPackageWithTransform(dir, aiRepairSourceTransform(aiRepairPrefix("lint"), os.Stderr, flags))
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty: %v\n", err)
		os.Exit(1)
	}
	res := resolve.ResolvePackage(pkg, resolve.NewPrelude())
	chk := check.Package(pkg, res, checkOpts())
	cfg, cfgBase, hasCfg := loadLintConfigWithBase(dir)
	outcome := runLintLoadedPackage(pkg, res, chk, flags, cfg, cfgBase, hasCfg)
	if outcome.anyErr || (flags.strict && outcome.anyWarn) {
		os.Exit(1)
	}
}

// runLintWorkspace lints each package inside dir, aggregating diagnostics
// so a single strict check covers the whole tree.
func runLintWorkspace(dir string, flags cliFlags) {
	ws, err := resolve.NewWorkspace(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty: %v\n", err)
		os.Exit(1)
	}
	ws.SourceTransform = aiRepairSourceTransform(aiRepairPrefix("lint"), os.Stderr, flags)
	ws.Stdlib = stdlib.LoadCached()
	anyErr, anyWarn := false, false
	runOne := func(path string) {
		pkg, err := ws.LoadPackage(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "osty: %v\n", err)
			anyErr = true
			return
		}
		res := resolve.ResolvePackage(pkg, resolve.NewPrelude())
		chk := check.Package(pkg, res, checkOpts())
		cfg, cfgBase, hasCfg := loadLintConfigWithBase(pkg.Dir)
		outcome := runLintLoadedPackage(pkg, res, chk, flags, cfg, cfgBase, hasCfg)
		if outcome.anyErr {
			anyErr = true
		}
		if outcome.anyWarn {
			anyWarn = true
		}
	}
	for _, p := range resolve.WorkspacePackagePaths(dir) {
		runOne(p)
	}
	if anyErr || (flags.strict && anyWarn) {
		os.Exit(1)
	}
}

type lintPackageOutcome struct {
	anyErr  bool
	anyWarn bool
}

func runLintLoadedPackage(
	pkg *resolve.Package,
	res *resolve.PackageResult,
	chk *check.Result,
	flags cliFlags,
	cfg lint.Config,
	cfgBase string,
	hasCfg bool,
) lintPackageOutcome {
	lr := lint.Package(pkg, res, chk)
	if hasCfg {
		lr = cfg.Apply(lr)
	}
	all := append(append(append([]*diag.Diagnostic{}, res.Diags...), chk.Diags...), lr.Diags...)
	printPackageDiags(pkg, all, flags)
	return lintPackageOutcome{anyErr: hasError(all), anyWarn: hasWarning(all)}
}

// runResolvePackage is runCheckPackage plus a resolution dump per file.
func runResolvePackage(dir string, flags cliFlags) {
	pkg, err := resolve.LoadPackageWithTransform(dir, aiRepairSourceTransform(aiRepairPrefix("resolve"), os.Stderr, flags))
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty: %v\n", err)
		os.Exit(1)
	}
	res := resolve.ResolvePackage(pkg, resolve.NewPrelude())
	printPackageDiags(pkg, res.Diags, flags)
	for _, f := range pkg.Files {
		if len(f.Refs) == 0 {
			continue
		}
		fmt.Printf("# %s\n", f.Path)
		printResolutionRefs(f.Refs)
	}
	if flags.showScopes {
		// Package scope's children are the file scopes, which in turn
		// host fn / block / closure / match-arm scopes — the full
		// nested tree for every file in the package.
		printScopeTree(pkg.PkgScope)
	}
	if hasError(res.Diags) {
		os.Exit(1)
	}
}

// printPackageDiags groups diagnostics by source file so each one is
// rendered with a formatter that has the right Source bytes. Diagnostics
// whose span is empty (no file context) fall back to a generic formatter
// built with whatever file we happen to scan first.
func printPackageDiags(pkg *resolve.Package, diags []*diag.Diagnostic, flags cliFlags) {
	if len(diags) == 0 {
		return
	}
	// Map line-origin positions back to files by offset ranges. The
	// simplest approach: bucket each diagnostic into the file whose path
	// matches where its source came from. Since we don't carry a
	// file-id on Pos, we fall back to routing every diagnostic through
	// each file's formatter in turn — diagnostics that don't match the
	// file's source will just show no snippet, which is acceptable.
	//
	// For accurate rendering we need the actual source, so we group by
	// comparing token offsets to each file's length.
	byFile := make([][]*diag.Diagnostic, len(pkg.Files))
	var noCtx []*diag.Diagnostic
	for _, d := range diags {
		fi := pickFile(pkg, d)
		if fi < 0 {
			noCtx = append(noCtx, d)
			continue
		}
		byFile[fi] = append(byFile[fi], d)
	}
	for i, f := range pkg.Files {
		if len(byFile[i]) == 0 {
			continue
		}
		fmter := newFormatter(f.Path, f.Source, flags)
		printDiags(fmter, byFile[i], flags)
	}
	if len(noCtx) > 0 {
		fmter := &diag.Formatter{}
		printDiags(fmter, noCtx, flags)
	}
}

// pickFile returns the index of the package file the diagnostic belongs
// to, based on byte offset. A return of -1 means no file matched
// (unusual — typically only for synthetic diagnostics).
func pickFile(pkg *resolve.Package, d *diag.Diagnostic) int {
	pos := d.PrimaryPos()
	if pos.Line == 0 {
		return -1
	}
	// Our positions are per-file (each parser starts at line 1), so
	// line + column aren't sufficient to disambiguate. Use the ParseDiags
	// slice identity: if this diagnostic is in a file's ParseDiags, that
	// file is the one. Otherwise, attribute resolver diagnostics by
	// scanning each file's Refs/TypeRefs for the position's byte offset.
	for i, f := range pkg.Files {
		for _, pd := range f.ParseDiags {
			if pd == d {
				return i
			}
		}
	}
	// Resolver-origin diagnostic: match by byte offset falling in the
	// file's source length, biased toward the first file whose source
	// is at least as large as the offset. This is a heuristic but works
	// in practice because positions originate from tokens the lexer
	// produced for exactly one input buffer.
	for i, f := range pkg.Files {
		if pos.Offset <= len(f.Source) {
			return i
		}
	}
	return -1
}

// printResolutionRefs dumps one file's resolved identifiers. Broken out
// of printResolution so the package walker can print one header per file.
func printResolutionRefs(refs map[*ast.Ident]*resolve.Symbol) {
	idents := make([]*ast.Ident, 0, len(refs))
	for id := range refs {
		idents = append(idents, id)
	}
	sort.Slice(idents, func(i, j int) bool {
		a, b := idents[i].PosV, idents[j].PosV
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Column < b.Column
	})
	for _, id := range idents {
		s := refs[id]
		def := "<builtin>"
		if s.Pos.Line > 0 {
			def = fmt.Sprintf("%d:%d", s.Pos.Line, s.Pos.Column)
		}
		fmt.Printf("%d:%d\t%-20s\t%-12s\t->%s\n",
			id.PosV.Line, id.PosV.Column, id.Name, s.Kind, def)
	}
}

func newFormatter(path string, src []byte, flags cliFlags) *diag.Formatter {
	color := false
	switch {
	case flags.forceColor:
		color = true
	case flags.noColor:
		color = false
	default:
		color = isTerminal(os.Stderr.Fd())
	}
	return &diag.Formatter{
		Filename: path,
		Source:   src,
		Color:    color,
	}
}

func isTerminal(fd uintptr) bool {
	return term.IsTerminal(int(fd))
}

// printDiags sends diagnostics to stderr in either human-readable or JSON
// form. The --max-errors flag, when positive, limits how many are shown
// (the summary still counts all of them).
func printDiags(f *diag.Formatter, diags []*diag.Diagnostic, flags cliFlags) {
	if len(diags) == 0 {
		return
	}
	shown := diags
	if flags.maxErrors > 0 && len(shown) > flags.maxErrors {
		shown = shown[:flags.maxErrors]
	}
	if flags.jsonOutput {
		enc := json.NewEncoder(os.Stderr)
		for _, d := range shown {
			_ = enc.Encode(d)
		}
	} else {
		out := f.FormatAll(shown)
		fmt.Fprintln(os.Stderr, out)
	}
	// Summary (counts all, not just shown).
	errs, warns := 0, 0
	for _, d := range diags {
		switch d.Severity {
		case diag.Error:
			errs++
		case diag.Warning:
			warns++
		}
	}
	if errs > 0 || warns > 0 {
		if flags.maxErrors > 0 && len(diags) > flags.maxErrors {
			fmt.Fprintf(os.Stderr, "  %d error(s), %d warning(s) (showing first %d)\n",
				errs, warns, flags.maxErrors)
		} else {
			fmt.Fprintf(os.Stderr, "  %d error(s), %d warning(s)\n", errs, warns)
		}
	}
	if flags.explain && !flags.jsonOutput {
		printExplainBlock(diags)
	}
}

// printExplainBlock walks the diagnostic list, deduplicates by code,
// and appends the same documentation `osty explain CODE` would emit.
// Skipped under --json (machine consumers should call `osty explain`
// themselves) and when no diagnostic carries a code.
func printExplainBlock(diags []*diag.Diagnostic) {
	seen := map[string]bool{}
	var codes []string
	for _, d := range diags {
		if d.Code == "" || seen[d.Code] {
			continue
		}
		seen[d.Code] = true
		codes = append(codes, d.Code)
	}
	if len(codes) == 0 {
		return
	}
	sort.Strings(codes)
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "── explanations ──────────────────────────────────────────────")
	for _, code := range codes {
		fmt.Fprintln(os.Stderr)
		// Reuse the same lookup paths the standalone `osty explain` uses
		// so output stays consistent across the two entry points.
		if r, ok := lint.LookupRule(code); ok {
			fmt.Fprintf(os.Stderr, "%s  %s\n", r.Code, r.Name)
			fmt.Fprintf(os.Stderr, "  %s\n", r.Summary)
			continue
		}
		if d, ok := diag.Explain(code); ok {
			fmt.Fprintf(os.Stderr, "%s  %s\n", d.Code, d.Name)
			if d.Summary != "" {
				fmt.Fprintf(os.Stderr, "  %s\n", d.Summary)
			}
			if d.Fix != "" {
				fmt.Fprintf(os.Stderr, "  Fix: %s\n", d.Fix)
			}
			continue
		}
		fmt.Fprintf(os.Stderr, "%s  (no explanation registered)\n", code)
	}
}

// isTraceableSingleFileCmd reports whether `--trace` should produce
// per-phase timing for the given subcommand. The streaming output
// only makes sense when the command's work is a strict prefix of the
// front-end pipeline (lex → parse → resolve → check → lint).
// Subcommands with their own internal phases (fmt, gen, build, run,
// test, publish, lsp) are excluded — they would need their own
// instrumentation, which would belong in their respective files.
func isTraceableSingleFileCmd(cmd string) bool {
	switch cmd {
	case "tokens", "parse", "resolve", "check", "typecheck", "lint":
		return true
	}
	return false
}

// resolveFile runs single-file name resolution with the cached stdlib
// registry attached. Collapses what would otherwise be a three-line
// incantation in every single-file subcommand.
func resolveFile(file *ast.File) *resolve.Result {
	return resolve.FileWithStdlib(file, resolve.NewPrelude(), stdlib.LoadCached())
}

func runLintEngine(file *ast.File, res *resolve.Result, chk *check.Result) *lint.Result {
	return lint.File(file, res, chk)
}

// checkOpts builds the check.Opts every subcommand passes to the type
// checker. Sourcing from the cached registry keeps the stdlib Load
// cost paid once per process.
func checkOpts() check.Opts {
	reg := stdlib.LoadCached()
	return check.Opts{UseGolegacy: true, Stdlib: reg, Primitives: reg.Primitives, ResultMethods: reg.ResultMethods}
}

func checkOptsForSource(src []byte) check.Opts {
	opts := checkOpts()
	opts.Source = src
	return opts
}

func hasError(diags []*diag.Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == diag.Error {
			return true
		}
	}
	return false
}

func hasWarning(diags []*diag.Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == diag.Warning {
			return true
		}
	}
	return false
}

func usesFrontEndAIRepair(cmd string) bool {
	return runner.UsesFrontEndAIRepair(cmd)
}

func consumeFrontEndAIRepairFlags(args []string, flags cliFlags) ([]string, cliFlags, error) {
	rest := make([]string, 0, len(args))
	flags.aiRepair = true
	if flags.aiMode == "" {
		flags.aiMode = airepair.ModeAutoAssist
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--airepair" || arg == "--repair":
			flags.aiRepair = true
		case arg == "--no-airepair" || arg == "--no-repair":
			flags.aiRepair = false
		case strings.HasPrefix(arg, "--airepair="):
			value := strings.TrimPrefix(arg, "--airepair=")
			enabled, err := strconv.ParseBool(value)
			if err != nil {
				return nil, flags, fmt.Errorf("invalid boolean for --airepair: %q", value)
			}
			flags.aiRepair = enabled
		case strings.HasPrefix(arg, "--repair="):
			value := strings.TrimPrefix(arg, "--repair=")
			enabled, err := strconv.ParseBool(value)
			if err != nil {
				return nil, flags, fmt.Errorf("invalid boolean for --repair: %q", value)
			}
			flags.aiRepair = enabled
		case arg == "--airepair-mode" || arg == "--repair-mode":
			if i+1 >= len(args) {
				return nil, flags, fmt.Errorf("%s requires a value", arg)
			}
			mode, ok := parseAIRepairMode(args[i+1])
			if !ok {
				return nil, flags, fmt.Errorf("unknown airepair mode %q (want auto, rewrite, parse, or frontend)", args[i+1])
			}
			flags.aiMode = mode
			i++
		case strings.HasPrefix(arg, "--airepair-mode="):
			value := strings.TrimPrefix(arg, "--airepair-mode=")
			mode, ok := parseAIRepairMode(value)
			if !ok {
				return nil, flags, fmt.Errorf("unknown airepair mode %q (want auto, rewrite, parse, or frontend)", value)
			}
			flags.aiMode = mode
		case strings.HasPrefix(arg, "--repair-mode="):
			value := strings.TrimPrefix(arg, "--repair-mode=")
			mode, ok := parseAIRepairMode(value)
			if !ok {
				return nil, flags, fmt.Errorf("unknown airepair mode %q (want auto, rewrite, parse, or frontend)", value)
			}
			flags.aiMode = mode
		default:
			rest = append(rest, arg)
		}
	}
	return rest, flags, nil
}

func aiRepairPrefix(cmd string) string {
	return fmt.Sprintf("osty %s --airepair", cmd)
}

func aiRepairSourceTransform(prefix string, summary io.Writer, flags cliFlags) resolve.SourceTransform {
	if !flags.aiRepair {
		return nil
	}
	return func(path string, src []byte) []byte {
		return maybeAIRepairSource(path, src, prefix, summary, flags)
	}
}

func maybeAIRepairSource(path string, src []byte, prefix string, summary io.Writer, flags cliFlags) []byte {
	if !flags.aiRepair {
		return src
	}
	result := airepair.Analyze(airepair.Request{
		Source:   src,
		Filename: path,
		Mode:     flags.aiMode,
	})
	if !flags.jsonOutput && result.Accepted && (len(result.Repair.Changes) > 0 || result.Repair.Skipped > 0) {
		reportRepairSummary(summary, prefix, path, result.Repair)
	}
	if result.Accepted {
		return result.Repaired
	}
	return src
}

// loadLintConfigWithBase is loadLintConfigNear that also returns the
// manifest directory. Callers need the base to resolve Exclude globs
// against the project root rather than the target path's parent.
func loadLintConfigWithBase(startPath string) (lint.Config, string, bool) {
	dir := startPath
	if info, err := os.Stat(startPath); err == nil && !info.IsDir() {
		dir = filepath.Dir(startPath)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return lint.Config{}, "", false
	}
	for {
		candidate := filepath.Join(abs, manifest.ManifestFile)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			raw, err := os.ReadFile(candidate)
			if err != nil {
				return lint.Config{}, "", false
			}
			m, err := manifest.Parse(raw)
			if err != nil {
				return lint.Config{}, "", false
			}
			if m.Lint == nil {
				return lint.Config{}, abs, false
			}
			return lint.Config{
				Allow:   m.Lint.Allow,
				Deny:    m.Lint.Deny,
				Exclude: m.Lint.Exclude,
			}, abs, true
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return lint.Config{}, "", false
		}
		abs = parent
	}
}

// loadLintConfigNear walks up from the target path (file or dir)
// looking for `osty.toml`. Returns the parsed `[lint]` section as a
// lint.Config if found. Missing manifest or missing [lint] is a
// no-op — linting proceeds with defaults.
//
// Malformed manifests are reported on stderr but do NOT abort the
// lint run — the user can still want a quick lint check on broken
// project metadata.
func loadLintConfigNear(startPath string) (lint.Config, bool) {
	dir := startPath
	if info, err := os.Stat(startPath); err == nil && !info.IsDir() {
		dir = filepath.Dir(startPath)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return lint.Config{}, false
	}
	for {
		candidate := filepath.Join(abs, manifest.ManifestFile)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			raw, err := os.ReadFile(candidate)
			if err != nil {
				fmt.Fprintf(os.Stderr, "osty: %v\n", err)
				return lint.Config{}, false
			}
			m, err := manifest.Parse(raw)
			if err != nil {
				fmt.Fprintf(os.Stderr, "osty: %s: %v\n", candidate, err)
				return lint.Config{}, false
			}
			if m.Lint == nil {
				return lint.Config{}, false
			}
			return lint.Config{Allow: m.Lint.Allow, Deny: m.Lint.Deny}, true
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return lint.Config{}, false // reached the root
		}
		abs = parent
	}
}

// printResolution writes a sorted table of every resolved identifier:
// `line:col  Name  Kind  def-pos`. Useful for sanity-checking the
// resolver's output without a debugger.
func printResolution(_ *ast.File, res *resolve.Result) {
	idents := make([]*ast.Ident, 0, len(res.Refs))
	for id := range res.Refs {
		idents = append(idents, id)
	}
	sort.Slice(idents, func(i, j int) bool {
		a, b := idents[i].PosV, idents[j].PosV
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Column < b.Column
	})
	for _, id := range idents {
		s := res.Refs[id]
		def := "<builtin>"
		if s.Pos.Line > 0 {
			def = fmt.Sprintf("%d:%d", s.Pos.Line, s.Pos.Column)
		}
		fmt.Printf("%d:%d\t%-20s\t%-12s\t->%s\n",
			id.PosV.Line, id.PosV.Column, id.Name, s.Kind, def)
	}
}

// printScopeTree dumps the nested scope tree rooted at s, in AST /
// creation order. Each scope prints a header `<kind> [<N> symbols]`
// followed by its symbols (sorted by declaration position, then name)
// and then its child scopes recursively indented by two spaces. Emitted
// to stdout so it composes with `osty resolve`'s existing output.
//
// Builtin symbols (prelude entries) are filtered out defensively — the
// CLI never roots the dump at the prelude, but if that ever changed
// the noise would be overwhelming.
func printScopeTree(s *resolve.Scope) {
	printScopeNode(s, 0)
}

func printScopeNode(s *resolve.Scope, depth int) {
	indent := strings.Repeat("  ", depth)
	symMap := s.Symbols()
	// Collect user-declared symbols only.
	syms := make([]*resolve.Symbol, 0, len(symMap))
	for _, sym := range symMap {
		if sym.IsBuiltin() {
			continue
		}
		syms = append(syms, sym)
	}
	sort.Slice(syms, func(i, j int) bool {
		a, b := syms[i].Pos, syms[j].Pos
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Column != b.Column {
			return a.Column < b.Column
		}
		return syms[i].Name < syms[j].Name
	})

	kind := s.Kind()
	if kind == "" {
		kind = "scope"
	}
	fmt.Printf("%s%s [%d symbols]\n", indent, kind, len(syms))
	for _, sym := range syms {
		fmt.Printf("%s  %-16s %-12s %d:%d\n",
			indent, sym.Name, sym.Kind, sym.Pos.Line, sym.Pos.Column)
	}
	for _, child := range s.Children() {
		printScopeNode(child, depth+1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: osty [flags] (parse|tokens|resolve|check|typecheck|lint|fmt|airepair|gen) FILE")
	fmt.Fprintln(os.Stderr, "       osty new [--lib] NAME     (scaffold a new project)")
	fmt.Fprintln(os.Stderr, "       osty init [--lib]         (scaffold into the current directory)")
	fmt.Fprintln(os.Stderr, "       osty build [DIR]          (manifest + deps + front end + backend artifacts)")
	fmt.Fprintln(os.Stderr, "       osty add NAME[@VER]       (add a dependency; also --path, --git)")
	fmt.Fprintln(os.Stderr, "       osty update [NAME...]     (refresh osty.lock)")
	fmt.Fprintln(os.Stderr, "       osty run [-- ARGS...]     (build + exec the project's binary)")
	fmt.Fprintln(os.Stderr, "       osty test [PATH|FILTER...] (discover + run test* functions; --seed, --serial, --jobs)")
	fmt.Fprintln(os.Stderr, "       osty publish              (pack + upload the package to a registry)")
	fmt.Fprintln(os.Stderr, "       osty search QUERY         (search the registry for packages)")
	fmt.Fprintln(os.Stderr, "       osty yank --version V [PKG]   (mark a published version as yanked)")
	fmt.Fprintln(os.Stderr, "       osty unyank --version V [PKG] (un-yank a previously yanked version)")
	fmt.Fprintln(os.Stderr, "       osty login [--registry N] (store an API token for publish/yank)")
	fmt.Fprintln(os.Stderr, "       osty logout [--registry N|--all] (forget a stored token)")
	fmt.Fprintln(os.Stderr, "       osty remove NAME [NAME...] (drop a dep from osty.toml; alias rm)")
	fmt.Fprintln(os.Stderr, "       osty fetch [--locked|--frozen] (resolve+vendor without building)")
	fmt.Fprintln(os.Stderr, "       osty info NAME [--all-versions] (show registry metadata for a package)")
	fmt.Fprintln(os.Stderr, "       osty registry serve [--addr A] [--root DIR] (run a package registry)")
	fmt.Fprintln(os.Stderr, "       osty doc [--format FMT] [--out PATH] PATH (generate API docs; markdown or html)")
	fmt.Fprintln(os.Stderr, "       osty ci [flags] [PATH]    (run the CI check bundle: fmt+lint+policy+lockfile)")
	fmt.Fprintln(os.Stderr, "       osty ci snapshot [-o OUT] (capture the exported API for future semver diffing)")
	fmt.Fprintln(os.Stderr, "       osty profiles             (list build profiles — debug, release, ...)")
	fmt.Fprintln(os.Stderr, "       osty targets              (list declared cross-compilation targets)")
	fmt.Fprintln(os.Stderr, "       osty features             (list declared opt-in features)")
	fmt.Fprintln(os.Stderr, "       osty cache [ls|clean|info] (inspect / prune the build cache)")
	fmt.Fprintln(os.Stderr, "       osty lsp                  (language server on stdio)")
	fmt.Fprintln(os.Stderr, "       osty explain [CODE]       (describe a diagnostic code; no arg lists every code)")
	fmt.Fprintln(os.Stderr, "       osty pipeline FILE|DIR    (run every front-end phase; per-stage timing; --gen supports --backend)")
	fmt.Fprintln(os.Stderr, "       osty airepair triage DIR  (summarize captured airepair reports)")
	fmt.Fprintln(os.Stderr, "       osty airepair learn DIR   (rank captured airepair failures into next-work priorities)")
	fmt.Fprintln(os.Stderr, "       osty airepair promote CASE (copy a captured case into the airepair corpus)")
	fmt.Fprintln(os.Stderr, "flags:")
	fmt.Fprintln(os.Stderr, "  --no-color         disable ANSI escapes")
	fmt.Fprintln(os.Stderr, "  --color            force ANSI escapes")
	fmt.Fprintln(os.Stderr, "  --max-errors N     show only the first N diagnostics")
	fmt.Fprintln(os.Stderr, "  --json             emit diagnostics as NDJSON")
	fmt.Fprintln(os.Stderr, "  --strict           lint: exit 1 on warnings (CI mode)")
	fmt.Fprintln(os.Stderr, "  --fix              lint: apply machine-applicable suggestions")
	fmt.Fprintln(os.Stderr, "  --fix-dry-run      lint: print fixed source without writing")
	fmt.Fprintln(os.Stderr, "  --scopes           resolve: also print the nested scope tree")
	fmt.Fprintln(os.Stderr, "  --trace            stream per-phase timing to stderr (front-end commands)")
	fmt.Fprintln(os.Stderr, "  --explain          append `osty explain CODE` text after each diagnostic block")
	fmt.Fprintln(os.Stderr, "  --inspect          check: emit one record per expression (rule, type, hint)")
	fmt.Fprintln(os.Stderr, "fmt-specific flags (after the subcommand):")
	fmt.Fprintln(os.Stderr, "  --check            exit 1 if FILE is not already formatted")
	fmt.Fprintln(os.Stderr, "  --engine NAME      formatter engine: go (default) or osty")
	fmt.Fprintln(os.Stderr, "  --write            overwrite FILE in place")
	fmt.Fprintln(os.Stderr, "  --airepair         enable the default pre-format AI repair pass")
	fmt.Fprintln(os.Stderr, "  --no-airepair      disable the default pre-format AI repair pass")
	fmt.Fprintln(os.Stderr, "airepair-specific flags (after the subcommand):")
	fmt.Fprintln(os.Stderr, "  triage DIR         summarize captured airepair reports in DIR")
	fmt.Fprintln(os.Stderr, "  learn DIR          rank captured airepair failures into next-work priorities")
	fmt.Fprintln(os.Stderr, "  promote CASE       copy CASE into internal/airepair/testdata/corpus")
	fmt.Fprintln(os.Stderr, "  --check            exit 1 if FILE would be repaired")
	fmt.Fprintln(os.Stderr, "  --write            overwrite FILE in place")
	fmt.Fprintln(os.Stderr, "  --json             emit a structured airepair report as JSON")
	fmt.Fprintln(os.Stderr, "  --capture-dir DIR  write captured airepair artifacts to DIR")
	fmt.Fprintln(os.Stderr, "  --capture-name N   basename for captured airepair artifacts")
	fmt.Fprintln(os.Stderr, "  --capture-if MODE  capture mode: residual, changed, or always")
	fmt.Fprintln(os.Stderr, "  --top N            triage: show up to N entries per summary section")
	fmt.Fprintln(os.Stderr, "  --corpus DIR       learn: corpus directory used for exact fixture coverage checks")
	fmt.Fprintln(os.Stderr, "  --dest DIR         promote: destination corpus directory")
	fmt.Fprintln(os.Stderr, "  --name NAME        promote: basename for promoted corpus files")
	fmt.Fprintln(os.Stderr, "  --stdin-name NAME  filename to use in reports when FILE is -")
	fmt.Fprintln(os.Stderr, "  --mode MODE        debug acceptance mode: auto, rewrite, parse, or frontend")
	fmt.Fprintln(os.Stderr, "front-end airepair flags (after check/resolve/typecheck/lint):")
	fmt.Fprintln(os.Stderr, "  --airepair         keep automatic AI repair enabled (default)")
	fmt.Fprintln(os.Stderr, "  --no-airepair      disable automatic AI repair")
	fmt.Fprintln(os.Stderr, "  --airepair-mode    debug acceptance mode: auto, rewrite, parse, or frontend")
	fmt.Fprintln(os.Stderr, "gen-specific flags (after the subcommand):")
	fmt.Fprintln(os.Stderr, "  -o PATH            write generated artifact to PATH instead of stdout")
	fmt.Fprintln(os.Stderr, "  --package NAME     backend package/module name (default: main)")
	fmt.Fprintln(os.Stderr, "  --backend NAME     code generation backend (llvm; default llvm)")
	fmt.Fprintln(os.Stderr, "  --emit MODE        artifact mode (llvm-ir; default follows backend)")
	fmt.Fprintln(os.Stderr, "new-specific flags (after the subcommand):")
	fmt.Fprintln(os.Stderr, "  --lib              scaffold a library project (lib.osty, no main)")
	fmt.Fprintln(os.Stderr, "  --bin              scaffold a binary project (main.osty) [default]")
	fmt.Fprintln(os.Stderr, "  --workspace        scaffold a virtual workspace with one default member")
	fmt.Fprintln(os.Stderr, "add-specific flags (after the subcommand):")
	fmt.Fprintln(os.Stderr, "  --path DIR         local-path dependency (no network)")
	fmt.Fprintln(os.Stderr, "  --git URL          git dependency; also --tag, --branch, --rev")
	fmt.Fprintln(os.Stderr, "  --version REQ      registry version requirement (e.g. ^1.0)")
	fmt.Fprintln(os.Stderr, "  --dev              add to [dev-dependencies]")
	fmt.Fprintln(os.Stderr, "  --rename NAME      local alias for the dep")
	fmt.Fprintln(os.Stderr, "publish-specific flags (after the subcommand):")
	fmt.Fprintln(os.Stderr, "  --registry NAME    target registry (defaults to [registries.\"\"])")
	fmt.Fprintln(os.Stderr, "  --token T          API token (or set $OSTY_PUBLISH_TOKEN)")
	fmt.Fprintln(os.Stderr, "  --dry-run          build the tarball but do not upload")
	fmt.Fprintln(os.Stderr, "Common flags for build / add / update / run / test:")
	fmt.Fprintln(os.Stderr, "  --offline          do not fetch dependencies; fail if caches are missing")
	fmt.Fprintln(os.Stderr, "build / run / test flags:")
	fmt.Fprintln(os.Stderr, "  --profile NAME     build profile (debug, release, profile, test, ...)")
	fmt.Fprintln(os.Stderr, "  --release          shorthand for --profile release")
	fmt.Fprintln(os.Stderr, "  --target TRIPLE    cross-compilation target (e.g. amd64-linux)")
	fmt.Fprintln(os.Stderr, "  --features LIST    comma-separated feature flags to enable")
	fmt.Fprintln(os.Stderr, "  --no-default-features  drop the manifest's [features].default set")
	fmt.Fprintln(os.Stderr, "  --backend NAME     code generation backend (llvm; default llvm)")
	fmt.Fprintln(os.Stderr, "  --emit MODE        artifact mode (llvm-ir, object, or binary)")
	fmt.Fprintln(os.Stderr, "build-specific flags:")
	fmt.Fprintln(os.Stderr, "  --force            ignore the build cache and rebuild from source")
}

// printTypes writes a compact `line:col <TYPE>` table for every
// expression the type checker assigned a non-error type to. Sorted by
// position so the output is reproducible and easy to diff.
func printTypes(r *check.Result) {
	type row struct {
		pos  token.Pos
		end  token.Pos
		text string
	}
	rows := make([]row, 0, len(r.Types))
	for e, t := range r.Types {
		if types.IsError(t) {
			continue
		}
		rows = append(rows, row{
			pos:  e.Pos(),
			end:  e.End(),
			text: t.String(),
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i].pos, rows[j].pos
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Column < b.Column
	})
	for _, r := range rows {
		fmt.Printf("%d:%d-%d:%d\t%s\n",
			r.pos.Line, r.pos.Column, r.end.Line, r.end.Column, r.text)
	}
}

// runFmt implements the `osty fmt` subcommand. The args slice holds
// everything on the command line following `fmt` — zero or more of
// --check/--write/--airepair/--no-airepair/--engine, then exactly
// one file path.
//
// Exit codes match gofmt conventions:
//
//	0   formatting succeeded (or file was already formatted under --check)
//	1   formatting differences found under --check, OR unrecoverable I/O error
//	2   usage error (missing path, unknown flag, parse error, etc.)
func runFmt(args []string) {
	fs := flag.NewFlagSet("fmt", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty fmt [--check] [--write] [--airepair] [--no-airepair] [--engine go|osty] FILE")
	}
	var checkMode, writeMode, noAIRepair bool
	engine := "go"
	repairMode := true
	fs.BoolVar(&checkMode, "check", false, "exit 1 if FILE is not already formatted")
	fs.BoolVar(&checkMode, "c", false, "alias for --check")
	fs.BoolVar(&writeMode, "write", false, "overwrite FILE in place")
	fs.BoolVar(&writeMode, "w", false, "alias for --write")
	fs.BoolVar(&repairMode, "airepair", true, "enable automatic AI repair before formatting")
	fs.BoolVar(&repairMode, "repair", true, "alias for --airepair")
	fs.BoolVar(&noAIRepair, "no-airepair", false, "disable automatic AI repair before formatting")
	fs.BoolVar(&noAIRepair, "no-repair", false, "alias for --no-airepair")
	fs.StringVar(&engine, "engine", engine, "formatter engine (go|osty)")
	_ = fs.Parse(args)
	if noAIRepair {
		repairMode = false
	}
	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}
	path := fs.Arg(0)
	if checkMode && writeMode {
		fmt.Fprintln(os.Stderr, "osty fmt: --check and --write are mutually exclusive")
		os.Exit(2)
	}

	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty: %v\n", err)
		os.Exit(1)
	}
	formatSrc := src
	var repairs repair.Result
	if repairMode {
		repairs = repair.Source(src)
		formatSrc = repairs.Source
	}
	var (
		out   []byte
		diags []*diag.Diagnostic
		ferr  error
	)
	switch engine {
	case "", "go", "ast":
		out, diags, ferr = format.Source(formatSrc)
	case "osty":
		out, diags, ferr = format.OstySource(formatSrc)
	default:
		fmt.Fprintf(os.Stderr, "osty fmt: unknown engine %q (want go or osty)\n", engine)
		os.Exit(2)
	}
	if ferr != nil {
		// Render parse diagnostics so the user can fix them.
		formatter := &diag.Formatter{Filename: path, Source: formatSrc}
		if len(diags) > 0 {
			fmt.Fprintln(os.Stderr, formatter.FormatAll(diags))
		}
		fmt.Fprintf(os.Stderr, "osty fmt: %v\n", ferr)
		os.Exit(2)
	}

	if checkMode {
		if !bytes.Equal(src, out) {
			if repairMode && (len(repairs.Changes) > 0 || repairs.Skipped > 0) {
				reportRepairSummary(os.Stderr, "osty fmt", path, repairs)
			}
			fmt.Fprintf(os.Stderr, "%s: not repaired/formatted\n", path)
			os.Exit(1)
		}
		return
	}
	if writeMode {
		if bytes.Equal(src, out) {
			return
		}
		if err := os.WriteFile(path, out, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "osty fmt: %v\n", err)
			os.Exit(1)
		}
		return
	}
	// Default: print to stdout.
	if _, err := os.Stdout.Write(out); err != nil {
		fmt.Fprintf(os.Stderr, "osty fmt: %v\n", err)
		os.Exit(1)
	}
}

// runGen implements the `osty gen` subcommand: emit a single .osty
// file through the selected backend and either print the requested
// text artifact to stdout or write it to the path given by --out/-o.
//
// Exit codes:
//
//	0   emission succeeded
//	1   unrecoverable I/O error, or backend emission returned an error even
//	    after partial output
//	2   usage error (missing path, unknown flag), or parse/resolve/check
//	    failures that would produce garbage backend output
func runGen(args []string, flags cliFlags) {
	fs := flag.NewFlagSet("gen", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty gen [--backend NAME] [--emit MODE] [-o OUT] FILE.osty")
	}
	var outPath string
	var pkgName string
	var backendName string
	var emitName string
	fs.StringVar(&outPath, "o", "", "write generated artifact to this file instead of stdout")
	fs.StringVar(&outPath, "out", "", "alias for -o")
	fs.StringVar(&pkgName, "package", "main", "backend package/module name (default: main)")
	fs.StringVar(&backendName, "backend", defaultBackendName(), "code generation backend (llvm)")
	fs.StringVar(&emitName, "emit", "", "artifact mode (llvm-ir; default follows backend)")
	_ = fs.Parse(args)
	backendID, emitMode := resolveBackendAndEmitFlags("gen", backendName, emitName)
	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}
	path := fs.Arg(0)
	flags.aiRepair = true
	if flags.aiMode == "" {
		flags.aiMode = airepair.ModeAutoAssist
	}
	entry, err := loadGenPackageEntryWithTransform(path, aiRepairSourceTransform("osty gen --airepair", os.Stderr, flags))
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty gen: %v\n", err)
		os.Exit(1)
	}
	blockingCheckDiags, deferredCheckDiags := splitGenCheckDiags(entry.chk.Diags)
	allDiags := append([]*diag.Diagnostic{}, entry.res.Diags...)
	allDiags = append(allDiags, blockingCheckDiags...)
	if hasError(allDiags) {
		printPackageDiags(entry.pkg, allDiags, flags)
		fmt.Fprintf(os.Stderr, "osty gen: front-end errors prevent transpilation\n")
		os.Exit(2)
	}
	if len(deferredCheckDiags) > 0 {
		reportDeferredGenCheck(path, deferredCheckDiags)
	}
	file, _, err := parseGenEmitFile(entry.pkg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty gen: %v\n", err)
		os.Exit(1)
	}

	out, result, emitErr := emitGenArtifact(backendID, emitMode, pkgName, entry.sourcePath, file, entry.fileResult(), entry.chk)
	if out == nil && emitErr != nil {
		exitBackendEmitError("gen", result, emitErr)
	}
	if emitErr == nil {
		reportTranspileWarning("osty gen", entry.sourcePath, outPath, firstBackendWarning(result))
	}

	if outPath != "" {
		if err := os.WriteFile(outPath, out, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "osty gen: %v\n", err)
			os.Exit(1)
		}
	} else if _, err := os.Stdout.Write(out); err != nil {
		fmt.Fprintf(os.Stderr, "osty gen: %v\n", err)
		os.Exit(1)
	}
	if emitErr != nil {
		adjustGenResultForUserOutput(result, backendID, outPath)
		exitBackendEmitError("gen", result, emitErr)
	}
}

func emitGenArtifact(name backend.Name, mode backend.EmitMode, pkgName, sourcePath string, file *ast.File, res *resolve.Result, chk *check.Result) ([]byte, *backend.Result, error) {
	b := backendFromCLI("gen", name)
	tmpRoot, err := os.MkdirTemp("", "osty-gen-*")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(tmpRoot)
	entry, err := backend.PrepareEntry(pkgName, sourcePath, file, res, chk)
	if err != nil {
		return nil, nil, err
	}

	result, emitErr := b.Emit(context.Background(), backend.Request{
		Layout: backend.Layout{
			Root:    tmpRoot,
			Profile: "gen",
		},
		Emit:  mode,
		Entry: entry,
	})
	if result == nil {
		return nil, nil, emitErr
	}
	artifact := result.Artifacts.SourcePath()
	if artifact == "" {
		if emitErr != nil {
			return nil, result, emitErr
		}
		return nil, result, fmt.Errorf("backend %q did not produce a source artifact", name)
	}
	data, readErr := os.ReadFile(artifact)
	if readErr != nil {
		if emitErr != nil {
			return nil, result, emitErr
		}
		return nil, result, readErr
	}
	return data, result, emitErr
}

func adjustGenResultForUserOutput(result *backend.Result, name backend.Name, outPath string) {
	if result == nil {
		return
	}
	result.Artifacts.RuntimeDir = ""
	if outPath == "" {
		result.Artifacts.LLVMIR = ""
		return
	}
	if name == backend.NameLLVM {
		result.Artifacts.LLVMIR = outPath
	}
}

type genPackageEntry struct {
	sourcePath string
	pkg        *resolve.Package
	res        *resolve.PackageResult
	chk        *check.Result
	file       *resolve.PackageFile
}

func loadGenPackageEntry(path string) (*genPackageEntry, error) {
	return loadGenPackageEntryWithTransform(path, nil)
}

func loadGenPackageEntryWithTransform(path string, transform resolve.SourceTransform) (*genPackageEntry, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if files := toolchainGenInputFiles(absPath); len(files) > 0 {
		return loadSelectedGenFilesWithTransform(absPath, files, transform)
	}
	ws, err := resolve.NewWorkspace(filepath.Dir(absPath))
	if err != nil {
		return nil, err
	}
	ws.SourceTransform = transform
	ws.Stdlib = stdlib.LoadCached()
	if _, err := ws.LoadPackage(""); err != nil {
		return nil, err
	}
	results := ws.ResolveAll()
	checks := check.Workspace(ws, results, checkOpts())
	pkg := ws.Packages[""]
	if pkg == nil {
		return nil, fmt.Errorf("%s: no package sources were loaded", filepath.Dir(absPath))
	}
	var entryFile *resolve.PackageFile
	for _, pf := range pkg.Files {
		if pf == nil {
			continue
		}
		fp, err := filepath.Abs(pf.Path)
		if err != nil {
			continue
		}
		if fp == absPath {
			entryFile = pf
			break
		}
	}
	if entryFile == nil {
		return nil, fmt.Errorf("%s is not part of the package rooted at %s", absPath, pkg.Dir)
	}
	res := results[""]
	if res == nil {
		return nil, fmt.Errorf("%s: package resolution did not produce a root result", pkg.Dir)
	}
	chk := checks[""]
	if chk == nil {
		chk = &check.Result{}
	}
	return &genPackageEntry{
		sourcePath: absPath,
		pkg:        pkg,
		res:        res,
		chk:        chk,
		file:       entryFile,
	}, nil
}

func loadSelectedGenFiles(sourcePath string, files []string) (*genPackageEntry, error) {
	return loadSelectedGenFilesWithTransform(sourcePath, files, nil)
}

func loadSelectedGenFilesWithTransform(sourcePath string, files []string, transform resolve.SourceTransform) (*genPackageEntry, error) {
	pkgDir := filepath.Dir(sourcePath)
	pkg := &resolve.Package{
		Dir:  pkgDir,
		Name: filepath.Base(pkgDir),
	}
	res := &resolve.PackageResult{}
	chk := &check.Result{}
	var entryFile *resolve.PackageFile
	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if transform != nil {
			src = transform(path, src)
		}
		parsed := parser.ParseDetailed(src)
		canonicalSrc, canonicalMap := canonical.SourceWithMap(src, parsed.File)
		pf := &resolve.PackageFile{
			Path:            path,
			Source:          src,
			CanonicalSource: canonicalSrc,
			CanonicalMap:    canonicalMap,
			File:            parsed.File,
			ParseDiags:      parsed.Diagnostics,
			ParseProvenance: parsed.Provenance,
		}
		pkg.Files = append(pkg.Files, pf)
		res.Diags = append(res.Diags, parsed.Diagnostics...)
		if path == sourcePath {
			entryFile = pf
		}
	}
	if entryFile == nil {
		return nil, fmt.Errorf("%s is not part of the selected gen input set", sourcePath)
	}
	return &genPackageEntry{
		sourcePath: sourcePath,
		pkg:        pkg,
		res:        res,
		chk:        chk,
		file:       entryFile,
	}, nil
}

func toolchainGenInputFiles(sourcePath string) []string {
	dir := filepath.Dir(sourcePath)
	if filepath.Base(dir) != "toolchain" {
		return nil
	}
	name := filepath.Base(sourcePath)
	var rel []string
	switch name {
	case "semver.osty":
		rel = []string{"semver.osty"}
	case "semver_parse.osty":
		rel = []string{"semver.osty", "semver_parse.osty"}
	case "frontend.osty":
		rel = []string{"semver.osty", "semver_parse.osty", "frontend.osty", "lexer.osty", "parser.osty", "formatter_ast.osty"}
	case "check_bridge.osty":
		rel = []string{"semver.osty", "semver_parse.osty", "frontend.osty", "lexer.osty", "parser.osty", "formatter_ast.osty", "check_bridge.osty"}
	case "check.osty":
		rel = []string{"semver.osty", "semver_parse.osty", "frontend.osty", "lexer.osty", "parser.osty", "formatter_ast.osty", "check_bridge.osty", "check.osty"}
	default:
		return nil
	}
	out := make([]string, 0, len(rel))
	for _, file := range rel {
		out = append(out, filepath.Join(dir, file))
	}
	return out
}

func (e *genPackageEntry) fileResult() *resolve.Result {
	if e == nil || e.file == nil {
		return nil
	}
	return &resolve.Result{
		Refs:      e.file.Refs,
		TypeRefs:  e.file.TypeRefs,
		FileScope: e.file.FileScope,
		Diags:     e.res.Diags,
	}
}

func splitGenCheckDiags(diags []*diag.Diagnostic) (blocking, deferred []*diag.Diagnostic) {
	if genAllowsDeferredCheckerErrors() {
		return nil, append([]*diag.Diagnostic(nil), diags...)
	}
	for _, d := range diags {
		if isDeferredGenCheckDiag(d) {
			deferred = append(deferred, d)
			continue
		}
		blocking = append(blocking, d)
	}
	return blocking, deferred
}

func isDeferredGenCheckDiag(d *diag.Diagnostic) bool {
	if d == nil {
		return false
	}
	return runner.IsDeferredGenCheckDiag(d.Severity.String(), d.Message)
}

func reportDeferredGenCheck(path string, diags []*diag.Diagnostic) {
	if len(diags) == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "osty gen: warning: native type checking is unavailable for %s; continuing with backend-only emission\n", path)
	seen := map[string]bool{}
	for _, d := range diags {
		for _, note := range d.Notes {
			note = strings.TrimSpace(note)
			if note == "" || seen[note] {
				continue
			}
			seen[note] = true
			fmt.Fprintf(os.Stderr, "osty gen: note: %s\n", note)
		}
	}
}

func parseGenEmitFile(pkg *resolve.Package) (*ast.File, []byte, error) {
	if pkg == nil {
		return nil, nil, fmt.Errorf("missing package input for gen")
	}
	var merged bytes.Buffer
	for _, pf := range pkg.Files {
		if pf == nil {
			continue
		}
		src := pf.CheckerSource()
		if len(src) == 0 {
			continue
		}
		if merged.Len() > 0 {
			merged.WriteByte('\n')
		}
		merged.Write(src)
		if !bytes.HasSuffix(src, []byte("\n")) {
			merged.WriteByte('\n')
		}
	}
	src := merged.Bytes()
	if len(src) == 0 {
		return nil, nil, fmt.Errorf("%s: no source bytes were available for backend emission", pkg.Dir)
	}
	// Reparse the synthetic single-file source we hand to the backend. Merely
	// stitching together per-file AST fragments leaves source positions and some
	// checker inference paths out of sync with the merged byte stream, which in
	// turn can poison the backend bridge with `<error>` types for otherwise valid
	// native-test programs.
	if reparsed, diags := parser.ParseDiagnostics(src); reparsed != nil && !hasError(diags) {
		return reparsed, src, nil
	}
	return nil, nil, fmt.Errorf("%s: merged package source could not be reparsed for backend emission", pkg.Dir)
}

// runNew implements the `osty new NAME` subcommand: scaffold a fresh
// project directory under the current working directory.
//
// Exit codes:
//
//	0   scaffold succeeded; created-path printed to stdout
//	1   I/O error or destination already exists
//	2   usage error (missing name, unknown flag, invalid name)
func runNew(args []string) {
	fs := flag.NewFlagSet("new", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty new [--bin|--lib|--workspace|--cli|--service] [--member NAME] NAME")
	}
	var libMode, binMode, wsMode, cliMode, svcMode bool
	var member string
	fs.BoolVar(&libMode, "lib", false, "scaffold a library project (lib.osty, no main)")
	fs.BoolVar(&binMode, "bin", false, "scaffold a binary project (main.osty) [default]")
	fs.BoolVar(&wsMode, "workspace", false, "scaffold a virtual workspace with one default member")
	fs.BoolVar(&cliMode, "cli", false, "scaffold a CLI app (Args struct + run() core)")
	fs.BoolVar(&svcMode, "service", false, "scaffold an HTTP service (Request/Response + handle())")
	fs.StringVar(&member, "member", "core", "default-member directory name when --workspace is set")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}
	kind, usageErr := pickScaffoldKind(libMode, binMode, wsMode, cliMode, svcMode)
	if usageErr != "" {
		fmt.Fprintln(os.Stderr, "osty new:", usageErr)
		os.Exit(2)
	}
	name := fs.Arg(0)
	dir, d := scaffold.Create(scaffold.Options{
		Name:            name,
		Kind:            kind,
		WorkspaceMember: member,
	})
	if d != nil {
		printScaffoldDiag(d)
		os.Exit(scaffoldExitCode(d))
	}
	fmt.Printf("Created %s %q at %s\n", kindLabel(kind), name, dir)
}

// runInit implements `osty init [--bin|--lib|--workspace] [--name NAME]`
// — scaffold into the current directory in place. When --name is
// absent the project name defaults to the cwd basename.
func runInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty init [--bin|--lib|--workspace|--cli|--service] [--name NAME] [--member NAME]")
	}
	var libMode, binMode, wsMode, cliMode, svcMode bool
	var name, member string
	fs.BoolVar(&libMode, "lib", false, "scaffold a library project (lib.osty, no main)")
	fs.BoolVar(&binMode, "bin", false, "scaffold a binary project (main.osty) [default]")
	fs.BoolVar(&wsMode, "workspace", false, "scaffold a virtual workspace with one default member")
	fs.BoolVar(&cliMode, "cli", false, "scaffold a CLI app (Args struct + run() core)")
	fs.BoolVar(&svcMode, "service", false, "scaffold an HTTP service (Request/Response + handle())")
	fs.StringVar(&name, "name", "", "project name (defaults to current directory basename)")
	fs.StringVar(&member, "member", "core", "default-member directory name when --workspace is set")
	_ = fs.Parse(args)
	if fs.NArg() != 0 {
		fs.Usage()
		os.Exit(2)
	}
	kind, usageErr := pickScaffoldKind(libMode, binMode, wsMode, cliMode, svcMode)
	if usageErr != "" {
		fmt.Fprintln(os.Stderr, "osty init:", usageErr)
		os.Exit(2)
	}
	dir, d := scaffold.Init(scaffold.Options{
		Name:            name,
		Kind:            kind,
		WorkspaceMember: member,
	})
	if d != nil {
		printScaffoldDiag(d)
		os.Exit(scaffoldExitCode(d))
	}
	label := kindLabel(kind)
	shownName := name
	if shownName == "" && kind != scaffold.KindWorkspace {
		shownName = filepath.Base(dir)
	}
	fmt.Printf("Initialized %s %q in %s\n", label, shownName, dir)
}

// pickScaffoldKind validates the mutually-exclusive kind flags and
// returns the selected Kind plus an empty string, or the zero Kind
// and a one-line usage error message.
func pickScaffoldKind(libMode, binMode, wsMode, cliMode, svcMode bool) (scaffold.Kind, string) {
	count := 0
	if libMode {
		count++
	}
	if binMode {
		count++
	}
	if wsMode {
		count++
	}
	if cliMode {
		count++
	}
	if svcMode {
		count++
	}
	if count > 1 {
		return 0, "--bin, --lib, --workspace, --cli, and --service are mutually exclusive"
	}
	switch {
	case libMode:
		return scaffold.KindLib, ""
	case wsMode:
		return scaffold.KindWorkspace, ""
	case cliMode:
		return scaffold.KindCli, ""
	case svcMode:
		return scaffold.KindService, ""
	default:
		return scaffold.KindBin, ""
	}
}

// kindLabel returns a user-facing noun for a scaffold Kind, used in
// the "Created X ..." success line.
func kindLabel(k scaffold.Kind) string {
	switch k {
	case scaffold.KindLib:
		return "library project"
	case scaffold.KindWorkspace:
		return "workspace"
	case scaffold.KindCli:
		return "CLI app project"
	case scaffold.KindService:
		return "HTTP service project"
	default:
		return "binary project"
	}
}

// printScaffoldDiag renders a scaffold diagnostic through the shared
// diag.Formatter so scaffold errors look like compile errors in the
// terminal. Scaffold diagnostics have no source bytes (they're about
// filesystem state), so the formatter falls back to the header+hint
// presentation.
func printScaffoldDiag(d *diag.Diagnostic) {
	f := &diag.Formatter{}
	fmt.Fprintln(os.Stderr, f.FormatAll([]*diag.Diagnostic{d}))
}

// scaffoldExitCode maps a scaffold diagnostic code to a process
// exit code. Rule table lives in toolchain/diag_policy.osty.
func scaffoldExitCode(d *diag.Diagnostic) int {
	if d == nil {
		return 0
	}
	return runner.ScaffoldExitCode(d.Code)
}

// runBuild lives in build.go — it pulls in the package-manager
// pipeline (pkgmgr, lockfile) so the imports stay scoped to that file.

// takeBoolFlag scans args for a flag-only occurrence of `name` (no
// attached value) and returns the remaining args plus whether the flag
// was present. Distinct from takeFlag, which always consumes the
// following arg as a value.
func takeBoolFlag(args []string, name string) (rest []string, present bool) {
	for i, a := range args {
		if a == name {
			return append(append([]string{}, args[:i]...), args[i+1:]...), true
		}
	}
	return args, false
}

// takeFlag scans args for `name=value` or `name value` and returns the
// remaining args plus the captured value. When absent, matched is ""
// (empty). When the flag is seen with no following arg, matched is
// "__present__". Used for subcommand-local value-taking flags like
// `--explain CODE`.
func takeFlag(args []string, name string) (rest []string, matched string) {
	for i, a := range args {
		if a == name {
			// `--explain CODE` (two-arg form). Require a following value.
			if i+1 < len(args) {
				return append(append([]string{}, args[:i]...), args[i+2:]...), args[i+1]
			}
			// `--list` (flag-only form).
			return append(append([]string{}, args[:i]...), args[i+1:]...), "__present__"
		}
		if len(a) > len(name)+1 && a[:len(name)+1] == name+"=" {
			return append(append([]string{}, args[:i]...), args[i+1:]...), a[len(name)+1:]
		}
	}
	return args, ""
}

// runLintExplain prints a single rule's metadata and exits 0 on success,
// 2 on unknown rule name. Accepts either a code (L0001) or a name
// (unused_let).
func runLintExplain(name string) {
	r, ok := lint.LookupRule(name)
	if !ok {
		fmt.Fprintf(os.Stderr, "osty lint --explain: unknown rule %q\n", name)
		fmt.Fprintln(os.Stderr, "use `osty lint --list` to see every rule")
		os.Exit(2)
	}
	fmt.Printf("%s  %s\n", r.Code, r.Name)
	fmt.Printf("category: %s\n", r.Category)
	fmt.Printf("summary:  %s\n\n", r.Summary)
	fmt.Println(r.Description)
}

// runLintList prints every rule, grouped by category. Machine-readable
// tab-separated format: CODE\tNAME\tCATEGORY\tSUMMARY.
func runLintList() {
	for _, r := range lint.Rules() {
		fmt.Printf("%s\t%s\t%s\t%s\n", r.Code, r.Name, r.Category, r.Summary)
	}
}
