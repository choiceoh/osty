package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/osty/osty/internal/docgen"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
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

	var pkgDoc *docgen.Package
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
		pkgDoc = docgen.FromPackage(pkg)
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
		pkgDoc = docgen.FromFile(label, file)
		pkgDoc.Modules[0].Path = path
	}

	if title != "" {
		pkgDoc.Name = title
	}

	// Verify examples first so a failure aborts before producing
	// output — rendering a doc that references broken examples is
	// not useful, and `--verify-examples --check` together means "CI
	// guard for both freshness and example correctness".
	if verifyEx {
		errs := docgen.VerifyExamples(pkgDoc)
		if len(errs) > 0 {
			for _, e := range errs {
				fmt.Fprintln(os.Stderr, e.Format())
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
		target := checkTarget(outPath, pkgDoc.Name, ext)
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

	if err := writeDocOutput(pkgDoc.Name, outPath, ext, rendered); err != nil {
		fmt.Fprintf(os.Stderr, "osty doc: %v\n", err)
		os.Exit(1)
	}
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
		return filepath.Join(dir, safeDocFilename(pkgName)+ext)
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
		name := safeDocFilename(pkgName) + ext
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

// safeDocFilename keeps the output filename deterministic and portable:
// non-[A-Za-z0-9._-] characters become '_' so a package named
// `my.pkg/foo` still resolves to a single file.
func safeDocFilename(name string) string {
	if name == "" {
		return "doc"
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}
