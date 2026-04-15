// Package scaffold creates fresh Osty project layouts on disk.
//
// It implements the `osty new NAME` and `osty init` subcommands
// (spec §13.1): given a name and a target directory, write out the
// canonical starter files so the user can `cd` into the project and
// run `osty gen`, `osty check`, `osty test`, etc. The scaffolder
// never overwrites an existing file or directory — callers always get
// a CodeScaffoldDestExists diagnostic back and can decide to bail or
// use a different destination.
//
// Layouts:
//
//	# Binary project (default, or --bin)
//	NAME/
//	├── osty.toml           # manifest (spec §13.2)
//	├── main.osty           # entry point: fn main()
//	├── main_test.osty      # test file skeleton (spec §11)
//	└── .gitignore
//
//	# Library project (--lib)
//	NAME/
//	├── osty.toml
//	├── lib.osty            # pub fn starter
//	├── lib_test.osty
//	└── .gitignore
//
//	# Workspace project (--workspace)
//	NAME/
//	├── osty.toml           # [workspace] members = ["core"]
//	├── .gitignore
//	└── core/               # one default member (a binary package)
//	    ├── osty.toml
//	    ├── main.osty
//	    ├── main_test.osty
//	    └── .gitignore
//
// `osty init` writes the chosen layout into the current directory in
// place (no outer wrapper) after verifying that no conflicting files
// already exist. The two entry points share every template so a
// directory initialized with `init` is byte-identical to one scaffolded
// with `new`.
//
// Diagnostics use the shared diag.Diagnostic type with E2050–E2069 in
// the manifest/scaffolding code range. This matches the rest of the
// toolchain and lets the CLI render caret underlines and fix hints for
// scaffold errors.
package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/token"
)

// Kind selects a layout template.
type Kind int

const (
	// KindBin scaffolds a project with an executable entry point
	// (`fn main`). This is the default.
	KindBin Kind = iota
	// KindLib scaffolds a project with no `main`; the starter file is
	// `lib.osty` exporting a `pub fn` example.
	KindLib
	// KindWorkspace scaffolds a virtual workspace root plus one
	// default binary-package member (directory "core"). Callers that
	// want additional members scaffold them separately with a second
	// Create call rooted at the workspace directory.
	KindWorkspace
)

// CurrentEdition is the spec version the scaffolder records in
// osty.toml when Options.Edition is empty. Kept in sync with the
// language spec directory name in the repo root.
const CurrentEdition = "0.3"

// Options configures a scaffold run.
type Options struct {
	// Name is the project name. Used as the created directory's
	// basename (for Create) and as the manifest's `name` field.
	// Empty Name is allowed only when Kind == KindWorkspace and Init
	// is used — a virtual workspace has no package identity.
	Name string
	// Parent is the directory under which the new project is created
	// by Create, or the directory Init writes into directly. An
	// empty Parent means the current working directory.
	Parent string
	// Kind selects the template. The zero value (KindBin) is a
	// binary project.
	Kind Kind
	// Edition is the Osty spec version recorded in osty.toml.
	// Defaults to CurrentEdition when empty.
	Edition string
	// WorkspaceMember is the directory name of the default member
	// created with Kind == KindWorkspace. Defaults to "core".
	WorkspaceMember string
}

// nameRE validates project names. A valid name starts with an ASCII
// letter or underscore and may contain letters, digits, underscores,
// or hyphens. Hyphens are accepted because filesystem project
// directories commonly use them ("my-app") even though they are not
// valid Osty identifiers — consumers that import this project with
// `use` reach it by directory basename, so those users should prefer
// a name that is also a valid identifier.
var nameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

