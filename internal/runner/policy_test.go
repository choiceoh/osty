package runner

import "testing"

func TestBinaryBaseNamePrecedence(t *testing.T) {
	cases := []struct {
		bin, pkg, want string
	}{
		{"cli", "mypkg", "cli"},
		{"", "mypkg", "mypkg"},
		{"", "", "app"},
	}
	for _, c := range cases {
		if got := BinaryBaseName(c.bin, c.pkg); got != c.want {
			t.Errorf("BinaryBaseName(%q, %q) = %q, want %q", c.bin, c.pkg, got, c.want)
		}
	}
}

func TestBinaryNameForAppliesExeOnWindows(t *testing.T) {
	cases := []struct {
		bin, pkg, goos, want string
	}{
		{"cli", "mypkg", "linux", "cli"},
		{"cli", "mypkg", "darwin", "cli"},
		{"cli", "mypkg", "windows", "cli.exe"},
		{"", "", "windows", "app.exe"},
		{"cli.exe", "", "windows", "cli.exe"},
	}
	for _, c := range cases {
		if got := BinaryNameFor(c.bin, c.pkg, c.goos); got != c.want {
			t.Errorf("BinaryNameFor(%q, %q, %q) = %q, want %q", c.bin, c.pkg, c.goos, got, c.want)
		}
	}
}

func TestBuildBinaryName(t *testing.T) {
	cases := []struct {
		name                                       string
		bin, pkg, triple, targetOS, hostGoos, want string
	}{
		{"host-linux", "cli", "pkg", "", "", "linux", "cli"},
		{"host-windows", "cli", "pkg", "", "", "windows", "cli.exe"},
		{"cross-from-linux-to-darwin", "cli", "pkg", "aarch64-apple-darwin", "darwin", "linux", "cli-aarch64-apple-darwin"},
		{"cross-from-windows-to-linux-drops-exe", "cli", "pkg", "x86_64-unknown-linux-gnu", "linux", "windows", "cli-x86_64-unknown-linux-gnu"},
		{"cross-from-windows-to-windows-keeps-exe", "cli", "pkg", "x86_64-pc-windows-msvc", "windows", "windows", "cli-x86_64-pc-windows-msvc.exe"},
		{"cross-from-linux-to-windows-no-exe-because-host-isnt-windows", "cli", "pkg", "x86_64-pc-windows-msvc", "windows", "linux", "cli-x86_64-pc-windows-msvc"},
		{"empty-targetos-defaults-to-host-goos", "cli", "pkg", "", "", "windows", "cli.exe"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := BuildBinaryName(c.bin, c.pkg, c.triple, c.targetOS, c.hostGoos)
			if got != c.want {
				t.Errorf("BuildBinaryName(%q, %q, %q, %q, %q) = %q, want %q",
					c.bin, c.pkg, c.triple, c.targetOS, c.hostGoos, got, c.want)
			}
		})
	}
}

func TestEntryPathForDefaultAndOverride(t *testing.T) {
	cases := []struct {
		root, bin, sep, want string
	}{
		{"/proj", "", "/", "/proj/main.osty"},
		{"/proj", "src/cli.osty", "/", "/proj/src/cli.osty"},
		{"/proj/", "main.osty", "/", "/proj/main.osty"},
		{"", "main.osty", "/", "main.osty"},
	}
	for _, c := range cases {
		if got := EntryPathFor(c.root, c.bin, c.sep); got != c.want {
			t.Errorf("EntryPathFor(%q, %q, %q) = %q, want %q", c.root, c.bin, c.sep, got, c.want)
		}
	}
}

func TestCrossCompileGuardAllowsHost(t *testing.T) {
	out := CrossCompileGuard("")
	if out.Blocked {
		t.Fatalf("expected host (empty triple) to pass, got blocked")
	}
	if out.Diag != nil {
		t.Fatalf("expected nil diag for host, got %+v", out.Diag)
	}
}

func TestCrossCompileGuardBlocksAndHints(t *testing.T) {
	out := CrossCompileGuard("aarch64-apple-darwin")
	if !out.Blocked {
		t.Fatalf("expected cross triple to be blocked")
	}
	if out.Diag == nil {
		t.Fatalf("expected diag, got nil")
	}
	if out.Diag.Code != "R0001" {
		t.Errorf("code = %q, want R0001", out.Diag.Code)
	}
	if out.Diag.Severity != "error" {
		t.Errorf("severity = %q, want error", out.Diag.Severity)
	}
	wantMsg := "cannot execute cross-compiled binary for aarch64-apple-darwin on host"
	if out.Diag.Message != wantMsg {
		t.Errorf("message = %q, want %q", out.Diag.Message, wantMsg)
	}
	wantHint := "use `osty build --target aarch64-apple-darwin` to produce the binary"
	if out.Diag.Hint != wantHint {
		t.Errorf("hint = %q, want %q", out.Diag.Hint, wantHint)
	}
}

