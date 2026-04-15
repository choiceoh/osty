// Package testgen generates an executable Go test harness from a
// type-checked Osty package. It completes the Phase-1 `osty test`
// pipeline: given a resolve.Package plus the set of discovered
// `test*` / `bench*` functions, it produces two Go source files —
// main.go (the merged user code + synthetic main) and harness.go
// (the fixed runtime that std.testing binds against) — which, when
// compiled together with `go run`, execute every test and print a
// pass/fail summary.
//
// Flow:
//
//  1. For each PackageFile, invoke gen.Generate. Each result is a
//     complete Go file with its own `package main` clause, imports,
//     and (when applicable) a copy of the Result<T, E> runtime type.
//
//  2. Parse every result with go/parser, then merge them into one
//     ast.File: dedupe imports, keep the first Result<T, E>
//     definition seen, and strip the no-op `var testing = struct{…}{…}`
//     stub that gen emits for `use std.testing`. The resulting
//     merged ast.File is printed back out as main.go.
//
//  3. Append a synthetic main() that iterates every discovered test
//     function (by name), wraps each call in defer/recover through
//     the harness, and prints a summary before exiting with the
//     appropriate code.
//
//  4. Emit a separate harness.go next to main.go that defines the
//     testing runtime struct, the _TestHarness type, and the helper
//     reflection routines. Keeping it in its own file means its
//     imports (reflect, fmt, os) don't have to be merged into the
//     generated code's import block.
//
// The emitted sources have no runtime dependency on anything under
// github.com/osty/osty and compile with a stock `go run`.
package testgen

import (
	"bytes"
	"fmt"
	goast "go/ast"
	goparser "go/parser"
	goprinter "go/printer"
	gotoken "go/token"
	"sort"
	"strings"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/gen"
	"github.com/osty/osty/internal/resolve"
)

// EntryKind identifies how the harness should invoke an entry — as a
// regular test (recorded in pass/fail totals) or as a benchmark
// (currently only invoked once; iteration counts and timing are
// future work, per LANG_SPEC_v0.3 §11.4).
type EntryKind int

const (
	KindTest EntryKind = iota
	KindBench
)

// Entry describes one discovered test or benchmark in the package.
// File is the basename the entry was declared in so failure output
// can point the user at the right source file; Line is the 1-based
// declaration line.
type Entry struct {
	Name string
	Kind EntryKind
	File string
	Line int
}

// Sources bundles the two Go files the harness generator produces.
// Callers typically write both into the same directory and invoke
// `go run .` (or `go run main.go harness.go`) against it.
type Sources struct {
	// Main is the merged Osty-generated Go source plus the synthetic
	// `func main()` that iterates discovered test entries.
	Main []byte
	// Harness is the fixed testing runtime — `var testing`, the
	// _TestHarness type, and the reflection helpers it depends on.
	// Byte-identical across runs; callers may cache the contents.
	Harness []byte
}

// GenerateHarness produces a Sources bundle for pkg. chk is the
// per-package check result; entries is the flat list of discovered
// tests and benchmarks (in declaration order across files).
//
// The returned Main is gofmt-formatted when the merge succeeds. On
// a non-fatal gen error the raw pre-format bytes are returned
// alongside the error so callers can inspect what was produced.
func GenerateHarness(pkg *resolve.Package, chk *check.Result, entries []Entry) (Sources, error) {
	if pkg == nil {
		return Sources{}, fmt.Errorf("testgen: nil package")
	}
	if chk == nil {
		chk = &check.Result{}
	}

	// Stable order so repeated runs produce byte-identical output
	// and the summary lists tests in source-declaration order.
	files := append([]*resolve.PackageFile(nil), pkg.Files...)
	sort.SliceStable(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})

	fset := gotoken.NewFileSet()
	merged := &goast.File{Name: goast.NewIdent("main")}
	importSet := map[string]bool{}
	var importSpecs []goast.Spec
	seenResult := false
	seenRange := false
	var firstGenErr error

	for _, pf := range files {
		if pf.File == nil {
			continue
		}
		res := &resolve.Result{
			Refs:      pf.Refs,
			TypeRefs:  pf.TypeRefs,
			FileScope: pf.FileScope,
		}
		src, gerr := gen.GenerateMapped("main", pf.File, res, chk, pf.Path)
		if gerr != nil && firstGenErr == nil {
			// Non-fatal: gen returns warnings alongside formatted
			// source. Remember the first one so the CLI can surface
			// it, but keep merging so the clean portions still run.
			firstGenErr = fmt.Errorf("gen %s: %w", pf.Path, gerr)
		}
		parsed, perr := goparser.ParseFile(fset, pf.Path, src, goparser.ParseComments)
		if perr != nil {
			return Sources{Main: src}, fmt.Errorf("testgen: parse emitted Go for %s: %w", pf.Path, perr)
		}
		for _, d := range parsed.Decls {
			if gd, ok := d.(*goast.GenDecl); ok && gd.Tok == gotoken.IMPORT {
				for _, sp := range gd.Specs {
					is, ok := sp.(*goast.ImportSpec)
					if !ok || is.Path == nil {
						continue
					}
					key := is.Path.Value
					if is.Name != nil {
						key = is.Name.Name + " " + is.Path.Value
					}
					if importSet[key] {
						continue
					}
					importSet[key] = true
					// Zero the position so the printer emits all
					// imports in one tidy block at the top.
					is.EndPos = 0
					importSpecs = append(importSpecs, is)
				}
				continue
			}
			if isResultRuntime(d) {
				if seenResult {
					continue
				}
				seenResult = true
			}
			if isRangeRuntime(d) {
				if seenRange {
					continue
				}
				seenRange = true
			}
			if isTestingStub(d) {
				// Dropped — replaced by the runtime in harness.go.
				continue
			}
			merged.Decls = append(merged.Decls, d)
		}
	}

	if len(importSpecs) > 0 {
		merged.Decls = append([]goast.Decl{&goast.GenDecl{
			Tok:    gotoken.IMPORT,
			Lparen: 1,
			Specs:  importSpecs,
			Rparen: 2,
		}}, merged.Decls...)
	}

	var body bytes.Buffer
	if err := goprinter.Fprint(&body, fset, merged); err != nil {
		return Sources{}, fmt.Errorf("testgen: print merged: %w", err)
	}

	var main bytes.Buffer
	main.WriteString("// Code generated by osty test. DO NOT EDIT.\n\n")
	main.Write(body.Bytes())
	main.WriteString("\n")
	main.WriteString(renderMain(entries))

	return Sources{
		Main:    main.Bytes(),
		Harness: []byte(harnessSource),
	}, firstGenErr
}