// ValidateName returns nil if name is acceptable as both a directory
// name and the `name` field of osty.toml. A non-nil return is already
// a diagnostic ready to render — the CLI just forwards it.
func ValidateName(name string) *diag.Diagnostic {
	if name == "" {
		return diag.New(diag.Error, "project name is empty").
			Code(diag.CodeScaffoldInvalidName).
			PrimaryPos(token.Pos{Line: 1, Column: 1}, "").
			Hint("pass a name: `osty new myproject`").
			Build()
	}
	if !nameRE.MatchString(name) {
		return diag.New(diag.Error,
			fmt.Sprintf("invalid project name %q", name)).
			Code(diag.CodeScaffoldInvalidName).
			PrimaryPos(token.Pos{Line: 1, Column: 1}, "").
			Note("must match [A-Za-z_][A-Za-z0-9_-]*").
			Hint("try all-lowercase with hyphens, e.g. `my-tool`").
			Build()
	}
	return nil
}

// Create writes a fresh project directory under opts.Parent (cwd by
// default) and returns the absolute path of the created directory.
// It is an error for the destination to already exist.
//
// The returned diagnostic is nil on success. On failure the returned
// path may be non-empty (partial write) but callers should not rely
// on its contents — scaffolding is best-effort transactional: we
// check existence before writing and stop on the first write error.
func Create(opts Options) (string, *diag.Diagnostic) {
	if opts.Kind != KindWorkspace {
		if d := ValidateName(opts.Name); d != nil {
			return "", d
		}
	} else if opts.Name == "" {
		return "", diag.New(diag.Error, "workspace requires a name").
			Code(diag.CodeScaffoldInvalidName).
			PrimaryPos(token.Pos{Line: 1, Column: 1}, "").
			Hint("usage: osty new --workspace NAME").
			Build()
	} else if d := ValidateName(opts.Name); d != nil {
		return "", d
	}
	parent, d := resolveParent(opts.Parent)
	if d != nil {
		return "", d
	}
	dir := filepath.Join(parent, opts.Name)
	if _, err := os.Stat(dir); err == nil {
		return "", existsDiag(dir)
	} else if !os.IsNotExist(err) {
		return "", ioErr(dir, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", ioErr(dir, err)
	}
	if d := writeLayout(dir, opts); d != nil {
		return dir, d
	}
	return dir, nil
}

// Init writes the scaffold into an existing directory in place
// (no outer wrapper). The directory must exist and must not already
// contain an osty.toml or any of the source files the layout would
// create — Init refuses to overwrite. The returned path is always the
// input directory (absolutized).
//
// If opts.Name is empty for Init, it defaults to the basename of the
// parent directory (matching `cargo init`'s convention). For
// KindWorkspace an empty Name is left blank — the workspace root does
// not have a package identity.
func Init(opts Options) (string, *diag.Diagnostic) {
	parent, d := resolveParent(opts.Parent)
	if d != nil {
		return "", d
	}
	if opts.Name == "" && opts.Kind != KindWorkspace {
		opts.Name = filepath.Base(parent)
	}
	if opts.Kind != KindWorkspace {
		if d := ValidateName(opts.Name); d != nil {
			return "", d
		}
	} else if opts.Name != "" {
		if d := ValidateName(opts.Name); d != nil {
			return "", d
		}
	}
	if info, err := os.Stat(parent); err != nil {
		return "", ioErr(parent, err)
	} else if !info.IsDir() {
		return "", diag.New(diag.Error,
			fmt.Sprintf("init target %s is not a directory", parent)).
			Code(diag.CodeScaffoldWriteError).
			PrimaryPos(token.Pos{Line: 1, Column: 1}, "").
			Build()
	}
	// Explicitly check each file we'd create; on conflict emit a
	// targeted diagnostic rather than the generic "dir exists".
	if d := checkNoConflicts(parent, opts); d != nil {
		return "", d
	}
	if d := writeLayout(parent, opts); d != nil {
		return parent, d
	}
	return parent, nil
}

// resolveParent turns opts.Parent into an absolute path. Empty
// Parent means the current working directory.
func resolveParent(p string) (string, *diag.Diagnostic) {
	if p == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", ioErr("<cwd>", err)
		}
		p = wd
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", ioErr(p, err)
	}
	return abs, nil
}

