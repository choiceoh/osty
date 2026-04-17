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
//	# CLI app project (--cli)
//	NAME/
//	├── osty.toml
//	├── main.osty           # thin entry: parseArgs → run
//	├── args.osty           # Args struct, defaultArgs, parseArgs, helpText
//	├── app.osty            # run(args): the testable core
//	├── app_test.osty       # tests for parseArgs and run
//	└── .gitignore
//
//	# HTTP service project (--service)
//	NAME/
//	├── osty.toml
//	├── main.osty           # thin entry: builds a sample Request, calls dispatch
//	├── routes.osty         # Request/Response, dispatch, healthHandler/rootHandler/notFoundHandler
//	├── routes_test.osty    # tests every handler + dispatch fan-out
//	└── .gitignore
//
// The --cli and --service variants are still binary packages (they
// build a `fn main`), but their starter source replaces the bare
// "Hello, Osty!" with a small but realistic skeleton split across
// multiple files: a thin process entry, a typed core, and tests
// against the core. The intent is to give users the same structural
// shape Osty itself favours — explicit struct types, a pure-ish core
// function, a separate thin shell around it — rather than a snippet
// they have to delete before writing real code.
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
	// KindCli scaffolds a binary package with a CLI-shaped starter:
	// an `Args` struct, a `defaultArgs` constructor, and a `run(args)`
	// core function that `main` calls. The split is meant to nudge
	// users toward writing tests against `run` rather than `main`.
	KindCli
	// KindService scaffolds a binary package with an HTTP-style
	// request/response skeleton: `Request` and `Response` structs and
	// a `handle(req) -> Response` core that `main` exercises with a
	// sample request. There is no actual network code — std.net is
	// not in Tier 1 — but the shape matches what a real service body
	// would grow into.
	KindService
)

