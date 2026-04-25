// native_check.go — self-host native check/workspace pipeline helpers.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/selfhost"
)

// runCheckPackage runs lex + parse + resolve over dir. Two modes:
//
//   - **Single-package**: dir contains `.osty` files directly. Loaded
//     via resolve.LoadPackageArenaFirst.
//   - **Workspace**: dir has no top-level `.osty` files but one or
//     more subdirectories do. The whole tree is loaded via Workspace
//     so cross-package `use` declarations resolve.
//
// Always routes through the self-host native path since Phase 1c.5
// retired the Go-hosted legacy alternative. Diagnostics are rendered
// with each file's own formatter so source snippets point at the right
// lines even when spanning packages.
func runCheckPackage(dir string, flags cliFlags) {
	if flags.inspect {
		fmt.Fprintf(os.Stderr, "osty: --inspect is not supported on the self-host check path\n")
		os.Exit(2)
	}
	if root, ok, abort := nativeWorkspaceRoot(dir, flags); abort {
		os.Exit(2)
	} else if ok {
		if runCheckWorkspaceNative(root, flags) != 0 {
			os.Exit(1)
		}
		return
	}
	if runCheckPackageNative(dir, flags) != 0 {
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
func nativeWorkspaceRoot(dir string, flags cliFlags) (string, bool, bool) {
	if _, _, err := manifestLookupNear(dir); err == nil {
		m, root, abort := loadManifestWithDiag(dir, flags)
		if abort {
			return root, false, true
		}
		if m != nil && m.Workspace != nil {
			return root, true, false
		}
	}
	if isWorkspace(dir) {
		return dir, true, false
	}
	return "", false, false
}

// runResolveFile is the extracted body of `osty resolve FILE`. It
// drives the single-file resolve happy path entirely through the
// self-host arena (selfhost.ResolveFromSource); the astbridge
// *ast.File is only materialized lazily via parser.ParseDiagnostics
// when a fallback needs it (printResolution with no native rows, or
// --show-scopes). Returns the subcommand's exit code: 0 on clean
// input, 1 when any error-severity diagnostic surfaces. Extracting
// this keeps the subcommand body testable in-process so the
// astbridge counter can pin the end-to-end CLI
// invariant (not just the library primitives).
// runTypecheckPackageNative is the DIR sibling of
// runTypecheckFileNative and the typecheck sibling of
// runCheckPackageNative. After the package check runs astbridge-free
// over the merged arena, it emits a per-file type dump with each
// file's header so `osty typecheck --native DIR` output stays
// readable for packages with more than one source file. Returns the
// subcommand exit code.
func runTypecheckPackageNative(dir string, flags cliFlags) int {
	pkg, err := resolve.LoadPackageForNativeWithTransform(dir, aiRepairSourceTransform(aiRepairPrefix("typecheck"), os.Stderr, flags))
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty: %v\n", err)
		return 1
	}
	input := nativePackageCheckInput(pkg, nil)
	checked, err := selfhost.CheckPackageStructured(input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty: native check: %v\n", err)
		return 1
	}
	diags := packageParseDiags(pkg)
	diags = append(diags, nativePackageCheckDiags(checked.Diagnostics, input.Files)...)
	printPackageDiags(pkg, diags, flags)
	printNativePackageTypes(checked, input.Files)
	if flags.dumpNativeDiags {
		dumpNativeDiagsForSummary(dir, checked.Summary)
	}
	if hasError(diags) {
		return 1
	}
	return 0
}
func runTypecheckWorkspaceNative(dir string, flags cliFlags) int {
	return runNativeWorkspaceCheck(dir, "typecheck", flags, true)
}

// printNativePackageTypes is the DIR renderer for --native typecheck:
// buckets TypedNodes by owning file (same findOwningFile walker used
// for diagnostics), relativizes byte offsets, and emits a
// `# <path>` header followed by the single-file printNativeTypes
// rows for each file that has at least one typed node. Files with
// no typed nodes are silently skipped so the dump stays compact.
func printNativePackageTypes(result selfhost.CheckResult, files []selfhost.PackageCheckFile) {
	if len(result.TypedNodes) == 0 || len(files) == 0 {
		return
	}
	buckets := make(map[int][]selfhost.CheckedNode, len(files))
	for _, n := range result.TypedNodes {
		if n.Type == nil {
			continue
		}
		idx := findOwningFile(files, n.Start)
		if idx < 0 {
			continue
		}
		rel := n
		rel.Start -= files[idx].Base
		rel.End -= files[idx].Base
		if rel.Start < 0 {
			rel.Start = 0
		}
		if rel.End < rel.Start {
			rel.End = rel.Start
		}
		buckets[idx] = append(buckets[idx], rel)
	}
	for i, f := range files {
		bucket, ok := buckets[i]
		if !ok || len(bucket) == 0 {
			continue
		}
		fmt.Printf("# %s\n", f.Path)
		printNativeTypes(f.Source, selfhost.CheckResult{TypedNodes: bucket})
	}
}

// runCheckPackageNative is the DIR sibling of runCheckFileNative.
// Loads the package via LoadPackageForNative (no eager *ast.File
// materialization), runs selfhost.CheckPackageStructured over the
// arena, and converts the resulting CheckDiagnosticRecord slice into
// per-file *diag.Diagnostic values so printPackageDiags keeps its
// file-bucketed rendering. Returns the subcommand exit code.
func runCheckPackageNative(dir string, flags cliFlags) int {
	pkg, err := resolve.LoadPackageForNativeWithTransform(dir, aiRepairSourceTransform(aiRepairPrefix("check"), os.Stderr, flags))
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty: %v\n", err)
		return 1
	}
	input := nativePackageCheckInput(pkg, nil)
	checked, err := selfhost.CheckPackageStructured(input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty: native check: %v\n", err)
		return 1
	}
	diags := packageParseDiags(pkg)
	diags = append(diags, nativePackageCheckDiags(checked.Diagnostics, input.Files)...)
	printPackageDiags(pkg, diags, flags)
	if flags.dumpNativeDiags {
		dumpNativeDiagsForSummary(dir, checked.Summary)
	}
	if hasError(diags) {
		return 1
	}
	return 0
}
func runCheckWorkspaceNative(dir string, flags cliFlags) int {
	return runNativeWorkspaceCheck(dir, "check", flags, false)
}
func runNativeWorkspaceCheck(dir, mode string, flags cliFlags, emitTypes bool) int {
	ws, err := loadNativeWorkspace(dir, mode, flags)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty: %v\n", err)
		return 1
	}
	anyErr := false
	for _, path := range nativeWorkspacePaths(ws) {
		pkg := ws.Packages[path]
		if pkg == nil {
			continue
		}
		input := nativePackageCheckInput(pkg, check.PackageImportSurfacesForSelfhost(pkg, ws, ws.Stdlib))
		checked, err := selfhost.CheckPackageStructured(input)
		if err != nil {
			fmt.Fprintf(os.Stderr, "osty: native check: %v\n", err)
			return 1
		}
		diags := packageParseDiags(pkg)
		diags = append(diags, nativePackageCheckDiags(checked.Diagnostics, input.Files)...)
		printPackageDiags(pkg, diags, flags)
		if emitTypes {
			printNativePackageTypes(checked, input.Files)
		}
		if flags.dumpNativeDiags {
			dumpNativeDiagsForSummary(path, checked.Summary)
		}
		if hasError(diags) {
			anyErr = true
		}
	}
	if anyErr {
		return 1
	}
	return 0
}
func loadNativeWorkspace(dir, mode string, flags cliFlags) (*resolve.Workspace, error) {
	ws, err := resolve.NewWorkspace(dir)
	if err != nil {
		return nil, err
	}
	ws.SourceTransform = aiRepairSourceTransform(aiRepairPrefix(mode), os.Stderr, flags)
	ws.Stdlib = nativeLazyStdlibProvider{}
	for _, p := range resolve.WorkspacePackagePaths(dir) {
		if _, err := ws.LoadPackageNative(p); err != nil {
			return nil, err
		}
	}
	return ws, nil
}