// checkNoConflicts scans the target directory for any file the layout
// would create. Stops at the first conflict with a pointed diagnostic.
func checkNoConflicts(dir string, opts Options) *diag.Diagnostic {
	paths := expectedPaths(dir, opts)
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return diag.New(diag.Error,
				fmt.Sprintf("init would overwrite existing file %s", p)).
				Code(diag.CodeScaffoldDestExists).
				PrimaryPos(token.Pos{Line: 1, Column: 1}, "").
				Hint("remove the conflicting file, or scaffold into an empty directory").
				Build()
		} else if !os.IsNotExist(err) {
			return ioErr(p, err)
		}
	}
	return nil
}

// expectedPaths lists every file the scaffolder will write for the
// given options, in source order. Used by Init to pre-flight the
// target directory for conflicts.
func expectedPaths(dir string, opts Options) []string {
	switch opts.Kind {
	case KindLib:
		return []string{
			filepath.Join(dir, "osty.toml"),
			filepath.Join(dir, "lib.osty"),
			filepath.Join(dir, "lib_test.osty"),
			filepath.Join(dir, ".gitignore"),
		}
	case KindWorkspace:
		member := opts.WorkspaceMember
		if member == "" {
			member = "core"
		}
		return []string{
			filepath.Join(dir, "osty.toml"),
			filepath.Join(dir, ".gitignore"),
			filepath.Join(dir, member, "osty.toml"),
			filepath.Join(dir, member, "main.osty"),
			filepath.Join(dir, member, "main_test.osty"),
			filepath.Join(dir, member, ".gitignore"),
		}
	default: // KindBin
		return []string{
			filepath.Join(dir, "osty.toml"),
			filepath.Join(dir, "main.osty"),
			filepath.Join(dir, "main_test.osty"),
			filepath.Join(dir, ".gitignore"),
		}
	}
}

// writeLayout renders and writes every file in the layout. Called by
// both Create and Init — the directory it writes into already exists.
func writeLayout(dir string, opts Options) *diag.Diagnostic {
	edition := opts.Edition
	if edition == "" {
		edition = CurrentEdition
	}
	switch opts.Kind {
	case KindWorkspace:
		member := opts.WorkspaceMember
		if member == "" {
			member = "core"
		}
		memberDir := filepath.Join(dir, member)
		if err := os.MkdirAll(memberDir, 0o755); err != nil {
			return ioErr(memberDir, err)
		}
		files := []struct {
			path    string
			content string
		}{
			{filepath.Join(dir, "osty.toml"), renderWorkspaceManifest(opts.Name, edition, member)},
			{filepath.Join(dir, ".gitignore"), gitignoreTemplate},
			{filepath.Join(memberDir, "osty.toml"), renderManifest(member, edition, KindBin)},
			{filepath.Join(memberDir, "main.osty"), binSourceTemplate},
			{filepath.Join(memberDir, "main_test.osty"), binTestTemplate},
			{filepath.Join(memberDir, ".gitignore"), gitignoreTemplate},
		}
		return writeFiles(files)
	case KindLib:
		files := []struct {
			path    string
			content string
		}{
			{filepath.Join(dir, "osty.toml"), renderManifest(opts.Name, edition, KindLib)},
			{filepath.Join(dir, "lib.osty"), libSourceTemplate},
			{filepath.Join(dir, "lib_test.osty"), libTestTemplate},
			{filepath.Join(dir, ".gitignore"), gitignoreTemplate},
		}
		return writeFiles(files)
	default: // KindBin
		files := []struct {
			path    string
			content string
		}{
			{filepath.Join(dir, "osty.toml"), renderManifest(opts.Name, edition, KindBin)},
			{filepath.Join(dir, "main.osty"), binSourceTemplate},
			{filepath.Join(dir, "main_test.osty"), binTestTemplate},
			{filepath.Join(dir, ".gitignore"), gitignoreTemplate},
		}
		return writeFiles(files)
	}
}

func writeFiles(files []struct {
	path    string
	content string
}) *diag.Diagnostic {
	for _, f := range files {
		if err := os.WriteFile(f.path, []byte(f.content), 0o644); err != nil {
			return ioErr(f.path, err)
		}
	}
	return nil
}

