package llvmgen

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/token"
)

// ErrUnsupported marks source shapes that this early LLVM emitter does not
// lower yet.
var ErrUnsupported = errors.New("llvmgen: unsupported source shape")

// Options configures textual LLVM IR emission.
type Options struct {
	PackageName string
	SourcePath  string
	Target      string
}

// SmokeExecutableCase describes one LLVM executable parity case. The data is
// generated from the Osty-authored backend core; Go tests only execute it.
type SmokeExecutableCase struct {
	Name    string
	Fixture string
	Stdout  string
}

// UnsupportedDiagnostic is the self-hosted LLVM backend unsupported policy
// projected into the Go bootstrap bridge.
type UnsupportedDiagnostic struct {
	Code    string
	Kind    string
	Message string
	Hint    string
}

// UnsupportedError keeps errors.Is(err, ErrUnsupported) working while carrying
// the self-hosted diagnostic policy.
type UnsupportedError struct {
	Diagnostic UnsupportedDiagnostic
}

func (e *UnsupportedError) Error() string {
	return UnsupportedSummary(e.Diagnostic)
}

func (e *UnsupportedError) Unwrap() error {
	return ErrUnsupported
}

// RenderSkeleton emits the inspectable unsupported-source LLVM placeholder.
// The implementation is generated from examples/selfhost-core/llvmgen.osty so
// the skeleton shape remains owned by the Osty emitter core.
func RenderSkeleton(packageName, sourcePath, emit, target string, reason error) []byte {
	unsupported := ""
	if reason != nil {
		unsupported = reason.Error()
	}
	return []byte(llvmRenderSkeleton(packageName, filepath.ToSlash(sourcePath), emit, target, unsupported))
}

// NeedsObjectArtifact reports whether an LLVM emit mode should continue past
// textual IR into an object file. The decision is generated from the
// Osty-authored backend core.
func NeedsObjectArtifact(emit string) bool {
	return llvmNeedsObjectArtifact(emit)
}

// NeedsBinaryArtifact reports whether an LLVM emit mode should link a host
// binary after object emission. The decision is generated from the
// Osty-authored backend core.
func NeedsBinaryArtifact(emit string) bool {
	return llvmNeedsBinaryArtifact(emit)
}

// ClangCompileObjectArgs returns the argv shape for `.ll -> .o`, generated
// from the Osty-authored backend core.
func ClangCompileObjectArgs(target, irPath, objectPath string) []string {
	return llvmClangCompileObjectArgs(target, irPath, objectPath)
}

// ClangLinkBinaryArgs returns the argv shape for `.o -> binary`, generated
// from the Osty-authored backend core.
func ClangLinkBinaryArgs(target, objectPath, binaryPath string) []string {
	return llvmClangLinkBinaryArgs(target, objectPath, binaryPath)
}

func MissingClangMessage() string {
	return llvmMissingClangMessage()
}

func MissingBinaryArtifactMessage() string {
	return llvmMissingBinaryArtifactMessage()
}

func ClangFailureMessage(action, command, output string) string {
	return llvmClangFailureMessage(action, command, output)
}

func UnsupportedBackendErrorMessage() string {
	return llvmUnsupportedBackendErrorMessage()
}

func UnsupportedDiagnosticFor(kind, detail string) UnsupportedDiagnostic {
	return fromUnsupportedDiagnostic(llvmUnsupportedDiagnostic(kind, detail))
}

func UnsupportedSummary(diag UnsupportedDiagnostic) string {
	return llvmUnsupportedSummary(toUnsupportedDiagnostic(diag))
}

func UnsupportedDiagnosticForError(err error) UnsupportedDiagnostic {
	var unsupported *UnsupportedError
	if errors.As(err, &unsupported) {
		return unsupported.Diagnostic
	}
	detail := ""
	if err != nil {
		detail = err.Error()
	}
	return UnsupportedDiagnosticFor("unsupported-source", detail)
}

func unsupported(kind, detail string) error {
	return &UnsupportedError{Diagnostic: UnsupportedDiagnosticFor(kind, detail)}
}

func unsupportedf(kind, format string, args ...any) error {
	return unsupported(kind, fmt.Sprintf(format, args...))
}

func unsupportedMessage(err error) string {
	if err == nil {
		return ""
	}
	var unsupported *UnsupportedError
	if errors.As(err, &unsupported) {
		return unsupported.Diagnostic.Message
	}
	return err.Error()
}