type nativeLazyStdlibProvider struct{}

func nativeWorkspacePaths(ws *resolve.Workspace) []string {
	if ws == nil {
		return nil
	}
	paths := make([]string, 0, len(ws.Packages))
	for path := range ws.Packages {
		if strings.HasPrefix(path, resolve.StdPrefix) {
			continue
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}
func nativePackageCheckInput(pkg *resolve.Package, imports []selfhost.PackageCheckImport) selfhost.PackageCheckInput {
	input := selfhost.PackageCheckInput{
		Files: make([]selfhost.PackageCheckFile, 0, len(pkg.Files)),
	}
	if len(imports) == 0 {
		imports = check.PackageImportSurfacesForSelfhost(pkg, nil, nativeLazyStdlibProvider{})
	}
	if len(imports) > 0 {
		input.Imports = append([]selfhost.PackageCheckImport(nil), imports...)
	}
	base := 0
	for _, pf := range pkg.Files {
		if pf == nil {
			continue
		}
		src := pf.CheckerSource()
		if len(src) == 0 {
			continue
		}
		name := filepath.Base(pf.Path)
		input.Files = append(input.Files, selfhost.PackageCheckFile{
			Source: append([]byte(nil), src...),
			Base:   base,
			Name:   name,
			Path:   pf.Path,
		})
		base += len(src) + 1
	}
	return input
}

// nativePackageCheckDiags buckets records by owning file (using the
// layout bases CheckPackageStructured emitted against), relativizes
// their offsets into each file's own source, and runs the per-file
// converter so line/column numbers print against the right file —
// not the concatenated bundle. Records that don't land inside any
// file range (should not happen for well-formed output) are dropped
// with their bundle offsets preserved as a defensive fallback.
func nativePackageCheckDiags(records []selfhost.CheckDiagnosticRecord, files []selfhost.PackageCheckFile) []*diag.Diagnostic {
	if len(records) == 0 || len(files) == 0 {
		return nil
	}
	buckets := make(map[int][]selfhost.CheckDiagnosticRecord, len(files))
	for _, rec := range records {
		idx := findOwningFile(files, rec.Start)
		if idx < 0 {
			continue
		}
		rel := rec
		rel.File = files[idx].Path
		rel.Start -= files[idx].Base
		rel.End -= files[idx].Base
		if rel.Start < 0 {
			rel.Start = 0
		}
		if rel.End < rel.Start {
			rel.End = rel.Start
		}
		buckets[idx] = append(buckets[idx], rel)
	}
	out := make([]*diag.Diagnostic, 0, len(records))
	for i := range files {
		bucket, ok := buckets[i]
		if !ok {
			continue
		}
		out = append(out, selfhost.CheckDiagnosticsAsDiag(files[i].Source, bucket)...)
	}
	return out
}
func findOwningFile(files []selfhost.PackageCheckFile, offset int) int {
	for i, f := range files {
		end := f.Base + len(f.Source)
		if offset >= f.Base && offset <= end {
			return i
		}
	}
	return -1
}

// runTypecheckFileNative is runCheckFileNative plus the type dump.
// After the check runs on the arena, prints every typed-node range
// selfhost.CheckResult.TypedNodes recorded — one row per node with
// line/column span and the inferred type. Zero astbridge lowerings
// on the happy path (same counter invariant as runCheckFileNative).
func runTypecheckFileNative(path string, src []byte, formatter *diag.Formatter, flags cliFlags) int {
	parseDiags, checked := selfhost.CheckFromSource(src)
	checkDiags := selfhost.CheckDiagnosticsAsDiag(src, checked.Diagnostics)
	for _, d := range checkDiags {
		if d != nil && d.File == "" {
			d.File = path
		}
	}
	all := append([]*diag.Diagnostic{}, parseDiags...)
	all = append(all, checkDiags...)
	printDiags(formatter, all, flags)
	printNativeTypes(src, checked)
	if flags.dumpNativeDiags {
		dumpNativeDiagsForSummary(path, checked.Summary)
	}
	if hasError(all) {
		return 1
	}
	return 0
}

// printNativeTypes is the --native sibling of printTypes: it renders
// selfhost.CheckResult.TypedNodes (node.Start, node.End are byte
// offsets produced by the native checker) as
// `line:col-line:col\tType` rows sorted by start position. Rows
// with nil Type (the checker's signal-only placeholders) are
// dropped so output stays compact.
func printNativeTypes(src []byte, result selfhost.CheckResult) {
	type row struct {
		start, end int
		text       string
	}
	rows := make([]row, 0, len(result.TypedNodes))
	for _, n := range result.TypedNodes {
		if n.Type == nil {
			continue
		}
		rows = append(rows, row{start: n.Start, end: n.End, text: n.Type.String()})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].start != rows[j].start {
			return rows[i].start < rows[j].start
		}
		return rows[i].end < rows[j].end
	})
	for _, r := range rows {
		sl, sc := byteOffsetLineCol(src, r.start)
		el, ec := byteOffsetLineCol(src, r.end)
		fmt.Printf("%d:%d-%d:%d\t%s\n", sl, sc, el, ec, r.text)
	}
}

