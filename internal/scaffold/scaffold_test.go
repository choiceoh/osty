package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// TestValidateName covers the happy and reject paths of the name
// check. We accept letters/digits/underscore/hyphen-starting-with-
// letter-or-underscore and reject empty, leading-digit, whitespace,
// path separators, and non-ASCII-letter starts.
func TestValidateName(t *testing.T) {
	good := []string{
		"foo",
		"Foo",
		"_foo",
		"my-app",
		"my_app",
		"foo123",
		"a",
	}
	for _, n := range good {
		if d := ValidateName(n); d != nil {
			t.Errorf("ValidateName(%q) = %s, want nil", n, d.Error())
		}
	}
	bad := []string{
		"",
		"1foo",
		"-foo",
		"foo/bar",
		"foo bar",
		"foo.bar",
		"foo!",
		"한글",
	}
	for _, n := range bad {
		if d := ValidateName(n); d == nil {
			t.Errorf("ValidateName(%q) = nil, want diagnostic", n)
		} else if d.Code != diag.CodeScaffoldInvalidName {
			t.Errorf("ValidateName(%q) code = %q, want %q", n, d.Code, diag.CodeScaffoldInvalidName)
		}
	}
}

// TestCreateBinaryProject verifies the default (binary) layout: four
// files with the expected names (manifest + source + test + gitignore),
// with populated contents.
func TestCreateBinaryProject(t *testing.T) {
	parent := t.TempDir()
	dir, d := Create(Options{Name: "myapp", Parent: parent})
	if d != nil {
		t.Fatalf("Create: %s", d.Error())
	}
	if filepath.Base(dir) != "myapp" {
		t.Errorf("dir basename = %q, want myapp", filepath.Base(dir))
	}

	for _, name := range []string{"osty.toml", "main.osty", "main_test.osty", ".gitignore"} {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("stat %s: %v", name, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("%s is empty", name)
		}
	}
	manifest := readFile(t, filepath.Join(dir, "osty.toml"))
	if !strings.Contains(manifest, `name = "myapp"`) {
		t.Errorf("manifest missing name: %s", manifest)
	}
	if !strings.Contains(manifest, `edition = "`+CurrentEdition+`"`) {
		t.Errorf("manifest missing edition: %s", manifest)
	}
	main := readFile(t, filepath.Join(dir, "main.osty"))
	if !strings.Contains(main, "fn main()") {
		t.Errorf("main.osty missing fn main: %s", main)
	}
	test := readFile(t, filepath.Join(dir, "main_test.osty"))
	if !strings.Contains(test, "fn test") {
		t.Errorf("main_test.osty missing test function: %s", test)
	}
}

// TestCreateLibraryProject: --lib swaps main.osty for lib.osty +
// lib_test.osty and records the library variant in the manifest
// header.
func TestCreateLibraryProject(t *testing.T) {
	parent := t.TempDir()
	dir, d := Create(Options{Name: "mylib", Parent: parent, Kind: KindLib})
	if d != nil {
		t.Fatalf("Create: %s", d.Error())
	}
	if _, err := os.Stat(filepath.Join(dir, "main.osty")); !os.IsNotExist(err) {
		t.Errorf("lib project should not have main.osty (err=%v)", err)
	}
	lib := readFile(t, filepath.Join(dir, "lib.osty"))
	if !strings.Contains(lib, "pub fn") {
		t.Errorf("lib.osty missing pub fn: %s", lib)
	}
	if _, err := os.Stat(filepath.Join(dir, "lib_test.osty")); err != nil {
		t.Errorf("lib_test.osty not created: %v", err)
	}
	manifest := readFile(t, filepath.Join(dir, "osty.toml"))
	if !strings.Contains(manifest, "Library project") {
		t.Errorf("library header missing: %s", manifest)
	}
}

// TestCreateWorkspaceProject verifies the --workspace layout: a
// virtual workspace root with one member package pre-created under
// `core/`.
func TestCreateWorkspaceProject(t *testing.T) {
	parent := t.TempDir()
	dir, d := Create(Options{Name: "myws", Parent: parent, Kind: KindWorkspace})
	if d != nil {
		t.Fatalf("Create: %s", d.Error())
	}
	// Root manifest should be a virtual workspace (no [package]).
	rootManifest := readFile(t, filepath.Join(dir, "osty.toml"))
	if !strings.Contains(rootManifest, "[workspace]") {
		t.Errorf("workspace header missing in root: %s", rootManifest)
	}
	if strings.Contains(rootManifest, "[package]") {
		t.Errorf("virtual workspace root should not have [package]: %s", rootManifest)
	}
	if !strings.Contains(rootManifest, `"core"`) {
		t.Errorf("members list missing default `core`: %s", rootManifest)
	}
	// Member has a package manifest + binary source + test.
	memberDir := filepath.Join(dir, "core")
	memberManifest := readFile(t, filepath.Join(memberDir, "osty.toml"))
	if !strings.Contains(memberManifest, `name = "core"`) {
		t.Errorf("member manifest missing name: %s", memberManifest)
	}
	for _, f := range []string{"main.osty", "main_test.osty", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(memberDir, f)); err != nil {
			t.Errorf("member missing %s: %v", f, err)
		}
	}
}

