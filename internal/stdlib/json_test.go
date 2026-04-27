package stdlib

import (
	"strings"
	"testing"
)

func TestJsonGenericWrappersAreLoweringStubs(t *testing.T) {
	reg := LoadCached()
	for _, name := range []string{"encode", "decode"} {
		fn := reg.LookupFnDecl("json", name)
		if fn == nil {
			t.Fatalf("LookupFnDecl(json, %s) = nil, want *ast.FnDecl", name)
		}
		if fn.Body != nil {
			t.Fatalf("json.%s has a source body, want declaration-only lowering stub", name)
		}
	}
	for _, name := range []string{"stringify", "parse"} {
		fn := reg.LookupFnDecl("json", name)
		if fn == nil {
			t.Fatalf("LookupFnDecl(json, %s) = nil, want *ast.FnDecl", name)
		}
		if fn.Body == nil {
			t.Fatalf("json.%s body = nil, want source wrapper body", name)
		}
	}
}

func TestJsonValueStringifierPreservesUTF8Bytes(t *testing.T) {
	src := jsonModuleSource(t)
	if strings.Contains(src, "b.toChar()") {
		t.Fatal("json stringifier must not transcode raw UTF-8 bytes through Byte.toChar")
	}
	for _, want := range []string{
		"out.push(b)",
		"bytes.from(out).toString().unwrap()",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("json stringifier source missing %q", want)
		}
	}
}

func TestJsonValueObjectStringifierIsDeterministic(t *testing.T) {
	src := jsonModuleSource(t)
	if !strings.Contains(src, "obj.keys().sorted()") {
		t.Fatal("json object stringifier must sort object keys before emission")
	}
}

func TestJsonValueParserRejectsNonFiniteNumbers(t *testing.T) {
	src := jsonModuleSource(t)
	for _, want := range []string{
		"n.isNaN() || n.isInfinite()",
		"json: number out of range",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("json number parser source missing %q", want)
		}
	}
}

func jsonModuleSource(t *testing.T) string {
	t.Helper()
	reg := LoadCached()
	mod := reg.Modules["json"]
	if mod == nil {
		t.Fatal("stdlib json module missing")
	}
	return string(mod.Source)
}
