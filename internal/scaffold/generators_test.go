package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/diag"
)

// ---- fixture ----

// TestRenderFixtureShape: the generated file contains the canonical
// table-test pieces (Case struct, table-builder fn, driver test fn)
// and includes exactly the requested number of rows.
func TestRenderFixtureShape(t *testing.T) {
	src, d := RenderFixture(FixtureOptions{Name: "user", Cases: 2})
	if d != nil {
		t.Fatalf("RenderFixture: %s", d.Error())
	}
	wants := []string{
		"struct UserCase {",
		"fn userCases() -> List<UserCase>",
		"fn testUserTable()",
		`name: "case1"`,
		`name: "case2"`,
	}
	for _, w := range wants {
		if !strings.Contains(src, w) {
			t.Errorf("fixture missing %q\n%s", w, src)
		}
	}
	if strings.Contains(src, `name: "case3"`) {
		t.Errorf("Cases=2 produced a case3 row:\n%s", src)
	}
}

// TestRenderFixtureDefaultCases: zero / negative cases default to 3
// (the documented value), so users don't have to pass `--cases` for
// a sensible starter.
func TestRenderFixtureDefaultCases(t *testing.T) {
	src, d := RenderFixture(FixtureOptions{Name: "thing"})
	if d != nil {
		t.Fatalf("RenderFixture: %s", d.Error())
	}
	for _, n := range []string{"case1", "case2", "case3"} {
		if !strings.Contains(src, n) {
			t.Errorf("default fixture missing %q", n)
		}
	}
}

// TestRenderFixtureRejectsBadName: invalid identifiers fail before
// any source is rendered, with the same code Create / Init use.
func TestRenderFixtureRejectsBadName(t *testing.T) {
	_, d := RenderFixture(FixtureOptions{Name: "1bad"})
	if d == nil || d.Code != diag.CodeScaffoldInvalidName {
		t.Fatalf("expected invalid-name diagnostic, got %v", d)
	}
}

// TestRenderFixtureCapsCaseCount: refuse pathological --cases values
// rather than producing a multi-megabyte file.
func TestRenderFixtureCapsCaseCount(t *testing.T) {
	_, d := RenderFixture(FixtureOptions{Name: "x", Cases: 65})
	if d == nil {
		t.Fatalf("expected diagnostic for over-cap cases, got nil")
	}
}

// TestWriteFixtureRefusesOverwrite: WriteFixture must never clobber
// an existing file — it is a scaffolding tool, not an editor.
func TestWriteFixtureRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	if _, d := WriteFixture(dir, FixtureOptions{Name: "user"}); d != nil {
		t.Fatalf("first write: %s", d.Error())
	}
	_, d := WriteFixture(dir, FixtureOptions{Name: "user"})
	if d == nil || d.Code != diag.CodeScaffoldDestExists {
		t.Errorf("second write should refuse, got %v", d)
	}
}

// TestRenderedFixtureCompiles parses the generated _test.osty
// through the Osty front-end. The generator's promise is that the
// output is a buildable starting point — assertCompiles is shared
// with the project-template tests.
func TestRenderedFixtureCompiles(t *testing.T) {
	src, d := RenderFixture(FixtureOptions{Name: "user", Cases: 4})
	if d != nil {
		t.Fatalf("RenderFixture: %s", d.Error())
	}
	assertCompiles(t, []byte(src))
}

// ---- schema ----

// TestRenderSchemaInfersTypes covers the type-inference axis: the
// flat sample exercises every primitive plus the optional, the list,
// and the nested-object cases.
func TestRenderSchemaInfersTypes(t *testing.T) {
	sample := []byte(`{
		"name": "alice",
		"age": 30,
		"score": 4.5,
		"active": true,
		"nickname": null,
		"tags": ["admin", "user"],
		"address": {"city": "Seoul", "zip-code": "12345"}
	}`)
	src, d := RenderSchema(SchemaOptions{Name: "User", Sample: sample})
	if d != nil {
		t.Fatalf("RenderSchema: %s", d.Error())
	}
	wants := []string{
		"pub struct User {",
		"pub name: String,",
		"pub age: Int,",
		"pub score: Float,",
		"pub active: Bool,",
		"pub nickname: Json?,",
		"pub tags: List<String>,",
		"pub address: UserAddress,",
		"pub struct UserAddress {",
		"pub city: String,",
		`#[json(key = "zip-code")]`,
		"pub zipCode: String,",
	}
	for _, w := range wants {
		if !strings.Contains(src, w) {
			t.Errorf("schema missing %q\n---\n%s", w, src)
		}
	}
}

