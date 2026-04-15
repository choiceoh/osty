package main

import (
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
// with its `///` doc comment, and render a markdown document.
//
// Output modes:
//
//   - default: print to stdout
//   - --out FILE: write to FILE
//   - --out DIR (ending with `/` or pre-existing dir): create DIR if
//     missing and write `<name>.md` inside it
//
// The front-end runs in "doc mode": parse diagnostics are shown but
// only hard parse errors abort the run. Name-resolution / type errors
// do NOT block doc generation — the AST is sufficient, and publishing
// docs from a work-in-progress tree is a common workflow.
//
// Exit codes:
//
//	0   doc generated (stdout or file)
//	1   I/O failure, or parse errors prevented producing a doc
//	2   usage error (missing path, unknown flag, etc.)
func runDoc(args []string, flags cliFlags) {
	fs := flag.NewFlagSet("doc", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty doc [--out PATH] [--title NAME] PATH")
		fmt.Fprintln(os.Stderr, "  PATH    a .osty file or a directory (single package)")
		fmt.Fprintln(os.Stderr, "  --out   output file or directory; default stdout")
		fmt.Fprintln(os.Stderr, "  --title override the package title used in the heading")
	}
	var outPath, title string
	fs.StringVar(&outPath, "out", "", "output file or directory")
	fs.StringVar(&outPath, "o", "", "alias for --out")
	fs.StringVar(&title, "title", "", "override the package title")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}
	path := fs.Arg(0)

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
		// Surface parse diagnostics per file but don't abort unless a
		// file failed so hard it produced no AST at all.
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

	md := docgen.RenderMarkdown(pkgDoc)
	if err := writeDocOutput(pkgDoc.Name, outPath, md); err != nil {
		fmt.Fprintf(os.Stderr, "osty doc: %v\n", err)
		os.Exit(1)
	}
}

// writeDocOutput routes the rendered markdown to the right sink.
//
//	out == ""           → stdout
//	out == "-"          → stdout
//	out exists as dir   → write <pkgName>.md inside
//	out ends with /     → create dir, write <pkgName>.md inside
//	otherwise           → treat as a file path and create/overwrite
func writeDocOutput(pkgName, out, content string) error {
	switch out {
	case "", "-":
		_, err := os.Stdout.WriteString(content)
		return err
	}

	// Directory mode: either the path already exists as a directory, or
	// the user asked for one via a trailing separator.
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
		name := safeDocFilename(pkgName) + ".md"
		target := filepath.Join(dir, name)
		return os.WriteFile(target, []byte(content), 0o644)
	}

	// File mode: make sure the parent exists so `--out docs/api.md`
	// works without a pre-existing `docs/` tree.
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