// SmokeExecutableCorpus returns the self-hosted executable parity plan for the
// LLVM smoke corpus.
func SmokeExecutableCorpus() []SmokeExecutableCase {
	cases := llvmSmokeExecutableCorpus()
	out := make([]SmokeExecutableCase, 0, len(cases))
	for _, tc := range cases {
		out = append(out, SmokeExecutableCase{
			Name:    tc.name,
			Fixture: tc.fixture,
			Stdout:  tc.stdout,
		})
	}
	return out
}

func fromUnsupportedDiagnostic(diag *LlvmUnsupportedDiagnostic) UnsupportedDiagnostic {
	if diag == nil {
		return UnsupportedDiagnostic{}
	}
	return UnsupportedDiagnostic{
		Code:    diag.code,
		Kind:    diag.kind,
		Message: diag.message,
		Hint:    diag.hint,
	}
}

func toUnsupportedDiagnostic(diag UnsupportedDiagnostic) *LlvmUnsupportedDiagnostic {
	return &LlvmUnsupportedDiagnostic{
		code:    diag.Code,
		kind:    diag.Kind,
		message: diag.Message,
		hint:    diag.Hint,
	}
}

// Generate emits textual LLVM IR for a minimal, scalar subset.
func Generate(file *ast.File, opts Options) ([]byte, error) {
	if file == nil {
		return nil, unsupported("source-layout", "nil file")
	}
	if diag, ok := fileUnsupportedDiagnostic(file); ok {
		return nil, &UnsupportedError{Diagnostic: diag}
	}
	g := &generator{
		sourcePath: filepath.ToSlash(firstNonEmpty(opts.SourcePath, "<unknown>")),
		target:     opts.Target,
	}
	if len(file.Stmts) > 0 {
		if len(file.Decls) > 0 {
			return nil, unsupported("source-layout", "mixed script statements and declarations")
		}
		mainIR, err := g.emitScriptMain(file.Stmts)
		if err != nil {
			return nil, err
		}
		return g.render([]string{mainIR}), nil
	}
	decls, err := collectFunctions(file)
	if err != nil {
		return nil, err
	}
	g.functions = decls.byName
	var defs []string
	for _, sig := range decls.ordered {
		if sig.name == "main" {
			continue
		}
		def, err := g.emitUserFunction(sig)
		if err != nil {
			return nil, err
		}
		defs = append(defs, def)
	}
	mainSig := decls.byName["main"]
	if mainSig == nil {
		return nil, unsupported("source-layout", "missing main function or script statements")
	}
	mainIR, err := g.emitMainFunction(mainSig)
	if err != nil {
		return nil, err
	}
	defs = append(defs, mainIR)
	return g.render(defs), nil
}

func fileUnsupportedDiagnostic(file *ast.File) (UnsupportedDiagnostic, bool) {
	for _, use := range file.Uses {
		if use != nil && use.IsGoFFI {
			return UnsupportedDiagnosticFor("go-ffi", use.GoPath), true
		}
	}
	return UnsupportedDiagnostic{}, false
}

type generator struct {
	sourcePath string
	target     string
	functions  map[string]*fnSig

	temp       int
	label      int
	stringID   int
	stringDefs []*LlvmStringGlobal
	body       []string
	locals     []map[string]value
	returnType string
}

type fnDecls struct {
	ordered []*fnSig
	byName  map[string]*fnSig
}

type fnSig struct {
	name   string
	ret    string
	params []paramInfo
	decl   *ast.FnDecl
}

type paramInfo struct {
	name string
	typ  string
}

type value struct {
	typ string
	ref string
	ptr bool
}

func collectFunctions(file *ast.File) (*fnDecls, error) {
	out := &fnDecls{byName: map[string]*fnSig{}}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FnDecl)
		if !ok {
			return nil, unsupportedf("source-layout", "top-level declaration %T", decl)
		}
		sig, err := signatureOf(fn)
		if err != nil {
			return nil, err
		}
		if _, exists := out.byName[sig.name]; exists {
			return nil, unsupportedf("source-layout", "duplicate function %q", sig.name)
		}
		out.ordered = append(out.ordered, sig)
		out.byName[sig.name] = sig
	}
	return out, nil
}