// byteOffsetLineCol is a single-pass scan equivalent of the
// positionAtOffset helper that lives inside selfhost for the
// diagnostic converter. Promoted to cmd/osty because the typed-node
// renderer needs it inline — keep the two scanners in sync with
// internal/selfhost/check_diag_convert.go:positionAtOffsetForDiag.
func byteOffsetLineCol(src []byte, offset int) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if offset > len(src) {
		offset = len(src)
	}
	line := 1
	col := 1
	for i := 0; i < offset; i++ {
		if src[i] == '\n' {
			line++
			col = 1
			continue
		}
		col++
	}
	return line, col
}

// runCheckFileNative drives `osty check --native FILE` end-to-end on
// the self-host arena pipeline: selfhost.CheckFromSource parses once
// and runs the native checker directly on the arena (arena-direct
// gate included), and selfhost.CheckDiagnosticsAsDiag lifts the
// structured records into the CLI's usual diag.Diagnostic shape.
// Zero astbridge lowerings on the happy path — the counter stays at
// 0 throughout, pinned by TestRunCheckFileNativeIsAstbridgeFree.
// Returns the
// subcommand's exit code (0 clean / 1 on any error-severity
// diagnostic).
func runCheckFileNative(path string, src []byte, formatter *diag.Formatter, flags cliFlags) int {
	parseDiags, checked := selfhost.CheckFromSource(src)
	checkDiags := selfhost.CheckDiagnosticsAsDiag(src, checked.Diagnostics)
	for _, d := range checkDiags {
		if d != nil && d.File == "" {
			d.File = path
		}
	}
	all := append([]*diag.Diagnostic{}, parseDiags...)
	all = append(all, checkDiags...)
	printDiags(formatter, all, flags)
	if flags.dumpNativeDiags {
		dumpNativeDiagsForSummary(path, checked.Summary)
	}
	if hasError(all) {
		return 1
	}
	return 0
}
