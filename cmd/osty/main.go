// Command osty is the CLI entry point for the Osty toolchain.
//
// Subcommands:
//
//	osty new <name>            Scaffold a new project directory (spec §13.1).
//	osty parse <file.osty>     Parse a file and print the AST as JSON.
//	osty tokens <file.osty>    Lex a file and print the token stream.
//	osty resolve <file.osty>   Lex+parse+name-resolve; print resolved refs.
//	osty check <file.osty>     Lex+parse+resolve+type-check diagnostics only.
//	osty typecheck <file.osty> Alias of `check` that also prints the inferred
//	                           type of each expression (debugging aid).
//	osty fmt <file.osty>       Format source to canonical style; see -check/-write.
//	osty gen <file.osty>       Transpile to Go (prints to stdout; -o writes to file).
//	osty doc <path>            Emit markdown API docs for a file or package.
//	osty lsp                   Run the Language Server Protocol server on stdio.
//	osty explain [CODE]        Describe a diagnostic code; with no arg, list every code.
//	osty pipeline <file.osty>  Run every front-end phase and print per-stage timing.
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
//	--write        overwrite the file in place instead of printing to stdout
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/format"
	"github.com/osty/osty/internal/gen"
	"github.com/osty/osty/internal/lexer"
	"github.com/osty/osty/internal/lint"
	"github.com/osty/osty/internal/lsp"
	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/pipeline"
	"github.com/osty/osty/internal/resolve"
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
	// sense in that subcommand. Every other subcommand takes exactly
	// one file path as its second positional arg.
	if cmd == "fmt" {
		runFmt(args[1:])
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
	// build is the manifest-driven front-end over a directory. When
	// passed a directory it loads osty.toml, validates, and runs
	// check + lint across the package(s) the manifest describes.
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
		// Allow `osty lint --fix FILE` and `osty lint --strict FILE`
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
		if rest, present := takeBoolFlag(args[1:], "--strict"); present {
			flags.strict = true
			args = append([]string{"lint"}, rest...)
		}
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
	// run builds the project (via gen Phase 1) and executes the
	// produced Go program; test walks *_test.osty files; publish packs
	// a tarball and uploads it to a configured registry.
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

	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty: %v\n", err)
		os.Exit(1)
	}
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
		file, diags := parser.ParseDiagnostics(src)
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
		file, diags := parser.ParseDiagnostics(src)
		res := resolveFile(file)
		chk := check.File(file, res, checkOpts())
		all := append(append(append([]*diag.Diagnostic{}, diags...), res.Diags...), chk.Diags...)
		printDiags(formatter, all, flags)
		if hasError(all) {
			os.Exit(1)
		}
	case "typecheck":
		file, diags := parser.ParseDiagnostics(src)
		res := resolveFile(file)
		chk := check.File(file, res, checkOpts())
		all := append(append(append([]*diag.Diagnostic{}, diags...), res.Diags...), chk.Diags...)
		printDiags(formatter, all, flags)
		printTypes(chk)
		if hasError(all) {
			os.Exit(1)
		}
	case "resolve":
		file, parseDiags := parser.ParseDiagnostics(src)
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
		file, parseDiags := parser.ParseDiagnostics(src)
		res := resolveFile(file)
		chk := check.File(file, res, checkOpts())
		lr := lint.File(file, res, chk)
		if cfg, ok := loadLintConfigNear(path); ok {
			lr = cfg.Apply(lr)
		}
		all := append(append(append([]*diag.Diagnostic{}, parseDiags...), res.Diags...), chk.Diags...)
		all = append(all, lr.Diags...)
		printDiags(formatter, all, flags)
		if flags.fix || flags.fixDryRun {
			newSrc, applied, skipped := lint.ApplyFixes(src, lr.Diags)
			switch {
			case flags.fixDryRun:
				// Write the would-be-applied source to stdout so users
				// can pipe it through `diff` / `less` before committing
				// to a real --fix pass. The file on disk is untouched.
				if _, err := os.Stdout.Write(newSrc); err != nil {
					fmt.Fprintf(os.Stderr, "osty lint --fix-dry-run: %v\n", err)
					os.Exit(1)
				}
				fmt.Fprintf(os.Stderr, "osty lint --fix-dry-run: %d fix(es) would apply, %d overlap(s) would be skipped\n", applied, skipped)
			case flags.fix:
				if applied > 0 {
					if err := os.WriteFile(path, newSrc, 0o644); err != nil {
						fmt.Fprintf(os.Stderr, "osty lint --fix: %v\n", err)
						os.Exit(1)
					}
				}
				fmt.Fprintf(os.Stderr, "osty lint --fix: applied %d fix(es), skipped %d overlap(s)\n", applied, skipped)
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
	pkg, err := resolve.LoadPackage(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty: %v\n", err)
		os.Exit(1)
	}
	res := resolve.ResolvePackage(pkg, resolve.NewPrelude())
	chk := check.Package(pkg, res, checkOpts())
	diags := append(append([]*diag.Diagnostic{}, res.Diags...), chk.Diags...)
	printPackageDiags(pkg, diags, flags)
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
		if cr, ok := checks[p]; ok && cr != nil {
			diags = append(diags, cr.Diags...)
		}
		printPackageDiags(pkg, diags, flags)
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
	pkg, err := resolve.LoadPackage(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty: %v\n", err)
		os.Exit(1)
	}
	res := resolve.ResolvePackage(pkg, resolve.NewPrelude())
	chk := check.Package(pkg, res, checkOpts())
	lr := lint.Package(pkg, res, chk)
	if cfg, ok := loadLintConfigNear(dir); ok {
		lr = cfg.Apply(lr)
	}
	all := append(append(append([]*diag.Diagnostic{}, res.Diags...), chk.Diags...), lr.Diags...)
	printPackageDiags(pkg, all, flags)
	if hasError(all) || (flags.strict && hasWarning(all)) {
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
		lr := lint.Package(pkg, res, chk)
		if cfg, ok := loadLintConfigNear(pkg.Dir); ok {
			lr = cfg.Apply(lr)
		}
		all := append(append(append([]*diag.Diagnostic{}, res.Diags...), chk.Diags...), lr.Diags...)
		printPackageDiags(pkg, all, flags)
		if hasError(all) {
			anyErr = true
		}
		if hasWarning(all) {
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

// runResolvePackage is runCheckPackage plus a resolution dump per file.
func runResolvePackage(dir string, flags cliFlags) {
	pkg, err := resolve.LoadPackage(dir)
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

// checkOpts builds the check.Opts every subcommand passes to the type
// checker. Sourcing from the cached registry keeps the stdlib Load
// cost paid once per process.
func checkOpts() check.Opts {
	return check.Opts{Primitives: stdlib.LoadCached().Primitives}
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
	fmt.Fprintln(os.Stderr, "usage: osty [flags] (parse|tokens|resolve|check|typecheck|lint|fmt|gen) FILE")
	fmt.Fprintln(os.Stderr, "       osty new [--lib] NAME     (scaffold a new project)")
	fmt.Fprintln(os.Stderr, "       osty init [--lib]         (scaffold into the current directory)")
	fmt.Fprintln(os.Stderr, "       osty build [DIR]          (manifest-driven front end over a project)")
	fmt.Fprintln(os.Stderr, "       osty add NAME[@VER]       (add a dependency; also --path, --git)")
	fmt.Fprintln(os.Stderr, "       osty update [NAME...]     (refresh osty.lock)")
	fmt.Fprintln(os.Stderr, "       osty run [-- ARGS...]     (build + exec the project's binary)")
	fmt.Fprintln(os.Stderr, "       osty test [PATH|FILTER...] (discover *_test.osty; report tests found)")
	fmt.Fprintln(os.Stderr, "       osty publish              (pack + upload the package to a registry)")
	fmt.Fprintln(os.Stderr, "       osty search QUERY         (search the registry for packages)")
	fmt.Fprintln(os.Stderr, "       osty yank --version V [PKG]   (mark a published version as yanked)")
	fmt.Fprintln(os.Stderr, "       osty unyank --version V [PKG] (un-yank a previously yanked version)")
	fmt.Fprintln(os.Stderr, "       osty login [--registry N] (store an API token for publish/yank)")
	fmt.Fprintln(os.Stderr, "       osty logout [--registry N|--all] (forget a stored token)")
	fmt.Fprintln(os.Stderr, "       osty remove NAME [NAME...] (drop a dep from osty.toml; alias rm)")
	fmt.Fprintln(os.Stderr, "       osty doc [--format FMT] [--out PATH] PATH (generate API docs; markdown or html)")
	fmt.Fprintln(os.Stderr, "       osty ci [flags] [PATH]    (run the CI check bundle: fmt+lint+policy+lockfile)")
	fmt.Fprintln(os.Stderr, "       osty ci snapshot [-o OUT] (capture the exported API for future semver diffing)")
	fmt.Fprintln(os.Stderr, "       osty profiles             (list build profiles — debug, release, ...)")
	fmt.Fprintln(os.Stderr, "       osty targets              (list declared cross-compilation targets)")
	fmt.Fprintln(os.Stderr, "       osty features             (list declared opt-in features)")
	fmt.Fprintln(os.Stderr, "       osty cache [ls|clean|info] (inspect / prune the build cache)")
	fmt.Fprintln(os.Stderr, "       osty lsp                  (language server on stdio)")
	fmt.Fprintln(os.Stderr, "       osty explain [CODE]       (describe a diagnostic code; no arg lists every code)")
	fmt.Fprintln(os.Stderr, "       osty pipeline FILE|DIR    (run every front-end phase; per-stage timing)")
	fmt.Fprintln(os.Stderr, "flags:")
	fmt.Fprintln(os.Stderr, "  --no-color         disable ANSI escapes")
	fmt.Fprintln(os.Stderr, "  --color            force ANSI escapes")
	fmt.Fprintln(os.Stderr, "  --max-errors N     show only the first N diagnostics")
	fmt.Fprintln(os.Stderr, "  --json             emit diagnostics as NDJSON")
	fmt.Fprintln(os.Stderr, "  --strict           lint: exit 1 on warnings (CI mode)")
	fmt.Fprintln(os.Stderr, "  --scopes           resolve: also print the nested scope tree")
	fmt.Fprintln(os.Stderr, "  --trace            stream per-phase timing to stderr (front-end commands)")
	fmt.Fprintln(os.Stderr, "  --explain          append `osty explain CODE` text after each diagnostic block")
	fmt.Fprintln(os.Stderr, "fmt-specific flags (after the subcommand):")
	fmt.Fprintln(os.Stderr, "  --check            exit 1 if FILE is not already formatted")
	fmt.Fprintln(os.Stderr, "  --write            overwrite FILE in place")
	fmt.Fprintln(os.Stderr, "gen-specific flags (after the subcommand):")
	fmt.Fprintln(os.Stderr, "  -o PATH            write Go source to PATH instead of stdout")
	fmt.Fprintln(os.Stderr, "  --package NAME     Go package clause (default: main)")
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
	fmt.Fprintln(os.Stderr, "build / run flags:")
	fmt.Fprintln(os.Stderr, "  --profile NAME     build profile (debug, release, profile, test, ...)")
	fmt.Fprintln(os.Stderr, "  --release          shorthand for --profile release")
	fmt.Fprintln(os.Stderr, "  --target TRIPLE    cross-compilation target (e.g. amd64-linux)")
	fmt.Fprintln(os.Stderr, "  --features LIST    comma-separated feature flags to enable")
	fmt.Fprintln(os.Stderr, "  --no-default-features  drop the manifest's [features].default set")
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
// --check/--write, then exactly one file path.
//
// Exit codes match gofmt conventions:
//
//	0   formatting succeeded (or file was already formatted under --check)
//	1   formatting differences found under --check, OR unrecoverable I/O error
//	2   usage error (missing path, unknown flag, parse error, etc.)
func runFmt(args []string) {
	fs := flag.NewFlagSet("fmt", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty fmt [--check] [--write] FILE")
	}
	var checkMode, writeMode bool
	fs.BoolVar(&checkMode, "check", false, "exit 1 if FILE is not already formatted")
	fs.BoolVar(&checkMode, "c", false, "alias for --check")
	fs.BoolVar(&writeMode, "write", false, "overwrite FILE in place")
	fs.BoolVar(&writeMode, "w", false, "alias for --write")
	_ = fs.Parse(args)
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
	out, diags, ferr := format.Source(src)
	if ferr != nil {
		// Render parse diagnostics so the user can fix them.
		formatter := &diag.Formatter{Filename: path, Source: src}
		if len(diags) > 0 {
			fmt.Fprintln(os.Stderr, formatter.FormatAll(diags))
		}
		fmt.Fprintf(os.Stderr, "osty fmt: %v\n", ferr)
		os.Exit(2)
	}

	if checkMode {
		if !bytes.Equal(src, out) {
			fmt.Fprintf(os.Stderr, "%s: not formatted\n", path)
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

// runGen implements the `osty gen` subcommand: transpile a single
// .osty file to Go and either print the result to stdout or write it
// to the path given by --out/-o.
//
// Exit codes:
//
//	0   transpilation succeeded
//	1   unrecoverable I/O error, or transpile returned an error even
//	    after partial output
//	2   usage error (missing path, unknown flag), or parse/resolve/check
//	    failures that would produce garbage Go
func runGen(args []string, flags cliFlags) {
	fs := flag.NewFlagSet("gen", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty gen [-o OUT.go] FILE.osty")
	}
	var outPath string
	var pkgName string
	fs.StringVar(&outPath, "o", "", "write Go source to this file instead of stdout")
	fs.StringVar(&outPath, "out", "", "alias for -o")
	fs.StringVar(&pkgName, "package", "main", "Go package clause (default: main)")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}
	path := fs.Arg(0)
	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty: %v\n", err)
		os.Exit(1)
	}

	// Emitting Go from a broken AST would cascade nonsense, so abort on
	// any front-end error before calling into gen.
	file, parseDiags := parser.ParseDiagnostics(src)
	res := resolveFile(file)
	chk := check.File(file, res, checkOpts())
	allDiags := append(append(append([]*diag.Diagnostic{}, parseDiags...), res.Diags...), chk.Diags...)
	if hasError(allDiags) {
		printDiags(newFormatter(path, src, flags), allDiags, flags)
		fmt.Fprintf(os.Stderr, "osty gen: front-end errors prevent transpilation\n")
		os.Exit(2)
	}

	goSrc, gerr := gen.Generate(pkgName, file, res, chk)
	if gerr != nil {
		// goSrc may still contain useful partial output; emit a warning
		// but also write the source so the user can inspect it.
		fmt.Fprintf(os.Stderr, "osty gen: %v\n", gerr)
	}

	if outPath != "" {
		if err := os.WriteFile(outPath, goSrc, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "osty gen: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if _, err := os.Stdout.Write(goSrc); err != nil {
		fmt.Fprintf(os.Stderr, "osty gen: %v\n", err)
		os.Exit(1)
	}
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

// scaffoldExitCode maps a scaffold diagnostic code to a process exit
// code. Usage errors (bad name, conflicting flags) exit 2; I/O
// failures and pre-existing destinations exit 1.
func scaffoldExitCode(d *diag.Diagnostic) int {
	if d == nil {
		return 0
	}
	switch d.Code {
	case diag.CodeScaffoldInvalidName:
		return 2
	default:
		return 1
	}
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
