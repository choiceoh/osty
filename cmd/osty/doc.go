package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/osty/osty/internal/docgen"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// runDoc implements the `osty doc` subcommand: parse source (single
// file or a whole package directory), extract every `pub` declaration
// with its `///` doc comment, and render the API documentation.
//
// Output modes:
//
//   - default: print to stdout
//   - --out FILE: write to FILE
//   - --out DIR (ending with `/` or pre-existing dir): create DIR if
//     missing and write `<name>.<ext>` inside it (ext from --format)
//
// Formats (--format):
//
//   - markdown (default): GitHub-flavoured markdown with TOC, tables,
//     anchored headings
//   - html: self-contained HTML page with inline CSS
//
// Modes (--check, --verify-examples):
//
//   - --check compares the generated output against an existing file
//     and exits 1 on mismatch; useful as a CI guard so docs don't
//     silently drift from source.
//   - --verify-examples extracts every `Example:` block from every
//     decl's doc comment and parses each snippet. Any parse-error
//     snippet fails the run. Keeps docstring examples honest.
//
// Exit codes:
//
//	0   doc generated (stdout or file), check clean, examples parsed
//	1   I/O failure, parse errors blocked doc, check mismatch, or
//	    example verification reported a failure
//	2   usage error (missing path, unknown flag, etc.)
func runDoc(args []string, flags cliFlags) {
	fs := flag.NewFlagSet("doc", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty doc [--format markdown|html] [--out PATH]")
		fmt.Fprintln(os.Stderr, "                [--title NAME] [--check FILE] [--verify-examples] PATH")
		fmt.Fprintln(os.Stderr, "  PATH              a .osty file or a directory (single package)")
		fmt.Fprintln(os.Stderr, "  --format FORMAT   output format: markdown (default) or html")
		fmt.Fprintln(os.Stderr, "  --out PATH        output file or directory; default stdout")
		fmt.Fprintln(os.Stderr, "  --title NAME      override the package title used in the heading")
		fmt.Fprintln(os.Stderr, "  --check           compare rendered output to --out/given file; exit 1 on diff")
		fmt.Fprintln(os.Stderr, "  --verify-examples parse every Example: block; exit 1 on any failure")
	}
	var (
		outPath, title, format string
		checkMode, verifyEx    bool
	)
	fs.StringVar(&outPath, "out", "", "output file or directory")
	fs.StringVar(&outPath, "o", "", "alias for --out")
	fs.StringVar(&title, "title", "", "override the package title")
	fs.StringVar(&format, "format", "markdown", "output format: markdown or html")
	fs.BoolVar(&checkMode, "check", false, "compare rendered output against --out path")
	fs.BoolVar(&verifyEx, "verify-examples", false, "parse every Example: block")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}
	path := fs.Arg(0)

	format = strings.ToLower(format)
	switch format {
	case "markdown", "md", "":
		format = "markdown"
	case "html":
	default:
		fmt.Fprintf(os.Stderr, "osty doc: unknown --format %q (want markdown|html)\n", format)
		os.Exit(2)
	}

	info, err := os.Stat(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty doc: %v\n", err)
		os.Exit(1)
	}

	// Workspace mode — `osty doc <workspace-root>` produces one doc
	// file per package plus an index page. Detected by the same
	// heuristic the rest of the CLI uses (resolve.IsWorkspaceRoot).
	// Workspace mode requires --out: we'd refuse to dump every
	// package's markdown to stdout, but we also can't mix workspace
	// rendering with stdout output, --check or --verify-examples
	// because those flags target a single rendered artifact.
	if info.IsDir() && resolve.IsWorkspaceRoot(path, "") {
		runWorkspaceDoc(path, format, outPath, title, checkMode, verifyEx, flags)
		return
	}

	var pkgDoc *docgen.SelfDocPackage
	if info.IsDir() {
		pkg, err := resolve.LoadPackage(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "osty doc: %v\n", err)
			os.Exit(1)
		}
		anyFatal := false
		for _, f := range pkg.Files {
			if f.File == nil {
				anyFatal = true
			}
			if len(f.ParseDiags) > 0 {
				fmter := newFormatter(f.Path, f.Source, flags)
				printDiags(fmter, f.ParseDiags, flags)
			}
		}
		if anyFatal {
			os.Exit(1)
		}
		pkgDoc = docPackageFromResolved(pkg)
	} else {
		src, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "osty doc: %v\n", err)
			os.Exit(1)
		}
		file, diags := parser.ParseDiagnostics(src)
		if len(diags) > 0 {
			fmter := newFormatter(path, src, flags)
			printDiags(fmter, diags, flags)
		}
		if file == nil {
			os.Exit(1)
		}
		label := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		pkgDoc = docgen.FromSource(label, path, string(src))
	}

	if title != "" {
		pkgDoc = docgen.RenamePackage(pkgDoc, title)
	}

	// Verify examples first so a failure aborts before producing
	// output — rendering a doc that references broken examples is
	// not useful, and `--verify-examples --check` together means "CI
	// guard for both freshness and example correctness".
	if verifyEx {
		errs := docgen.VerifyExamples(pkgDoc)
		if len(errs) > 0 {
			for _, e := range errs {
				fmt.Fprintln(os.Stderr, docgen.ExampleErrorFormat(e))
			}
			fmt.Fprintf(os.Stderr, "osty doc: %d example(s) failed to parse\n", len(errs))
			os.Exit(1)
		}
	}

	var rendered string
	switch format {
	case "html":
		rendered = docgen.RenderHTML(pkgDoc)
	default:
		rendered = docgen.RenderMarkdown(pkgDoc)
	}

	ext := extForFormat(format)

	if checkMode {
		target := checkTarget(outPath, docgen.PackageName(pkgDoc), ext)
		if target == "" {
			fmt.Fprintln(os.Stderr, "osty doc: --check requires --out FILE or --out DIR to locate the file to compare against")
			os.Exit(2)
		}
		existing, err := os.ReadFile(target)
		if err != nil {
			fmt.Fprintf(os.Stderr, "osty doc: --check: %v\n", err)
			os.Exit(1)
		}
		if !bytes.Equal(normalizeLineEndings(existing), []byte(rendered)) {
			fmt.Fprintf(os.Stderr, "osty doc: %s is out of date\n", target)
			fmt.Fprintln(os.Stderr, "  regenerate with `osty doc --out "+target+" "+path+"`")
			os.Exit(1)
		}
		return
	}

	if err := writeDocOutput(docgen.PackageName(pkgDoc), outPath, ext, rendered); err != nil {
		fmt.Fprintf(os.Stderr, "osty doc: %v\n", err)
		os.Exit(1)
	}
}