func TestMergeEnvOverridesShadowParent(t *testing.T) {
	parent := []string{"PATH=/bin", "HOME=/root", "CGO_ENABLED=0"}
	overrides := []EnvEntry{
		{Key: "CGO_ENABLED", Value: "1"},
		{Key: "GOOS", Value: "linux"},
	}
	got := MergeEnv(parent, overrides)
	want := []string{"CGO_ENABLED=1", "GOOS=linux", "PATH=/bin", "HOME=/root"}
	if len(got) != len(want) {
		t.Fatalf("MergeEnv length = %d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("MergeEnv[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestMergeEnvNoOverridesReturnsParent(t *testing.T) {
	parent := []string{"PATH=/bin"}
	got := MergeEnv(parent, nil)
	if len(got) != 1 || got[0] != "PATH=/bin" {
		t.Fatalf("MergeEnv(parent, nil) = %v, want parent verbatim", got)
	}
}

func TestMergeEnvKeepsMalformedParentEntries(t *testing.T) {
	parent := []string{"STRAY_NO_EQUALS", "GOOS=darwin"}
	overrides := []EnvEntry{{Key: "GOOS", Value: "linux"}}
	got := MergeEnv(parent, overrides)
	want := []string{"GOOS=linux", "STRAY_NO_EQUALS"}
	if len(got) != len(want) {
		t.Fatalf("MergeEnv length = %d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("MergeEnv[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestDefaultEmitModeByTool(t *testing.T) {
	cases := []struct {
		tool, want string
	}{
		{"gen", "llvm-ir"},
		{"pipeline", "llvm-ir"},
		{"run", "binary"},
		{"test", "binary"},
		{"build", "binary"},
	}
	for _, c := range cases {
		if got := DefaultEmitMode(c.tool); got != c.want {
			t.Errorf("DefaultEmitMode(%q) = %q, want %q", c.tool, got, c.want)
		}
	}
}

func TestToolEmitCompatHappyPath(t *testing.T) {
	cases := []struct {
		tool, backend, emit string
	}{
		{"gen", "go", "go"},
		{"gen", "llvm", "llvm-ir"},
		{"pipeline", "go", "go"},
		{"pipeline", "llvm", "llvm-ir"},
		{"run", "llvm", "binary"},
		{"test", "llvm", "binary"},
		{"build", "llvm", "binary"},
	}
	for _, c := range cases {
		if d := ToolEmitCompat(c.tool, c.backend, c.emit); d != nil {
			t.Errorf("ToolEmitCompat(%q, %q, %q) = %+v, want nil", c.tool, c.backend, c.emit, d)
		}
	}
}

func TestToolEmitCompatGenGoMustEmitGo(t *testing.T) {
	d := ToolEmitCompat("gen", "go", "llvm-ir")
	if d == nil {
		t.Fatal("expected diag, got nil")
	}
	if d.Code != "R0010" {
		t.Errorf("code = %q, want R0010", d.Code)
	}
	want := `gen with backend "go" cannot emit "llvm-ir" (want "go")`
	if d.Message != want {
		t.Errorf("message = %q, want %q", d.Message, want)
	}
}

func TestToolEmitCompatGenLlvmMustEmitIr(t *testing.T) {
	d := ToolEmitCompat("pipeline", "llvm", "binary")
	if d == nil {
		t.Fatal("expected diag, got nil")
	}
	if d.Code != "R0011" {
		t.Errorf("code = %q, want R0011", d.Code)
	}
	want := `pipeline with backend "llvm" cannot emit "binary" (want "llvm-ir")`
	if d.Message != want {
		t.Errorf("message = %q, want %q", d.Message, want)
	}
}

func TestToolEmitCompatRunNeedsBinary(t *testing.T) {
	for _, tool := range []string{"run", "test"} {
		d := ToolEmitCompat(tool, "llvm", "llvm-ir")
		if d == nil {
			t.Fatalf("%s: expected diag, got nil", tool)
		}
		if d.Code != "R0012" {
			t.Errorf("%s: code = %q, want R0012", tool, d.Code)
		}
		want := tool + ` requires --emit="binary"`
		if d.Message != want {
			t.Errorf("%s: message = %q, want %q", tool, d.Message, want)
		}
	}
}
