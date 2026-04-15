package gen

import (
	"bytes"
	"fmt"
	"go/format"
	"sort"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/types"
)

// Generate translates a type-checked Osty file into Go source bytes.
//
// pkgName is the Go package clause to emit ("main" for executables).
// The returned bytes are gofmt-formatted; a post-process format.Source
// call both catches syntax bugs in the generator and normalises output.
//
// On a generator-internal error (unsupported construct, missing type
// info) the error is returned; any partial output is still returned so
// callers can inspect what was produced. On gofmt failure the raw
// pre-format bytes are returned alongside the error.
func Generate(pkgName string, file *ast.File, res *resolve.Result, chk *check.Result) ([]byte, error) {
	g := newGen(pkgName, file, res, chk)
	return g.run()
}

// GenerateMapped is Generate with source-origin comments enabled.
//
// The generated Go contains `// Osty: path:line:column` markers before
// emitted declarations and statements. CLI commands use those markers
// to map a later `go build`, `go run`, or panic stack trace line back
// to the nearest Osty source construct.
func GenerateMapped(pkgName string, file *ast.File, res *resolve.Result, chk *check.Result, sourcePath string) ([]byte, error) {
	g := newGen(pkgName, file, res, chk)
	g.sourcePath = sourcePath
	return g.run()
}

// gen is the transpiler state for one file.
type gen struct {
	pkgName string
	file    *ast.File
	res     *resolve.Result
	chk     *check.Result

	body    *writer           // body of the output (decls + main)
	imports map[string]string // Go import path → alias ("" = no alias)

	// errors accumulates non-fatal issues. The first fatal error
	// shortcircuits run and is returned; non-fatal issues are reported
	// with emitComment as `// TODO: ...` lines in the output.
	errs []error

	// loopDepth tracks nesting so break/continue can be rewritten when
	// we later support labelled loops. Zero means top level.
	loopDepth int

	// selfType is the name of the enclosing struct/enum while we emit
	// a method body; empty at top level. Used to rewrite `Self { ... }`
	// struct literals and `Self`-typed return annotations.
	selfType string

	// variantOwner maps an enum variant's name to its owning enum's
	// name. Built during the first pass so variant-construction calls
	// can emit `Enum_Variant{...}` without another AST walk.
	variantOwner map[string]string

	// enumTypes is the set of enum names declared in this file, used
	// to distinguish method-call rewriting between struct and enum
	// receivers at emitCall time.
	enumTypes map[string]bool

	// structTypes is the set of struct names declared in this file.
	// User structs lower as reference types, so expression emitters need
	// a quick local-name check even for AST nodes that the resolver does
	// not record in Refs.
	structTypes map[string]bool

	// methodNames[typeName] is the set of method names declared on
	// that type, used at emitCall to distinguish instance-method
	// invocation from field access to a function-valued field.
	methodNames map[string]map[string]bool

	// freshCounter backs freshVar for synthesized match / IIFE names.
	freshCounter int

	// needResult is set when any reference to Result<T, E> is emitted,
	// prompting a runtime type definition at file top.
	needResult bool

	// needErrorRuntime is set when a value typed as Osty's Error
	// interface needs dynamic message dispatch at runtime.
	needErrorRuntime bool

	// needStringRuntime is set when generated code needs the Osty
	// toString protocol bridge for interpolation or nested stdlib
	// toString implementations.
	needStringRuntime bool

	// needRange is set when a standalone range literal is emitted, so
	// the runtime Range struct can be injected at the top of the file.
	needRange bool

	// needHandle is set when a Handle[T] is referenced, pulling in the
	// runtime struct that backs `taskGroup` / `spawn` results.
	needHandle bool

	// needTaskGroup is set when `taskGroup(|g| { ... })` or
	// `parallel(...)` is emitted, pulling in the structured-concurrency
	// runtime (TaskGroup, spawnInGroup, runTaskGroup, runParallel).
	needTaskGroup bool

	// needSelect is set when `thread.select(|s| { ... })` is lowered,
	// pulling in the time import used by `s.timeout(...)` arms.
	needSelect bool

	// needFS / needRandomRuntime / needURLRuntime are set by stdlib use
	// bridges whose Osty surface cannot map directly to exported Go
	// package identifiers (for example `fs.readToString(...)`,
	// `random.default()` and `rng.int(...)`).
	needFS            bool
	fsOSAlias         string
	fsUTF8Alias       string
	needRandomRuntime bool
	needURLRuntime    bool

	// needEncoding is set when std.encoding lowers to Go's encoding
	// packages, pulling in base64/hex/url helper functions.
	needEncoding bool

	// needEnv is set when std.env lowers to os-backed helper functions.
	needEnv bool

	// needCsvRuntime is set when std.csv is used and needs inline
	// Go runtime helpers (_ostyCsv* functions).
	needCsvRuntime bool

	// needJSON is set when std.json lowers to encoding/json helpers.
	needJSON bool

	// needCompress is set when std.compress lowers to gzip helpers.
	needCompress bool

	// needCrypto is set when std.crypto lowers to hashing/HMAC/CSPRNG
	// helper functions.
	needCrypto bool

	// needUUID is set when std.uuid lowers to the runtime Uuid type and
	// generation/parsing helpers.
	needUUID bool

	// needBytesRuntime is set when Bytes primitive methods need runtime
	// helpers backed by Go's bytes package.
	needBytesRuntime bool

	// needRefRuntime is set when std.ref.same lowers to the runtime
	// reference-identity helper.
	needRefRuntime bool

	// needEqualRuntime is set when ==/!= need Osty's structural equality
	// instead of Go's pointer/interface identity for lowered reference values.
	needEqualRuntime bool

	// currentRetType tracks the enclosing function's return type so the
	// `?` lift at let-stmt position can reconstruct the Result with the
	// correct type parameters when the operand's T differs.
	currentRetType ast.Type

	// currentRetGo is the same contract after semantic inference. It is
	// needed for closures, whose return type often comes from context
	// rather than an explicit AST annotation.
	currentRetGo string

	// questionSubs maps a QuestionExpr AST node to the Go expression
	// text that should be emitted in its place. Populated by the
	// statement-level pre-lift pass (see preLiftQuestions); consumed by
	// emitQuestion. Nil when no lift is in progress.
	questionSubs map[*ast.QuestionExpr]string

	// instQueue is the pending list of generic-fn monomorphizations
	// that still need to be emitted. emitCall appends to this queue
	// each time it encounters a generic call site whose (sym, type
	// tuple) pair hasn't yet been emitted; drainInstances walks the
	// queue to a fixed point after the normal decl pass so transitive
	// instantiations (a generic body calling another generic fn) are
	// materialized (§2.7.3).
	instQueue []instRecord

	// instByKey dedupes the queue — maps (sym, goTypes) key to the
	// mangled name already assigned. Read by callers that need the
	// mangled name without re-enqueueing.
	instByKey map[string]string

	// substEnv is the current generic-parameter → Go-type substitution
	// environment. Non-empty only while we emit inside a monomorphized
	// body; goTypeExpr and goType consult it so every reference to `T`
	// in the source surfaces as the concrete Go type in the output.
	substEnv map[string]string

	// sourcePath enables source-origin comments in CLI-generated Go.
	// Library tests call Generate directly and keep this empty so their
	// historical source snapshots stay compact.
	sourcePath string
}