// TestCreateRefusesExistingDir: the scaffolder never overwrites —
// returns a CodeScaffoldDestExists diagnostic and leaves the existing
// directory untouched.
func TestCreateRefusesExistingDir(t *testing.T) {
	parent := t.TempDir()
	existing := filepath.Join(parent, "taken")
	if err := os.Mkdir(existing, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sentinel := filepath.Join(existing, "user-file.txt")
	if err := os.WriteFile(sentinel, []byte("keep me"), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}
	_, d := Create(Options{Name: "taken", Parent: parent})
	if d == nil {
		t.Fatalf("Create returned nil, want diagnostic")
	}
	if d.Code != diag.CodeScaffoldDestExists {
		t.Errorf("code = %q, want %q", d.Code, diag.CodeScaffoldDestExists)
	}
	got, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("sentinel gone: %v", err)
	}
	if string(got) != "keep me" {
		t.Errorf("sentinel modified: %q", got)
	}
}

// TestCreateRejectsInvalidName ensures validation runs before any
// filesystem operation — no partial directory gets left behind.
func TestCreateRejectsInvalidName(t *testing.T) {
	parent := t.TempDir()
	_, d := Create(Options{Name: "1bad", Parent: parent})
	if d == nil {
		t.Fatalf("Create(\"1bad\") = nil, want diagnostic")
	}
	if d.Code != diag.CodeScaffoldInvalidName {
		t.Errorf("code = %q, want %q", d.Code, diag.CodeScaffoldInvalidName)
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("parent dirtied by rejected scaffold: %v", entries)
	}
}

// TestInitIntoEmptyDir: Init scaffolds into an existing (empty)
// directory in place rather than creating a wrapper subdirectory.
func TestInitIntoEmptyDir(t *testing.T) {
	parent := t.TempDir()
	got, d := Init(Options{Name: "myapp", Parent: parent})
	if d != nil {
		t.Fatalf("Init: %s", d.Error())
	}
	// The Parent might be resolved through /private on macOS; compare
	// with EvalSymlinks.
	wantParent, _ := filepath.EvalSymlinks(parent)
	gotParent, _ := filepath.EvalSymlinks(got)
	if wantParent != gotParent {
		t.Errorf("Init returned %q; want %q", gotParent, wantParent)
	}
	if _, err := os.Stat(filepath.Join(parent, "osty.toml")); err != nil {
		t.Errorf("osty.toml not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(parent, "main.osty")); err != nil {
		t.Errorf("main.osty not created: %v", err)
	}
}

// TestInitDefaultsNameToBasename mirrors `cargo init` — when no name is
// provided, Init uses the basename of the target directory.
func TestInitDefaultsNameToBasename(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "my-tool")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, d := Init(Options{Parent: target}); d != nil {
		t.Fatalf("Init: %s", d.Error())
	}
	manifest := readFile(t, filepath.Join(target, "osty.toml"))
	if !strings.Contains(manifest, `name = "my-tool"`) {
		t.Errorf("manifest did not default name to basename: %s", manifest)
	}
}