func signatureOf(fn *ast.FnDecl) (*fnSig, error) {
	if fn == nil {
		return nil, unsupported("source-layout", "nil function")
	}
	if !isLLVMIdent(fn.Name) {
		return nil, unsupportedf("name", "function name %q", fn.Name)
	}
	if fn.Recv != nil {
		return nil, unsupported("function-signature", "methods are not supported")
	}
	if len(fn.Generics) != 0 {
		return nil, unsupported("function-signature", "generic functions are not supported")
	}
	if fn.Body == nil {
		return nil, unsupportedf("source-layout", "function %q has no body", fn.Name)
	}
	sig := &fnSig{name: fn.Name, decl: fn}
	if fn.Name == "main" {
		if len(fn.Params) != 0 || fn.ReturnType != nil {
			return nil, unsupported("function-signature", "LLVM main must have no params and no return type")
		}
		return sig, nil
	}
	ret, err := llvmType(fn.ReturnType)
	if err != nil {
		return nil, unsupportedf("type-system", "function %q return type: %s", fn.Name, unsupportedMessage(err))
	}
	sig.ret = ret
	for _, p := range fn.Params {
		if p.Pattern != nil || p.Name == "" {
			return nil, unsupportedf("function-signature", "function %q has non-identifier parameter", fn.Name)
		}
		if p.Default != nil {
			return nil, unsupportedf("function-signature", "function %q has default parameter values", fn.Name)
		}
		if !isLLVMIdent(p.Name) {
			return nil, unsupportedf("name", "parameter name %q", p.Name)
		}
		typ, err := llvmType(p.Type)
		if err != nil {
			return nil, unsupportedf("type-system", "function %q parameter %q: %s", fn.Name, p.Name, unsupportedMessage(err))
		}
		sig.params = append(sig.params, paramInfo{name: p.Name, typ: typ})
	}
	return sig, nil
}

func (g *generator) emitScriptMain(stmts []ast.Stmt) (string, error) {
	g.beginFunction()
	if err := g.emitBlock(stmts); err != nil {
		return "", err
	}
	emitter := g.toOstyEmitter()
	llvmReturnI32Zero(emitter)
	g.takeOstyEmitter(emitter)
	return g.renderFunction("i32", "main", nil), nil
}

func (g *generator) emitMainFunction(sig *fnSig) (string, error) {
	g.beginFunction()
	if err := g.emitBlock(sig.decl.Body.Stmts); err != nil {
		return "", err
	}
	emitter := g.toOstyEmitter()
	llvmReturnI32Zero(emitter)
	g.takeOstyEmitter(emitter)
	return g.renderFunction("i32", "main", nil), nil
}

func (g *generator) emitUserFunction(sig *fnSig) (string, error) {
	g.beginFunction()
	g.returnType = sig.ret
	for _, p := range sig.params {
		g.bindLocal(p.name, value{typ: p.typ, ref: "%" + p.name})
	}
	if err := g.emitReturningBlock(sig.decl.Body.Stmts, sig.ret); err != nil {
		return "", err
	}
	return g.renderFunction(sig.ret, sig.name, sig.params), nil
}

func (g *generator) beginFunction() {
	g.temp = 0
	g.label = 0
	g.body = nil
	g.locals = []map[string]value{{}}
	g.returnType = ""
}

func (g *generator) emitReturningBlock(stmts []ast.Stmt, retType string) error {
	if len(stmts) == 0 {
		return unsupported("function-signature", "function body has no return value")
	}
	for i, stmt := range stmts {
		if i != len(stmts)-1 {
			if err := g.emitStmt(stmt); err != nil {
				return err
			}
			continue
		}
		switch s := stmt.(type) {
		case *ast.ReturnStmt:
			if s.Value == nil {
				return unsupported("function-signature", "bare return in value-returning function")
			}
			v, err := g.emitExpr(s.Value)
			if err != nil {
				return err
			}
			if v.typ != retType {
				return unsupportedf("type-system", "return type %s, want %s", v.typ, retType)
			}
			emitter := g.toOstyEmitter()
			llvmReturn(emitter, toOstyValue(v))
			g.takeOstyEmitter(emitter)
			return nil
		case *ast.ExprStmt:
			v, err := g.emitExpr(s.X)
			if err != nil {
				return err
			}
			if v.typ != retType {
				return unsupportedf("type-system", "trailing expression type %s, want %s", v.typ, retType)
			}
			emitter := g.toOstyEmitter()
			llvmReturn(emitter, toOstyValue(v))
			g.takeOstyEmitter(emitter)
			return nil
		default:
			return unsupportedf("statement", "final function statement %T", stmt)
		}
	}
	return nil
}