// isResultRuntime reports whether d is the Result[T, E] type-spec
// gen emits at file-top when a file uses Result. Matched structurally
// so cosmetic changes to the gen template don't break the detector.
func isResultRuntime(d goast.Decl) bool {
	gd, ok := d.(*goast.GenDecl)
	if !ok || gd.Tok != gotoken.TYPE || len(gd.Specs) != 1 {
		return false
	}
	ts, ok := gd.Specs[0].(*goast.TypeSpec)
	if !ok || ts.Name == nil || ts.Name.Name != "Result" {
		return false
	}
	if ts.TypeParams == nil || len(ts.TypeParams.List) != 2 {
		return false
	}
	_, isStruct := ts.Type.(*goast.StructType)
	return isStruct
}

// isRangeRuntime reports whether d is the Range struct gen emits
// for standalone range-literal values. Shape-only match (name +
// struct body) for the same reason as isResultRuntime.
func isRangeRuntime(d goast.Decl) bool {
	gd, ok := d.(*goast.GenDecl)
	if !ok || gd.Tok != gotoken.TYPE || len(gd.Specs) != 1 {
		return false
	}
	ts, ok := gd.Specs[0].(*goast.TypeSpec)
	if !ok || ts.Name == nil || ts.Name.Name != "Range" {
		return false
	}
	_, isStruct := ts.Type.(*goast.StructType)
	return isStruct
}

// isTestingStub reports whether d is the no-op `var testing =
// struct{…}{…}` gen emits for `use std.testing`. Matched by var
// name + composite-literal shape; the stub has no real behaviour so
// dropping it loses nothing.
func isTestingStub(d goast.Decl) bool {
	gd, ok := d.(*goast.GenDecl)
	if !ok || gd.Tok != gotoken.VAR || len(gd.Specs) != 1 {
		return false
	}
	vs, ok := gd.Specs[0].(*goast.ValueSpec)
	if !ok || len(vs.Names) != 1 || vs.Names[0].Name != "testing" {
		return false
	}
	if len(vs.Values) != 1 {
		return false
	}
	cl, ok := vs.Values[0].(*goast.CompositeLit)
	if !ok {
		return false
	}
	_, ok = cl.Type.(*goast.StructType)
	return ok
}

// renderMain emits the synthetic main() that drives the discovered
// entries. Each test is wrapped in its own defer/recover inside the
// harness so one failure doesn't abort the suite; benchmarks run
// once (iteration counting lands with spec §11.4).
func renderMain(entries []Entry) string {
	var b strings.Builder
	b.WriteString("func main() {\n")
	b.WriteString("\t_harness := &_TestHarness{}\n")
	for _, e := range entries {
		kind := "_kindTest"
		if e.Kind == KindBench {
			kind = "_kindBench"
		}
		fmt.Fprintf(&b, "\t_harness.run(%q, %s, %s)\n", e.Name, kind, e.Name)
	}
	b.WriteString("\t_harness.summary()\n")
	b.WriteString("}\n")
	return b.String()
}