func existsDiag(dir string) *diag.Diagnostic {
	return diag.New(diag.Error,
		fmt.Sprintf("destination %s already exists", dir)).
		Code(diag.CodeScaffoldDestExists).
		PrimaryPos(token.Pos{Line: 1, Column: 1}, "").
		Hint("pick a different name, or use `osty init` to scaffold into the current directory").
		Build()
}

func ioErr(path string, err error) *diag.Diagnostic {
	return diag.New(diag.Error,
		fmt.Sprintf("%s: %v", path, err)).
		Code(diag.CodeScaffoldWriteError).
		PrimaryPos(token.Pos{Line: 1, Column: 1}, "").
		Build()
}

// RenderManifest returns the osty.toml contents for a new package.
// Exported so tests (and the Init variant) can assert the exact output
// without re-creating a directory.
func RenderManifest(name, edition string, k Kind) string {
	return renderManifest(name, edition, k)
}

// RenderSource returns the starter .osty source for the selected
// package kind. Workspace templates do not call this — they scaffold
// their member as KindBin under the hood.
func RenderSource(k Kind) string {
	if k == KindLib {
		return libSourceTemplate
	}
	return binSourceTemplate
}

// RenderTestSource returns the starter `_test.osty` file for the
// given package kind.
func RenderTestSource(k Kind) string {
	if k == KindLib {
		return libTestTemplate
	}
	return binTestTemplate
}

// RenderWorkspaceManifest returns the osty.toml contents for a virtual
// workspace root with `members = [member]`.
func RenderWorkspaceManifest(name, edition, member string) string {
	return renderWorkspaceManifest(name, edition, member)
}

func renderManifest(name, edition string, k Kind) string {
	header := "# Binary project; `osty gen main.osty` transpiles the entry point to Go."
	if k == KindLib {
		header = "# Library project; exposes a public API via `pub` declarations."
	}
	return fmt.Sprintf(`%s

[package]
name = "%s"
version = "0.1.0"
edition = "%s"

[dependencies]
# Add dependencies here, for example:
# json-ext = { path = "../json-ext" }
`, header, name, edition)
}

func renderWorkspaceManifest(name, edition, member string) string {
	nameLine := ""
	if name != "" {
		nameLine = fmt.Sprintf("# Workspace: %s\n", name)
	}
	_ = edition // edition currently unused for the virtual root;
	// kept in the signature for symmetry with renderManifest so
	// future additions (e.g. [workspace.package] inheritance) don't
	// need a plumbing change at call sites.
	return fmt.Sprintf(`%s# Virtual workspace root. Member paths below are resolved relative
# to this directory (spec §5). Add members with:
#
#     osty new --bin NAME        # inside this directory
#
# then append the directory name to the members list below.

[workspace]
members = ["%s"]
`, nameLine, member)
}

const binSourceTemplate = `fn main() {
    println("Hello, Osty!")
}
`

const libSourceTemplate = `pub fn greet(name: String) -> String {
    "Hello, {name}!"
}
`

// binTestTemplate is a smoke-test skeleton for binary projects. It is
// deliberately minimal: spec §11 discovers `test*` functions by name,
// so the important bit is that the function exists. Assertions are
// commented out pending the std.testing module (spec §10, not yet
// implemented).
const binTestTemplate = `// main_test.osty — tests for main.osty (spec §11).
//
// Test files end in ` + "`_test.osty`" + ` and are discovered by
// ` + "`osty test`" + `. Functions whose names begin with ` + "`test`" + `
// are run as tests.

fn testSmoke() {
    // Placeholder. Replace with real assertions once std.testing
    // ships (spec §10 / §11).
    let _ = "ok"
}
`

const libTestTemplate = `// lib_test.osty — tests for lib.osty (spec §11).

fn testGreetReturnsNonEmpty() {
    // Placeholder. Replace with ` + "`testing.assertEq`" + ` once
    // std.testing ships (spec §10 / §11).
    let _ = greet("world")
}
`

// gitignoreTemplate is intentionally minimal. We ignore only editor /
// OS cruft by default; the build-artifact convention is not yet fixed
// in the spec, so users can append their own patterns as the project
// grows.
const gitignoreTemplate = `# Editor / OS cruft
.DS_Store
.vscode/
.idea/
*.swp
`
