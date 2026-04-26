// lint_cmd.go — lint subcommand helpers (package/workspace/file modes,
// fix application, config loading, explain/list).

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/canonical"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/lint"
	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// runLintPackage runs the lint pass over every .osty file in dir as a
// single package so cross-file uses of `use` aliases and top-level
// declarations don't trigger false "unused" warnings. Workspace mode
// (dir-of-packages) runs lint per contained package via
// runLintWorkspace. Single-package path loads arena-first (Phase 1c.2)
// so parse goes through selfhost.Run; pf.File / canonical materialize
// lazily for the Go resolver + linter until lint moves onto the
// engine path in a later Phase 1c.5 slice.
func runLintPackage(dir string, flags cliFlags) {
	if isWorkspace(dir) {
		runLintWorkspace(dir, flags)
		return
	}
	pkg, err := resolve.LoadPackageArenaFirstWithTransform(dir, aiRepairSourceTransform(aiRepairPrefix("lint"), os.Stderr, flags))
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty: %v\n", err)
		os.Exit(1)
	}
	res := resolve.ResolvePackageDefault(pkg)
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
		pkg, err := ws.LoadPackageArenaFirst(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "osty: %v\n", err)
			anyErr = true
			return
		}
		res := resolve.ResolvePackageDefault(pkg)
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
	if flags.fix || flags.fixDryRun {
		applyPackageFixes(pkg, lr.Diags, flags)
	}
	return lintPackageOutcome{anyErr: hasError(all), anyWarn: hasWarning(all)}
}

// applyPackageFixes runs lint.ApplyFixes on each file in the package
// using only diagnostics stamped for that file, then either rewrites the
// files in place (--fix) or dumps a concatenated dry-run preview
// (--fix-dry-run). Emits a final summary line counting fixes applied
// and overlaps skipped across the whole package.
//
// Diagnostics whose File is empty are attached to the package's first
// file — package-mode lint.Package does stamp File on every lint diag,
// but the fallback keeps this robust against downstream regressions.
func applyPackageFixes(pkg *resolve.Package, diags []*diag.Diagnostic, flags cliFlags) {
	if pkg == nil || len(pkg.Files) == 0 {
		return
	}
	byFile := map[string][]*diag.Diagnostic{}
	for _, d := range diags {
		path := d.File
		if path == "" {
			path = pkg.Files[0].Path
		}
		byFile[path] = append(byFile[path], d)
	}
	totalApplied, totalSkipped := 0, 0
	mode := "osty lint"
	for _, f := range pkg.Files {
		ds := byFile[f.Path]
		if len(ds) == 0 {
			continue
		}
		newSrc, applied, skipped := lint.ApplyFixes(f.Source, ds)
		totalApplied += applied
		totalSkipped += skipped
		switch {
		case flags.fixDryRun:
			if applied == 0 {
				continue
			}

			fmt.Fprintf(os.Stdout, "// ==== %s ====\n", f.Path)
			if _, err := os.Stdout.Write(newSrc); err != nil {
				fmt.Fprintf(os.Stderr, "%s --fix-dry-run: %v\n", mode, err)
				os.Exit(1)
			}
			if len(newSrc) > 0 && newSrc[len(newSrc)-1] != '\n' {
				fmt.Fprintln(os.Stdout)
			}
		case flags.fix:
			if applied == 0 {
				continue
			}
			if err := os.WriteFile(f.Path, newSrc, 0o644); err != nil {
				fmt.Fprintf(os.Stderr, "%s --fix: %v\n", mode, err)
				os.Exit(1)
			}
		}
	}
	switch {
	case flags.fixDryRun:
		fmt.Fprintf(os.Stderr, "%s --fix-dry-run: %d fix(es) would apply, %d overlap(s) would be skipped\n", mode, totalApplied, totalSkipped)
	case flags.fix:
		fmt.Fprintf(os.Stderr, "%s --fix: applied %d fix(es), skipped %d overlap(s)\n", mode, totalApplied, totalSkipped)
	}
}