// CurrentEdition is the spec version the scaffolder records in
// osty.toml when Options.Edition is empty. Kept in sync with the
// language spec directory name in the repo root.
const CurrentEdition = "0.4"

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
	tx := &writeTx{}
	if err := tx.mkdir(dir); err != nil {
		return "", ioErr(dir, err)
	}
	if d := writeLayout(tx, dir, opts); d != nil {
		tx.rollback()
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
	tx := &writeTx{}
	if d := writeLayout(tx, parent, opts); d != nil {
		tx.rollback()
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
	case KindCli:
		// CLI layout splits responsibilities across three source
		// files so the testable surface (parseArgs, run) lives apart
		// from the process-binding `main`. The single `_test.osty`
		// covers both halves.
		return []string{
			filepath.Join(dir, "osty.toml"),
			filepath.Join(dir, "main.osty"),
			filepath.Join(dir, "args.osty"),
			filepath.Join(dir, "app.osty"),
			filepath.Join(dir, "app_test.osty"),
			filepath.Join(dir, ".gitignore"),
		}
	case KindService:
		// Service layout splits the entry point from the routing
		// table so handlers can be added without touching `main`.
		return []string{
			filepath.Join(dir, "osty.toml"),
			filepath.Join(dir, "main.osty"),
			filepath.Join(dir, "routes.osty"),
			filepath.Join(dir, "routes_test.osty"),
			filepath.Join(dir, ".gitignore"),
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
// tx tracks files and directories the layout creates so Create/Init
// can roll back on a mid-write failure, leaving no partial state.
func writeLayout(tx *writeTx, dir string, opts Options) *diag.Diagnostic {
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
		if err := tx.mkdir(memberDir); err != nil {
			return ioErr(memberDir, err)
		}
		return tx.writeFiles([]fileSpec{
			{filepath.Join(dir, "osty.toml"), renderWorkspaceManifest(opts.Name, edition, member)},
			{filepath.Join(dir, ".gitignore"), gitignoreTemplate},
			{filepath.Join(memberDir, "osty.toml"), renderManifest(member, edition, KindBin)},
			{filepath.Join(memberDir, "main.osty"), binSourceTemplate},
			{filepath.Join(memberDir, "main_test.osty"), binTestTemplate},
			{filepath.Join(memberDir, ".gitignore"), gitignoreTemplate},
		})
	case KindLib:
		return tx.writeFiles([]fileSpec{
			{filepath.Join(dir, "osty.toml"), renderManifest(opts.Name, edition, KindLib)},
			{filepath.Join(dir, "lib.osty"), libSourceTemplate},
			{filepath.Join(dir, "lib_test.osty"), libTestTemplate},
			{filepath.Join(dir, ".gitignore"), gitignoreTemplate},
		})
	case KindCli:
		return tx.writeFiles([]fileSpec{
			{filepath.Join(dir, "osty.toml"), renderManifest(opts.Name, edition, KindCli)},
			{filepath.Join(dir, "main.osty"), cliMainTemplate},
			{filepath.Join(dir, "args.osty"), cliArgsTemplate},
			{filepath.Join(dir, "app.osty"), cliAppTemplate},
			{filepath.Join(dir, "app_test.osty"), cliAppTestTemplate},
			{filepath.Join(dir, ".gitignore"), gitignoreTemplate},
		})
	case KindService:
		return tx.writeFiles([]fileSpec{
			{filepath.Join(dir, "osty.toml"), renderManifest(opts.Name, edition, KindService)},
			{filepath.Join(dir, "main.osty"), serviceMainTemplate},
			{filepath.Join(dir, "routes.osty"), serviceRoutesTemplate},
			{filepath.Join(dir, "routes_test.osty"), serviceRoutesTestTemplate},
			{filepath.Join(dir, ".gitignore"), gitignoreTemplate},
		})
	default: // KindBin
		return tx.writeFiles([]fileSpec{
			{filepath.Join(dir, "osty.toml"), renderManifest(opts.Name, edition, KindBin)},
			{filepath.Join(dir, "main.osty"), binSourceTemplate},
			{filepath.Join(dir, "main_test.osty"), binTestTemplate},
			{filepath.Join(dir, ".gitignore"), gitignoreTemplate},
		})
	}
}

type fileSpec struct {
	path    string
	content string
}

// writeTx records directories and files a scaffold run creates so
// rollback can restore the filesystem to its pre-run state on a
// mid-write failure. Without rollback, a partial scaffold blocks the
// next `osty new` / `osty init` with CodeScaffoldDestExists and forces
// the user to clean up by hand.
type writeTx struct {
	createdDirs  []string // mkdir order; removed in reverse
	createdFiles []string // writeFile order; removed in reverse
}

// mkdir creates dir (and parents) and records it for rollback. Only
// directories mkdir creates belong to the transaction — preexisting
// directories are left alone on rollback.
func (tx *writeTx) mkdir(dir string) error {
	if _, err := os.Stat(dir); err == nil {
		return nil // already exists; not ours to remove on rollback
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tx.createdDirs = append(tx.createdDirs, dir)
	return nil
}

// writeFiles writes each fileSpec in order, recording successful
// writes for rollback. Stops on first error.
func (tx *writeTx) writeFiles(files []fileSpec) *diag.Diagnostic {
	for _, f := range files {
		if err := os.WriteFile(f.path, []byte(f.content), 0o644); err != nil {
			return ioErr(f.path, err)
		}
		tx.createdFiles = append(tx.createdFiles, f.path)
	}
	return nil
}

// rollback best-effort removes files and directories the transaction
// created. Errors are swallowed so the caller's original diagnostic is
// preserved. os.Remove on a non-empty directory returns ENOTEMPTY
// (not a no-op), so a dir that somehow acquired unrelated contents
// between mkdir and rollback stays put — the swallowed error is the
// right outcome.
func (tx *writeTx) rollback() {
	for i := len(tx.createdFiles) - 1; i >= 0; i-- {
		_ = os.Remove(tx.createdFiles[i])
	}
	for i := len(tx.createdDirs) - 1; i >= 0; i-- {
		_ = os.Remove(tx.createdDirs[i])
	}
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

// RenderSource returns the starter `main.osty`-equivalent source for
// the selected package kind. For the multi-file kinds (KindCli /
// KindService) this returns the entry-point file only — callers that
// need the supporting files (`args.osty`, `app.osty`, `routes.osty`)
// scaffold them via Create / Init or read the per-kind constants
// directly. Workspace templates do not call this — they scaffold
// their member as KindBin under the hood.
func RenderSource(k Kind) string {
	switch k {
	case KindLib:
		return libSourceTemplate
	case KindCli:
		return cliMainTemplate
	case KindService:
		return serviceMainTemplate
	default:
		return binSourceTemplate
	}
}

// RenderTestSource returns the primary `_test.osty` file for the
// given package kind. Multi-file kinds put their tests in a
// kind-specific filename (`app_test.osty`, `routes_test.osty`); this
// helper still returns that file's contents so callers asserting on
// "the test source" see the right bytes.
func RenderTestSource(k Kind) string {
	switch k {
	case KindLib:
		return libTestTemplate
	case KindCli:
		return cliAppTestTemplate
	case KindService:
		return serviceRoutesTestTemplate
	default:
		return binTestTemplate
	}
}

// RenderWorkspaceManifest returns the osty.toml contents for a virtual
// workspace root with `members = [member]`.
func RenderWorkspaceManifest(name, edition, member string) string {
	return renderWorkspaceManifest(name, edition, member)
}

func renderManifest(name, edition string, k Kind) string {
	header := "# Binary project; `osty gen main.osty` uses the native LLVM backend and emits LLVM IR for the entry point."
	switch k {
	case KindLib:
		header = "# Library project; exposes a public API via `pub` declarations."
	case KindCli:
		header = "# CLI app project; main.osty splits parsed Args from the testable run() core."
	case KindService:
		header = "# HTTP service project; main.osty defines Request/Response and a handle() core."
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

// cliMainTemplate is the thin entry-point file for `--cli`. It does
// no work other than handing an empty argv to `parseArgs` and
// forwarding the result to `run`. Once a future std.env module ships
// `env.args()`, the empty list literal becomes the only line that
// changes — the rest of the package keeps its shape.
const cliMainTemplate = `// main.osty — CLI process entry point.
//
// Keep this file deliberately small. ` + "`parseArgs`" + ` (in args.osty)
// turns argv into a typed ` + "`Args`" + ` value; ` + "`run`" + ` (in app.osty) is the
// testable core that performs the work. Tests cover those two —
// ` + "`main`" + ` itself is just the wiring.

fn main() {
    let argv: List<String> = []
    run(parseArgs(argv))
}
`

// cliArgsTemplate defines the ` + "`Args`" + ` struct, its defaults, the
// argv parser, and a help-text helper. The parser intentionally
// handles only a couple of common flag shapes (` + "`-v`" + ` / ` + "`--name VALUE`" + `)
// so the user has a working example to extend, not a placeholder.
const cliArgsTemplate = `// args.osty — command-line argument parsing.
//
// ` + "`Args`" + ` is the parsed shape consumed by ` + "`run`" + `. ` + "`parseArgs`" + ` walks
// argv applying the same defaults as ` + "`defaultArgs`" + `; the surface is
// shaped so a future std.flag-driven implementation can replace the
// body without moving the public API.

pub struct Args {
    pub verbose: Bool,
    pub name: String,
}

pub fn defaultArgs() -> Args {
    Args { verbose: false, name: "world" }
}

pub fn parseArgs(argv: List<String>) -> Args {
    let mut verbose = false
    let mut name = "world"
    let mut expectName = false
    for a in argv {
        if expectName {
            name = a
            expectName = false
        } else if a == "-v" {
            verbose = true
        } else if a == "--name" {
            expectName = true
        }
    }
    Args { verbose, name }
}

pub fn helpText() -> String {
    "usage: app [-v] [--name NAME]"
}
`

// cliAppTemplate holds the side-effecting core. It is the function
// real tests should drive — pass a crafted ` + "`Args`" + ` and assert on the
// output stream once std.testing's capture surface lands.
const cliAppTemplate = `// app.osty — the testable CLI core.
//
// ` + "`run`" + ` accepts an already-parsed ` + "`Args`" + ` so unit tests can drive
// every code path without spawning a subprocess or touching argv.

pub fn run(args: Args) {
    if args.verbose {
        println("verbose: greeting {args.name}")
    }
    println("Hello, {args.name}!")
}
`

// cliAppTestTemplate covers both the parser and the runtime core.
// Both halves live in the same package so the test file can name
// every public symbol from args.osty / app.osty without a `use`
// clause.
const cliAppTestTemplate = `// app_test.osty — tests for the CLI core (spec §11).
//
// ` + "`parseArgs`" + ` and ` + "`run`" + ` live in the same package as this file,
// so we call them directly. Replace the placeholder ` + "`let _ = ...`" + `
// lines with ` + "`testing.assertEq`" + ` once std.testing ships.

fn testParseArgsDefaults() {
    let args = parseArgs([])
    let _ = args.verbose
    let _ = args.name
}

fn testParseArgsVerboseFlag() {
    let args = parseArgs(["-v"])
    let _ = args.verbose
}

fn testParseArgsNameFlag() {
    let args = parseArgs(["--name", "tester"])
    let _ = args.name
}

fn testRunWithCustomName() {
    let args = Args { verbose: false, name: "tester" }
    run(args)
}

fn testHelpTextIsNonEmpty() {
    let _ = helpText()
}
`

// serviceMainTemplate is the thin entry point for `--service`. It
// builds one synthetic ` + "`Request`" + ` and prints the dispatched response
// so ` + "`osty run`" + ` produces visible output. A real binary would
// replace the body with a listener loop forwarding each incoming
// request to ` + "`dispatch`" + ` — the routing surface stays unchanged.
const serviceMainTemplate = `// main.osty — service process entry point.
//
// Real services replace the synthetic request below with a listener
// loop (` + "`std.net`" + ` is not Tier 1 yet). The shape of the call —
// ` + "`dispatch(req)`" + ` returning a ` + "`Response`" + ` — is what stays stable
// once that loop appears.

fn main() {
    let req = Request { method: "GET", path: "/health" }
    let res = dispatch(req)
    println("{res.status} {res.body}")
}
`

// serviceRoutesTemplate defines the request / response shapes plus a
// dispatch function that fans out to per-route handlers. The
// if/else-if chain is intentional — it shows the most idiomatic
// starter shape in Osty before reaching for a `Map<String, fn>` on
// day two. Each handler takes the ` + "`Request`" + ` so future routes can
// inspect headers, query strings, etc., without changing the
// dispatch surface.
const serviceRoutesTemplate = `// routes.osty — request shapes, dispatch, and per-route handlers.
//
// ` + "`dispatch`" + ` is the single entry point ` + "`main`" + ` (and tests) call. To
// add a route, drop a new ` + "`pub fn xHandler(req: Request) -> Response`" + `
// and an ` + "`else if`" + ` branch in ` + "`dispatch`" + ` — the rest of the package
// stays untouched.

pub struct Request {
    pub method: String,
    pub path: String,
}

pub struct Response {
    pub status: Int,
    pub body: String,
}

pub fn dispatch(req: Request) -> Response {
    if req.path == "/health" {
        healthHandler(req)
    } else if req.path == "/" {
        rootHandler(req)
    } else {
        notFoundHandler(req)
    }
}

pub fn healthHandler(_req: Request) -> Response {
    Response { status: 200, body: "ok" }
}

pub fn rootHandler(_req: Request) -> Response {
    Response { status: 200, body: "Hello from Osty service" }
}

pub fn notFoundHandler(req: Request) -> Response {
    Response { status: 404, body: "not found: {req.path}" }
}
`

// serviceRoutesTestTemplate exercises every handler plus the
// dispatch fan-out so users see the table-driven shape rather than a
// single smoke check.
const serviceRoutesTestTemplate = `// routes_test.osty — tests for routes.osty (spec §11).
//
// Each handler is exercised directly, then ` + "`dispatch`" + ` is exercised
// once per branch so a regression in routing surfaces immediately.
// Replace the placeholder ` + "`let _ = ...`" + ` lines with
// ` + "`testing.assertEq`" + ` once std.testing ships.

fn testHealthHandlerReturnsOk() {
    let req = Request { method: "GET", path: "/health" }
    let res = healthHandler(req)
    let _ = res.status
}

fn testRootHandlerGreets() {
    let req = Request { method: "GET", path: "/" }
    let res = rootHandler(req)
    let _ = res.body
}

fn testNotFoundHandlerReports404() {
    let req = Request { method: "GET", path: "/nope" }
    let res = notFoundHandler(req)
    let _ = res.status
}

fn testDispatchRoutesHealth() {
    let req = Request { method: "GET", path: "/health" }
    let res = dispatch(req)
    let _ = res.status
}

fn testDispatchRoutesRoot() {
    let req = Request { method: "GET", path: "/" }
    let res = dispatch(req)
    let _ = res.body
}

fn testDispatchFallsThroughTo404() {
    let req = Request { method: "GET", path: "/missing" }
    let res = dispatch(req)
    let _ = res.status
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
