package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
)

func TestIterModuleSurface(t *testing.T) {
	reg := LoadCached()
	if reg.Modules["iter"] == nil {
		t.Fatal("stdlib iter module missing")
	}
	for _, d := range reg.Diags {
		if d != nil && strings.Contains(d.Error(), "modules/iter.osty") {
			t.Fatalf("stdlib iter diagnostic: %s", d.Error())
		}
	}

	for _, name := range []string{"from", "empty", "range"} {
		fn := reg.LookupFnDecl("iter", name)
		if fn == nil {
			t.Fatalf("LookupFnDecl(iter, %s) = nil, want stdlib helper", name)
		}
		if fn.Body == nil {
			t.Fatalf("iter.%s body = nil, want pure-Osty helper body", name)
		}
	}

	for _, name := range []string{
		"map",
		"filter",
		"inspect",
		"filterMap",
		"mapWhile",
		"flatMap",
		"take",
		"takeLast",
		"takeWhile",
		"skip",
		"skipLast",
		"skipWhile",
		"stepBy",
		"chain",
		"intersperse",
		"reversed",
		"rev",
		"enumerate",
		"zip",
		"zip3",
		"chunked",
		"chunks",
		"windowed",
		"windows",
		"toList",
		"collect",
		"count",
		"countWhere",
		"isEmpty",
		"first",
		"last",
		"single",
		"nth",
		"contains",
		"find",
		"lastWhere",
		"findMap",
		"indexWhere",
		"lastIndexWhere",
		"any",
		"all",
		"forEach",
		"fold",
		"reduce",
		"scan",
		"partition",
	} {
		fn := reg.LookupMethodDecl("iter", "Iter", name)
		if fn == nil {
			t.Fatalf("LookupMethodDecl(iter, Iter, %s) = nil, want stdlib helper; methods=%s",
				name, strings.Join(iterMethodNames(t), ", "))
		}
		if fn.Body == nil {
			t.Fatalf("iter.Iter.%s body = nil, want pure-Osty helper body", name)
		}
	}
}

func TestIterHelpersArePinned(t *testing.T) {
	src := iterModuleSource(t)
	for _, want := range []string{
		"pub fn inspect(self, f: fn(T) -> ()) -> Iter<T>",
		"pub fn filterMap<U>(self, f: fn(T) -> U?) -> Iter<U>",
		"match f(item)",
		"pub fn mapWhile<U>(self, f: fn(T) -> U?) -> Iter<U>",
		"if mapped.isNone()",
		"pub fn flatMap<U>(self, f: fn(T) -> Iter<U>) -> Iter<U>",
		"for value in mapped.items",
		"pub fn takeLast(self, n: Int) -> Iter<T>",
		"pub fn skipLast(self, n: Int) -> Iter<T>",
		"pub fn skipWhile(self, f: fn(T) -> Bool) -> Iter<T>",
		"pub fn stepBy(self, step: Int) -> Iter<T>",
		"Iter.stepBy: step must be positive",
		"pub fn intersperse(self, separator: T) -> Iter<T>",
		"pub fn reversed(self) -> Iter<T>",
		"pub fn rev(self) -> Iter<T>",
		"pub fn enumerate(self) -> Iter<(Int, T)>",
		"pub fn zip<U>(self, other: Iter<U>) -> Iter<(T, U)>",
		"pub fn zip3<U, V>(self, other1: Iter<U>, other2: Iter<V>) -> Iter<(T, U, V)>",
		"pub fn chunked(self, size: Int) -> Iter<List<T>>",
		"pub fn chunks(self, size: Int) -> Iter<List<T>>",
		"pub fn windowed(self, size: Int, step: Int) -> Iter<List<T>>",
		"pub fn windows(self, size: Int, step: Int) -> Iter<List<T>>",
		"out.push(self.items.drop(start).take(size))",
		"pub fn collect(self) -> List<T>",
		"pub fn countWhere(self, f: fn(T) -> Bool) -> Int",
		"pub fn single(self) -> T?",
		"pub fn nth(self, n: Int) -> T?",
		"pub fn contains(self, item: T) -> Bool",
		"pub fn lastWhere(self, f: fn(T) -> Bool) -> T?",
		"pub fn findMap<U>(self, f: fn(T) -> U?) -> U?",
		"pub fn indexWhere(self, f: fn(T) -> Bool) -> Int?",
		"pub fn lastIndexWhere(self, f: fn(T) -> Bool) -> Int?",
		"pub fn any(self, f: fn(T) -> Bool) -> Bool",
		"pub fn all(self, f: fn(T) -> Bool) -> Bool",
		"pub fn reduce(self, f: fn(T, T) -> T) -> T?",
		"pub fn scan<U>(self, init: U, f: fn(U, T) -> U) -> Iter<U>",
		"pub fn partition(self, f: fn(T) -> Bool) -> (List<T>, List<T>)",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("iter module source missing %q", want)
		}
	}
}