// harnessSource is the fixed runtime written alongside main.go. It
// keeps its own import block (reflect/fmt/os) so the merged main.go
// doesn't have to plumb them through the per-file import dedupe.
const harnessSource = `// Code generated by osty test. DO NOT EDIT.
//
// Runtime for the osty test harness: the package-level ` + "`testing`" + `
// variable that std.testing calls bind against, plus the
// _TestHarness type main() uses to drive each discovered entry.

package main

import (
	"fmt"
	"os"
	"reflect"
	"strings"
)

// _failure is the sentinel panic payload every std.testing assertion
// helper raises on a failed check. The harness recovers these
// specifically and attributes them to the enclosing test; any other
// panic is reported as "panic: ..." so misbehaving code surfaces too.
type _failure struct {
	msg string
}

func (f *_failure) Error() string { return f.msg }

// _ctxStack records the active testing.context labels so a failure
// inside a nested context gets the full path prefixed to its error
// message.
var _ctxStack []string

// testing is the runtime surface gen's ` + "`use std.testing`" + ` stub would
// otherwise emit as no-ops. Field names and signatures mirror the
// Phase-1 gen expectations so user calls compile against the real
// implementations here.
var testing = struct {
	assert      func(bool)
	assertEq    func(any, any)
	assertNe    func(any, any)
	expectOk    func(any) any
	expectError func(any) any
	context     func(string, func())
	fail        func(string)
}{
	assert: func(cond bool) {
		if !cond {
			panic(&_failure{msg: "assertion failed"})
		}
	},
	assertEq: func(actual, expected any) {
		if !reflect.DeepEqual(actual, expected) {
			panic(&_failure{msg: fmt.Sprintf("assertEq failed: actual=%#v, expected=%#v", actual, expected)})
		}
	},
	assertNe: func(actual, expected any) {
		if reflect.DeepEqual(actual, expected) {
			panic(&_failure{msg: fmt.Sprintf("assertNe failed: both=%#v", actual)})
		}
	},
	expectOk: func(r any) any {
		v, ok := _unwrapResult(r)
		if !ok {
			panic(&_failure{msg: "expectOk: got Err"})
		}
		return v
	},
	expectError: func(r any) any {
		_, ok := _unwrapResult(r)
		if ok {
			panic(&_failure{msg: "expectError: got Ok"})
		}
		return nil
	},
	context: func(label string, body func()) {
		_ctxStack = append(_ctxStack, label)
		defer func() { _ctxStack = _ctxStack[:len(_ctxStack)-1] }()
		body()
	},
	fail: func(msg string) {
		panic(&_failure{msg: msg})
	},
}

// _unwrapResult peels a Result[T, E] payload without the harness
// needing to know the concrete type params: gen emits Result as
// ` + "`struct{ Value T; Error E; IsOk bool }`" + `, so we just look up
// those field names via reflect.
func _unwrapResult(r any) (any, bool) {
	v := reflect.ValueOf(r)
	if v.Kind() != reflect.Struct {
		return nil, false
	}
	okField := v.FieldByName("IsOk")
	valField := v.FieldByName("Value")
	if !okField.IsValid() || !valField.IsValid() {
		return nil, false
	}
	if !okField.Bool() {
		return nil, false
	}
	return valField.Interface(), true
}

type _testKind int

const (
	_kindTest _testKind = iota
	_kindBench
)

type _testRecord struct {
	name   string
	kind   _testKind
	failed bool
	err    string
}

// _TestHarness accumulates per-entry results so summary() can print
// totals and exit with the right code. The zero value is ready to
// use; main() constructs one and feeds it every discovered test.
type _TestHarness struct {
	records []_testRecord
}

// run invokes fn under defer/recover so a failed assertion or stray
// panic becomes a recorded result rather than a crashed process.
// Context labels that were active when the panic fired are prefixed
// onto the error message.
func (h *_TestHarness) run(name string, kind _testKind, fn func()) {
	rec := _testRecord{name: name, kind: kind}
	_ctxStack = _ctxStack[:0]
	func() {
		defer func() {
			if r := recover(); r != nil {
				rec.failed = true
				switch v := r.(type) {
				case *_failure:
					rec.err = v.msg
				case error:
					rec.err = "panic: " + v.Error()
				default:
					rec.err = fmt.Sprintf("panic: %v", r)
				}
				if len(_ctxStack) > 0 {
					rec.err = "[" + strings.Join(_ctxStack, " > ") + "] " + rec.err
				}
			}
		}()
		fn()
	}()
	h.records = append(h.records, rec)

	label := "TEST"
	if kind == _kindBench {
		label = "BENCH"
	}
	if rec.failed {
		fmt.Printf("%s %s ... FAIL\n      %s\n", label, name, rec.err)
	} else {
		fmt.Printf("%s %s ... ok\n", label, name)
	}
}

// summary prints the pass/fail banner and exits with code 1 when any
// test failed. Benchmarks count toward the totals but aren't gated
// on a pass/fail verdict — reaching summary means the benchmark body
// returned without panicking, which is itself a smoke-level success.
func (h *_TestHarness) summary() {
	passed, failed := 0, 0
	for _, r := range h.records {
		if r.failed {
			failed++
		} else {
			passed++
		}
	}
	fmt.Printf("\n%d passed, %d failed, %d total\n", passed, failed, len(h.records))
	if failed > 0 {
		os.Exit(1)
	}
}
`