// runLintFile is the extracted `osty lint FILE` body (minus the
// exclude-config early-return, which stays in the caller so the "skip"
// message fires before parse work begins). Runs parse → resolveFile →
// check.SelfhostFile → lint engine; handles --fix / --fix-dry-run
// stdout/disk side-effects inside the function.
//
// Uses check.SelfhostFile (not check.File) since Phase 1c.5 — lint on
// a single file never exercises the AST-level builder-desugar rewrite
// (desugar runs on check.Package for multi-file auto-derive chains),
// so the selfhost-direct entry shaves that pass without altering the
// `*check.Result` shape the lint engine consumes.
func runLintFile(path string, src []byte, formatter *diag.Formatter, flags cliFlags, lintCfg lint.Config, lintCfgOk bool) int {
	parsed := parser.ParseDetailed(src)
	file, parseDiags := parsed.File, parsed.Diagnostics
	res := resolveFile(file)
	chk := check.SelfhostFile(file, res, checkOptsForFile(path, canonical.Source(src, file)))
	lr := runLintEngine(file, src, res, chk)
	if lintCfgOk {
		lr = lintCfg.Apply(lr)
	}
	all := append(append(append([]*diag.Diagnostic{}, parseDiags...), res.Diags...), chk.Diags...)
	all = append(all, lr.Diags...)
	printDiags(formatter, all, flags)
	if flags.fix || flags.fixDryRun {
		newSrc, applied, skipped := lint.ApplyFixes(src, lr.Diags)
		mode := "osty lint"
		switch {
		case flags.fixDryRun:

			if _, err := os.Stdout.Write(newSrc); err != nil {
				fmt.Fprintf(os.Stderr, "%s --fix-dry-run: %v\n", mode, err)
				return 1
			}
			fmt.Fprintf(os.Stderr, "%s --fix-dry-run: %d fix(es) would apply, %d overlap(s) would be skipped\n", mode, applied, skipped)
		case flags.fix:
			if applied > 0 {
				if err := os.WriteFile(path, newSrc, 0o644); err != nil {
					fmt.Fprintf(os.Stderr, "%s --fix: %v\n", mode, err)
					return 1
				}
			}
			fmt.Fprintf(os.Stderr, "%s --fix: applied %d fix(es), skipped %d overlap(s)\n", mode, applied, skipped)
		}
	}
	if hasError(all) || (flags.strict && hasWarning(all)) {
		return 1
	}
	return 0
}
func runLintEngine(file *ast.File, src []byte, res *resolve.Result, chk *check.Result) *lint.Result {
	return lint.File(file, src, res, chk)
}

// loadLintConfigWithBase walks up from the target path collecting
// every `osty.toml` [lint] section and merging them hierarchically:
// the closest (child) config overrides Allow/Deny from parent
// configs further up the tree, while Exclude patterns from all
// levels are unioned. It also returns the manifest directory of the
// closest osty.toml (even when it has no [lint] section) so callers
// can resolve Exclude globs against the project root.
func loadLintConfigWithBase(startPath string) (lint.Config, string, bool) {
	dir := startPath
	if info, err := os.Stat(startPath); err == nil && !info.IsDir() {
		dir = filepath.Dir(startPath)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return lint.Config{}, "", false
	}
	var cfg lint.Config
	base := ""
	found := false
	for {
		candidate := filepath.Join(abs, manifest.ManifestFile)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			if base == "" {

				base = abs
			}
			raw, err := os.ReadFile(candidate)
			if err != nil {
				break
			}
			m, err := manifest.Parse(raw)
			if err != nil {
				break
			}
			if m.Lint != nil {
				layer := lint.Config{
					Allow:   m.Lint.Allow,
					Deny:    m.Lint.Deny,
					Exclude: m.Lint.Exclude,
				}
				if !found {
					cfg = lint.Config{}.Merge(layer)
					found = true
				} else {

					cfg = cfg.Merge(layer)
				}
			}
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			break
		}
		abs = parent
	}
	return cfg, base, found
}

// loadLintConfigNear returns the merged [lint] configuration for the
// file or directory at startPath by walking up the directory tree and
// merging every ancestor osty.toml. Exclude is not returned because
// the caller does not have a base directory for glob resolution;
// use loadLintConfigWithBase when Exclude is needed.
func loadLintConfigNear(startPath string) (lint.Config, bool) {
	cfg, _, ok := loadLintConfigWithBase(startPath)
	cfg.Exclude = nil
	return cfg, ok
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
