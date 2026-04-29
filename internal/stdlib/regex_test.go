package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
)

func TestRegexModuleSurface(t *testing.T) {
	reg := LoadCached()
	if reg.Modules["regex"] == nil {
		t.Fatal("stdlib regex module missing")
	}

	for _, name := range []string{
		"matches",
		"find",
		"findAll",
		"captures",
		"capturesAll",
		"replace",
		"replaceAll",
		"split",
	} {
		fn := reg.LookupFnDecl("regex", name)
		if fn == nil {
			t.Fatalf("LookupFnDecl(regex, %s) = nil, want one-shot helper", name)
		}
		if fn.Body == nil {
			t.Fatalf("regex.%s body = nil, want pure-Osty compile-and-apply wrapper", name)
		}
	}

	compile := reg.LookupFnDecl("regex", "compile")
	if compile == nil {
		t.Fatal("LookupFnDecl(regex, compile) = nil, want runtime bridge declaration")
	}
	if compile.Body != nil {
		t.Fatal("regex.compile has a source body, want runtime bridge declaration")
	}

	for _, tc := range []struct {
		typeName string
		method   string
		bodied   bool
	}{
		{"Regex", "matches", false},
		{"Regex", "find", false},
		{"Regex", "findAll", false},
		{"Regex", "captures", false},
		{"Regex", "capturesAll", false},
		{"Regex", "replace", false},
		{"Regex", "replaceAll", false},
		{"Regex", "split", false},
		{"RegexError", "message", true},
		{"Captures", "get", false},
		{"Captures", "named", false},
	} {
		fn := reg.LookupMethodDecl("regex", tc.typeName, tc.method)
		if fn == nil {
			t.Fatalf("LookupMethodDecl(regex, %s, %s) = nil", tc.typeName, tc.method)
		}
		if tc.bodied && fn.Body == nil {
			t.Fatalf("regex.%s.%s body = nil, want source method body", tc.typeName, tc.method)
		}
		if !tc.bodied && fn.Body != nil {
			t.Fatalf("regex.%s.%s has a source body, want runtime bridge declaration", tc.typeName, tc.method)
		}
	}
}

func TestRegexOneShotCaptureHelpersArePinned(t *testing.T) {
	src := regexModuleSource(t)
	for _, want := range []string{
		"pub fn captures(text: String, pattern: String) -> Result<Captures?, RegexError>",
		"Ok(re.captures(text))",
		"pub fn capturesAll(text: String, pattern: String) -> Result<List<Captures>, RegexError>",
		"Ok(re.capturesAll(text))",
		"pub fn message(self) -> String",
		"self.message",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("regex module source missing %q", want)
		}
	}
}

func TestRegexImportResolvesOneShotHelpers(t *testing.T) {
	src := `
use std.regex

pub fn demo(text: String) -> Result<String, regex.RegexError> {
    let re = regex.compile(r"(?P<word>\w+)")?
    let ok = regex.matches(text, r"\w+")?
    let first = regex.captures(text, r"(?P<word>\w+)")?
    let all = regex.capturesAll(text, r"(?P<word>\w+)")?
    let words = re.findAll(text)
    let pieces = regex.split(text, r"\s+")?
    let cleaned = regex.replaceAll(text, r"\s+", " ")?
    match first {
        Some(caps) -> {
            let head = caps.get(0) ?? "missing"
            Ok("{ok}:{all.len()}:{words.len()}:{pieces.len()}:{head}:{cleaned}")
        },
        None -> Ok("{ok}:0"),
    }
}
`
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	if len(parseDiags) != 0 {
		t.Fatalf("parse diagnostics: %v", parseDiags)
	}
	res := resolve.ResolveFileSourceDefault([]byte(src), file, Load())
	for _, d := range res.Diags {
		if d == nil || d.Severity != diag.Error {
			continue
		}
		t.Errorf("resolver rejected std.regex fixture: %s: %s", d.Code, d.Message)
	}
}

func regexModuleSource(t *testing.T) string {
	t.Helper()
	reg := LoadCached()
	mod := reg.Modules["regex"]
	if mod == nil {
		t.Fatal("stdlib regex module missing")
	}
	return string(mod.Source)
}
