package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/diag"
)

func TestValidateUsesSelfHostedManifestCore(t *testing.T) {
	src := []byte(`
[package]
name = "demo"
version = "1.2.3"
edition = "0.5"

[dependencies]
ok = "^1.0"
bad = "1.*.3"
`)
	m, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	diags := Validate(m)
	if got := countManifestCode(diags, diag.CodeManifestBadDepSpec); got != 1 {
		t.Fatalf("bad dep diagnostics = %d, want 1; diags = %#v", got, diags)
	}
}

func TestValidateSelfHostedPackageAndTargetRules(t *testing.T) {
	src := []byte(`
[package]
name = "1bad"
version = "v1.2.3"
edition = "9.9"

[target.amd64]
`)
	m, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	diags := Validate(m)
	for _, code := range []string{
		diag.CodeManifestBadName,
		diag.CodeManifestBadVersion,
		diag.CodeManifestBadEdition,
		diag.CodeManifestBadDepSpec,
	} {
		if got := countManifestCode(diags, code); got != 1 {
			t.Fatalf("diagnostics for %s = %d, want 1; diags = %#v", code, got, diags)
		}
	}
}

func TestValidateWrapsSelfHostedDiagnosticDetails(t *testing.T) {
	src := []byte(`
[package]
name = "demo"
version = "1.2.3"
`)
	m, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	d := findManifestCode(Validate(m), diag.CodeManifestMissingField)
	if d == nil {
		t.Fatalf("missing edition diagnostic not found")
	}
	if d.Severity != diag.Warning {
		t.Fatalf("severity = %v, want warning", d.Severity)
	}
	if got := d.PrimaryPos().Line; got != 2 {
		t.Fatalf("line = %d, want package table line 2", got)
	}
	if d.Hint != `add edition = "0.5" to pin the spec version` {
		t.Fatalf("hint = %q", d.Hint)
	}
}

func TestParseDefersManifestSemanticsToSelfHostedValidation(t *testing.T) {
	m, err := Parse([]byte(`
[package]
edition = "0.5"
`))
	if err != nil {
		t.Fatal(err)
	}
	diags := Validate(m)
	if got := countManifestCode(diags, diag.CodeManifestMissingField); got != 2 {
		t.Fatalf("missing field diagnostics = %d, want 2; diags = %#v", got, diags)
	}
}

func TestReadRejectsSelfHostedValidationErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), ManifestFile)
	if err := os.WriteFile(path, []byte(`
[package]
edition = "0.5"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Read(path)
	if err == nil {
		t.Fatal("Read succeeded, want validation error")
	}
	if !strings.Contains(err.Error(), "missing required field `name`") {
		t.Fatalf("error = %q, want missing name validation error", err)
	}
}

// TestParseTargetLinkArray covers the v0.5 `[target.<triple>].link`
// list-of-strings — system libraries the linker pulls in for `use c
// "..."` extern symbols (LANG_SPEC §12.8). Source order is preserved
// so manifest authors can express link order when it matters.
func TestParseTargetLinkArray(t *testing.T) {
	src := []byte(`
[package]
name = "demo"
version = "1.0.0"
edition = "0.5"

[target.amd64-linux]
cgo = false
link = ["m", "pthread", "osty_demo"]
`)
	m, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(m.Targets) != 1 {
		t.Fatalf("targets = %d, want 1", len(m.Targets))
	}
	tgt := m.Targets[0]
	if got, want := tgt.Triple, "amd64-linux"; got != want {
		t.Fatalf("triple = %q, want %q", got, want)
	}
	if got, want := tgt.Link, []string{"m", "pthread", "osty_demo"}; !equalStringSlices(got, want) {
		t.Fatalf("link = %v, want %v", got, want)
	}
}

func TestParseTargetLinkRejectsNonString(t *testing.T) {
	src := []byte(`
[package]
name = "demo"
version = "1.0.0"
edition = "0.5"

[target.amd64-linux]
link = ["m", 42]
`)
	_, err := Parse(src)
	if err == nil {
		t.Fatal("Parse succeeded, want error for non-string link entry")
	}
	if !strings.Contains(err.Error(), "target.amd64-linux.link") {
		t.Fatalf("error = %q, want it to mention target.amd64-linux.link", err)
	}
}

func TestParseTargetLinkRejectsNonArray(t *testing.T) {
	src := []byte(`
[package]
name = "demo"
version = "1.0.0"
edition = "0.5"

[target.amd64-linux]
link = "m"
`)
	_, err := Parse(src)
	if err == nil {
		t.Fatal("Parse succeeded, want error for non-array link")
	}
	if !strings.Contains(err.Error(), "must be an array of strings") {
		t.Fatalf("error = %q, want array-of-strings rejection", err)
	}
}

func TestParseCapabilitiesRuntime(t *testing.T) {
	src := []byte(`
[package]
name = "toolchain"
version = "1.0.0"
edition = "0.5"

[capabilities]
runtime = true
`)
	m, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Capabilities == nil || !m.Capabilities.Runtime {
		t.Fatalf("capabilities = %#v, want runtime=true", m.Capabilities)
	}
	out := string(Marshal(m))
	if !strings.Contains(out, "[capabilities]\nruntime = true\n") {
		t.Fatalf("Marshal missing capabilities table:\n%s", out)
	}
}

func TestParseCapabilitiesRejectsNonBool(t *testing.T) {
	_, err := Parse([]byte(`
[package]
name = "demo"
version = "1.0.0"
edition = "0.5"

[capabilities]
runtime = "true"
`))
	if err == nil {
		t.Fatal("Parse succeeded, want non-bool capabilities.runtime error")
	}
	if !strings.Contains(err.Error(), "capabilities.runtime") {
		t.Fatalf("error = %q, want capabilities.runtime", err)
	}
}

func TestParseGuiWebView2Section(t *testing.T) {
	src := []byte(`
[package]
name = "demo"
version = "1.0.0"
edition = "0.5"

[gui]
backend = "webview2"
entry = "ui/index.html"

[gui.webview2]
devtools = true
runtime = "evergreen"
`)
	m, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.GUI == nil {
		t.Fatal("GUI = nil, want parsed [gui] section")
	}
	if m.GUI.Backend != "webview2" || m.GUI.Entry != "ui/index.html" {
		t.Fatalf("GUI = %#v, want webview2 ui/index.html", m.GUI)
	}
	if m.GUI.WebView2 == nil || !m.GUI.WebView2.DevTools || !m.GUI.WebView2.HasDevTools || m.GUI.WebView2.Runtime != "evergreen" {
		t.Fatalf("GUI.WebView2 = %#v, want devtools evergreen", m.GUI.WebView2)
	}
	out := string(Marshal(m))
	for _, want := range []string{
		"[gui]\nbackend = \"webview2\"\nentry = \"ui/index.html\"\n",
		"[gui.webview2]\ndevtools = true\nruntime = \"evergreen\"\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("Marshal missing %q:\n%s", want, out)
		}
	}
}

func TestParseGuiWebView2RejectsBadShape(t *testing.T) {
	_, err := Parse([]byte(`
[package]
name = "demo"
version = "1.0.0"
edition = "0.5"

[gui.webview2]
devtools = "yes"
`))
	if err == nil {
		t.Fatal("Parse succeeded, want non-bool gui.webview2.devtools error")
	}
	if !strings.Contains(err.Error(), "gui.webview2.devtools") {
		t.Fatalf("error = %q, want gui.webview2.devtools", err)
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func countManifestCode(diags []*diag.Diagnostic, code string) int {
	var count int
	for _, d := range diags {
		if d != nil && d.Code == code {
			count++
		}
	}
	return count
}

func findManifestCode(diags []*diag.Diagnostic, code string) *diag.Diagnostic {
	for _, d := range diags {
		if d != nil && d.Code == code {
			return d
		}
	}
	return nil
}