func (g *generator) emitBlock(stmts []ast.Stmt) error {
	for _, stmt := range stmts {
		if err := g.emitStmt(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (g *generator) emitStmt(stmt ast.Stmt) error {
	switch s := stmt.(type) {
	case *ast.Block:
		g.pushScope()
		defer g.popScope()
		return g.emitBlock(s.Stmts)
	case *ast.LetStmt:
		return g.emitLet(s)
	case *ast.AssignStmt:
		return g.emitAssign(s)
	case *ast.ForStmt:
		return g.emitFor(s)
	case *ast.ExprStmt:
		if ifExpr, ok := s.X.(*ast.IfExpr); ok {
			return g.emitIfStmt(ifExpr)
		}
		return g.emitExprStmt(s.X)
	default:
		return unsupportedf("statement", "statement %T", stmt)
	}
}

func (g *generator) emitLet(stmt *ast.LetStmt) error {
	name, err := identPatternName(stmt.Pattern)
	if err != nil {
		return err
	}
	if stmt.Value == nil {
		return unsupportedf("statement", "let %q has no value", name)
	}
	v, err := g.emitExpr(stmt.Value)
	if err != nil {
		return err
	}
	if stmt.Type != nil {
		typ, err := llvmType(stmt.Type)
		if err != nil {
			return err
		}
		if typ != v.typ {
			return unsupportedf("type-system", "let %q type %s, value %s", name, typ, v.typ)
		}
	}
	if stmt.Mut {
		emitter := g.toOstyEmitter()
		slot := llvmMutableLetSlot(emitter, name, toOstyValue(v))
		g.takeOstyEmitter(emitter)
		g.bindLocal(name, fromOstyValue(slot))
		return nil
	}
	g.bindLocal(name, v)
	return nil
}

func (g *generator) emitAssign(stmt *ast.AssignStmt) error {
	if stmt.Op != token.ASSIGN {
		return unsupportedf("statement", "compound assignment %q", stmt.Op)
	}
	if len(stmt.Targets) != 1 {
		return unsupported("statement", "multi-target assignment")
	}
	target, ok := stmt.Targets[0].(*ast.Ident)
	if !ok {
		return unsupportedf("statement", "assignment target %T", stmt.Targets[0])
	}
	slot, ok := g.lookupLocal(target.Name)
	if !ok {
		return unsupportedf("name", "assignment to unknown identifier %q", target.Name)
	}
	if !slot.ptr {
		return unsupportedf("statement", "assignment to immutable identifier %q", target.Name)
	}
	v, err := g.emitExpr(stmt.Value)
	if err != nil {
		return err
	}
	if v.typ != slot.typ {
		return unsupportedf("type-system", "assignment to %q type %s, value %s", target.Name, slot.typ, v.typ)
	}
	emitter := g.toOstyEmitter()
	llvmStore(emitter, toOstyValue(slot), toOstyValue(v))
	g.takeOstyEmitter(emitter)
	return nil
}

func (g *generator) emitFor(stmt *ast.ForStmt) error {
	if stmt.IsForLet {
		return unsupported("control-flow", "for-let is not supported")
	}
	if stmt.Body == nil {
		return unsupported("control-flow", "for has no body")
	}
	iterName, err := identPatternName(stmt.Pattern)
	if err != nil {
		return err
	}
	rng, ok := stmt.Iter.(*ast.RangeExpr)
	if !ok {
		return unsupported("control-flow", "only range for-loops are supported")
	}
	if rng.Start == nil || rng.Stop == nil {
		return unsupported("control-flow", "open-ended ranges are not supported")
	}
	start, err := g.emitExpr(rng.Start)
	if err != nil {
		return err
	}
	stop, err := g.emitExpr(rng.Stop)
	if err != nil {
		return err
	}
	if start.typ != "i64" || stop.typ != "i64" {
		return unsupported("type-system", "range bounds must be Int")
	}
	emitter := g.toOstyEmitter()
	loop := llvmRangeStart(emitter, iterName, toOstyValue(start), toOstyValue(stop), rng.Inclusive)
	g.takeOstyEmitter(emitter)
	g.pushScope()
	g.bindLocal(iterName, value{typ: "i64", ref: loop.current})
	if err := g.emitBlock(stmt.Body.Stmts); err != nil {
		g.popScope()
		return err
	}
	g.popScope()
	emitter = g.toOstyEmitter()
	llvmRangeEnd(emitter, loop)
	g.takeOstyEmitter(emitter)
	return nil
}

func (g *generator) emitExprStmt(expr ast.Expr) error {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return unsupported("statement", "only println calls are supported as expression statements")
	}
	return g.emitPrintln(call)
}

func (g *generator) emitIfStmt(expr *ast.IfExpr) error {
	if expr.IsIfLet {
		return unsupported("control-flow", "if-let is not supported")
	}
	if expr.Then == nil {
		return unsupported("control-flow", "if has no then block")
	}
	cond, err := g.emitExpr(expr.Cond)
	if err != nil {
		return err
	}
	if cond.typ != "i1" {
		return unsupportedf("type-system", "if condition type %s, want i1", cond.typ)
	}
	emitter := g.toOstyEmitter()
	labels := llvmIfStart(emitter, toOstyValue(cond))
	g.takeOstyEmitter(emitter)
	g.pushScope()
	if err := g.emitBlock(expr.Then.Stmts); err != nil {
		g.popScope()
		return err
	}
	g.popScope()
	emitter = g.toOstyEmitter()
	llvmIfElse(emitter, labels)
	g.takeOstyEmitter(emitter)
	if expr.Else != nil {
		if err := g.emitElse(expr.Else); err != nil {
			return err
		}
	}
	emitter = g.toOstyEmitter()
	llvmIfEnd(emitter, labels)
	g.takeOstyEmitter(emitter)
	return nil
}

func (g *generator) emitElse(expr ast.Expr) error {
	switch e := expr.(type) {
	case *ast.Block:
		g.pushScope()
		defer g.popScope()
		return g.emitBlock(e.Stmts)
	case *ast.IfExpr:
		return g.emitIfStmt(e)
	default:
		return unsupportedf("control-flow", "else expression %T", expr)
	}
}

func (g *generator) emitPrintln(call *ast.CallExpr) error {
	id, ok := call.Fn.(*ast.Ident)
	if !ok || id.Name != "println" {
		return unsupported("call", "only println calls are supported")
	}
	if len(call.Args) != 1 || call.Args[0].Name != "" || call.Args[0].Value == nil {
		return unsupported("call", "println requires one positional argument")
	}
	v, err := g.emitExpr(call.Args[0].Value)
	if err != nil {
		return err
	}
	emitter := g.toOstyEmitter()
	switch v.typ {
	case "i64":
		llvmPrintlnI64(emitter, toOstyValue(v))
	case "ptr":
		llvmPrintlnString(emitter, toOstyValue(v))
	default:
		g.takeOstyEmitter(emitter)
		return unsupported("type-system", "println currently supports Int expressions and plain String values only")
	}
	g.takeOstyEmitter(emitter)
	return nil
}

func (g *generator) emitStringLiteral(lit *ast.StringLit) (value, error) {
	text, ok := plainStringLiteral(lit)
	if !ok {
		return value{}, unsupported("expression", "interpolated String literals are not supported by LLVM")
	}
	if !isLLVMASCIIStringText(text) {
		return value{}, unsupported("type-system", "plain String literals currently require ASCII text with printable bytes or newline, tab, and carriage-return escapes")
	}
	emitter := g.toOstyEmitter()
	out := llvmStringLiteral(emitter, text)
	g.takeOstyEmitter(emitter)
	return fromOstyValue(out), nil
}

func (g *generator) emitExpr(expr ast.Expr) (value, error) {
	switch e := expr.(type) {
	case *ast.IntLit:
		text := strings.ReplaceAll(e.Text, "_", "")
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return value{}, unsupportedf("expression", "invalid Int literal %q", e.Text)
		}
		return value{typ: "i64", ref: strconv.FormatInt(n, 10)}, nil
	case *ast.BoolLit:
		if e.Value {
			return value{typ: "i1", ref: "true"}, nil
		}
		return value{typ: "i1", ref: "false"}, nil
	case *ast.StringLit:
		return g.emitStringLiteral(e)
	case *ast.Ident:
		if v, ok := g.lookupLocal(e.Name); ok {
			if v.ptr {
				emitter := g.toOstyEmitter()
				out := llvmLoad(emitter, toOstyValue(v))
				g.takeOstyEmitter(emitter)
				return fromOstyValue(out), nil
			}
			return v, nil
		}
		return value{}, unsupportedf("name", "unknown identifier %q", e.Name)
	case *ast.ParenExpr:
		return g.emitExpr(e.X)
	case *ast.UnaryExpr:
		return g.emitUnary(e)
	case *ast.BinaryExpr:
		return g.emitBinary(e)
	case *ast.CallExpr:
		return g.emitCall(e)
	case *ast.IfExpr:
		return g.emitIfExprValue(e)
	default:
		return value{}, unsupportedf("expression", "expression %T", expr)
	}
}

func (g *generator) emitUnary(e *ast.UnaryExpr) (value, error) {
	v, err := g.emitExpr(e.X)
	if err != nil {
		return value{}, err
	}
	switch e.Op {
	case token.PLUS:
		if v.typ != "i64" {
			return value{}, unsupportedf("type-system", "unary plus on %s", v.typ)
		}
		return v, nil
	case token.MINUS:
		if v.typ != "i64" {
			return value{}, unsupportedf("type-system", "unary minus on %s", v.typ)
		}
		emitter := g.toOstyEmitter()
		out := llvmBinaryI64(emitter, "sub", llvmIntLiteral(0), toOstyValue(v))
		g.takeOstyEmitter(emitter)
		return fromOstyValue(out), nil
	case token.NOT:
		if v.typ != "i1" {
			return value{}, unsupportedf("type-system", "logical not on %s", v.typ)
		}
		emitter := g.toOstyEmitter()
		out := llvmNotI1(emitter, toOstyValue(v))
		g.takeOstyEmitter(emitter)
		return fromOstyValue(out), nil
	default:
		return value{}, unsupportedf("expression", "unary operator %q", e.Op)
	}
}

func (g *generator) emitBinary(e *ast.BinaryExpr) (value, error) {
	left, err := g.emitExpr(e.Left)
	if err != nil {
		return value{}, err
	}
	right, err := g.emitExpr(e.Right)
	if err != nil {
		return value{}, err
	}
	if isCompareOp(e.Op) {
		return g.emitCompare(e.Op, left, right)
	}
	if e.Op == token.AND || e.Op == token.OR {
		return g.emitLogical(e.Op, left, right)
	}
	if left.typ != "i64" || right.typ != "i64" {
		return value{}, unsupportedf("type-system", "binary operator %q on %s/%s", e.Op, left.typ, right.typ)
	}
	op := ""
	switch e.Op {
	case token.PLUS:
		op = "add"
	case token.MINUS:
		op = "sub"
	case token.STAR:
		op = "mul"
	case token.SLASH:
		op = "sdiv"
	case token.PERCENT:
		op = "srem"
	default:
		return value{}, unsupportedf("expression", "binary operator %q", e.Op)
	}
	emitter := g.toOstyEmitter()
	out := llvmBinaryI64(emitter, op, toOstyValue(left), toOstyValue(right))
	g.takeOstyEmitter(emitter)
	return fromOstyValue(out), nil
}

func (g *generator) emitLogical(op token.Kind, left, right value) (value, error) {
	if left.typ != "i1" || right.typ != "i1" {
		return value{}, unsupportedf("type-system", "logical operator %q on %s/%s", op, left.typ, right.typ)
	}
	inst := ""
	switch op {
	case token.AND:
		inst = "and"
	case token.OR:
		inst = "or"
	default:
		return value{}, unsupportedf("expression", "logical operator %q", op)
	}
	emitter := g.toOstyEmitter()
	out := llvmLogicalI1(emitter, inst, toOstyValue(left), toOstyValue(right))
	g.takeOstyEmitter(emitter)
	return fromOstyValue(out), nil
}

func (g *generator) emitCompare(op token.Kind, left, right value) (value, error) {
	if left.typ != right.typ {
		return value{}, unsupportedf("type-system", "compare type mismatch %s/%s", left.typ, right.typ)
	}
	pred := ""
	switch op {
	case token.EQ:
		pred = "eq"
	case token.NEQ:
		pred = "ne"
	case token.LT:
		pred = "slt"
	case token.GT:
		pred = "sgt"
	case token.LEQ:
		pred = "sle"
	case token.GEQ:
		pred = "sge"
	default:
		return value{}, unsupportedf("expression", "comparison operator %q", op)
	}
	switch left.typ {
	case "i64", "i1":
	default:
		return value{}, unsupportedf("type-system", "compare type %s", left.typ)
	}
	emitter := g.toOstyEmitter()
	out := llvmCompare(emitter, pred, toOstyValue(left), toOstyValue(right))
	g.takeOstyEmitter(emitter)
	return fromOstyValue(out), nil
}

func (g *generator) emitIfExprValue(expr *ast.IfExpr) (value, error) {
	if expr.IsIfLet {
		return value{}, unsupported("control-flow", "if-let is not supported")
	}
	if expr.Then == nil {
		return value{}, unsupported("control-flow", "if expression has no then block")
	}
	if expr.Else == nil {
		return value{}, unsupported("control-flow", "if expression has no else branch")
	}
	cond, err := g.emitExpr(expr.Cond)
	if err != nil {
		return value{}, err
	}
	if cond.typ != "i1" {
		return value{}, unsupportedf("type-system", "if condition type %s, want i1", cond.typ)
	}
	emitter := g.toOstyEmitter()
	labels := llvmIfExprStart(emitter, toOstyValue(cond))
	g.takeOstyEmitter(emitter)

	g.pushScope()
	thenValue, err := g.emitBlockValue(expr.Then)
	g.popScope()
	if err != nil {
		return value{}, err
	}
	emitter = g.toOstyEmitter()
	llvmIfExprElse(emitter, labels)
	g.takeOstyEmitter(emitter)

	elseValue, err := g.emitElseValue(expr.Else)
	if err != nil {
		return value{}, err
	}
	if thenValue.typ != elseValue.typ {
		return value{}, unsupportedf("type-system", "if expression branch types %s/%s", thenValue.typ, elseValue.typ)
	}
	emitter = g.toOstyEmitter()
	out := llvmIfExprEnd(emitter, thenValue.typ, toOstyValue(thenValue), toOstyValue(elseValue), labels)
	g.takeOstyEmitter(emitter)
	return fromOstyValue(out), nil
}

func (g *generator) emitBlockValue(block *ast.Block) (value, error) {
	if block == nil || len(block.Stmts) == 0 {
		return value{}, unsupported("expression", "block has no value")
	}
	for i, stmt := range block.Stmts {
		if i != len(block.Stmts)-1 {
			if err := g.emitStmt(stmt); err != nil {
				return value{}, err
			}
			continue
		}
		exprStmt, ok := stmt.(*ast.ExprStmt)
		if !ok {
			return value{}, unsupportedf("statement", "final block statement %T", stmt)
		}
		return g.emitExpr(exprStmt.X)
	}
	return value{}, unsupported("expression", "block has no value")
}

func (g *generator) emitElseValue(expr ast.Expr) (value, error) {
	switch e := expr.(type) {
	case *ast.Block:
		g.pushScope()
		defer g.popScope()
		return g.emitBlockValue(e)
	case *ast.IfExpr:
		return g.emitIfExprValue(e)
	default:
		return value{}, unsupportedf("control-flow", "else expression %T", expr)
	}
}

func (g *generator) emitCall(call *ast.CallExpr) (value, error) {
	id, ok := call.Fn.(*ast.Ident)
	if !ok {
		return value{}, unsupportedf("call", "call target %T", call.Fn)
	}
	if id.Name == "println" {
		return value{}, unsupported("call", "println is only supported as a statement")
	}
	sig := g.functions[id.Name]
	if sig == nil {
		return value{}, unsupportedf("name", "unknown function %q", id.Name)
	}
	if sig.ret == "" {
		return value{}, unsupportedf("call", "function %q has no return value", id.Name)
	}
	if len(call.Args) != len(sig.params) {
		return value{}, unsupportedf("call", "function %q argument count", id.Name)
	}
	args := make([]*LlvmValue, 0, len(call.Args))
	for i, arg := range call.Args {
		if arg.Name != "" || arg.Value == nil {
			return value{}, unsupportedf("call", "function %q requires positional arguments", id.Name)
		}
		v, err := g.emitExpr(arg.Value)
		if err != nil {
			return value{}, err
		}
		param := sig.params[i]
		if v.typ != param.typ {
			return value{}, unsupportedf("type-system", "function %q arg %d type %s, want %s", id.Name, i+1, v.typ, param.typ)
		}
		args = append(args, toOstyValue(v))
	}
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, sig.ret, sig.name, args)
	g.takeOstyEmitter(emitter)
	return fromOstyValue(out), nil
}

