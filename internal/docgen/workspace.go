package docgen

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Workspace bundles every Package extracted from a multi-package
// project tree, plus the shared root directory used as the relative
// base for index links.
//
// The CLI builds one Workspace via FromWorkspace and then renders
// either a markdown or HTML index page (RenderWorkspaceMarkdown /
// RenderWorkspaceHTML) plus one per-package doc file. Together they
// form a self-contained static doc site.
type Workspace struct {
	// Root is the workspace root directory — used to derive each
	// package's display path relative to the project.
	Root string
	// Packages is one entry per loaded package, in deterministic
	// order (lexicographic by import path).
	Packages []*Package
}

// FromWorkspaceMap turns a map of import-path → Package into a
// Workspace with deterministic ordering. The map shape matches the
// CLI's iteration over resolve.Workspace.Packages.
func FromWorkspaceMap(root string, pkgs map[string]*Package) *Workspace {
	out := &Workspace{Root: root}
	keys := make([]string, 0, len(pkgs))
	for k := range pkgs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if pkgs[k] == nil {
			continue
		}
		out.Packages = append(out.Packages, pkgs[k])
	}
	return out
}

// RenderWorkspaceMarkdown emits a workspace index page that lists
// every package with its summary and a relative link to the
// per-package doc file (assumed to be in the same output dir, named
// via safeDocFilename + ext).
//
// The renderer is format-agnostic about the per-package files — the
// caller writes those separately. The link extension is supplied so
// the same function works for both markdown (.md) and HTML (.html)
// site builds, with the index page in the chosen format.
func RenderWorkspaceMarkdown(ws *Workspace, ext string) string {
	var b strings.Builder
	b.WriteString("# Workspace API documentation\n\n")
	if ws.Root != "" {
		fmt.Fprintf(&b, "_Root: `%s`_\n\n", ws.Root)
	}
	if len(ws.Packages) == 0 {
		b.WriteString("_No documented packages._\n")
		return b.String()
	}
	b.WriteString("## Packages\n\n")
	b.WriteString("| Package | Summary |\n|---|---|\n")
	for _, pkg := range ws.Packages {
		summary := workspacePackageSummary(pkg)
		link := fmt.Sprintf("[`%s`](%s)",
			pkg.Name, safePackageFilename(pkg.Name)+ext)
		fmt.Fprintf(&b, "| %s | %s |\n", link, escapeMarkdown(summary))
	}
	b.WriteByte('\n')
	return b.String()
}

// RenderWorkspaceHTML is the HTML twin of RenderWorkspaceMarkdown.
// Style: same inline stylesheet, table of packages with links.
func RenderWorkspaceHTML(ws *Workspace, ext string) string {
	var b strings.Builder
	b.WriteString("<!doctype html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n")
	b.WriteString("<title>Workspace API docs — Osty</title>\n")
	b.WriteString(htmlStylesheet)
	b.WriteString("</head>\n<body>\n<main>\n")
	b.WriteString("<h1>Workspace API documentation</h1>\n")
	if ws.Root != "" {
		fmt.Fprintf(&b, "<p class=\"src-path\">Root: <code>%s</code></p>\n", htmlEscape(ws.Root))
	}
	if len(ws.Packages) == 0 {
		b.WriteString("<p><em>No documented packages.</em></p>\n")
		b.WriteString("</main>\n</body>\n</html>\n")
		return b.String()
	}
	b.WriteString("<h2>Packages</h2>\n<table>\n")
	b.WriteString("<thead><tr><th>Package</th><th>Summary</th></tr></thead>\n<tbody>\n")
	for _, pkg := range ws.Packages {
		summary := workspacePackageSummary(pkg)
		fmt.Fprintf(&b, "<tr><td><a href=\"%s\"><code>%s</code></a></td><td>%s</td></tr>\n",
			safePackageFilename(pkg.Name)+ext,
			htmlEscape(pkg.Name),
			htmlEscape(summary))
	}
	b.WriteString("</tbody></table>\n</main>\n</body>\n</html>\n")
	return b.String()
}

// workspacePackageSummary picks one human-readable sentence to
// describe a package on the index. Strategy: the first non-empty
// Summary among the package's documented decls. Empty when no decl
// has a parsed Summary.
func workspacePackageSummary(pkg *Package) string {
	for _, m := range pkg.Modules {
		for _, d := range m.Decls {
			if d.Info.Summary != "" {
				return d.Info.Summary
			}
		}
	}
	return ""
}

// safePackageFilename mirrors the CLI's safeDocFilename so the index
// page links and the per-package file outputs always agree on
// filenames. Kept here (rather than shared) because the docgen
// package shouldn't depend on the cmd/osty binary.
func safePackageFilename(name string) string {
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

// escapeMarkdown protects pipe characters that would otherwise break
// the surrounding table. Sufficient for one-line summaries; the
// renderer never embeds raw user prose anywhere richer.
func escapeMarkdown(s string) string {
	return strings.ReplaceAll(s, "|", `\|`)
}

// PreferredPackageName chooses a stable display name for a package
// loaded by the resolver. Falls back to the import-path / directory
// basename when Name is empty (root packages frequently have no Name
// set by the loader).
func PreferredPackageName(name, importPath, dir string) string {
	if name != "" {
		return name
	}
	if importPath != "" {
		return importPath
	}
	if dir != "" {
		return filepath.Base(dir)
	}
	return "package"
}
