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
//	--no-airepair  disable the default pre-format AI repair pass
//	--write        overwrite the file in place instead of printing to stdout
package main

import (
	"bytes"
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
	// a `check` / `typecheck` run. Legacy paths read it from
	// `internal/check.Result`; self-host native CLI paths read the same
	// counters directly from `selfhost.CheckSummary`. Off by default.
	dumpNativeDiags bool
	// native is accepted as a backwards-compat no-op: the self-host
	// arena pipeline (selfhost.CheckFromSource) has been the
	// `check` / `typecheck` default since Phase 1c.1 and is now the
	// only path after 1c.5 retired the Go-hosted escape hatch. The
	// flag is still recognised by parseFlags so old scripts keep
	// working, but it no longer selects between code paths.
	native bool
	// suppressSummary silences the `N error(s), M warning(s)` trailer
	// inside a single printDiags call. The package-diagnostic walker sets
	// this per-file bucket and then prints one consolidated summary
	// afterwards, so multi-file failures don't emit a summary line per
	// file. Never surfaced as a CLI flag.
	suppressSummary bool
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
		// `--native` is accepted after the subcommand as a backwards-
		// compat no-op; the self-host pipeline is the only path since
		// Phase 1c.5 retired `--legacy`. Strip it so the downstream
		// file-required check and subcommand dispatch see positional-
		// only input.
		if rest, present := takeBoolFlag(args[1:], "--native"); present {
			flags.native = true
			args = append([]string{"check"}, rest...)
		}
	}
	if cmd == "typecheck" {
		if rest, present := takeBoolFlag(args[1:], "--native"); present {
			flags.native = true
			args = append([]string{"typecheck"}, rest...)
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
		runPipeline(args[1:], flags)
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
		case "typecheck":
			if flags.inspect {
				fmt.Fprintf(os.Stderr, "osty: --inspect is not supported on the self-host typecheck path\n")
				os.Exit(2)
			}
			if root, ok, abort := nativeWorkspaceRoot(path, flags); abort {
				os.Exit(2)
			} else if ok {
				if runTypecheckWorkspaceNative(root, flags) != 0 {
					os.Exit(1)
				}
				return
			}
			if runTypecheckPackageNative(path, flags) != 0 {
				os.Exit(1)
			}
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
				if flags.dumpNativeDiags {
					dumpNativeDiagsFor(path, selected.chk)
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
				diags := selected.res.Diags
				if nativeDiags, err := nativeResolvePackageDiagnostics(selected.pkg); err == nil {
					diags = append(packageParseDiags(selected.pkg), nativeDiags...)
				}
				printPackageDiags(selected.pkg, diags, flags)
				if rows, err := nativeResolvePackageRows(selected.pkg, selected.file.Path); err == nil && len(rows) > 0 {
					fmt.Printf("# %s\n", selected.file.Path)
					printNativeResolutionRows(rows)
				} else if len(selected.file.RefsByID) > 0 {
					fmt.Printf("# %s\n", selected.file.Path)
					printResolutionRefs(selected.file.RefIdents, selected.file.RefsByID)
				}
				if flags.showScopes {
					if pkgScope := selected.file.FileScope.Parent(); pkgScope != nil {
						printScopeTree(pkgScope)
					} else {
						printScopeTree(selected.file.FileScope)
					}
				}
				if hasError(diags) {
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
		if flags.inspect {
			fmt.Fprintf(os.Stderr, "osty: --inspect is not supported on the self-host check path\n")
			os.Exit(2)
		}
		if runCheckFileNative(path, src, formatter, flags) != 0 {
			os.Exit(1)
		}
	case "typecheck":
		if flags.inspect {
			fmt.Fprintf(os.Stderr, "osty: --inspect is not supported on the self-host typecheck path\n")
			os.Exit(2)
		}
		if runTypecheckFileNative(path, src, formatter, flags) != 0 {
			os.Exit(1)
		}
	case "resolve":
		if runResolveFile(path, src, formatter, flags) != 0 {
			os.Exit(1)
		}
	case "lint":
		lintCfg, lintBase, lintOk := loadLintConfigWithBase(path)
		if lintOk {
			if pat, matched := lintCfg.MatchingExclude(path, lintBase); matched {
				fmt.Fprintf(os.Stderr, "osty lint: skipping %s ([lint] exclude matches %q)\n", path, pat)
				return
			}
		}
		if code := runLintFile(path, src, formatter, flags, lintCfg, lintOk); code != 0 {
			os.Exit(code)
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
	flag.BoolVar(&f.inspect, "inspect", false, "check: emit one record per expression showing the inference rule, type, and hint (see LANG_SPEC_v0.5/02a-type-inference.md)")
	flag.BoolVar(&f.dumpNativeDiags, "dump-native-diags", false, "check/typecheck: after the run, print the native checker's per-context error histogram to stderr")
	flag.BoolVar(&f.native, "native", true, "check/typecheck: backwards-compat no-op since Phase 1c.5. The self-host arena pipeline is the only path; this flag is accepted so old scripts keep working")
	flag.Usage = usage
	flag.Parse()
	return f
}

// runResolvePackage is runCheckPackage plus a resolution dump per file.
func runResolvePackage(dir string, flags cliFlags) {
	if code := runResolvePackageInner(dir, flags); code != 0 {
		os.Exit(code)
	}
}

// runResolvePackageInner is the extracted body of runResolvePackage,
// returning an exit code instead of calling os.Exit so the DIR
// resolve happy path is exercisable in-process for astbridge counter
// assertions. Same behavior as the previous inline body —
// LoadPackageForNative produces FrontendRuns per file and leaves
// pf.File nil until a fallback (--show-scopes / Go-native resolver
// printing branch) calls EnsureFile lazily.
func runResolvePackageInner(dir string, flags cliFlags) int {
	pkg, err := resolve.LoadPackageForNativeWithTransform(dir, aiRepairSourceTransform(aiRepairPrefix("resolve"), os.Stderr, flags))
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty: %v\n", err)
		return 1
	}
	var res *resolve.PackageResult
	ensureGoResolve := func() *resolve.PackageResult {
		if res != nil {
			return res
		}
		// Go-native resolver reads pf.File directly; materialize
		// before calling so Run-only loaded files are lowered once.
		pkg.EnsureFiles()
		res = resolve.ResolvePackage(pkg, resolve.NewPrelude())
		return res
	}
	diags := []*diag.Diagnostic(nil)
	if nativeDiags, err := nativeResolvePackageDiagnostics(pkg); err == nil {
		diags = append(packageParseDiags(pkg), nativeDiags...)
	} else {
		r := ensureGoResolve()
		diags = r.Diags
	}
	printPackageDiags(pkg, diags, flags)
	nativeRowsUsed := false
	nativeErrored := false
	for _, f := range pkg.Files {
		rows, err := nativeResolvePackageRows(pkg, f.Path)
		if err != nil {
			// Only a hard error in the native row builder justifies
			// forcing the Go fallback (which would trigger
			// EnsureFiles and therefore per-file astbridge
			// lowerings). An empty row slice from a clean file is
			// legitimate output — declaration-only files genuinely
			// have no refs to print — and must NOT drag the rest of
			// the package through *ast.File.
			nativeErrored = true
			ensureGoResolve()
			if len(f.RefsByID) == 0 {
				continue
			}
			fmt.Printf("# %s\n", f.Path)
			printResolutionRefs(f.RefIdents, f.RefsByID)
			continue
		}
		if len(rows) == 0 {
			// No refs in this file — declaration-only, skip silently.
			continue
		}
		nativeRowsUsed = true
		fmt.Printf("# %s\n", f.Path)
		printNativeResolutionRows(rows)
	}
	if nativeErrored && !nativeRowsUsed {
		// Native row generation failed for every file we tried —
		// fall back to the Go resolver's Refs so the output is not
		// silently empty. EnsureFiles has already been called by
		// the loop above in this branch.
		for _, f := range pkg.Files {
			if len(f.RefsByID) == 0 {
				continue
			}
			fmt.Printf("# %s\n", f.Path)
			printResolutionRefs(f.RefIdents, f.RefsByID)
		}
	}
	if flags.showScopes {
		ensureGoResolve()
		// Package scope's children are the file scopes, which in turn
		// host fn / block / closure / match-arm scopes — the full
		// nested tree for every file in the package.
		printScopeTree(pkg.PkgScope)
	}
	if hasError(diags) {
		return 1
	}
	return 0
}

// printPackageDiags groups diagnostics by source file so each one is
// rendered with a formatter that has the right Source bytes. Diagnostics
// whose span is empty (no file context) fall back to a generic formatter
// built with whatever file we happen to scan first.
func printPackageDiags(pkg *resolve.Package, diags []*diag.Diagnostic, flags cliFlags) {
	if len(diags) == 0 {
		return
	}
	// Bucket diagnostics by their owning file. Diagnostics stamped with
	// d.File land directly; unstamped ones fall back to text-based
	// disambiguation (see pickFile). pickFile's result is written back
	// onto d.File so downstream consumers (JSON output, LSP, tooling)
	// see a stamped diagnostic regardless of which subsystem emitted it.
	byFile := make([][]*diag.Diagnostic, len(pkg.Files))
	var noCtx []*diag.Diagnostic
	for _, d := range diags {
		fi := pickFile(pkg, d)
		if fi < 0 {
			noCtx = append(noCtx, d)
			continue
		}
		if d.File == "" {
			d.File = pkg.Files[fi].Path
		}
		byFile[fi] = append(byFile[fi], d)
	}
	// Per-file rendering uses each file's own source for snippet accuracy,
	// but the summary is printed once across the whole package so a
	// multi-file failure doesn't spam `N error(s)` lines per file.
	pkgFlags := flags
	pkgFlags.suppressSummary = true
	for i, f := range pkg.Files {
		if len(byFile[i]) == 0 {
			continue
		}
		fmter := newFormatter(f.Path, f.Source, flags)
		printDiags(fmter, byFile[i], pkgFlags)
	}
	if len(noCtx) > 0 {
		fmter := &diag.Formatter{}
		printDiags(fmter, noCtx, pkgFlags)
	}
	printSummary(diags, flags)
}

// printSummary emits the `N error(s), M warning(s)` tail line and the
// optional explain block, using the same formatting rules as printDiags
// but without re-printing any diagnostics. Called once per package so
// multi-file failures don't emit a summary per file.
func printSummary(diags []*diag.Diagnostic, flags cliFlags) {
	if flags.jsonOutput {
		return
	}
	errs, warns := 0, 0
	for _, d := range diags {
		switch d.Severity {
		case diag.Error:
			errs++
		case diag.Warning:
			warns++
		}
	}
	if errs == 0 && warns == 0 {
		return
	}
	if flags.maxErrors > 0 && len(diags) > flags.maxErrors {
		fmt.Fprintf(os.Stderr, "  %d error(s), %d warning(s) (showing first %d)\n",
			errs, warns, flags.maxErrors)
	} else {
		fmt.Fprintf(os.Stderr, "  %d error(s), %d warning(s)\n", errs, warns)
	}
	if flags.explain {
		printExplainBlock(diags)
	}
}

// pickFile returns the index of the package file the diagnostic belongs
// to. A return of -1 means no file matched (unusual — typically only
// for synthetic diagnostics).
//
// Priority:
//  1. Honor d.File when a package walker has stamped it.
//  2. ParseDiags slice identity — parser diagnostics already carry a
//     pointer identity that ties them to their file.
//  3. Verify the primary span's position actually covers one of the
//     backticked identifiers in the message. Accept a file only when
//     the match is unique; ambiguous matches fall through so we never
//     guess wrong.
//  4. Fall back to the legacy "first file whose source is ≥ offset"
//     heuristic for diagnostics with no way to disambiguate.
func pickFile(pkg *resolve.Package, d *diag.Diagnostic) int {
	pos := d.PrimaryPos()
	if pos.Line == 0 {
		return -1
	}
	if d.File != "" {
		for i, f := range pkg.Files {
			if f.Path == d.File {
				return i
			}
		}
	}
	for i, f := range pkg.Files {
		for _, pd := range f.ParseDiags {
			if pd == d {
				return i
			}
		}
	}
	if idx := pickFileByName(pkg, pos, d.Message); idx >= 0 {
		return idx
	}
	for i, f := range pkg.Files {
		if pos.Offset <= len(f.Source) {
			return i
		}
	}
	return -1
}

// pickFileByName scans every backticked identifier in msg and checks
// whether the primary offset in each package file actually contains one
// of them. Returns the unique matching file index, or -1 when zero or
// more than one file matches (so the caller can fall back).
//
// Using ALL backticks (not just the first) handles messages like
// "cannot assign `Int` to `String`" where the offset may hit either
// side. Uniqueness protects against false positives when multiple
// files happen to have the same byte pattern at the same offset.
func pickFileByName(pkg *resolve.Package, pos token.Pos, msg string) int {
	names := backtickedIdents(msg)
	if len(names) == 0 {
		return -1
	}
	match := -1
	for i, f := range pkg.Files {
		if fileOffsetMatchesAny(f.Source, pos, names) {
			if match >= 0 {
				return -1
			}
			match = i
		}
	}
	return match
}

// backtickedIdents returns every `identifier` substring of msg, in
// left-to-right order. An unterminated backtick pair is ignored. Never
// returns duplicates — if the same name appears twice, it is kept once
// (we only care whether the bytes at the offset match any of them).
func backtickedIdents(msg string) []string {
	var out []string
	seen := map[string]bool{}
	rest := msg
	for {
		start := strings.IndexByte(rest, '`')
		if start < 0 {
			return out
		}
		rest = rest[start+1:]
		end := strings.IndexByte(rest, '`')
		if end < 0 {
			return out
		}
		name := rest[:end]
		rest = rest[end+1:]
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
}

// fileOffsetMatchesAny reports whether src starting at pos.Offset
// contains any of the candidate names.
func fileOffsetMatchesAny(src []byte, pos token.Pos, names []string) bool {
	if pos.Offset < 0 || pos.Offset >= len(src) {
		return false
	}
	tail := src[pos.Offset:]
	for _, n := range names {
		if len(n) == 0 || len(n) > len(tail) {
			continue
		}
		if string(tail[:len(n)]) == n {
			return true
		}
	}
	return false
}

// printResolutionRefs dumps one file's resolved identifiers. Broken out
// of printResolution so the package walker can print one header per file.
func printResolutionRefs(refIdents []*ast.Ident, refs map[ast.NodeID]*resolve.Symbol) {
	idents := make([]*ast.Ident, 0, len(refIdents))
	idents = append(idents, refIdents...)
	sort.Slice(idents, func(i, j int) bool {
		a, b := idents[i].PosV, idents[j].PosV
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Column < b.Column
	})
	for _, id := range idents {
		s := refs[id.ID]
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
	if flags.suppressSummary {
		return
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

// checkOpts builds the check.Opts every subcommand passes to the type
// checker. Sourcing from the cached registry keeps the stdlib Load
// cost paid once per process.
func checkOpts() check.Opts {
	reg := stdlib.LoadCached()
	return check.Opts{Stdlib: reg, Primitives: reg.Primitives, ResultMethods: reg.ResultMethods}
}

func checkOptsForSource(src []byte) check.Opts {
	opts := checkOpts()
	opts.Source = src
	return opts
}

// checkOptsForFile is checkOptsForSource plus the file path, so the
// checker can stamp d.File on diagnostics it emits. Used by the
// single-file CLI entry points (osty check FILE / osty typecheck FILE).
func checkOptsForFile(path string, src []byte) check.Opts {
	opts := checkOptsForSource(src)
	opts.Path = path
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

// printResolution writes a sorted table of every resolved identifier:
// `line:col  Name  Kind  def-pos`. Useful for sanity-checking the
// resolver's output without a debugger.
func printResolution(_ *ast.File, res *resolve.Result) {
	idents := make([]*ast.Ident, 0, len(res.RefIdents))
	idents = append(idents, res.RefIdents...)
	sort.Slice(idents, func(i, j int) bool {
		a, b := idents[i].PosV, idents[j].PosV
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Column < b.Column
	})
	for _, id := range idents {
		s := res.RefsByID[id.ID]
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
	fmt.Fprintln(os.Stderr, "       osty new [--bin|--lib|--workspace|--cli|--service] [--member NAME] NAME")
	fmt.Fprintln(os.Stderr, "       osty init [--bin|--lib|--workspace|--cli|--service] [--name NAME] [--member NAME]")
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
	fmt.Fprintln(os.Stderr, "       osty scaffold <fixture|schema|ffi> [flags] NAME")
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
	fmt.Fprintln(os.Stderr, "  --cli              scaffold a multi-file CLI app starter")
	fmt.Fprintln(os.Stderr, "  --service          scaffold a multi-file HTTP service starter")
	fmt.Fprintln(os.Stderr, "  --member NAME      workspace member directory name (default: core)")
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
// --check/--write/--airepair/--no-airepair, then exactly one file
// path.
//
// Exit codes match gofmt conventions:
//
//	0   formatting succeeded (or file was already formatted under --check)
//	1   formatting differences found under --check, OR unrecoverable I/O error
//	2   usage error (missing path, unknown flag, parse error, etc.)
func runFmt(args []string) {
	fs := flag.NewFlagSet("fmt", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty fmt [--check] [--write] [--airepair] [--no-airepair] FILE")
	}
	var checkMode, writeMode, noAIRepair bool
	repairMode := true
	// Accepted but ignored — historical alias for the formatter engine
	// selector. There is now only one engine (the self-host formatter).
	var engineUnused string
	fs.BoolVar(&checkMode, "check", false, "exit 1 if FILE is not already formatted")
	fs.BoolVar(&checkMode, "c", false, "alias for --check")
	fs.BoolVar(&writeMode, "write", false, "overwrite FILE in place")
	fs.BoolVar(&writeMode, "w", false, "alias for --write")
	fs.BoolVar(&repairMode, "airepair", true, "enable automatic AI repair before formatting")
	fs.BoolVar(&repairMode, "repair", true, "alias for --airepair")
	fs.BoolVar(&noAIRepair, "no-airepair", false, "disable automatic AI repair before formatting")
	fs.BoolVar(&noAIRepair, "no-repair", false, "alias for --no-airepair")
	fs.StringVar(&engineUnused, "engine", "", "deprecated; the only engine is the self-host formatter")
	_ = fs.Parse(args)
	_ = engineUnused
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
		result := airepair.Analyze(airepair.Request{
			Source:   src,
			Filename: path,
		})
		if result.Accepted {
			formatSrc = result.Repaired
			repairs = result.Repair
		}
	}
	parserCanonical, parserCanonicalChanged := parserCanonicalFmtSource(formatSrc)
	if parserCanonicalChanged {
		formatSrc = parserCanonical
	}
	var (
		out   []byte
		diags []*diag.Diagnostic
		ferr  error
	)
	if !repairMode && parserCanonicalChanged {
		out = formatSrc
	} else {
		out, diags, ferr = format.Source(formatSrc)
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

func parserCanonicalFmtSource(src []byte) ([]byte, bool) {
	parsed := parser.ParseDetailed(src)
	if parsed.File == nil || parsed.Provenance == nil || parsed.Provenance.Empty() {
		return src, false
	}
	canonicalSrc := canonical.Source(src, parsed.File)
	if len(canonicalSrc) == 0 || bytes.Equal(canonicalSrc, src) {
		return src, false
	}
	return canonicalSrc, true
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
	out, result, emitErr := emitGenArtifact(backendID, emitMode, pkgName, entry)
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
	if _, err := ws.LoadPackageArenaFirst(""); err != nil {
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
		RefsByID:      e.file.RefsByID,
		TypeRefsByID:  e.file.TypeRefsByID,
		RefIdents:     e.file.RefIdents,
		TypeRefIdents: e.file.TypeRefIdents,
		FileScope:     e.file.FileScope,
		Diags:         e.res.Diags,
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