func (g *generator) render(defs []string) []byte {
	return []byte(llvmRenderModuleWithGlobals(g.sourcePath, g.target, g.stringDefs, defs))
}

func (g *generator) renderFunction(ret, name string, params []paramInfo) string {
	return llvmRenderFunction(ret, name, toLLVMParams(params), g.body)
}

func (g *generator) toOstyEmitter() *LlvmEmitter {
	return &LlvmEmitter{
		temp:          g.temp,
		label:         g.label,
		stringId:      g.stringID,
		body:          append([]string(nil), g.body...),
		stringGlobals: append([]*LlvmStringGlobal(nil), g.stringDefs...),
	}
}

func (g *generator) takeOstyEmitter(emitter *LlvmEmitter) {
	g.temp = emitter.temp
	g.label = emitter.label
	g.stringID = emitter.stringId
	g.body = emitter.body
	g.stringDefs = emitter.stringGlobals
}

func toOstyValue(v value) *LlvmValue {
	return &LlvmValue{
		typ:     v.typ,
		name:    v.ref,
		pointer: v.ptr,
	}
}

func fromOstyValue(v *LlvmValue) value {
	return value{
		typ: v.typ,
		ref: v.name,
		ptr: v.pointer,
	}
}

func plainStringLiteral(lit *ast.StringLit) (string, bool) {
	if lit == nil || lit.IsRaw || lit.IsTriple {
		return "", false
	}
	var b strings.Builder
	for _, part := range lit.Parts {
		if !part.IsLit {
			return "", false
		}
		b.WriteString(part.Lit)
	}
	return b.String(), true
}