// TestRenderSchemaEmptyArray falls back to List<Json> rather than
// guessing an element type from no evidence.
func TestRenderSchemaEmptyArray(t *testing.T) {
	src, d := RenderSchema(SchemaOptions{Name: "Bag", Sample: []byte(`{"items": []}`)})
	if d != nil {
		t.Fatalf("RenderSchema: %s", d.Error())
	}
	if !strings.Contains(src, "pub items: List<Json>") {
		t.Errorf("expected List<Json> for empty array:\n%s", src)
	}
}

// TestRenderSchemaRejectsNonObjectTopLevel — JSON arrays / scalars
// at the top level can't become a struct, so the generator refuses
// up front.
func TestRenderSchemaRejectsNonObjectTopLevel(t *testing.T) {
	cases := [][]byte{
		[]byte(`[1, 2, 3]`),
		[]byte(`"hello"`),
		[]byte(`42`),
	}
	for _, s := range cases {
		_, d := RenderSchema(SchemaOptions{Name: "X", Sample: s})
		if d == nil {
			t.Errorf("expected rejection for %s", s)
		}
	}
}

// TestRenderSchemaInvalidJSON: surfaces the json package's error
// behind a scaffold diagnostic with a hint pointing at the input.
func TestRenderSchemaInvalidJSON(t *testing.T) {
	_, d := RenderSchema(SchemaOptions{Name: "X", Sample: []byte(`{not json`)})
	if d == nil {
		t.Fatal("expected diagnostic for invalid JSON")
	}
}

// TestRenderedSchemaCompiles routes the generator's output through
// the Osty front-end. Inferred-type sources must always compile or
// the tool is worse than nothing.
func TestRenderedSchemaCompiles(t *testing.T) {
	sample := []byte(`{
		"name": "alice",
		"age": 30,
		"score": 4.5,
		"active": true,
		"tags": ["admin", "user"],
		"address": {"city": "Seoul", "zip": "12345"}
	}`)
	src, d := RenderSchema(SchemaOptions{Name: "User", Sample: sample})
	if d != nil {
		t.Fatalf("RenderSchema: %s", d.Error())
	}
	assertCompiles(t, []byte(src))
}

// TestWriteSchemaRefusesOverwrite mirrors the fixture conflict
// guard.
func TestWriteSchemaRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	opts := SchemaOptions{Name: "User", Sample: []byte(`{"a": 1}`)}
	if _, d := WriteSchema(dir, opts); d != nil {
		t.Fatalf("first write: %s", d.Error())
	}
	if _, d := WriteSchema(dir, opts); d == nil || d.Code != diag.CodeScaffoldDestExists {
		t.Errorf("second write should refuse, got %v", d)
	}
}

// ---- ffi ----

// TestRenderFFIBasicDecls covers the happy path: void, primitive,
// pointer-to-char, and unsigned-int returns/params.
func TestRenderFFIBasicDecls(t *testing.T) {
	header := []byte(`
		int my_open(const char *path, int flags);
		void my_close(int handle);
		double my_compute(double a, double b);
		unsigned int my_count(void);
	`)
	src, d := RenderFFI(FFIOptions{Module: "mylib", Header: header})
	if d != nil {
		t.Fatalf("RenderFFI: %s", d.Error())
	}
	wants := []string{
		"pub fn myOpen(path: String, flags: Int32) -> Int32",
		"pub fn myClose(handle: Int32) -> ()",
		"pub fn myCompute(a: Float64, b: Float64) -> Float64",
		"pub fn myCount() -> UInt32",
		"use std.process",
	}
	for _, w := range wants {
		if !strings.Contains(src, w) {
			t.Errorf("FFI missing %q\n---\n%s", w, src)
		}
	}
}