func newGen(pkgName string, file *ast.File, res *resolve.Result, chk *check.Result) *gen {
	g := &gen{
		pkgName:      pkgName,
		file:         file,
		res:          res,
		chk:          chk,
		body:         newWriter(),
		imports:      map[string]string{},
		variantOwner: map[string]string{},
		enumTypes:    map[string]bool{},
		structTypes:  map[string]bool{},
		methodNames:  map[string]map[string]bool{},
	}
	g.indexTypes()
	g.initInstances()
	return g
}

// indexTypes walks top-level declarations once to build the variant
// owner / enum-type / method-name maps. Keeps the emit path branch-free
// by having direct lookups available.
func (g *gen) indexTypes() {
	for _, d := range g.file.Decls {
		switch d := d.(type) {
		case *ast.EnumDecl:
			g.enumTypes[d.Name] = true
			for _, v := range d.Variants {
				g.variantOwner[v.Name] = d.Name
			}
			if g.methodNames[d.Name] == nil {
				g.methodNames[d.Name] = map[string]bool{}
			}
			for _, m := range d.Methods {
				g.methodNames[d.Name][m.Name] = true
			}
		case *ast.StructDecl:
			g.structTypes[d.Name] = true
			if g.methodNames[d.Name] == nil {
				g.methodNames[d.Name] = map[string]bool{}
			}
			for _, m := range d.Methods {
				g.methodNames[d.Name][m.Name] = true
			}
		}
	}
}

// use records a Go import with no alias (bare `import "path"`). A
// subsequent useAs for the same path overrides with an alias; a
// bare use after an alias leaves the alias intact.
func (g *gen) use(path string) {
	if _, ok := g.imports[path]; !ok {
		g.imports[path] = ""
	}
}

// useAs records a Go import under an alias (`import alias "path"`).
// Used by `use go "path" as alias { ... }` FFI declarations when the
// Osty alias differs from the Go package's default name.
func (g *gen) useAs(path, alias string) {
	g.imports[path] = alias
}

// todo emits a `// TODO: <msg>` comment and records the first occurrence
// as a non-fatal error. Used when a construct isn't yet supported.
func (g *gen) todo(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	g.body.writef("/* TODO: %s */", msg)
	g.errs = append(g.errs, fmt.Errorf("gen: %s", msg))
}

func (g *gen) sourceMarker(n ast.Node) {
	if g.sourcePath == "" || n == nil {
		return
	}
	pos := n.Pos()
	if pos.Line <= 0 {
		return
	}
	g.body.writef("// Osty: %s:%d:%d\n", g.sourcePath, pos.Line, pos.Column)
}