// runWorkspaceDoc handles the workspace branch of `osty doc`: load
// every package under root, render each as its own file (markdown or
// HTML), and write an `index.<ext>` listing them all. The CLI calls
// this only for actual workspace roots so the simple file/single-
// package paths stay unchanged.
//
// Constraints:
//   - --out is required and must name a directory (we create it if
//     missing). Stdout output makes no sense when N+1 files are
//     produced.
//   - --check is unsupported in this mode for now: the comparison
//     would need to walk every emitted file, which is more plumbing
//     than this iteration warrants. Surface a usage error instead of
//     silently doing the wrong thing.
//   - --verify-examples runs across every package's examples and
//     aggregates the failures.
func runWorkspaceDoc(root, format, outPath, title string,
	checkMode, verifyEx bool, flags cliFlags) {
	if outPath == "" || outPath == "-" {
		fmt.Fprintln(os.Stderr,
			"osty doc: workspace mode requires --out DIR (multiple files are produced)")
		os.Exit(2)
	}
	if checkMode {
		fmt.Fprintln(os.Stderr,
			"osty doc: --check is not supported in workspace mode")
		os.Exit(2)
	}

	ws, err := resolve.NewWorkspace(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty doc: %v\n", err)
		os.Exit(1)
	}
	ws.Stdlib = stdlib.LoadCached()
	for _, p := range resolve.WorkspacePackagePaths(root) {
		_, _ = ws.LoadPackage(p)
	}

	// Build docgen Packages keyed by the workspace's import paths so
	// the index page's display order matches the resolver's view.
	docPkgs := map[string]*docgen.SelfDocPackage{}
	anyFatal := false
	for impPath, pkg := range ws.Packages {
		if pkg == nil {
			continue
		}
		// Surface parse diagnostics for each file as before — keeps
		// "docs from a noisy WIP tree" workable, fails only on AST-
		// less files.
		for _, f := range pkg.Files {
			if f.File == nil {
				anyFatal = true
			}
			if len(f.ParseDiags) > 0 {
				fmter := newFormatter(f.Path, f.Source, flags)
				printDiags(fmter, f.ParseDiags, flags)
			}
		}
		dp := docPackageFromResolved(pkg)
		// Workspace packages often have no Name set by the loader —
		// fall back to the import path or directory basename.
		dp = docgen.RenamePackage(dp,
			docgen.PreferredPackageName(docgen.PackageName(dp), impPath, pkg.Dir))
		docPkgs[impPath] = dp
	}
	if anyFatal {
		os.Exit(1)
	}

	keys := make([]string, 0, len(docPkgs))
	for impPath := range docPkgs {
		keys = append(keys, impPath)
	}
	sort.Strings(keys)
	orderedPkgs := make([]*docgen.SelfDocPackage, 0, len(keys))
	for _, impPath := range keys {
		if docPkgs[impPath] != nil {
			orderedPkgs = append(orderedPkgs, docPkgs[impPath])
		}
	}
	wsDoc := docgen.FromWorkspacePackages(root, orderedPkgs)
	_ = title // workspace heading is "Workspace API documentation" by convention

	if verifyEx {
		var totalErrs int
		for _, dp := range orderedPkgs {
			errs := docgen.VerifyExamples(dp)
			totalErrs += len(errs)
			for _, e := range errs {
				fmt.Fprintln(os.Stderr, docgen.ExampleErrorFormat(e))
			}
		}
		if totalErrs > 0 {
			fmt.Fprintf(os.Stderr, "osty doc: %d example(s) failed to parse\n", totalErrs)
			os.Exit(1)
		}
	}

	ext := extForFormat(format)
	dir := strings.TrimRight(outPath, "/"+string(os.PathSeparator))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "osty doc: %v\n", err)
		os.Exit(1)
	}

	// Per-package files first, then the index — that ordering means a
	// half-written run still leaves valid per-package docs even if
	// something goes wrong producing the index.
	for _, dp := range orderedPkgs {
		var rendered string
		if format == "html" {
			rendered = docgen.RenderHTML(dp)
		} else {
			rendered = docgen.RenderMarkdown(dp)
		}
		fname := docgen.SafePackageFilename(docgen.PackageName(dp)) + ext
		target := filepath.Join(dir, fname)
		if err := os.WriteFile(target, []byte(rendered), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "osty doc: %v\n", err)
			os.Exit(1)
		}
	}

	// Index page lives at <out>/index.<ext>. Hardcoded name keeps
	// links predictable and matches the convention of every static-
	// site generator on the planet.
	var index string
	if format == "html" {
		index = docgen.RenderWorkspaceHTML(wsDoc, ext)
	} else {
		index = docgen.RenderWorkspaceMarkdown(wsDoc, ext)
	}
	indexPath := filepath.Join(dir, "index"+ext)
	if err := os.WriteFile(indexPath, []byte(index), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "osty doc: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "osty doc: wrote %d package(s) + index to %s\n",
		len(orderedPkgs), dir)
}

