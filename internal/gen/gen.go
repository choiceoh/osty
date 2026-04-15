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

	// needOstyError: Osty `Error` was referenced; emit the interface.
	// needFFIBasicError: Result bridge wrapped a Go error; emit the
	// adapter struct (which implies needOstyError at emit time).
	needOstyError     bool
	needFFIBasicError bool

	// currentRetType tracks the enclosing function's return type so the
	// `?` lift at let-stmt position can reconstruct the Result with the
	// correct type parameters when the operand's T differs.
	currentRetType ast.Type

	// questionSubs maps a QuestionExpr AST node to the Go expression
	// text that should be emitted in its place. Populated by the
	// statement-level pre-lift pass (see preLiftQuestions); consumed by
	// emitQuestion. Nil when no lift is in progress.
	questionSubs map[*ast.QuestionExpr]string
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

// run walks the file and returns the formatted Go source.
func (g *gen) run() ([]byte, error) {
	// 1a. Use declarations are stored in File.Uses separately from
	//     File.Decls. Walk them first so their aliases are in scope
	//     for the rest of the file (only matters for source order in
	//     Go, but keeps output tidy).
	for _, u := range g.file.Uses {
		g.emitUseDecl(u)
	}

	// 1b. Emit every declaration in source order.
	for _, d := range g.file.Decls {
		g.emitDecl(d)
	}

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

	// 3. Assemble header + body.
	var out bytes.Buffer
	fmt.Fprintln(&out, "// Code generated by osty. DO NOT EDIT.")
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
	if g.needResult {
		out.WriteString(`
// Result is the runtime representation of Osty's Result<T, E>.
// IsOk distinguishes Ok(Value) from Err(Error).
type Result[T any, E any] struct {
	Value T
	Error E
	IsOk  bool
}
`)
	}
	// basicFFIError implements ostyError structurally, so the adapter
	// can't be emitted without the interface it satisfies.
	if g.needOstyError || g.needFFIBasicError {
		out.WriteString(`
// ostyError is the Go-side representation of Osty's prelude Error
// interface (§7.1). Any concrete type with a "message() string"
// method satisfies it via Go's method-set matching.
type ostyError interface {
	message() string
}
`)
	}
	if g.needFFIBasicError {
		out.WriteString(`
// basicFFIError adapts a Go error into Osty's prelude Error interface
// (§12.4). The FFI Result bridge wraps a non-nil Go error in one of
// these so user code can call .message() on the bound Err arm; the
// underlying Go error is preserved for fmt via Error().
type basicFFIError struct{ err error }

func (b basicFFIError) message() string { return b.err.Error() }
func (b basicFFIError) Error() string   { return b.err.Error() }
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