// run walks the file and returns the formatted Go source.
func (g *gen) run() ([]byte, error) {
	// 1a. Use declarations are stored in File.Uses separately from
	//     File.Decls. Walk them first so their aliases are in scope
	//     for the rest of the file (only matters for source order in
	//     Go, but keeps output tidy).
	for _, u := range g.file.Uses {
		g.emitUseDecl(u)
	}

	// 1b. Emit every declaration in source order. Generic fns are
	//     skipped inline — their specializations are materialized
	//     by drainInstances once every reachable (fn × type-tuple)
	//     pair has been requested by a call site.
	for _, d := range g.file.Decls {
		g.emitDecl(d)
	}

	// 1c. Fixed-point drain of queued monomorphizations. Emitting one
	//     specialization may discover further generic calls in its
	//     body (transitive instantiation); drainInstances walks the
	//     queue with an index-based loop so append-during-iteration
	//     works as expected.
	g.drainInstances()

	// 2. Script-mode: if the file has top-level statements, wrap them
	//    into a synthetic `main` function. A file with neither a
	//    user-defined `main` nor top-level statements produces a
	//    library package with no entry point (caller chooses pkgName).
	if len(g.file.Stmts) > 0 {
		g.body.nl()
		g.body.writeln("func main() {")
		g.body.indent()
		g.emitStmts(g.file.Stmts)
		g.body.dedent()
		g.body.writeln("}")
	}

	// 2b. Runtime flags set during body emission may demand additional
	//     imports. Resolve them before we serialize the import block.
	if g.needTaskGroup {
		g.use("sync")
		g.use("context")
	}
	if g.needFS {
		g.needResult = true
		g.useAs("os", g.fsOSAlias)
		g.useAs("unicode/utf8", g.fsUTF8Alias)
	}
	if g.needRandomRuntime {
		g.useAs("math/rand", "mathrand")
		g.use("sync")
		g.use("time")
	}
	if g.needURLRuntime {
		g.needResult = true
		g.useAs("net/url", "neturl")
		g.use("strconv")
	}
	if g.needErrorRuntime {
		g.use("fmt")
	}
	if g.needEncoding {
		g.needResult = true
		g.use("fmt")
		g.useAs("encoding/base64", "stdbase64")
		g.useAs("encoding/hex", "stdhex")
		g.useAs("net/url", "neturl")
		g.useAs("strings", "stdstrings")
		g.useAs("unicode/utf8", "utf8")
	}
	if g.needEnv {
		g.needResult = true
		g.use("fmt")
		g.use("os")
		g.useAs("strings", "stdstrings")
	}
	if g.needCsvRuntime {
		g.needResult = true
		g.use("fmt")
		g.useAs("strings", "_ostyCsvStrings")
	}
	if g.needJSON {
		g.needResult = true
		g.useAs("encoding/json", "stdjson")
		g.use("fmt")
		g.use("reflect")
	}
	if g.needBytesRuntime {
		g.needResult = true
		g.useAs("bytes", "stdbytes")
	}
	if g.needCompress {
		g.needResult = true
		if !g.needBytesRuntime {
			g.useAs("bytes", "stdbytes")
		}
		g.useAs("compress/gzip", "stdgzip")
		g.useAs("io", "stdio")
	}
	if g.needCrypto {
		g.useAs("crypto/hmac", "stdhmac")
		g.useAs("crypto/md5", "stdmd5")
		g.useAs("crypto/rand", "stdrand")
		g.useAs("crypto/sha1", "stdsha1")
		g.useAs("crypto/sha256", "stdsha256")
		g.useAs("crypto/sha512", "stdsha512")
		g.useAs("crypto/subtle", "stdsubtle")
	}
	if g.needUUID {
		g.needResult = true
		g.useAs("crypto/rand", "stdrand")
		g.useAs("encoding/hex", "stdhex")
		g.use("fmt")
		g.useAs("strings", "stdstrings")
		g.use("time")
	}
	if g.needResult || g.needStringRuntime || g.needErrorRuntime {
		g.use("fmt")
	}
	if g.needRefRuntime || g.needEqualRuntime {
		g.use("reflect")
	}
	if g.needRefRuntime {
		g.use("unsafe")
	}

	// 3. Assemble header + body.
	var out bytes.Buffer
	fmt.Fprintln(&out, "// Code generated by osty. DO NOT EDIT.")
	if g.sourcePath != "" {
		fmt.Fprintf(&out, "// Osty source: %s\n", g.sourcePath)
	}
	fmt.Fprintln(&out)
	fmt.Fprintf(&out, "package %s\n", g.pkgName)

	if len(g.imports) > 0 {
		out.WriteByte('\n')
		paths := make([]string, 0, len(g.imports))
		for p := range g.imports {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		// Aliased imports always use the block form — a single aliased
		// import is still clearer as `import ( alias "path" )` and gofmt
		// accepts either.
		hasAlias := false
		for _, a := range g.imports {
			if a != "" {
				hasAlias = true
				break
			}
		}
		if len(paths) == 1 && !hasAlias {
			fmt.Fprintf(&out, "import %q\n", paths[0])
		} else {
			out.WriteString("import (\n")
			for _, p := range paths {
				if alias := g.imports[p]; alias != "" {
					fmt.Fprintf(&out, "\t%s %q\n", alias, p)
				} else {
					fmt.Fprintf(&out, "\t%q\n", p)
				}
			}
			out.WriteString(")\n")
		}
	}

	out.WriteByte('\n')
	// Inject the runtime type definitions we depend on. Emitted at top
	// of body (after imports) so every downstream declaration can use
	// them without caring about order.
	if g.needResult || g.needStringRuntime {
		out.WriteString(`
type ostyStringer interface {
	toString() string
}

func ostyToString(v any) string {
	if s, ok := v.(ostyStringer); ok {
		return s.toString()
	}
	return fmt.Sprint(v)
}
`)
	}
	if g.needResult {
		out.WriteString(`
// Result is the runtime representation of Osty's Result<T, E>.
// IsOk distinguishes Ok(Value) from Err(Error).
type Result[T any, E any] struct {
	Value T
	Error E
	IsOk  bool
	ref   *byte
}

func resultRef() *byte { return new(byte) }

func resultOk[T any, E any](value T) Result[T, E] {
	return Result[T, E]{Value: value, IsOk: true, ref: resultRef()}
}

func resultErr[T any, E any](err E) Result[T, E] {
	return Result[T, E]{Error: err, ref: resultRef()}
}

func (r Result[T, E]) isOk() bool { return r.IsOk }

func (r Result[T, E]) isErr() bool { return !r.IsOk }

func (r Result[T, E]) unwrap() T {
	if !r.IsOk {
		panic("called unwrap on Err")
	}
	return r.Value
}

func (r Result[T, E]) unwrapErr() E {
	if r.IsOk {
		panic("called unwrapErr on Ok")
	}
	return r.Error
}

func (r Result[T, E]) unwrapOr(fallback T) T {
	if r.IsOk {
		return r.Value
	}
	return fallback
}

func (r Result[T, E]) ok() *T {
	if r.IsOk {
		return &r.Value
	}
	return nil
}

func (r Result[T, E]) err() *E {
	if !r.IsOk {
		return &r.Error
	}
	return nil
}

func (r Result[T, E]) toString() string {
	if r.IsOk {
		return "Ok(" + ostyToString(r.Value) + ")"
	}
	return "Err(" + ostyToString(r.Error) + ")"
}

func resultMap[T any, E any, U any](r Result[T, E], f func(T) U) Result[U, E] {
	if r.IsOk {
		return resultOk[U, E](f(r.Value))
	}
	return resultErr[U, E](r.Error)
}

func resultMapErr[T any, E any, F any](r Result[T, E], f func(E) F) Result[T, F] {
	if r.IsOk {
		return resultOk[T, F](r.Value)
	}
	return resultErr[T, F](f(r.Error))
}
`)
	}
	if g.needErrorRuntime {
		out.WriteString(`
type ostyBasicError struct {
	messageText string
}

func ostyErrorNew(message string) any {
	return ostyBasicError{messageText: message}
}

func (e ostyBasicError) message() string { return e.messageText }

func (e ostyBasicError) source() *any { return nil }

func ostyErrorDowncast[T any](err any) *T {
	v, ok := err.(T)
	if !ok {
		return nil
	}
	return &v
}

func ostyErrorMessage(e any) string {
	if e == nil {
		return ""
	}
	if m, ok := e.(interface{ message() string }); ok {
		return m.message()
	}
	if err, ok := e.(error); ok {
		return err.Error()
	}
	if s, ok := e.(string); ok {
		return s
	}
	return fmt.Sprint(e)
}

func ostyErrorSource(err any) *any {
	if err == nil {
		return nil
	}
	if e, ok := err.(interface{ source() *any }); ok {
		return e.source()
	}
	return nil
}
`)
	}
	if g.needRange {
		out.WriteString(`
// Range is the runtime representation of an Osty range literal.
// Used only when a range value is stored (outside a for-in head).
type Range struct {
	Start, Stop                  int
	HasStart, HasStop, Inclusive bool
}
`)
	}
	if g.needJSON {
		out.WriteString(`
// Json is the runtime representation of std.json.Json.
type Json = any

type jsonEncoder interface {
	toJson() Json
}

type jsonDecoderFunc func([]byte) (any, error)

type jsonField struct {
	Name    string
	Value   any
	OmitNil bool
}

var jsonDecoders = map[reflect.Type]jsonDecoderFunc{}

func jsonRegisterDecoder(t reflect.Type, fn jsonDecoderFunc) {
	jsonDecoders[t] = fn
}

func jsonEncode(value any) string {
	data, err := jsonMarshalAny(value)
	if err != nil {
		panic(fmt.Sprintf("std.json.encode: %v", err))
	}
	return string(data)
}

func jsonStringify(value any) string { return jsonEncode(value) }

func jsonDecode[T any](text string) Result[T, any] {
	var out T
	if err := jsonRejectLoneSurrogates(text); err != nil {
		return resultErr[T, any](err)
	}
	if err := jsonUnmarshalInto([]byte(text), &out); err != nil {
		return resultErr[T, any](err)
	}
	return resultOk[T, any](out)
}

func jsonObject(value map[string]Json) Json { return value }

func jsonArray(value []Json) Json { return value }

func jsonString(value string) Json { return value }

func jsonNumber(value float64) Json { return value }

func jsonBool(value bool) Json { return value }

func jsonMarshalAny(value any) ([]byte, error) {
	if enc, ok := value.(jsonEncoder); ok {
		return jsonMarshalAny(enc.toJson())
	}
	return stdjson.Marshal(value)
}

func jsonUnmarshalInto[T any](data []byte, out *T) error {
	target := reflect.TypeOf((*T)(nil)).Elem()
	return jsonUnmarshalReflect(data, target, reflect.ValueOf(out).Elem())
}

func jsonUnmarshalReflect(data []byte, target reflect.Type, dest reflect.Value) error {
	if dec := jsonDecoders[target]; dec != nil {
		value, err := dec(data)
		if err != nil {
			return err
		}
		rv := reflect.ValueOf(value)
		if !rv.Type().AssignableTo(target) {
			if rv.Type().ConvertibleTo(target) {
				rv = rv.Convert(target)
			} else {
				return fmt.Errorf("std.json: decoded %T cannot be assigned to %v", value, target)
			}
		}
		dest.Set(rv)
		return nil
	}
	if target.Kind() == reflect.Pointer {
		if jsonRawIsNull(data) {
			if target.Implements(reflect.TypeOf((*stdjson.Unmarshaler)(nil)).Elem()) {
				ptr := reflect.New(target.Elem())
				if err := stdjson.Unmarshal(data, ptr.Interface()); err != nil {
					return err
				}
				dest.Set(ptr)
				return nil
			}
			dest.SetZero()
			return nil
		}
		ptr := reflect.New(target.Elem())
		if err := jsonUnmarshalReflect(data, target.Elem(), ptr.Elem()); err != nil {
			return err
		}
		dest.Set(ptr)
		return nil
	}
	if jsonRawIsNull(data) && target.Kind() != reflect.Interface {
		if dest.CanAddr() && reflect.PointerTo(target).Implements(reflect.TypeOf((*stdjson.Unmarshaler)(nil)).Elem()) {
			return stdjson.Unmarshal(data, dest.Addr().Interface())
		}
		return fmt.Errorf("std.json: null cannot be decoded into %v", target)
	}
	switch target.Kind() {
	case reflect.Slice:
		var raw []stdjson.RawMessage
		if err := stdjson.Unmarshal(data, &raw); err != nil {
			return err
		}
		out := reflect.MakeSlice(target, len(raw), len(raw))
		for i := range raw {
			if err := jsonUnmarshalReflect(raw[i], target.Elem(), out.Index(i)); err != nil {
				return fmt.Errorf("std.json: array[%d]: %w", i, err)
			}
		}
		dest.Set(out)
		return nil
	case reflect.Array:
		var raw []stdjson.RawMessage
		if err := stdjson.Unmarshal(data, &raw); err != nil {
			return err
		}
		if len(raw) != target.Len() {
			return fmt.Errorf("std.json: array expected %d values, got %d", target.Len(), len(raw))
		}
		for i := range raw {
			if err := jsonUnmarshalReflect(raw[i], target.Elem(), dest.Index(i)); err != nil {
				return fmt.Errorf("std.json: array[%d]: %w", i, err)
			}
		}
		return nil
	case reflect.Map:
		if target.Key().Kind() == reflect.String {
			var raw map[string]stdjson.RawMessage
			if err := stdjson.Unmarshal(data, &raw); err != nil {
				return err
			}
			out := reflect.MakeMapWithSize(target, len(raw))
			for key, value := range raw {
				elem := reflect.New(target.Elem()).Elem()
				if err := jsonUnmarshalReflect(value, target.Elem(), elem); err != nil {
					return fmt.Errorf("std.json: object[%q]: %w", key, err)
				}
				out.SetMapIndex(reflect.ValueOf(key).Convert(target.Key()), elem)
			}
			dest.Set(out)
			return nil
		}
	}
	if dest.CanAddr() {
		return stdjson.Unmarshal(data, dest.Addr().Interface())
	}
	ptr := reflect.New(target)
	if err := stdjson.Unmarshal(data, ptr.Interface()); err != nil {
		return err
	}
	dest.Set(ptr.Elem())
	return nil
}

func jsonParseValue(data []byte) (Json, error) {
	if err := jsonRejectLoneSurrogates(string(data)); err != nil {
		return nil, err
	}
	var value Json
	if err := stdjson.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func jsonAsError(value any) error {
	if err, ok := value.(error); ok {
		return err
	}
	return fmt.Errorf("%v", value)
}

func jsonMarshalObject(fields []jsonField) ([]byte, error) {
	out := make(map[string]stdjson.RawMessage, len(fields))
	for _, field := range fields {
		if field.OmitNil && jsonIsNil(field.Value) {
			continue
		}
		data, err := jsonMarshalAny(field.Value)
		if err != nil {
			return nil, err
		}
		out[field.Name] = stdjson.RawMessage(data)
	}
	return stdjson.Marshal(out)
}

func jsonMarshalEnum(tag string, values ...any) ([]byte, error) {
	fields := []jsonField{{Name: "tag", Value: tag}}
	switch len(values) {
	case 0:
	case 1:
		fields = append(fields, jsonField{Name: "value", Value: values[0]})
	default:
		fields = append(fields, jsonField{Name: "value", Value: values})
	}
	return jsonMarshalObject(fields)
}

func jsonSkippedVariant(enumName, variantName string) ([]byte, error) {
	return nil, fmt.Errorf("std.json: %s.%s is marked #[json(skip)]", enumName, variantName)
}

func jsonUnmarshalArray(data []byte, want int, label string) ([]stdjson.RawMessage, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("std.json: %s missing value", label)
	}
	var values []stdjson.RawMessage
	if err := stdjson.Unmarshal(data, &values); err != nil {
		return nil, fmt.Errorf("std.json: %s value: %w", label, err)
	}
	if len(values) != want {
		return nil, fmt.Errorf("std.json: %s expected %d values, got %d", label, want, len(values))
	}
	return values, nil
}

func jsonRawIsNull(data []byte) bool {
	start, end := 0, len(data)
	for start < end && data[start] <= ' ' {
		start++
	}
	for end > start && data[end-1] <= ' ' {
		end--
	}
	return end-start == 4 && string(data[start:end]) == "null"
}

func jsonIsNil(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}
func jsonRejectLoneSurrogates(text string) error {
	inString := false
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '"':
			inString = !inString
		case '\\':
			if !inString || i+1 >= len(text) {
				continue
			}
			if text[i+1] != 'u' {
				i++
				continue
			}
			r, ok := jsonHex4(text[i+2:])
			if !ok {
				continue
			}
			if r >= 0xD800 && r <= 0xDBFF {
				low, lowOK := rune(0), false
				if i+12 <= len(text) && text[i+6] == '\\' && text[i+7] == 'u' {
					low, lowOK = jsonHex4(text[i+8:])
				}
				if !lowOK || low < 0xDC00 || low > 0xDFFF {
					return fmt.Errorf("std.json.decode: lone surrogate escape \\u%04X", r)
				}
				i += 11
				continue
			}
			if r >= 0xDC00 && r <= 0xDFFF {
				return fmt.Errorf("std.json.decode: lone surrogate escape \\u%04X", r)
			}
			i += 5
		}
	}
	return nil
}

func jsonHex4(s string) (rune, bool) {
	if len(s) < 4 {
		return 0, false
	}
	var r rune
	for i := 0; i < 4; i++ {
		c := s[i]
		var v byte
		switch {
		case c >= '0' && c <= '9':
			v = c - '0'
		case c >= 'a' && c <= 'f':
			v = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			v = c - 'A' + 10
		default:
			return 0, false
		}
		r = r*16 + rune(v)
	}
	return r, true
}
`)
	}
	if g.needEqualRuntime {
		out.WriteString(`
func ostyEqual(a, b any) bool {
	av := reflect.ValueOf(a)
	bv := reflect.ValueOf(b)
	if !av.IsValid() || !bv.IsValid() {
		return ostyIsNilValue(av) && ostyIsNilValue(bv)
	}
	return ostyEqualValue(av, bv, map[ostyEqualVisit]bool{})
}

type ostyEqualVisit struct {
	a, b uintptr
	typ  reflect.Type
}

func ostyEqualValue(a, b reflect.Value, seen map[ostyEqualVisit]bool) bool {
	if !a.IsValid() || !b.IsValid() {
		return ostyIsNilValue(a) && ostyIsNilValue(b)
	}
	if a.Type() != b.Type() {
		return ostyIsNilValue(a) && ostyIsNilValue(b)
	}
	switch a.Kind() {
	case reflect.Interface:
		if a.IsNil() || b.IsNil() {
			return a.IsNil() == b.IsNil()
		}
		return ostyEqualValue(a.Elem(), b.Elem(), seen)
	case reflect.Pointer:
		if a.IsNil() || b.IsNil() {
			return a.IsNil() == b.IsNil()
		}
		visit := ostyEqualVisit{a: a.Pointer(), b: b.Pointer(), typ: a.Type()}
		if seen[visit] {
			return true
		}
		seen[visit] = true
		return ostyEqualValue(a.Elem(), b.Elem(), seen)
	case reflect.Struct:
		for i := 0; i < a.NumField(); i++ {
			if a.Type().Field(i).Name == "ref" {
				continue
			}
			if !ostyEqualValue(a.Field(i), b.Field(i), seen) {
				return false
			}
		}
		return true
	case reflect.Array, reflect.Slice:
		if a.Kind() == reflect.Slice && (a.IsNil() || b.IsNil()) {
			return a.IsNil() == b.IsNil()
		}
		if a.Len() != b.Len() {
			return false
		}
		for i := 0; i < a.Len(); i++ {
			if !ostyEqualValue(a.Index(i), b.Index(i), seen) {
				return false
			}
		}
		return true
	case reflect.Map:
		if a.IsNil() || b.IsNil() {
			return a.IsNil() == b.IsNil()
		}
		if a.Len() != b.Len() {
			return false
		}
		for _, key := range a.MapKeys() {
			bv := b.MapIndex(key)
			if !bv.IsValid() || !ostyEqualValue(a.MapIndex(key), bv, seen) {
				return false
			}
		}
		return true
	case reflect.String:
		return a.String() == b.String()
	case reflect.Bool:
		return a.Bool() == b.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return a.Int() == b.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return a.Uint() == b.Uint()
	case reflect.Float32, reflect.Float64:
		return a.Float() == b.Float()
	case reflect.Complex64, reflect.Complex128:
		return a.Complex() == b.Complex()
	case reflect.Func:
		return a.IsNil() && b.IsNil()
	case reflect.Chan, reflect.UnsafePointer:
		return a.Pointer() == b.Pointer()
	default:
		if a.CanInterface() && b.CanInterface() {
			return reflect.DeepEqual(a.Interface(), b.Interface())
		}
		return false
	}
}

func ostyIsNilValue(v reflect.Value) bool {
	if !v.IsValid() {
		return true
	}
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}
`)
	}
	if g.needRefRuntime {
		out.WriteString(`
func refSame(a, b any) bool {
	av := reflect.ValueOf(a)
	bv := reflect.ValueOf(b)
	if !av.IsValid() || !bv.IsValid() || av.Type() != bv.Type() {
		return false
	}
	switch av.Kind() {
	case reflect.Func:
		ap := refInterfaceData(a)
		return ap != nil && ap == refInterfaceData(b)
	case reflect.Slice:
		ap := av.Pointer()
		return ap != 0 && ap == bv.Pointer()
	case reflect.Chan, reflect.Map, reflect.Pointer, reflect.UnsafePointer:
		ap := av.Pointer()
		return ap != 0 && ap == bv.Pointer()
	case reflect.Struct:
		af := av.FieldByName("ref")
		bf := bv.FieldByName("ref")
		if af.IsValid() && bf.IsValid() && af.Kind() == reflect.Pointer && bf.Kind() == reflect.Pointer {
			ap := af.Pointer()
			return ap != 0 && ap == bf.Pointer()
		}
		return false
	default:
		return false
	}
}

type refInterfaceHeader struct {
	typ  unsafe.Pointer
	data unsafe.Pointer
}

func refInterfaceData(v any) unsafe.Pointer {
	return (*refInterfaceHeader)(unsafe.Pointer(&v)).data
}
`)
	}
	if g.needRandomRuntime {
		out.WriteString(`
// Rng is the runtime representation of std.random.Rng.
type Rng struct {
	mu *sync.Mutex
	r  *mathrand.Rand
}

func newRng(seed int64) Rng {
	return Rng{mu: &sync.Mutex{}, r: mathrand.New(mathrand.NewSource(seed))}
}

func randomDefault() Rng { return newRng(time.Now().UnixNano()) }

func randomSeeded(seed int64) Rng { return newRng(seed) }

func (r Rng) ready() Rng {
	if r.r == nil {
		return randomDefault()
	}
	if r.mu == nil {
		r.mu = &sync.Mutex{}
	}
	return r
}

func (r Rng) int(min, max int) int {
	if max <= min {
		panic("std.random.Rng.int requires max > min")
	}
	r = r.ready()
	r.mu.Lock()
	defer r.mu.Unlock()
	return min + r.r.Intn(max-min)
}

func (r Rng) intInclusive(min, max int) int {
	if max < min {
		panic("std.random.Rng.intInclusive requires max >= min")
	}
	return r.int(min, max+1)
}

func (r Rng) float() float64 {
	r = r.ready()
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.r.Float64()
}

func (r Rng) bool() bool { return r.int(0, 2) == 1 }

func (r Rng) bytes(n int) []byte {
	if n < 0 {
		panic("std.random.Rng.bytes requires n >= 0")
	}
	r = r.ready()
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]byte, n)
	for i := range out {
		out[i] = byte(r.r.Intn(256))
	}
	return out
}

func rngChoice[T any](r Rng, items []T) *T {
	if len(items) == 0 {
		return nil
	}
	v := items[r.int(0, len(items))]
	return &v
}

func rngShuffle[T any](r Rng, items []T) {
	r = r.ready()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.r.Shuffle(len(items), func(i, j int) {
		items[i], items[j] = items[j], items[i]
	})
}
`)
	}
	if g.needURLRuntime {
		out.WriteString(`
// Url is the runtime representation of std.url.Url.
type Url struct {
	scheme   string
	host     string
	port     *int
	path     string
	query    map[string]string
	queryAll map[string][]string
	fragment *string
}

func urlParse(text string) Result[Url, any] {
	parsed, err := neturl.Parse(text)
	if err != nil {
		return resultErr[Url, any](err.Error())
	}
	var port *int
	if raw := parsed.Port(); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return resultErr[Url, any](err.Error())
		}
		port = &n
	}
	var fragment *string
	if parsed.Fragment != "" {
		v := parsed.Fragment
		fragment = &v
	}
	query := map[string]string{}
	queryAll := map[string][]string{}
	for key, values := range parsed.Query() {
		cp := append([]string(nil), values...)
		queryAll[key] = cp
		if len(values) == 0 {
			query[key] = ""
		} else {
			query[key] = values[0]
		}
	}
	return resultOk[Url, any](Url{
		scheme:   parsed.Scheme,
		host:     parsed.Hostname(),
		port:     port,
		path:     parsed.Path,
		query:    query,
		queryAll: queryAll,
		fragment: fragment,
	})
}

func urlJoin(base, relative string) Result[string, any] {
	b, err := neturl.Parse(base)
	if err != nil {
		return resultErr[string, any](err.Error())
	}
	r, err := neturl.Parse(relative)
	if err != nil {
		return resultErr[string, any](err.Error())
	}
	return resultOk[string, any](b.ResolveReference(r).String())
}

func (u Url) toString() string {
	out := neturl.URL{
		Scheme: u.scheme,
		Host:   u.host,
		Path:   u.path,
	}
	if u.port != nil && u.host != "" {
		out.Host = u.host + ":" + strconv.Itoa(*u.port)
	}
	if u.fragment != nil {
		out.Fragment = *u.fragment
	}
	q := neturl.Values{}
	if u.queryAll != nil {
		for key, values := range u.queryAll {
			for _, value := range values {
				q.Add(key, value)
			}
		}
	} else {
		for key, value := range u.query {
			q.Set(key, value)
		}
	}
	out.RawQuery = q.Encode()
	return out.String()
}

func (u Url) queryValues(key string) []string {
	if u.queryAll != nil {
		return append([]string(nil), u.queryAll[key]...)
	}
	if value, ok := u.query[key]; ok {
		return []string{value}
	}
	return []string{}
}
`)
	}
	if g.needEncoding {
		out.WriteString(`
func encodingBase64Encode(data []byte) string {
	return stdbase64.StdEncoding.EncodeToString(data)
}

func encodingBase64Decode(text string) Result[[]byte, any] {
	data, err := stdbase64.StdEncoding.DecodeString(text)
	if err != nil {
		return resultErr[[]byte, any](err)
	}
	return resultOk[[]byte, any](data)
}

func encodingBase64URLEncode(data []byte) string {
	return stdbase64.URLEncoding.EncodeToString(data)
}

func encodingBase64URLDecode(text string) Result[[]byte, any] {
	data, err := stdbase64.URLEncoding.DecodeString(text)
	if err != nil {
		return resultErr[[]byte, any](err)
	}
	return resultOk[[]byte, any](data)
}

func encodingHexEncode(data []byte) string {
	return stdhex.EncodeToString(data)
}

func encodingHexDecode(text string) Result[[]byte, any] {
	data, err := stdhex.DecodeString(text)
	if err != nil {
		return resultErr[[]byte, any](err)
	}
	return resultOk[[]byte, any](data)
}

func encodingURLEncode(text string) string {
	return stdstrings.ReplaceAll(neturl.QueryEscape(text), "+", "%20")
}

func encodingURLDecode(text string) Result[string, any] {
	text, err := neturl.QueryUnescape(text)
	if err != nil {
		return resultErr[string, any](err)
	}
	return resultOk[string, any](text)
}
`)
	}
	if g.needEnv {
		out.WriteString(`
func envArgs() []string {
	args := make([]string, len(os.Args)-1)
	copy(args, os.Args[1:])
	return args
}

func envGet(name string) *string {
	value, ok := os.LookupEnv(name)
	if !ok {
		return nil
	}
	return &value
}

func envRequire(name string) Result[string, any] {
	value, ok := os.LookupEnv(name)
	if !ok {
		return resultErr[string, any](fmt.Errorf("environment variable %s is not set", name))
	}
	return resultOk[string, any](value)
}

func envSet(name, value string) Result[struct{}, any] {
	if err := os.Setenv(name, value); err != nil {
		return resultErr[struct{}, any](err)
	}
	return resultOk[struct{}, any](struct{}{})
}

func envUnset(name string) Result[struct{}, any] {
	if err := os.Unsetenv(name); err != nil {
		return resultErr[struct{}, any](err)
	}
	return resultOk[struct{}, any](struct{}{})
}

func envVars() map[string]string {
	out := map[string]string{}
	for _, pair := range os.Environ() {
		if key, value, ok := stdstrings.Cut(pair, "="); ok {
			out[key] = value
		}
	}
	return out
}

func envCurrentDir() Result[string, any] {
	dir, err := os.Getwd()
	if err != nil {
		return resultErr[string, any](err)
	}
	return resultOk[string, any](dir)
}

func envSetCurrentDir(path string) Result[struct{}, any] {
	if err := os.Chdir(path); err != nil {
		return resultErr[struct{}, any](err)
	}
	return resultOk[struct{}, any](struct{}{})
}
`)
	}
	if g.needCsvRuntime {
		g.emitCsvRuntime(&out)
	}
	if g.needCompress {
		out.WriteString(`
func compressGzipEncode(data []byte) []byte {
	var b stdbytes.Buffer
	w := stdgzip.NewWriter(&b)
	if _, err := w.Write(data); err != nil {
		panic(err)
	}
	if err := w.Close(); err != nil {
		panic(err)
	}
	return b.Bytes()
}

func compressGzipDecode(data []byte) Result[[]byte, any] {
	r, err := stdgzip.NewReader(stdbytes.NewReader(data))
	if err != nil {
		return resultErr[[]byte, any](err)
	}
	out, readErr := stdio.ReadAll(r)
	closeErr := r.Close()
	if readErr != nil {
		return resultErr[[]byte, any](readErr)
	}
	if closeErr != nil {
		return resultErr[[]byte, any](closeErr)
	}
	return resultOk[[]byte, any](out)
}
`)
	}
	if g.needCrypto {
		out.WriteString(`
func cryptoSHA256(data []byte) []byte {
	sum := stdsha256.Sum256(data)
	return append([]byte(nil), sum[:]...)
}

func cryptoSHA512(data []byte) []byte {
	sum := stdsha512.Sum512(data)
	return append([]byte(nil), sum[:]...)
}

func cryptoSHA1(data []byte) []byte {
	sum := stdsha1.Sum(data)
	return append([]byte(nil), sum[:]...)
}

func cryptoMD5(data []byte) []byte {
	sum := stdmd5.Sum(data)
	return append([]byte(nil), sum[:]...)
}

func cryptoHMACSHA256(key, message []byte) []byte {
	mac := stdhmac.New(stdsha256.New, key)
	_, _ = mac.Write(message)
	return mac.Sum(nil)
}

func cryptoHMACSHA512(key, message []byte) []byte {
	mac := stdhmac.New(stdsha512.New, key)
	_, _ = mac.Write(message)
	return mac.Sum(nil)
}

func cryptoRandomBytes(n int) []byte {
	if n < 0 {
		panic("crypto.randomBytes called with a negative length")
	}
	out := make([]byte, n)
	if _, err := stdrand.Read(out); err != nil {
		panic(err)
	}
	return out
}

func cryptoConstantTimeEq(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	return stdsubtle.ConstantTimeCompare(a, b) == 1
}
`)
	}
	if g.needUUID {
		out.WriteString(`
type Uuid [16]byte

func uuidV4() Uuid {
	var u Uuid
	uuidFillRandom(u[:])
	u[6] = (u[6] & 0x0f) | 0x40
	u[8] = (u[8] & 0x3f) | 0x80
	return u
}

func uuidV7() Uuid {
	var u Uuid
	uuidFillRandom(u[:])
	ms := uint64(time.Now().UnixMilli())
	u[0] = byte(ms >> 40)
	u[1] = byte(ms >> 32)
	u[2] = byte(ms >> 24)
	u[3] = byte(ms >> 16)
	u[4] = byte(ms >> 8)
	u[5] = byte(ms)
	u[6] = (u[6] & 0x0f) | 0x70
	u[8] = (u[8] & 0x3f) | 0x80
	return u
}

func uuidParse(text string) Result[Uuid, any] {
	var compact string
	switch len(text) {
	case 32:
		compact = text
	case 36:
		if text[8] != '-' || text[13] != '-' || text[18] != '-' || text[23] != '-' {
			return resultErr[Uuid, any](fmt.Errorf("invalid UUID %q", text))
		}
		compact = stdstrings.ReplaceAll(text, "-", "")
	default:
		return resultErr[Uuid, any](fmt.Errorf("invalid UUID %q", text))
	}
	var u Uuid
	if _, err := stdhex.Decode(u[:], []byte(compact)); err != nil {
		return resultErr[Uuid, any](err)
	}
	return resultOk[Uuid, any](u)
}

func uuidNil() Uuid { return Uuid{} }

func uuidFillRandom(dst []byte) {
	if _, err := stdrand.Read(dst); err != nil {
		panic(err)
	}
}

func (u Uuid) toString() string {
	var out [36]byte
	stdhex.Encode(out[0:8], u[0:4])
	out[8] = '-'
	stdhex.Encode(out[9:13], u[4:6])
	out[13] = '-'
	stdhex.Encode(out[14:18], u[6:8])
	out[18] = '-'
	stdhex.Encode(out[19:23], u[8:10])
	out[23] = '-'
	stdhex.Encode(out[24:36], u[10:16])
	return string(out[:])
}

func (u Uuid) toBytes() []byte {
	return append([]byte(nil), u[:]...)
}
`)
	}
	if g.needHandle {
		out.WriteString(`
// Handle is the runtime representation of a task handle from
// spawn/taskGroup. Join blocks until the goroutine finishes and
// returns its result. The result channel is buffered to size 1 so
// the goroutine never blocks on send when Join hasn't been called.
type Handle[T any] struct {
	result chan T
}

func spawnHandle[T any](body func() T) Handle[T] {
	h := Handle[T]{result: make(chan T, 1)}
	go func() {
		h.result <- body()
	}()
	return h
}

func (h Handle[T]) Join() T { return <-h.result }
`)
	}
	if g.needTaskGroup {
		out.WriteString(`
// TaskGroup backs §8 structured concurrency: every task spawned via
// Spawn must finish before the group exits. A context is carried
// through so Cancel propagates to cooperating children via
// IsCancelled / Done. Context-aware stdlib blocking calls (the full
// spec-mandated cancellation protocol) are not shipped — tasks that
// don't explicitly check IsCancelled will run to completion.
type TaskGroup struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func (g *TaskGroup) Done() <-chan struct{} { return g.ctx.Done() }

func (g *TaskGroup) IsCancelled() bool {
	select {
	case <-g.ctx.Done():
		return true
	default:
		return false
	}
}

func (g *TaskGroup) Cancel() { g.cancel() }

// spawnInGroup is the TaskGroup-aware variant of spawnHandle. The
// WaitGroup counter increments synchronously so runTaskGroup's Wait
// sees the task even if the goroutine hasn't started yet.
func spawnInGroup[T any](g *TaskGroup, body func() T) Handle[T] {
	g.wg.Add(1)
	h := Handle[T]{result: make(chan T, 1)}
	go func() {
		defer g.wg.Done()
		h.result <- body()
	}()
	return h
}

// runTaskGroup is the lowering target for ` + "`taskGroup(|g| body)`" + `.
// The closure runs synchronously; on exit the context is cancelled
// (so late children observing IsCancelled see the signal) and Wait
// drains every unfinished spawned task before returning.
func runTaskGroup[T any](body func(*TaskGroup) T) T {
	ctx, cancel := context.WithCancel(context.Background())
	g := &TaskGroup{ctx: ctx, cancel: cancel}
	defer func() {
		cancel()
		g.wg.Wait()
	}()
	return body(g)
}

// runParallel is the lowering target for ` + "`parallel(|| a, || b, ...)`" + `.
// Every closure runs concurrently; the returned slice preserves the
// source order so Osty-side destructuring lines up.
func runParallel[T any](bodies ...func() T) []T {
	results := make([]T, len(bodies))
	var wg sync.WaitGroup
	wg.Add(len(bodies))
	for i, body := range bodies {
		i, body := i, body
		go func() {
			defer wg.Done()
			results[i] = body()
		}()
	}
	wg.Wait()
	return results
}

// runParallelMap backs §8.3 parallel(items, concurrency, f). It keeps
// source order in the returned slice while limiting in-flight workers.
func runParallelMap[T any, R any](items []T, concurrency int, fn func(T) R) []R {
	if concurrency <= 0 {
		concurrency = 1
	}
	results := make([]R, len(items))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i, item := range items {
		i, item := i, item
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = fn(item)
		}()
	}
	wg.Wait()
	return results
}
`)
	}
	out.Write(g.body.bytes())

	// 4. Normalise via gofmt. Returns the raw bytes on failure so the
	//    caller can still inspect the (malformed) output.
	src := out.Bytes()
	formatted, err := format.Source(src)
	if err != nil {
		return src, fmt.Errorf("gofmt: %w", err)
	}
	if len(g.errs) > 0 {
		return formatted, fmt.Errorf("%d generator issue(s); first: %w",
			len(g.errs), g.errs[0])
	}
	return formatted, nil
}

// symbolFor returns the resolver Symbol for an Ident, or nil.
func (g *gen) symbolFor(id *ast.Ident) *resolve.Symbol {
	if g.res == nil {
		return nil
	}
	return g.res.Refs[id]
}

// typeOf returns the checked type of an expression, or nil when the
// checker didn't visit it.
func (g *gen) typeOf(e ast.Expr) types.Type {
	if g.chk == nil {
		return nil
	}
	return g.chk.Types[e]
}

// symTypeOf returns the checked type of a symbol, or nil.
func (g *gen) symTypeOf(s *resolve.Symbol) types.Type {
	if g.chk == nil || s == nil {
		return nil
	}
	return g.chk.SymTypes[s]
}
