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

	// methodNames[typeName] is the set of method names declared on
	// that type, used at emitCall to distinguish instance-method
	// invocation from field access to a function-valued field.
	methodNames map[string]map[string]bool

	// freshCounter backs freshVar for synthesized match / IIFE names.
	freshCounter int

	// needResult is set when any reference to Result<T, E> is emitted,
	// prompting a runtime type definition at file top.
	needResult bool

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

	// needRandomRuntime / needURLRuntime are set by stdlib use bridges
	// whose Osty surface cannot map directly to exported Go package
	// identifiers (for example `random.default()` and `rng.int(...)`).
	needRandomRuntime bool
	needURLRuntime    bool

	// needRegex is set when std.regex lowers to Go's regexp package,
	// pulling in Regex/Match/Captures runtime wrappers.
	needRegex bool

	// needEncoding is set when std.encoding lowers to Go's encoding
	// packages, pulling in base64/hex/url helper functions.
	needEncoding bool

	// needEnv is set when std.env lowers to os-backed helper functions.
	needEnv bool

	// needCSV is set when std.csv lowers to encoding/csv helper
	// functions and the CsvOptions runtime struct.
	needCSV bool

	// needCompress is set when std.compress lowers to gzip helpers.
	needCompress bool

	// needCrypto is set when std.crypto lowers to hashing/HMAC/CSPRNG
	// helper functions.
	needCrypto bool

	// needUUID is set when std.uuid lowers to the runtime Uuid type and
	// generation/parsing helpers.
	needUUID bool

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
	if g.needRegex {
		g.needResult = true
		g.use("regexp")
	}
	if g.needEncoding {
		g.needResult = true
		g.useAs("encoding/base64", "stdbase64")
		g.useAs("encoding/hex", "stdhex")
		g.useAs("net/url", "neturl")
		g.useAs("strings", "stdstrings")
	}
	if g.needEnv {
		g.needResult = true
		g.use("fmt")
		g.use("os")
		g.useAs("strings", "stdstrings")
	}
	if g.needCSV {
		g.needResult = true
		g.useAs("encoding/csv", "stdcsv")
		g.use("fmt")
		g.useAs("strings", "stdstrings")
	}
	if g.needCompress {
		g.needResult = true
		g.useAs("bytes", "stdbytes")
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
	if g.needResult || g.needStringRuntime {
		g.use("fmt")
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
		return Result[U, E]{Value: f(r.Value), IsOk: true}
	}
	return Result[U, E]{Error: r.Error}
}

func resultMapErr[T any, E any, F any](r Result[T, E], f func(E) F) Result[T, F] {
	if r.IsOk {
		return Result[T, F]{Value: r.Value, IsOk: true}
	}
	return Result[T, F]{Error: f(r.Error)}
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
		return Result[Url, any]{Error: err.Error()}
	}
	var port *int
	if raw := parsed.Port(); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return Result[Url, any]{Error: err.Error()}
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
	return Result[Url, any]{
		Value: Url{
			scheme:   parsed.Scheme,
			host:     parsed.Hostname(),
			port:     port,
			path:     parsed.Path,
			query:    query,
			queryAll: queryAll,
			fragment: fragment,
		},
		IsOk: true,
	}
}