func docPackageFromResolved(pkg *resolve.Package) *docgen.SelfDocPackage {
	paths := make([]string, 0, len(pkg.Files))
	sources := make([]string, 0, len(pkg.Files))
	for _, f := range pkg.Files {
		paths = append(paths, f.Path)
		sources = append(sources, string(f.Source))
	}
	return docgen.FromSources(pkg.Name, pkg.Dir, paths, sources)
}

// extForFormat maps a format name to the on-disk extension the writer
// uses when --out points at a directory. Kept as a helper so the CLI
// and --check path agree on the default filename.
func extForFormat(format string) string {
	if format == "html" {
		return ".html"
	}
	return ".md"
}

// normalizeLineEndings strips a UTF-8 BOM and folds CRLF to LF so
// --check is stable across editors. Mirrors codesdoc.normalize.
func normalizeLineEndings(b []byte) []byte {
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	return b
}

// checkTarget picks the file path `--check` should compare against.
// If --out names a file, that's the target; if it's a directory (or
// directory-like), we synthesise the same filename the writer would
// have produced. Empty return means "cannot determine" — the caller
// turns that into a usage error.
func checkTarget(out, pkgName, ext string) string {
	if out == "" || out == "-" {
		return ""
	}
	wantDir := strings.HasSuffix(out, string(os.PathSeparator)) ||
		strings.HasSuffix(out, "/")
	if info, err := os.Stat(out); err == nil && info.IsDir() {
		wantDir = true
	}
	if wantDir {
		dir := strings.TrimRight(out, "/"+string(os.PathSeparator))
		return filepath.Join(dir, docgen.SafePackageFilename(pkgName)+ext)
	}
	return out
}

// writeDocOutput routes rendered content to the right sink.
//
//	out == ""           → stdout
//	out == "-"          → stdout
//	out exists as dir   → write <pkgName><ext> inside
//	out ends with /     → create dir, write <pkgName><ext> inside
//	otherwise           → treat as a file path and create/overwrite
func writeDocOutput(pkgName, out, ext, content string) error {
	switch out {
	case "", "-":
		_, err := os.Stdout.WriteString(content)
		return err
	}

	wantDir := strings.HasSuffix(out, string(os.PathSeparator)) ||
		strings.HasSuffix(out, "/")
	if info, err := os.Stat(out); err == nil && info.IsDir() {
		wantDir = true
	}
	if wantDir {
		dir := strings.TrimRight(out, "/"+string(os.PathSeparator))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		name := docgen.SafePackageFilename(pkgName) + ext
		target := filepath.Join(dir, name)
		return os.WriteFile(target, []byte(content), 0o644)
	}

	if parent := filepath.Dir(out); parent != "" && parent != "." {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(out, []byte(content), 0o644)
}