func TestIterImportResolvesPipelineHelpers(t *testing.T) {
	src := `
use std.iter

pub fn demo(xs: List<Int>, labels: List<String>) -> List<Int> {
    let scanned = iter.from(xs)
        .filter(|n| n >= 0)
        .inspect(|n| { let _ = n })
        .flatMap(|n| iter.from([n, n * 10]))
        .skipWhile(|n| n < 3)
        .stepBy(2)
        .take(4)
        .scan(0, |acc, n| acc + n)
        .collect()
    let _ = iter.from(xs).filterMap(|n| if n > 0 { Some(n.toString()) } else { None }).count()
    let _ = iter.from(xs).mapWhile(|n| if n < 100 { Some(n * 2) } else { None }).toList()
    let _ = iter.from(xs).takeLast(2).skipLast(1).toList()
    let _ = iter.from(xs).reversed().rev().intersperse(0).toList()
    let _ = iter.from(xs).enumerate().count()
    let _ = iter.from(xs).zip(iter.from(labels)).count()
    let _ = iter.from(xs).zip3(iter.from(labels), iter.from(xs)).count()
    let _ = iter.from(xs).chunked(2).count()
    let _ = iter.from(xs).chunks(2).count()
    let _ = iter.from(xs).windowed(2, 1).count()
    let _ = iter.from(xs).windows(2, 1).count()
    let _ = iter.from(xs).countWhere(|n| n > 1)
    let _ = iter.from(xs).single()
    let _ = iter.from(xs).nth(1)
    let _ = iter.from(xs).contains(3)
    let _ = iter.from(xs).find(|n| n > 10)
    let _ = iter.from(xs).lastWhere(|n| n > 10)
    let _ = iter.from(xs).findMap(|n| if n > 0 { Some(n.toString()) } else { None })
    let _ = iter.from(xs).indexWhere(|n| n > 10)
    let _ = iter.from(xs).lastIndexWhere(|n| n > 10)
    let _ = iter.from(xs).any(|n| n == 0)
    let _ = iter.from(xs).all(|n| n >= 0)
    let _ = iter.from(xs).reduce(|a, b| a + b)
    let _ = iter.from(xs).partition(|n| n % 2 == 0)
    scanned
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
		t.Errorf("resolver rejected std.iter fixture: %s: %s", d.Code, d.Message)
	}
}

func iterModuleSource(t *testing.T) string {
	t.Helper()
	reg := LoadCached()
	mod := reg.Modules["iter"]
	if mod == nil {
		t.Fatal("stdlib iter module missing")
	}
	return string(mod.Source)
}

func iterMethodNames(t *testing.T) []string {
	t.Helper()
	reg := LoadCached()
	mod := reg.Modules["iter"]
	if mod == nil || mod.File == nil {
		return nil
	}
	var names []string
	for _, decl := range mod.File.Decls {
		structDecl, ok := decl.(*ast.StructDecl)
		if !ok || structDecl.Name != "Iter" {
			continue
		}
		for _, method := range structDecl.Methods {
			if method != nil {
				names = append(names, method.Name)
			}
		}
	}
	return names
}