func isLLVMASCIIStringText(text string) bool {
	for i := 0; i < len(text); i++ {
		ch := text[i]
		switch ch {
		case '\n', '\t', '\r':
			continue
		}
		if ch < 0x20 || ch > 0x7e {
			return false
		}
	}
	return true
}

func toLLVMParams(params []paramInfo) []*LlvmParam {
	out := make([]*LlvmParam, 0, len(params))
	for _, p := range params {
		out = append(out, llvmParam(p.name, p.typ))
	}
	return out
}

func (g *generator) pushScope() {
	g.locals = append(g.locals, map[string]value{})
}

func (g *generator) popScope() {
	g.locals = g.locals[:len(g.locals)-1]
}

func (g *generator) bindLocal(name string, v value) {
	g.locals[len(g.locals)-1][name] = v
}

func (g *generator) lookupLocal(name string) (value, bool) {
	for i := len(g.locals) - 1; i >= 0; i-- {
		if v, ok := g.locals[i][name]; ok {
			return v, true
		}
	}
	return value{}, false
}

func identPatternName(p ast.Pattern) (string, error) {
	id, ok := p.(*ast.IdentPat)
	if !ok || id.Name == "" {
		return "", unsupported("statement", "only identifier let patterns are supported")
	}
	if !isLLVMIdent(id.Name) {
		return "", unsupportedf("name", "let name %q", id.Name)
	}
	return id.Name, nil
}

func llvmType(t ast.Type) (string, error) {
	named, ok := t.(*ast.NamedType)
	if !ok {
		return "", unsupportedf("type-system", "type %T", t)
	}
	if len(named.Args) != 0 || len(named.Path) != 1 {
		return "", unsupportedf("type-system", "type %T", t)
	}
	switch named.Path[0] {
	case "Int":
		return "i64", nil
	case "Bool":
		return "i1", nil
	case "String":
		return "ptr", nil
	default:
		return "", unsupportedf("type-system", "type %q", strings.Join(named.Path, "."))
	}
}

func isCompareOp(op token.Kind) bool {
	switch op {
	case token.EQ, token.NEQ, token.LT, token.GT, token.LEQ, token.GEQ:
		return true
	default:
		return false
	}
}

func isLLVMIdent(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c == '_' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || (i > 0 && '0' <= c && c <= '9') {
			continue
		}
		return false
	}
	return true
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