// TestRenderFFIVariadicUnparsed: variadic decls aren't representable
// in Osty's signature surface, so they belong in the unparsed list
// rather than as a half-correct wrapper.
func TestRenderFFIVariadicUnparsed(t *testing.T) {
	header := []byte(`int logf(const char *fmt, ...);`)
	src, d := RenderFFI(FFIOptions{Module: "logger", Header: header})
	if d != nil {
		t.Fatalf("RenderFFI: %s", d.Error())
	}
	if strings.Contains(src, "pub fn logf") {
		t.Errorf("variadic should not be wrapped:\n%s", src)
	}
	if !strings.Contains(src, "unparsed declarations") {
		t.Errorf("variadic should be flagged as unparsed:\n%s", src)
	}
}

// TestRenderFFIPreprocessorIgnored: `#ifndef` / `#define` / `#include`
// must not get glued onto the next declaration. Regression test for
// a bug that swallowed the first real decl after a preprocessor
// block.
func TestRenderFFIPreprocessorIgnored(t *testing.T) {
	header := []byte(`#ifndef LIB_H
#define LIB_H
#include <stdio.h>
int my_first(int x);
#endif
`)
	src, d := RenderFFI(FFIOptions{Module: "lib", Header: header})
	if d != nil {
		t.Fatalf("RenderFFI: %s", d.Error())
	}
	if !strings.Contains(src, "pub fn myFirst(x: Int32) -> Int32") {
		t.Errorf("first decl after preprocessor block was lost:\n%s", src)
	}
}

// TestRenderFFICommentsStripped: `//` and `/* */` comments are
// removed before parsing so they don't break the declaration regex.
func TestRenderFFICommentsStripped(t *testing.T) {
	header := []byte(`
		// leading line comment
		/* block
		   comment */
		int x(int /* inline */ a);
	`)
	src, d := RenderFFI(FFIOptions{Module: "lib", Header: header})
	if d != nil {
		t.Fatalf("RenderFFI: %s", d.Error())
	}
	if !strings.Contains(src, "pub fn x(a: Int32) -> Int32") {
		t.Errorf("comments interfered with parsing:\n%s", src)
	}
}

// TestRenderFFIEmptyHeader: a header with no recognised decls still
// produces a syntactically-valid file, with an explanatory note
// rather than a zero-byte output.
func TestRenderFFIEmptyHeader(t *testing.T) {
	src, d := RenderFFI(FFIOptions{Module: "lib", Header: []byte(`#define X 1`)})
	if d != nil {
		t.Fatalf("RenderFFI: %s", d.Error())
	}
	if !strings.Contains(src, "No C function declarations were recognised") {
		t.Errorf("expected explanatory note:\n%s", src)
	}
}

// TestRenderedFFICompiles: the wrapper file routes through the Osty
// front-end. Without this guard the generator could ship a file the
// user can't actually run.
func TestRenderedFFICompiles(t *testing.T) {
	header := []byte(`
		int my_open(const char *path, int flags);
		void my_close(int handle);
		double my_compute(double a, double b);
	`)
	src, d := RenderFFI(FFIOptions{Module: "mylib", Header: header})
	if d != nil {
		t.Fatalf("RenderFFI: %s", d.Error())
	}
	assertCompiles(t, []byte(src))
}

// TestWriteFFIWritesExpectedFile checks the on-disk path follows the
// `<dir>/<lower(module)>.osty` convention and contains the rendered
// source verbatim.
func TestWriteFFIWritesExpectedFile(t *testing.T) {
	dir := t.TempDir()
	header := []byte(`int x(int a);`)
	path, d := WriteFFI(dir, FFIOptions{Module: "MyLib", Header: header})
	if d != nil {
		t.Fatalf("WriteFFI: %s", d.Error())
	}
	if filepath.Base(path) != "mylib.osty" {
		t.Errorf("filename = %q, want mylib.osty", filepath.Base(path))
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if !strings.Contains(string(got), "pub fn x(a: Int32)") {
		t.Errorf("written file missing expected wrapper:\n%s", got)
	}
}