// TestInitRefusesConflict: Init must refuse to overwrite an existing
// osty.toml and leave the file intact.
func TestInitRefusesConflict(t *testing.T) {
	parent := t.TempDir()
	existing := filepath.Join(parent, "osty.toml")
	if err := os.WriteFile(existing, []byte("# user manifest\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, d := Init(Options{Name: "x", Parent: parent})
	if d == nil {
		t.Fatalf("Init returned nil, want diagnostic")
	}
	if d.Code != diag.CodeScaffoldDestExists {
		t.Errorf("code = %q, want %q", d.Code, diag.CodeScaffoldDestExists)
	}
	got := readFile(t, existing)
	if got != "# user manifest\n" {
		t.Errorf("existing manifest modified: %q", got)
	}
}

// TestInitWorkspace confirms --workspace + init creates the virtual
// workspace layout into the current directory, with the default member
// as a subdirectory.
func TestInitWorkspace(t *testing.T) {
	parent := t.TempDir()
	if _, d := Init(Options{Name: "ws", Parent: parent, Kind: KindWorkspace}); d != nil {
		t.Fatalf("Init: %s", d.Error())
	}
	if _, err := os.Stat(filepath.Join(parent, "core", "main.osty")); err != nil {
		t.Errorf("default member core/main.osty missing: %v", err)
	}
}

// TestScaffoldedBinaryCompiles runs the full front-end (parse +
// resolve + type-check) over the binary project's main.osty and
// main_test.osty and asserts zero error-severity diagnostics. The
// templates must always produce a buildable starting point.
func TestScaffoldedBinaryCompiles(t *testing.T) {
	parent := t.TempDir()
	dir, d := Create(Options{Name: "demo", Parent: parent})
	if d != nil {
		t.Fatalf("Create: %s", d.Error())
	}
	assertCompiles(t, readBytes(t, filepath.Join(dir, "main.osty")))
	assertCompiles(t, readBytes(t, filepath.Join(dir, "main_test.osty")))
}

// TestScaffoldedLibraryCompiles is the --lib analogue.
func TestScaffoldedLibraryCompiles(t *testing.T) {
	parent := t.TempDir()
	dir, d := Create(Options{Name: "demolib", Parent: parent, Kind: KindLib})
	if d != nil {
		t.Fatalf("Create: %s", d.Error())
	}
	assertCompiles(t, readBytes(t, filepath.Join(dir, "lib.osty")))
	// The test file references `greet` from lib.osty, so they must be
	// loaded together as a package. Use LoadPackageWithTests to get
	// both files in one resolve pass.
	pkg, err := resolve.LoadPackageWithTests(dir)
	if err != nil {
		t.Fatalf("LoadPackage: %v", err)
	}
	res := resolve.ResolvePackage(pkg, resolve.NewPrelude())
	for _, dd := range res.Diags {
		if dd.Severity == diag.Error {
			t.Errorf("lib package produced error: %s", dd.Error())
		}
	}
}

// TestCreateCliProject verifies the --cli layout: a multi-file
// binary package where main.osty is a thin entry shell, args.osty
// owns the `Args` struct + parser, and app.osty owns the testable
// `run` core. The test asserts the full fileset is present and that
// each file carries its expected symbols.
func TestCreateCliProject(t *testing.T) {
	parent := t.TempDir()
	dir, d := Create(Options{Name: "mycli", Parent: parent, Kind: KindCli})
	if d != nil {
		t.Fatalf("Create: %s", d.Error())
	}
	for _, name := range []string{
		"osty.toml", "main.osty", "args.osty", "app.osty", "app_test.osty", ".gitignore",
	} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	// main.osty is intentionally tiny — just `fn main()` calling
	// parseArgs + run. Asserting on the call shape catches future
	// regressions that try to inline logic back into main.
	main := readFile(t, filepath.Join(dir, "main.osty"))
	if !strings.Contains(main, "run(parseArgs(argv))") {
		t.Errorf("main.osty should call run(parseArgs(argv)):\n%s", main)
	}
	args := readFile(t, filepath.Join(dir, "args.osty"))
	for _, want := range []string{
		"pub struct Args",
		"pub fn defaultArgs()",
		"pub fn parseArgs(argv: List<String>)",
		"pub fn helpText()",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("args.osty missing %q:\n%s", want, args)
		}
	}
	app := readFile(t, filepath.Join(dir, "app.osty"))
	if !strings.Contains(app, "pub fn run(args: Args)") {
		t.Errorf("app.osty should expose pub fn run(args: Args):\n%s", app)
	}
	test := readFile(t, filepath.Join(dir, "app_test.osty"))
	for _, want := range []string{"parseArgs([])", `parseArgs(["-v"])`, "run(args)"} {
		if !strings.Contains(test, want) {
			t.Errorf("app_test.osty missing %q:\n%s", want, test)
		}
	}
	manifest := readFile(t, filepath.Join(dir, "osty.toml"))
	if !strings.Contains(manifest, "CLI app project") {
		t.Errorf("manifest header should mark CLI variant:\n%s", manifest)
	}
}

// TestCreateServiceProject verifies the --service layout: main.osty
// is a thin entry, routes.osty owns Request/Response + dispatch +
// per-route handlers, and routes_test.osty exercises every handler
// plus the dispatch fan-out.
func TestCreateServiceProject(t *testing.T) {
	parent := t.TempDir()
	dir, d := Create(Options{Name: "mysvc", Parent: parent, Kind: KindService})
	if d != nil {
		t.Fatalf("Create: %s", d.Error())
	}
	for _, name := range []string{
		"osty.toml", "main.osty", "routes.osty", "routes_test.osty", ".gitignore",
	} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	main := readFile(t, filepath.Join(dir, "main.osty"))
	if !strings.Contains(main, "dispatch(req)") {
		t.Errorf("main.osty should call dispatch(req):\n%s", main)
	}
	routes := readFile(t, filepath.Join(dir, "routes.osty"))
	for _, want := range []string{
		"pub struct Request",
		"pub struct Response",
		"pub fn dispatch(req: Request) -> Response",
		"pub fn healthHandler",
		"pub fn rootHandler",
		"pub fn notFoundHandler",
		`req.path == "/health"`,
	} {
		if !strings.Contains(routes, want) {
			t.Errorf("routes.osty missing %q:\n%s", want, routes)
		}
	}
	test := readFile(t, filepath.Join(dir, "routes_test.osty"))
	for _, want := range []string{"healthHandler(req)", "rootHandler(req)", "notFoundHandler(req)", "dispatch(req)"} {
		if !strings.Contains(test, want) {
			t.Errorf("routes_test.osty missing %q:\n%s", want, test)
		}
	}
	manifest := readFile(t, filepath.Join(dir, "osty.toml"))
	if !strings.Contains(manifest, "HTTP service project") {
		t.Errorf("manifest header should mark service variant:\n%s", manifest)
	}
}

// TestScaffoldedCliCompiles loads the full --cli package (every
// .osty file under the project root) through resolve. This is the
// only meaningful guard for the multi-file kind — main.osty alone
// references symbols from args.osty / app.osty, so a standalone
// parse would always fail. The package-level check is what catches
// real regressions in the templates.
func TestScaffoldedCliCompiles(t *testing.T) {
	parent := t.TempDir()
	dir, d := Create(Options{Name: "democli", Parent: parent, Kind: KindCli})
	if d != nil {
		t.Fatalf("Create: %s", d.Error())
	}
	pkg, err := resolve.LoadPackageWithTests(dir)
	if err != nil {
		t.Fatalf("LoadPackageWithTests: %v", err)
	}
	res := resolve.ResolvePackage(pkg, resolve.NewPrelude())
	for _, dd := range res.Diags {
		if dd.Severity == diag.Error {
			t.Errorf("cli package error: %s", dd.Error())
		}
	}
}

// TestScaffoldedServiceCompiles is the --service analogue of
// TestScaffoldedCliCompiles.
func TestScaffoldedServiceCompiles(t *testing.T) {
	parent := t.TempDir()
	dir, d := Create(Options{Name: "demosvc", Parent: parent, Kind: KindService})
	if d != nil {
		t.Fatalf("Create: %s", d.Error())
	}
	pkg, err := resolve.LoadPackageWithTests(dir)
	if err != nil {
		t.Fatalf("LoadPackageWithTests: %v", err)
	}
	res := resolve.ResolvePackage(pkg, resolve.NewPrelude())
	for _, dd := range res.Diags {
		if dd.Severity == diag.Error {
			t.Errorf("service package error: %s", dd.Error())
		}
	}
}

// TestScaffoldedWorkspaceMemberCompiles runs the workspace member
// through the front-end. Workspace root has no package so we only
// exercise the member.
func TestScaffoldedWorkspaceMemberCompiles(t *testing.T) {
	parent := t.TempDir()
	dir, d := Create(Options{Name: "demows", Parent: parent, Kind: KindWorkspace})
	if d != nil {
		t.Fatalf("Create: %s", d.Error())
	}
	member := filepath.Join(dir, "core")
	assertCompiles(t, readBytes(t, filepath.Join(member, "main.osty")))
}

// assertCompiles runs one source through lex → parse → resolve →
// check and fails on any error-severity diagnostic.
func assertCompiles(t *testing.T, src []byte) {
	t.Helper()
	file, parseDiags := parser.ParseDiagnostics(src)
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), stdlib.Load())
	chk := check.File(file, res)
	all := append(append(append([]*diag.Diagnostic{}, parseDiags...), res.Diags...), chk.Diags...)
	for _, d := range all {
		if d.Severity == diag.Error {
			t.Errorf("scaffolded source produced diagnostic: %s", d.Error())
		}
	}
}

// ---- helpers ----

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func readBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

// TestCreateUsesCwdWhenParentEmpty verifies the documented default:
// Parent == "" means "current working directory".
func TestCreateUsesCwdWhenParentEmpty(t *testing.T) {
	tmp := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	dir, d := Create(Options{Name: "cwdproj"})
	if d != nil {
		t.Fatalf("Create: %s", d.Error())
	}
	wantParent, _ := filepath.EvalSymlinks(tmp)
	gotParent, _ := filepath.EvalSymlinks(filepath.Dir(dir))
	if wantParent != gotParent {
		t.Errorf("parent = %q, want %q", gotParent, wantParent)
	}
}