func urlJoin(base, relative string) Result[string, any] {
	b, err := neturl.Parse(base)
	if err != nil {
		return Result[string, any]{Error: err.Error()}
	}
	r, err := neturl.Parse(relative)
	if err != nil {
		return Result[string, any]{Error: err.Error()}
	}
	return Result[string, any]{Value: b.ResolveReference(r).String(), IsOk: true}
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
	if g.needRegex {
		out.WriteString(`
type Regex struct {
	re *regexp.Regexp
}

type RegexMatch struct {
	text       string
	start, end int
}

type Captures struct {
	values []*string
	names  map[string]int
}

func regexCompile(pattern string) Result[Regex, error] {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return Result[Regex, error]{Error: err}
	}
	return Result[Regex, error]{Value: Regex{re: re}, IsOk: true}
}

func (r Regex) matches(text string) bool { return r.re.MatchString(text) }

func (r Regex) find(text string) *RegexMatch {
	loc := r.re.FindStringIndex(text)
	if loc == nil {
		return nil
	}
	m := RegexMatch{text: text[loc[0]:loc[1]], start: loc[0], end: loc[1]}
	return &m
}

func (r Regex) findAll(text string) []RegexMatch {
	locs := r.re.FindAllStringIndex(text, -1)
	out := make([]RegexMatch, 0, len(locs))
	for _, loc := range locs {
		out = append(out, RegexMatch{text: text[loc[0]:loc[1]], start: loc[0], end: loc[1]})
	}
	return out
}

func (r Regex) captures(text string) *Captures {
	idx := r.re.FindStringSubmatchIndex(text)
	if idx == nil {
		return nil
	}
	c := regexCaptures(r.re, text, idx)
	return &c
}

func (r Regex) capturesAll(text string) []Captures {
	idxs := r.re.FindAllStringSubmatchIndex(text, -1)
	out := make([]Captures, 0, len(idxs))
	for _, idx := range idxs {
		out = append(out, regexCaptures(r.re, text, idx))
	}
	return out
}

func (r Regex) replace(text, replacement string) string {
	idx := r.re.FindStringSubmatchIndex(text)
	if idx == nil {
		return text
	}
	dst := make([]byte, 0, len(text)+len(replacement))
	dst = append(dst, text[:idx[0]]...)
	dst = r.re.ExpandString(dst, replacement, text, idx)
	dst = append(dst, text[idx[1]:]...)
	return string(dst)
}

func (r Regex) replaceAll(text, replacement string) string {
	return r.re.ReplaceAllString(text, replacement)
}

func (r Regex) split(text string) []string { return r.re.Split(text, -1) }

func regexCaptures(re *regexp.Regexp, text string, idx []int) Captures {
	names := re.SubexpNames()
	c := Captures{values: make([]*string, len(idx)/2), names: map[string]int{}}
	for i := range c.values {
		start, end := idx[2*i], idx[2*i+1]
		if start >= 0 && end >= 0 {
			v := text[start:end]
			c.values[i] = &v
		}
		if i < len(names) && names[i] != "" {
			c.names[names[i]] = i
		}
	}
	return c
}

func (c Captures) get(i int) *string {
	if i < 0 || i >= len(c.values) {
		return nil
	}
	return c.values[i]
}

func (c Captures) named(name string) *string {
	i, ok := c.names[name]
	if !ok {
		return nil
	}
	return c.get(i)
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
		return Result[[]byte, any]{Error: err}
	}
	return Result[[]byte, any]{Value: data, IsOk: true}
}

func encodingBase64URLEncode(data []byte) string {
	return stdbase64.URLEncoding.EncodeToString(data)
}

func encodingBase64URLDecode(text string) Result[[]byte, any] {
	data, err := stdbase64.URLEncoding.DecodeString(text)
	if err != nil {
		return Result[[]byte, any]{Error: err}
	}
	return Result[[]byte, any]{Value: data, IsOk: true}
}

func encodingHexEncode(data []byte) string {
	return stdhex.EncodeToString(data)
}

func encodingHexDecode(text string) Result[[]byte, any] {
	data, err := stdhex.DecodeString(text)
	if err != nil {
		return Result[[]byte, any]{Error: err}
	}
	return Result[[]byte, any]{Value: data, IsOk: true}
}

func encodingURLEncode(text string) string {
	return stdstrings.ReplaceAll(neturl.QueryEscape(text), "+", "%20")
}

func encodingURLDecode(text string) Result[string, any] {
	text, err := neturl.QueryUnescape(text)
	if err != nil {
		return Result[string, any]{Error: err}
	}
	return Result[string, any]{Value: text, IsOk: true}
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
		return Result[string, any]{Error: fmt.Errorf("environment variable %s is not set", name)}
	}
	return Result[string, any]{Value: value, IsOk: true}
}

func envSet(name, value string) Result[struct{}, any] {
	if err := os.Setenv(name, value); err != nil {
		return Result[struct{}, any]{Error: err}
	}
	return Result[struct{}, any]{Value: struct{}{}, IsOk: true}
}

func envUnset(name string) Result[struct{}, any] {
	if err := os.Unsetenv(name); err != nil {
		return Result[struct{}, any]{Error: err}
	}
	return Result[struct{}, any]{Value: struct{}{}, IsOk: true}
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
		return Result[string, any]{Error: err}
	}
	return Result[string, any]{Value: dir, IsOk: true}
}

func envSetCurrentDir(path string) Result[struct{}, any] {
	if err := os.Chdir(path); err != nil {
		return Result[struct{}, any]{Error: err}
	}
	return Result[struct{}, any]{Value: struct{}{}, IsOk: true}
}
`)
	}
	if g.needCSV {
		out.WriteString(`
type CsvOptions struct {
	delimiter rune
	quote     rune
	trimSpace bool
}

func csvDefaultOptions() CsvOptions {
	return CsvOptions{delimiter: ',', quote: '"'}
}

func csvNormalizeOptions(options CsvOptions) CsvOptions {
	if options.delimiter == 0 {
		options.delimiter = ','
	}
	if options.quote == 0 {
		options.quote = '"'
	}
	return options
}

func csvEncode(rows [][]string) string {
	return csvEncodeWith(rows, csvDefaultOptions())
}

func csvEncodeWith(rows [][]string, options CsvOptions) string {
	options = csvNormalizeOptions(options)
	if options.quote != '"' {
		return csvEncodeCustom(rows, options)
	}
	var b stdstrings.Builder
	w := stdcsv.NewWriter(&b)
	w.Comma = options.delimiter
	_ = w.WriteAll(rows)
	return b.String()
}

func csvEncodeCustom(rows [][]string, options CsvOptions) string {
	var b stdstrings.Builder
	for _, row := range rows {
		for i, field := range row {
			if i > 0 {
				b.WriteRune(options.delimiter)
			}
			csvWriteCustomField(&b, field, options)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func csvWriteCustomField(b *stdstrings.Builder, field string, options CsvOptions) {
	needsQuote := field == "" ||
		stdstrings.ContainsRune(field, options.delimiter) ||
		stdstrings.ContainsRune(field, options.quote) ||
		stdstrings.ContainsAny(field, "\r\n") ||
		field != stdstrings.TrimSpace(field)
	if !needsQuote {
		b.WriteString(field)
		return
	}
	b.WriteRune(options.quote)
	for _, r := range field {
		if r == options.quote {
			b.WriteRune(options.quote)
		}
		b.WriteRune(r)
	}
	b.WriteRune(options.quote)
}

func csvDecode(text string) Result[[][]string, any] {
	return csvDecodeWith(text, csvDefaultOptions())
}

func csvDecodeWith(text string, options CsvOptions) Result[[][]string, any] {
	options = csvNormalizeOptions(options)
	if options.quote != '"' {
		rows, err := csvDecodeCustom(text, options)
		if err != nil {
			return Result[[][]string, any]{Error: err}
		}
		return Result[[][]string, any]{Value: rows, IsOk: true}
	}
	r := stdcsv.NewReader(stdstrings.NewReader(text))
	r.Comma = options.delimiter
	r.TrimLeadingSpace = options.trimSpace
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		return Result[[][]string, any]{Error: err}
	}
	return Result[[][]string, any]{Value: rows, IsOk: true}
}

func csvDecodeCustom(text string, options CsvOptions) ([][]string, error) {
	var rows [][]string
	var row []string
	var field stdstrings.Builder
	var inQuote, quoted, sawAny, endedRecord bool
	appendField := func() {
		value := field.String()
		if options.trimSpace && !quoted {
			value = stdstrings.TrimSpace(value)
		}
		row = append(row, value)
		field.Reset()
		quoted = false
	}
	appendRecord := func() {
		appendField()
		rows = append(rows, row)
		row = nil
		endedRecord = true
	}
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		sawAny = true
		if inQuote {
			if r == options.quote {
				if i+1 < len(runes) && runes[i+1] == options.quote {
					field.WriteRune(options.quote)
					i++
				} else {
					inQuote = false
					quoted = true
				}
			} else {
				field.WriteRune(r)
			}
			endedRecord = false
			continue
		}
		switch r {
		case options.quote:
			if field.Len() == 0 {
				inQuote = true
			} else {
				field.WriteRune(r)
			}
			endedRecord = false
		case options.delimiter:
			appendField()
			endedRecord = false
		case '\r':
			if i+1 < len(runes) && runes[i+1] == '\n' {
				i++
			}
			appendRecord()
		case '\n':
			appendRecord()
		default:
			field.WriteRune(r)
			endedRecord = false
		}
	}
	if inQuote {
		return nil, fmt.Errorf("csv: unterminated quoted field")
	}
	if sawAny && !endedRecord {
		appendRecord()
	}
	return rows, nil
}

func csvDecodeHeaders(text string) Result[[]map[string]string, any] {
	rowsRes := csvDecode(text)
	if !rowsRes.IsOk {
		return Result[[]map[string]string, any]{Error: rowsRes.Error}
	}
	rows := rowsRes.Value
	if len(rows) == 0 {
		return Result[[]map[string]string, any]{Value: []map[string]string{}, IsOk: true}
	}
	headers := rows[0]
	out := make([]map[string]string, 0, len(rows)-1)
	for _, row := range rows[1:] {
		record := map[string]string{}
		for i, header := range headers {
			if i < len(row) {
				record[header] = row[i]
			} else {
				record[header] = ""
			}
		}
		out = append(out, record)
	}
	return Result[[]map[string]string, any]{Value: out, IsOk: true}
}
`)
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
		return Result[[]byte, any]{Error: err}
	}
	out, readErr := stdio.ReadAll(r)
	closeErr := r.Close()
	if readErr != nil {
		return Result[[]byte, any]{Error: readErr}
	}
	if closeErr != nil {
		return Result[[]byte, any]{Error: closeErr}
	}
	return Result[[]byte, any]{Value: out, IsOk: true}
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
			return Result[Uuid, any]{Error: fmt.Errorf("invalid UUID %q", text)}
		}
		compact = stdstrings.ReplaceAll(text, "-", "")
	default:
		return Result[Uuid, any]{Error: fmt.Errorf("invalid UUID %q", text)}
	}
	var u Uuid
	if _, err := stdhex.Decode(u[:], []byte(compact)); err != nil {
		return Result[Uuid, any]{Error: err}
	}
	return Result[Uuid, any]{Value: u, IsOk: true}
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
