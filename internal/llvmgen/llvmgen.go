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
		sourcePath:      filepath.ToSlash(firstNonEmpty(opts.SourcePath, "<unknown>")),
		target:          opts.Target,
		runtimeFFI:      map[string]map[string]*runtimeFFIFunction{},
		runtimeFFIPaths: map[string]string{},
		runtimeDecls:    map[string]runtimeDecl{},
	}
	if len(file.Stmts) > 0 {
		if len(file.Decls) > 0 {
			return nil, unsupported("source-layout", "mixed script statements and declarations")
		}
		g.runtimeFFI = collectRuntimeFFI(file, nil, nil)
		g.runtimeFFIPaths = collectRuntimeFFIPaths(file)
		mainIR, err := g.emitScriptMain(file.Stmts)
		if err != nil {
			return nil, err
		}
		return g.render([]string{mainIR}), nil
	}
	decls, err := collectDeclarations(file)
	if err != nil {
		return nil, err
	}
	g.functions = decls.functionsByName
	g.structs = decls.structsOrdered
	g.structsByName = decls.structsByName
	g.structsByType = decls.structsByType
	g.enums = decls.enumsOrdered
	g.enumsByName = decls.enumsByName
	g.enumsByType = decls.enumsByType
	g.runtimeFFI = collectRuntimeFFI(file, decls.structsByName, decls.enumsByName)
	g.runtimeFFIPaths = collectRuntimeFFIPaths(file)
	var defs []string
	for _, sig := range decls.functionsOrdered {
		if sig.name == "main" {
			continue
		}
		def, err := g.emitUserFunction(sig)
		if err != nil {
			return nil, err
		}
		defs = append(defs, def)
	}
	mainSig := decls.functionsByName["main"]
	if mainSig == nil {
		if len(defs) == 0 {
			return nil, unsupported("source-layout", "missing main function or script statements")
		}
		return g.render(defs), nil
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
		if use != nil && use.IsRuntimeFFI && !isKnownRuntimeFFIPath(use.RuntimePath) {
			return UnsupportedDiagnosticFor("runtime-ffi", use.RuntimePath), true
		}
	}
	return UnsupportedDiagnostic{}, false
}

type generator struct {
	sourcePath       string
	target           string
	functions        map[string]*fnSig
	structs          []*structInfo
	structsByName    map[string]*structInfo
	structsByType    map[string]*structInfo
	enums            []*enumInfo
	enumsByName      map[string]*enumInfo
	enumsByType      map[string]*enumInfo
	runtimeFFI       map[string]map[string]*runtimeFFIFunction
	runtimeFFIPaths  map[string]string
	runtimeDecls     map[string]runtimeDecl
	runtimeDeclOrder []string

	temp         int
	label        int
	stringID     int
	stringDefs   []*LlvmStringGlobal
	body         []string
	locals       []map[string]value
	returnType   string
	currentBlock string

	needsGCRuntime bool
	gcRootSlots    []value
}

type declarations struct {
	functionsOrdered []*fnSig
	functionsByName  map[string]*fnSig
	structsOrdered   []*structInfo
	structsByName    map[string]*structInfo
	structsByType    map[string]*structInfo
	enumsOrdered     []*enumInfo
	enumsByName      map[string]*enumInfo
	enumsByType      map[string]*enumInfo
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

type structInfo struct {
	name   string
	typ    string
	decl   *ast.StructDecl
	fields []fieldInfo
	byName map[string]fieldInfo
}

type fieldInfo struct {
	name  string
	typ   string
	index int
}

type enumInfo struct {
	name       string
	typ        string
	decl       *ast.EnumDecl
	hasPayload bool
	payloadTyp string
	variants   map[string]variantInfo
}

type variantInfo struct {
	name     string
	tag      int
	payloads []string
}

type enumVariantRef struct {
	enum    *enumInfo
	variant variantInfo
}

type enumPatternInfo struct {
	variant           variantInfo
	payloadName       string
	payloadType       string
	hasPayloadBinding bool
}

type value struct {
	typ       string
	ref       string
	ptr       bool
	gcManaged bool
}

const (
	llvmGcRuntimeFrameSlotKind = 5
)

type runtimeFFIFunction struct {
	path        string
	sourceName  string
	symbol      string
	ret         string
	params      []paramInfo
	unsupported string
}

type runtimeDecl struct {
	symbol string
	ret    string
	params []paramInfo
}

func collectDeclarations(file *ast.File) (*declarations, error) {
	out := &declarations{
		functionsByName: map[string]*fnSig{},
		structsByName:   map[string]*structInfo{},
		structsByType:   map[string]*structInfo{},
		enumsByName:     map[string]*enumInfo{},
		enumsByType:     map[string]*enumInfo{},
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.StructDecl:
			info, err := collectStructShell(d)
			if err != nil {
				return nil, err
			}
			if _, exists := out.structsByName[info.name]; exists {
				return nil, unsupportedf("source-layout", "duplicate struct %q", info.name)
			}
			out.structsOrdered = append(out.structsOrdered, info)
			out.structsByName[info.name] = info
			out.structsByType[info.typ] = info
		case *ast.EnumDecl:
			info, err := collectEnum(d)
			if err != nil {
				return nil, err
			}
			if _, exists := out.enumsByName[info.name]; exists {
				return nil, unsupportedf("source-layout", "duplicate enum %q", info.name)
			}
			out.enumsOrdered = append(out.enumsOrdered, info)
			out.enumsByName[info.name] = info
			if info.hasPayload {
				out.enumsByType[info.typ] = info
			}
		case *ast.FnDecl:
			// Function signatures are collected after struct shells so named
			// struct types can appear in parameters and returns.
		default:
			return nil, unsupportedf("source-layout", "top-level declaration %T", decl)
		}
	}
	for _, info := range out.structsOrdered {
		if err := collectStructFields(info, out.structsByName); err != nil {
			return nil, err
		}
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FnDecl)
		if !ok {
			continue
		}
		sig, err := signatureOf(fn, out.structsByName, out.enumsByName)
		if err != nil {
			return nil, err
		}
		if _, exists := out.functionsByName[sig.name]; exists {
			return nil, unsupportedf("source-layout", "duplicate function %q", sig.name)
		}
		out.functionsOrdered = append(out.functionsOrdered, sig)
		out.functionsByName[sig.name] = sig
	}
	return out, nil
}

func collectRuntimeFFI(file *ast.File, structs map[string]*structInfo, enums map[string]*enumInfo) map[string]map[string]*runtimeFFIFunction {
	out := map[string]map[string]*runtimeFFIFunction{}
	if file == nil {
		return out
	}
	for _, use := range file.Uses {
		if use == nil || !use.IsRuntimeFFI || !isKnownRuntimeFFIPath(use.RuntimePath) {
			continue
		}
		alias := runtimeFFIAlias(use)
		if alias == "" {
			continue
		}
		funcs := out[alias]
		if funcs == nil {
			funcs = map[string]*runtimeFFIFunction{}
			out[alias] = funcs
		}
		for _, decl := range use.GoBody {
			fn, ok := decl.(*ast.FnDecl)
			if !ok || fn == nil || fn.Name == "" {
				continue
			}
			funcs[fn.Name] = runtimeFFISignature(use.RuntimePath, fn, structs, enums)
		}
	}
	return out
}

func collectRuntimeFFIPaths(file *ast.File) map[string]string {
	out := map[string]string{}
	if file == nil {
		return out
	}
	for _, use := range file.Uses {
		if use == nil || !use.IsRuntimeFFI || !isKnownRuntimeFFIPath(use.RuntimePath) {
			continue
		}
		if alias := runtimeFFIAlias(use); alias != "" {
			out[alias] = use.RuntimePath
		}
	}
	return out
}

func runtimeFFISignature(path string, fn *ast.FnDecl, structs map[string]*structInfo, enums map[string]*enumInfo) *runtimeFFIFunction {
	out := &runtimeFFIFunction{
		path:       path,
		sourceName: fn.Name,
		symbol:     runtimeFFISymbol(path, fn.Name),
	}
	if fn.Recv != nil {
		out.unsupported = "methods are not supported"
		return out
	}
	if len(fn.Generics) != 0 {
		out.unsupported = "generic functions are not supported"
		return out
	}
	if fn.ReturnType == nil {
		out.ret = "void"
	} else {
		ret, err := llvmRuntimeABIType(fn.ReturnType, structs, enums)
		if err != nil {
			out.unsupported = "return type: " + unsupportedMessage(err)
			return out
		}
		out.ret = ret
	}
	for _, p := range fn.Params {
		if p == nil {
			out.unsupported = "nil parameter"
			return out
		}
		if p.Pattern != nil || p.Default != nil {
			out.unsupported = "pattern/default parameters are not supported"
			return out
		}
		name := p.Name
		if name == "" {
			name = fmt.Sprintf("arg%d", len(out.params))
		}
		typ, err := llvmRuntimeABIType(p.Type, structs, enums)
		if err != nil {
			out.unsupported = fmt.Sprintf("parameter %q: %s", name, unsupportedMessage(err))
			return out
		}
		out.params = append(out.params, paramInfo{name: name, typ: typ})
	}
	return out
}

func runtimeFFIAlias(use *ast.UseDecl) string {
	if use == nil {
		return ""
	}
	if use.Alias != "" {
		return use.Alias
	}
	if len(use.Path) > 0 {
		return use.Path[len(use.Path)-1]
	}
	parts := strings.Split(use.RuntimePath, ".")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

func runtimeFFISymbol(path, name string) string {
	path = strings.TrimPrefix(path, "runtime.")
	var b strings.Builder
	b.WriteString("osty_rt_")
	for i := 0; i < len(path); i++ {
		c := path[i]
		if c == '.' || c == '/' || c == '-' {
			b.WriteByte('_')
			continue
		}
		if c == '_' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9') {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('_')
	}
	b.WriteByte('_')
	b.WriteString(name)
	return b.String()
}

func isKnownRuntimeFFIPath(path string) bool {
	if strings.HasPrefix(path, "runtime.package.") {
		return true
	}
	switch path {
	case "runtime.strings", "runtime.path.filepath", "runtime.selfhost.astbridge":
		return true
	default:
		return false
	}
}

func llvmRuntimeABIType(t ast.Type, structs map[string]*structInfo, enums map[string]*enumInfo) (string, error) {
	switch tt := t.(type) {
	case nil:
		return "void", nil
	case *ast.NamedType:
		if len(tt.Path) == 1 && len(tt.Args) == 0 {
			switch tt.Path[0] {
			case "Int", "Float", "Bool", "String":
				return llvmType(tt, structs, enums)
			}
			if info := structs[tt.Path[0]]; info != nil {
				return info.typ, nil
			}
			if info := enums[tt.Path[0]]; info != nil {
				return info.typ, nil
			}
		}
		return "ptr", nil
	case *ast.OptionalType, *ast.TupleType, *ast.FnType:
		return "ptr", nil
	default:
		return "", unsupportedf("type-system", "runtime ABI type %T", t)
	}
}

func collectStructShell(decl *ast.StructDecl) (*structInfo, error) {
	if decl == nil {
		return nil, unsupported("source-layout", "nil struct")
	}
	if !isLLVMIdent(decl.Name) {
		return nil, unsupportedf("name", "struct name %q", decl.Name)
	}
	if len(decl.Generics) != 0 {
		return nil, unsupportedf("type-system", "generic struct %q is not supported", decl.Name)
	}
	if len(decl.Methods) != 0 {
		return nil, unsupportedf("function-signature", "struct %q methods are not supported", decl.Name)
	}
	return &structInfo{
		name:   decl.Name,
		typ:    "%" + decl.Name,
		decl:   decl,
		byName: map[string]fieldInfo{},
	}, nil
}

func collectStructFields(info *structInfo, structs map[string]*structInfo) error {
	if info == nil || info.decl == nil {
		return unsupported("source-layout", "nil struct")
	}
	for i, field := range info.decl.Fields {
		if field == nil {
			return unsupportedf("source-layout", "struct %q has nil field", info.name)
		}
		if !isLLVMIdent(field.Name) {
			return unsupportedf("name", "struct %q field name %q", info.name, field.Name)
		}
		if field.Default != nil {
			return unsupportedf("type-system", "struct %q field %q has a default value", info.name, field.Name)
		}
		if _, exists := info.byName[field.Name]; exists {
			return unsupportedf("source-layout", "struct %q duplicate field %q", info.name, field.Name)
		}
		typ, err := llvmType(field.Type, structs, nil)
		if err != nil {
			return unsupportedf("type-system", "struct %q field %q: %s", info.name, field.Name, unsupportedMessage(err))
		}
		if typ == info.typ {
			return unsupportedf("type-system", "struct %q recursive field %q", info.name, field.Name)
		}
		fieldInfo := fieldInfo{name: field.Name, typ: typ, index: i}
		info.fields = append(info.fields, fieldInfo)
		info.byName[field.Name] = fieldInfo
	}
	return nil
}

func collectEnum(decl *ast.EnumDecl) (*enumInfo, error) {
	if decl == nil {
		return nil, unsupported("source-layout", "nil enum")
	}
	if !isLLVMIdent(decl.Name) {
		return nil, unsupportedf("name", "enum name %q", decl.Name)
	}
	if len(decl.Generics) != 0 {
		return nil, unsupportedf("type-system", "generic enum %q is not supported", decl.Name)
	}
	if len(decl.Methods) != 0 {
		return nil, unsupportedf("function-signature", "enum %q methods are not supported", decl.Name)
	}
	info := &enumInfo{
		name:     decl.Name,
		typ:      "i64",
		decl:     decl,
		variants: map[string]variantInfo{},
	}
	for i, variant := range decl.Variants {
		if variant == nil {
			return nil, unsupportedf("source-layout", "enum %q has nil variant", decl.Name)
		}
		if !isLLVMIdent(variant.Name) {
			return nil, unsupportedf("name", "enum %q variant name %q", decl.Name, variant.Name)
		}
		payloads := make([]string, 0, len(variant.Fields))
		if len(variant.Fields) > 1 {
			return nil, unsupportedf("type-system", "enum %q variant %q has %d payload fields; only one scalar payload is supported", decl.Name, variant.Name, len(variant.Fields))
		}
		if len(variant.Fields) == 1 {
			typ, err := llvmEnumPayloadType(variant.Fields[0])
			if err != nil {
				return nil, unsupportedf("type-system", "enum %q variant %q payload: %s", decl.Name, variant.Name, unsupportedMessage(err))
			}
			if info.payloadTyp == "" {
				info.payloadTyp = typ
			} else if info.payloadTyp != typ {
				return nil, unsupportedf("type-system", "enum %q mixes payload types %s and %s", decl.Name, info.payloadTyp, typ)
			}
			payloads = append(payloads, typ)
			info.hasPayload = true
		}
		if _, exists := info.variants[variant.Name]; exists {
			return nil, unsupportedf("source-layout", "enum %q duplicate variant %q", decl.Name, variant.Name)
		}
		info.variants[variant.Name] = variantInfo{name: variant.Name, tag: i, payloads: payloads}
	}
	if info.hasPayload {
		info.typ = "%" + info.name
	}
	return info, nil
}

func llvmEnumPayloadType(t ast.Type) (string, error) {
	named, ok := t.(*ast.NamedType)
	if !ok || len(named.Args) != 0 || len(named.Path) != 1 {
		return "", unsupported("type-system", "LLVM enum payloads currently support Int or Float only")
	}
	switch named.Path[0] {
	case "Int":
		return "i64", nil
	case "Float":
		return "double", nil
	case "String":
		return "ptr", nil
	default:
		return "", unsupported("type-system", "LLVM enum payloads currently support Int, Float, or String only")
	}
}

func signatureOf(fn *ast.FnDecl, structs map[string]*structInfo, enums map[string]*enumInfo) (*fnSig, error) {
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
	if fn.ReturnType == nil {
		sig.ret = "void"
	} else {
		ret, err := llvmType(fn.ReturnType, structs, enums)
		if err != nil {
			return nil, unsupportedf("type-system", "function %q return type: %s", fn.Name, unsupportedMessage(err))
		}
		sig.ret = ret
	}
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
		typ, err := llvmType(p.Type, structs, enums)
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
	g.releaseGCRoots(emitter)
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
	g.releaseGCRoots(emitter)
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
	if sig.ret == "void" {
		if err := g.emitBlock(sig.decl.Body.Stmts); err != nil {
			return "", err
		}
		g.body = append(g.body, "  ret void")
	} else {
		if err := g.emitReturningBlock(sig.decl.Body.Stmts, sig.ret); err != nil {
			return "", err
		}
	}
	return g.renderFunction(sig.ret, sig.name, sig.params), nil
}

func (g *generator) beginFunction() {
	g.temp = 0
	g.label = 0
	g.body = nil
	g.locals = []map[string]value{{}}
	g.returnType = ""
	g.gcRootSlots = nil
	g.currentBlock = "entry"
}

func (g *generator) bindGCRootIfManagedPointer(emitter *LlvmEmitter, slot value) {
	if slot.typ != "ptr" || !slot.gcManaged {
		return
	}
	llvmGcRootBind(emitter, toOstyValue(slot))
	g.gcRootSlots = append(g.gcRootSlots, slot)
	g.needsGCRuntime = true
}

func (g *generator) postGCWriteIfPointer(emitter *LlvmEmitter, slot, v value) {
	if slot.typ != "ptr" || !slot.gcManaged || v.typ != "ptr" || !v.gcManaged {
		return
	}
	llvmGcPostWrite(emitter, toOstyValue(slot), toOstyValue(v), llvmGcRuntimeFrameSlotKind)
	g.needsGCRuntime = true
}

func (g *generator) releaseGCRoots(emitter *LlvmEmitter) {
	for i := len(g.gcRootSlots) - 1; i >= 0; i-- {
		llvmGcRootRelease(emitter, toOstyValue(g.gcRootSlots[i]))
	}
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
			g.releaseGCRoots(emitter)
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
			g.releaseGCRoots(emitter)
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
		typ, err := llvmType(stmt.Type, g.structsByName, g.enumsByName)
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
		slotValue := fromOstyValue(slot)
		slotValue.gcManaged = v.gcManaged
		g.bindGCRootIfManagedPointer(emitter, slotValue)
		g.takeOstyEmitter(emitter)
		g.bindLocal(name, slotValue)
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
	g.postGCWriteIfPointer(emitter, slot, v)
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
	if emitted, err := g.emitRuntimeFFICallStmt(call); emitted || err != nil {
		return err
	}
	if emitted, err := g.emitUserCallStmt(call); emitted || err != nil {
		return err
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
	g.currentBlock = labels.thenLabel
	g.pushScope()
	if err := g.emitBlock(expr.Then.Stmts); err != nil {
		g.popScope()
		return err
	}
	g.popScope()
	emitter = g.toOstyEmitter()
	llvmIfElse(emitter, labels)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.elseLabel
	if expr.Else != nil {
		if err := g.emitElse(expr.Else); err != nil {
			return err
		}
	}
	emitter = g.toOstyEmitter()
	llvmIfEnd(emitter, labels)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.endLabel
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
	case "double":
		llvmPrintlnF64(emitter, toOstyValue(v))
	case "ptr":
		llvmPrintlnString(emitter, toOstyValue(v))
	default:
		g.takeOstyEmitter(emitter)
		return unsupported("type-system", "println currently supports Int, Float, and plain String values only")
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
	case *ast.FloatLit:
		text := strings.ReplaceAll(e.Text, "_", "")
		f, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return value{}, unsupportedf("expression", "invalid Float literal %q", e.Text)
		}
		out := llvmFloatLiteral(strconv.FormatFloat(f, 'e', 16, 64))
		return fromOstyValue(out), nil
	case *ast.BoolLit:
		if e.Value {
			return value{typ: "i1", ref: "true"}, nil
		}
		return value{typ: "i1", ref: "false"}, nil
	case *ast.StringLit:
		return g.emitStringLiteral(e)
	case *ast.Ident:
		return g.emitIdent(e.Name)
	case *ast.ParenExpr:
		return g.emitExpr(e.X)
	case *ast.UnaryExpr:
		return g.emitUnary(e)
	case *ast.BinaryExpr:
		return g.emitBinary(e)
	case *ast.CallExpr:
		return g.emitCall(e)
	case *ast.FieldExpr:
		return g.emitFieldExpr(e)
	case *ast.StructLit:
		return g.emitStructLit(e)
	case *ast.IfExpr:
		return g.emitIfExprValue(e)
	case *ast.MatchExpr:
		return g.emitMatchExprValue(e)
	default:
		return value{}, unsupportedf("expression", "expression %T", expr)
	}
}

func (g *generator) emitIdent(name string) (value, error) {
	if v, ok := g.lookupLocal(name); ok {
		return g.loadIfPointer(v)
	}
	if v, found, err := g.enumVariantIdent(name); found || err != nil {
		return v, err
	}
	return value{}, unsupportedf("name", "unknown identifier %q", name)
}

func (g *generator) loadIfPointer(v value) (value, error) {
	if !v.ptr {
		return v, nil
	}
	emitter := g.toOstyEmitter()
	out := llvmLoad(emitter, toOstyValue(v))
	g.takeOstyEmitter(emitter)
	return fromOstyValue(out), nil
}

func (g *generator) emitStructLit(lit *ast.StructLit) (value, error) {
	typeName, ok := structTypeExprName(lit.Type)
	if !ok {
		return value{}, unsupportedf("type-system", "struct literal type %T", lit.Type)
	}
	info := g.structsByName[typeName]
	if info == nil {
		return value{}, unsupportedf("type-system", "unknown struct %q", typeName)
	}
	if lit.Spread != nil {
		return value{}, unsupportedf("expression", "struct %q spread literal", typeName)
	}
	fields := map[string]*ast.StructLitField{}
	for _, field := range lit.Fields {
		if field == nil {
			return value{}, unsupportedf("expression", "struct %q has nil literal field", typeName)
		}
		if !isLLVMIdent(field.Name) {
			return value{}, unsupportedf("name", "struct %q literal field name %q", typeName, field.Name)
		}
		if _, exists := fields[field.Name]; exists {
			return value{}, unsupportedf("expression", "struct %q duplicate literal field %q", typeName, field.Name)
		}
		if _, exists := info.byName[field.Name]; !exists {
			return value{}, unsupportedf("expression", "struct %q unknown literal field %q", typeName, field.Name)
		}
		fields[field.Name] = field
	}
	values := make([]*LlvmValue, 0, len(info.fields))
	for _, field := range info.fields {
		litField := fields[field.name]
		if litField == nil {
			return value{}, unsupportedf("expression", "struct %q missing literal field %q", typeName, field.name)
		}
		var v value
		var err error
		if litField.Value == nil {
			v, err = g.emitIdent(litField.Name)
		} else {
			v, err = g.emitExpr(litField.Value)
		}
		if err != nil {
			return value{}, err
		}
		if v.typ != field.typ {
			return value{}, unsupportedf("type-system", "struct %q field %q type %s, value %s", typeName, field.name, field.typ, v.typ)
		}
		values = append(values, toOstyValue(v))
	}
	emitter := g.toOstyEmitter()
	out := llvmStructLiteral(emitter, info.typ, values)
	g.takeOstyEmitter(emitter)
	return fromOstyValue(out), nil
}

func (g *generator) emitFieldExpr(expr *ast.FieldExpr) (value, error) {
	if expr.IsOptional {
		return value{}, unsupported("expression", "optional field access is not supported")
	}
	if v, found, err := g.enumVariantValue(expr); found || err != nil {
		return v, err
	}
	base, err := g.emitExpr(expr.X)
	if err != nil {
		return value{}, err
	}
	info := g.structsByType[base.typ]
	if info == nil {
		return value{}, unsupportedf("type-system", "field access on %s", base.typ)
	}
	field, ok := info.byName[expr.Name]
	if !ok {
		return value{}, unsupportedf("expression", "struct %q has no field %q", info.name, expr.Name)
	}
	emitter := g.toOstyEmitter()
	out := llvmExtractValue(emitter, toOstyValue(base), field.typ, field.index)
	g.takeOstyEmitter(emitter)
	return fromOstyValue(out), nil
}

func (g *generator) enumVariantValue(expr *ast.FieldExpr) (value, bool, error) {
	ref, ok := g.enumVariantByField(expr)
	if !ok {
		return value{}, false, nil
	}
	out, err := g.enumVariantConstant(ref.enum, ref.variant)
	return out, true, err
}

func (g *generator) enumVariantIdent(name string) (value, bool, error) {
	found, count := g.findBareEnumVariant(name)
	if count == 0 {
		return value{}, false, nil
	}
	if count > 1 {
		return value{}, true, unsupportedf("name", "ambiguous enum variant %q", name)
	}
	out, err := g.enumVariantConstant(found.enum, found.variant)
	return out, true, err
}

func (g *generator) enumVariantConstant(info *enumInfo, variant variantInfo) (value, error) {
	if info.hasPayload {
		if len(variant.payloads) != 0 {
			return value{}, unsupportedf("expression", "enum variant %q requires a payload", variant.name)
		}
		return g.emitEnumPayloadVariant(info, variant, value{typ: info.payloadTyp, ref: zeroLiteral(info.payloadTyp)})
	}
	out := llvmEnumVariant(info.name, variant.tag)
	return fromOstyValue(out), nil
}

func (g *generator) findBareEnumVariant(name string) (enumVariantRef, int) {
	var found enumVariantRef
	count := 0
	for _, info := range g.enums {
		if variant, ok := info.variants[name]; ok {
			found = enumVariantRef{enum: info, variant: variant}
			count++
		}
	}
	return found, count
}

func (g *generator) enumVariantByField(expr *ast.FieldExpr) (enumVariantRef, bool) {
	base, ok := expr.X.(*ast.Ident)
	if !ok {
		return enumVariantRef{}, false
	}
	info := g.enumsByName[base.Name]
	if info == nil {
		return enumVariantRef{}, false
	}
	variant, ok := info.variants[expr.Name]
	if !ok {
		return enumVariantRef{}, false
	}
	return enumVariantRef{enum: info, variant: variant}, true
}

func (g *generator) emitEnumPayloadVariant(info *enumInfo, variant variantInfo, payload value) (value, error) {
	if !info.hasPayload {
		return value{}, unsupportedf("expression", "enum %q has no payload layout", info.name)
	}
	if payload.typ != info.payloadTyp {
		return value{}, unsupportedf("type-system", "enum %q variant %q payload type %s, want %s", info.name, variant.name, payload.typ, info.payloadTyp)
	}
	emitter := g.toOstyEmitter()
	out := llvmEnumPayloadVariant(emitter, info.typ, variant.tag, toOstyValue(payload))
	g.takeOstyEmitter(emitter)
	return fromOstyValue(out), nil
}

func (g *generator) emitUnary(e *ast.UnaryExpr) (value, error) {
	v, err := g.emitExpr(e.X)
	if err != nil {
		return value{}, err
	}
	switch e.Op {
	case token.PLUS:
		if v.typ != "i64" && v.typ != "double" {
			return value{}, unsupportedf("type-system", "unary plus on %s", v.typ)
		}
		return v, nil
	case token.MINUS:
		emitter := g.toOstyEmitter()
		var out *LlvmValue
		switch v.typ {
		case "i64":
			out = llvmBinaryI64(emitter, "sub", llvmIntLiteral(0), toOstyValue(v))
		case "double":
			out = llvmBinaryF64(emitter, "fsub", llvmFloatLiteral("0.0"), toOstyValue(v))
		default:
			g.takeOstyEmitter(emitter)
			return value{}, unsupportedf("type-system", "unary minus on %s", v.typ)
		}
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
	if left.typ == "double" && right.typ == "double" {
		op := ""
		switch e.Op {
		case token.PLUS:
			op = "fadd"
		case token.MINUS:
			op = "fsub"
		case token.STAR:
			op = "fmul"
		case token.SLASH:
			op = "fdiv"
		default:
			return value{}, unsupportedf("expression", "binary operator %q", e.Op)
		}
		emitter := g.toOstyEmitter()
		out := llvmBinaryF64(emitter, op, toOstyValue(left), toOstyValue(right))
		g.takeOstyEmitter(emitter)
		return fromOstyValue(out), nil
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
	emitter := g.toOstyEmitter()
	var out *LlvmValue
	switch left.typ {
	case "i64", "i1":
		pred, err := llvmIntComparePred(op)
		if err != nil {
			g.takeOstyEmitter(emitter)
			return value{}, err
		}
		out = llvmCompare(emitter, pred, toOstyValue(left), toOstyValue(right))
	case "double":
		pred, err := llvmFloatComparePred(op)
		if err != nil {
			g.takeOstyEmitter(emitter)
			return value{}, err
		}
		out = llvmCompareF64(emitter, pred, toOstyValue(left), toOstyValue(right))
	case "ptr":
		g.takeOstyEmitter(emitter)
		return g.emitRuntimeStringCompare(op, left, right)
	default:
		g.takeOstyEmitter(emitter)
		return value{}, unsupportedf("type-system", "compare type %s", left.typ)
	}
	g.takeOstyEmitter(emitter)
	return fromOstyValue(out), nil
}

func (g *generator) emitRuntimeStringCompare(op token.Kind, left, right value) (value, error) {
	if op != token.EQ && op != token.NEQ {
		return value{}, unsupportedf("type-system", "compare type %s", left.typ)
	}
	g.declareRuntimeSymbol("osty_rt_strings_Equal", "i1", []paramInfo{
		{typ: "ptr"},
		{typ: "ptr"},
	})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "i1", "osty_rt_strings_Equal", []*LlvmValue{toOstyValue(left), toOstyValue(right)})
	if op == token.NEQ {
		out = llvmNotI1(emitter, out)
	}
	g.takeOstyEmitter(emitter)
	return fromOstyValue(out), nil
}

func llvmIntComparePred(op token.Kind) (string, error) {
	switch op {
	case token.EQ:
		return "eq", nil
	case token.NEQ:
		return "ne", nil
	case token.LT:
		return "slt", nil
	case token.GT:
		return "sgt", nil
	case token.LEQ:
		return "sle", nil
	case token.GEQ:
		return "sge", nil
	default:
		return "", unsupportedf("expression", "comparison operator %q", op)
	}
}

func llvmFloatComparePred(op token.Kind) (string, error) {
	switch op {
	case token.EQ:
		return "oeq", nil
	case token.NEQ:
		return "one", nil
	case token.LT:
		return "olt", nil
	case token.GT:
		return "ogt", nil
	case token.LEQ:
		return "ole", nil
	case token.GEQ:
		return "oge", nil
	default:
		return "", unsupportedf("expression", "comparison operator %q", op)
	}
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
	g.currentBlock = labels.thenLabel

	g.pushScope()
	thenValue, err := g.emitBlockValue(expr.Then)
	g.popScope()
	if err != nil {
		return value{}, err
	}
	thenPred := g.currentBlock
	emitter = g.toOstyEmitter()
	llvmIfExprElse(emitter, labels)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.elseLabel

	elseValue, err := g.emitElseValue(expr.Else)
	if err != nil {
		return value{}, err
	}
	elsePred := g.currentBlock
	if thenValue.typ != elseValue.typ {
		return value{}, unsupportedf("type-system", "if expression branch types %s/%s", thenValue.typ, elseValue.typ)
	}
	return g.emitIfExprPhi(labels, thenPred, elsePred, thenValue, elseValue)
}

func (g *generator) emitIfExprPhi(labels *LlvmIfLabels, thenPred, elsePred string, thenValue, elseValue value) (value, error) {
	if labels == nil {
		return value{}, unsupported("control-flow", "missing if-expression labels")
	}
	if thenValue.typ != elseValue.typ {
		return value{}, unsupportedf("type-system", "if expression branch types %s/%s", thenValue.typ, elseValue.typ)
	}
	emitter := g.toOstyEmitter()
	emitter.body = append(emitter.body, fmt.Sprintf("  br label %%%s", labels.endLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", labels.endLabel))
	tmp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf(
		"  %s = phi %s [ %s, %%%s ], [ %s, %%%s ]",
		tmp,
		thenValue.typ,
		thenValue.ref,
		thenPred,
		elseValue.ref,
		elsePred,
	))
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.endLabel
	return value{typ: thenValue.typ, ref: tmp}, nil
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

func (g *generator) emitMatchExprValue(expr *ast.MatchExpr) (value, error) {
	if expr == nil {
		return value{}, unsupported("expression", "nil match expression")
	}
	if len(expr.Arms) == 0 {
		return value{}, unsupported("expression", "match with no arms")
	}
	scrutinee, err := g.emitExpr(expr.Scrutinee)
	if err != nil {
		return value{}, err
	}
	for _, arm := range expr.Arms {
		if arm == nil {
			return value{}, unsupported("expression", "nil match arm")
		}
		if arm.Guard != nil {
			return value{}, unsupported("control-flow", "match guards are not supported")
		}
	}
	if scrutinee.typ == "i64" {
		return g.emitTagEnumMatchExprValue(scrutinee, expr.Arms)
	}
	if info := g.enumsByType[scrutinee.typ]; info != nil && info.hasPayload {
		if len(expr.Arms) != 2 {
			return value{}, unsupportedf("expression", "payload enum match with %d arms", len(expr.Arms))
		}
		first := expr.Arms[0]
		second := expr.Arms[1]
		return g.emitPayloadEnumMatchExprValue(scrutinee, info, first, second)
	}
	return value{}, unsupportedf("type-system", "match scrutinee type %s, want enum tag", scrutinee.typ)
}

func (g *generator) emitTagEnumMatchExprValue(scrutinee value, arms []*ast.MatchArm) (value, error) {
	if len(arms) == 2 {
		return g.emitTagEnumMatchIfExprValue(scrutinee, arms[0], arms[1])
	}
	for _, arm := range arms {
		if !matchArmBodyIsSelectSafe(arm.Body) {
			return value{}, unsupported("expression", "multi-arm match currently requires literal/identifier arm bodies")
		}
	}
	var current value
	haveCurrent := false
	for i := len(arms) - 1; i >= 0; i-- {
		arm := arms[i]
		if _, catchAll := arm.Pattern.(*ast.WildcardPat); catchAll {
			if i != len(arms)-1 {
				return value{}, unsupported("expression", "wildcard match arm must be last")
			}
			v, err := g.emitMatchArmBodyValue(arm.Body)
			if err != nil {
				return value{}, err
			}
			current = v
			haveCurrent = true
			continue
		}
		tag, ok, err := g.matchEnumTag(arm.Pattern)
		if err != nil {
			return value{}, err
		}
		if !ok {
			return value{}, unsupported("expression", "match arm must be a payload-free enum variant")
		}
		armValue, err := g.emitMatchArmBodyValue(arm.Body)
		if err != nil {
			return value{}, err
		}
		if !haveCurrent {
			current = armValue
			haveCurrent = true
			continue
		}
		if armValue.typ != current.typ {
			return value{}, unsupportedf("type-system", "match arm types %s/%s", armValue.typ, current.typ)
		}
		emitter := g.toOstyEmitter()
		cond := llvmCompare(emitter, "eq", toOstyValue(scrutinee), toOstyValue(value{typ: "i64", ref: strconv.Itoa(tag)}))
		g.takeOstyEmitter(emitter)
		current, err = g.emitSelectValue(cond, armValue, current)
		if err != nil {
			return value{}, err
		}
	}
	if !haveCurrent {
		return value{}, unsupported("expression", "match with no arms")
	}
	return current, nil
}

func (g *generator) emitTagEnumMatchIfExprValue(scrutinee value, first, second *ast.MatchArm) (value, error) {
	tag, ok, err := g.matchEnumTag(first.Pattern)
	if err != nil {
		return value{}, err
	}
	if !ok {
		return value{}, unsupported("expression", "first match arm must be a payload-free enum variant")
	}
	if _, catchAll := second.Pattern.(*ast.WildcardPat); !catchAll {
		if _, _, err := g.matchEnumTag(second.Pattern); err != nil {
			return value{}, err
		}
	}
	emitter := g.toOstyEmitter()
	cond := llvmCompare(emitter, "eq", toOstyValue(scrutinee), toOstyValue(value{typ: "i64", ref: strconv.Itoa(tag)}))
	labels := llvmIfExprStart(emitter, cond)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.thenLabel

	thenValue, err := g.emitMatchArmBodyValue(first.Body)
	if err != nil {
		return value{}, err
	}
	thenPred := g.currentBlock
	emitter = g.toOstyEmitter()
	llvmIfExprElse(emitter, labels)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.elseLabel

	elseValue, err := g.emitMatchArmBodyValue(second.Body)
	if err != nil {
		return value{}, err
	}
	elsePred := g.currentBlock
	if thenValue.typ != elseValue.typ {
		return value{}, unsupportedf("type-system", "match arm types %s/%s", thenValue.typ, elseValue.typ)
	}
	return g.emitIfExprPhi(labels, thenPred, elsePred, thenValue, elseValue)
}

func (g *generator) emitSelectValue(cond *LlvmValue, thenValue, elseValue value) (value, error) {
	if cond == nil || cond.typ != "i1" {
		return value{}, unsupported("type-system", "select condition must be Bool")
	}
	if thenValue.typ != elseValue.typ {
		return value{}, unsupportedf("type-system", "select branch types %s/%s", thenValue.typ, elseValue.typ)
	}
	emitter := g.toOstyEmitter()
	tmp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = select i1 %s, %s %s, %s %s", tmp, cond.name, thenValue.typ, thenValue.ref, elseValue.typ, elseValue.ref))
	g.takeOstyEmitter(emitter)
	return value{typ: thenValue.typ, ref: tmp}, nil
}

func matchArmBodyIsSelectSafe(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.IntLit, *ast.FloatLit, *ast.BoolLit, *ast.StringLit, *ast.Ident, *ast.FieldExpr:
		return true
	case *ast.Block:
		if e == nil || len(e.Stmts) != 1 {
			return false
		}
		stmt, ok := e.Stmts[0].(*ast.ExprStmt)
		return ok && matchArmBodyIsSelectSafe(stmt.X)
	default:
		return false
	}
}

func (g *generator) emitPayloadEnumMatchExprValue(scrutinee value, info *enumInfo, first, second *ast.MatchArm) (value, error) {
	firstPattern, ok, err := g.matchPayloadEnumPattern(info, first.Pattern)
	if err != nil {
		return value{}, err
	}
	if !ok {
		return value{}, unsupportedf("expression", "first match arm must be an enum %q variant", info.name)
	}
	var secondPattern enumPatternInfo
	secondHasPattern := false
	if _, catchAll := second.Pattern.(*ast.WildcardPat); !catchAll {
		secondPattern, secondHasPattern, err = g.matchPayloadEnumPattern(info, second.Pattern)
		if err != nil {
			return value{}, err
		}
		if !secondHasPattern {
			return value{}, unsupportedf("expression", "second match arm must be an enum %q variant or wildcard", info.name)
		}
	}

	emitter := g.toOstyEmitter()
	tag := llvmExtractValue(emitter, toOstyValue(scrutinee), "i64", 0)
	cond := llvmCompare(emitter, "eq", tag, toOstyValue(value{typ: "i64", ref: strconv.Itoa(firstPattern.variant.tag)}))
	labels := llvmIfExprStart(emitter, cond)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.thenLabel

	g.pushScope()
	if err := g.bindPayloadEnumPattern(scrutinee, firstPattern); err != nil {
		g.popScope()
		return value{}, err
	}
	thenValue, err := g.emitMatchArmBodyValue(first.Body)
	g.popScope()
	if err != nil {
		return value{}, err
	}
	thenPred := g.currentBlock
	emitter = g.toOstyEmitter()
	llvmIfExprElse(emitter, labels)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.elseLabel

	g.pushScope()
	if secondHasPattern {
		if err := g.bindPayloadEnumPattern(scrutinee, secondPattern); err != nil {
			g.popScope()
			return value{}, err
		}
	}
	elseValue, err := g.emitMatchArmBodyValue(second.Body)
	g.popScope()
	if err != nil {
		return value{}, err
	}
	elsePred := g.currentBlock
	if thenValue.typ != elseValue.typ {
		return value{}, unsupportedf("type-system", "match arm types %s/%s", thenValue.typ, elseValue.typ)
	}
	return g.emitIfExprPhi(labels, thenPred, elsePred, thenValue, elseValue)
}

func (g *generator) bindPayloadEnumPattern(scrutinee value, pattern enumPatternInfo) error {
	if !pattern.hasPayloadBinding {
		return nil
	}
	emitter := g.toOstyEmitter()
	payload := llvmExtractValue(emitter, toOstyValue(scrutinee), pattern.payloadType, 1)
	g.takeOstyEmitter(emitter)
	g.bindLocal(pattern.payloadName, fromOstyValue(payload))
	return nil
}

func (g *generator) matchPayloadEnumPattern(info *enumInfo, pattern ast.Pattern) (enumPatternInfo, bool, error) {
	switch p := pattern.(type) {
	case *ast.IdentPat:
		variant, ok := info.variants[p.Name]
		if !ok {
			return enumPatternInfo{}, false, nil
		}
		if len(variant.payloads) != 0 {
			return enumPatternInfo{}, true, unsupportedf("expression", "enum variant pattern %q must bind its payload", p.Name)
		}
		return enumPatternInfo{variant: variant}, true, nil
	case *ast.VariantPat:
		if len(p.Path) == 0 || len(p.Path) > 2 {
			return enumPatternInfo{}, false, nil
		}
		if len(p.Path) == 2 && p.Path[0] != info.name {
			return enumPatternInfo{}, false, nil
		}
		name := p.Path[len(p.Path)-1]
		variant, ok := info.variants[name]
		if !ok {
			return enumPatternInfo{}, false, nil
		}
		if len(p.Args) != len(variant.payloads) {
			return enumPatternInfo{}, true, unsupportedf("expression", "enum variant pattern %q payload count", name)
		}
		out := enumPatternInfo{variant: variant}
		if len(variant.payloads) == 0 {
			return out, true, nil
		}
		out.payloadType = variant.payloads[0]
		switch arg := p.Args[0].(type) {
		case *ast.IdentPat:
			if !isLLVMIdent(arg.Name) {
				return enumPatternInfo{}, true, unsupportedf("name", "enum payload binding name %q", arg.Name)
			}
			out.payloadName = arg.Name
			out.hasPayloadBinding = true
		case *ast.WildcardPat:
		default:
			return enumPatternInfo{}, true, unsupportedf("expression", "enum variant payload pattern %T", arg)
		}
		return out, true, nil
	default:
		return enumPatternInfo{}, false, nil
	}
}

func (g *generator) emitMatchArmBodyValue(expr ast.Expr) (value, error) {
	switch e := expr.(type) {
	case *ast.Block:
		g.pushScope()
		defer g.popScope()
		return g.emitBlockValue(e)
	default:
		return g.emitExpr(expr)
	}
}

func (g *generator) matchEnumTag(pattern ast.Pattern) (int, bool, error) {
	switch p := pattern.(type) {
	case *ast.IdentPat:
		var found variantInfo
		count := 0
		for _, info := range g.enums {
			if info.hasPayload {
				continue
			}
			if variant, ok := info.variants[p.Name]; ok {
				found = variant
				count++
			}
		}
		if count == 0 {
			return 0, false, nil
		}
		if count > 1 {
			return 0, true, unsupportedf("name", "ambiguous enum variant pattern %q", p.Name)
		}
		return found.tag, true, nil
	case *ast.VariantPat:
		if len(p.Args) != 0 || len(p.Path) == 0 {
			return 0, false, nil
		}
		name := p.Path[len(p.Path)-1]
		if len(p.Path) == 2 {
			info := g.enumsByName[p.Path[0]]
			if info == nil || info.hasPayload {
				return 0, false, nil
			}
			variant, ok := info.variants[name]
			if !ok {
				return 0, false, nil
			}
			return variant.tag, true, nil
		}
		return g.matchEnumTag(&ast.IdentPat{Name: name})
	default:
		return 0, false, nil
	}
}

func (g *generator) emitCall(call *ast.CallExpr) (value, error) {
	if v, found, err := g.emitEnumVariantCall(call); found || err != nil {
		return v, err
	}
	if v, found, err := g.emitRuntimeFFICall(call); found || err != nil {
		return v, err
	}
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
	if sig.ret == "void" {
		return value{}, unsupportedf("call", "function %q has no return value", id.Name)
	}
	args, err := g.userCallArgs(sig, call)
	if err != nil {
		return value{}, err
	}
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, sig.ret, sig.name, args)
	g.takeOstyEmitter(emitter)
	return fromOstyValue(out), nil
}

func (g *generator) emitRuntimeFFICall(call *ast.CallExpr) (value, bool, error) {
	fn, found, err := g.runtimeFFICallTarget(call)
	if !found || err != nil {
		return value{}, found, err
	}
	if fn.ret == "void" {
		return value{}, true, unsupportedf("call", "runtime FFI %s.%s has no return value", fn.path, fn.sourceName)
	}
	args, err := g.runtimeFFICallArgs(fn, call.Args)
	if err != nil {
		return value{}, true, err
	}
	g.declareRuntimeFFI(fn)
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, fn.ret, fn.symbol, args)
	g.takeOstyEmitter(emitter)
	return fromOstyValue(out), true, nil
}

func (g *generator) emitRuntimeFFICallStmt(call *ast.CallExpr) (bool, error) {
	fn, found, err := g.runtimeFFICallTarget(call)
	if !found || err != nil {
		return found, err
	}
	args, err := g.runtimeFFICallArgs(fn, call.Args)
	if err != nil {
		return true, err
	}
	g.declareRuntimeFFI(fn)
	if fn.ret == "void" {
		g.body = append(g.body, fmt.Sprintf("  call void @%s(%s)", fn.symbol, llvmCallArgs(args)))
		return true, nil
	}
	emitter := g.toOstyEmitter()
	llvmCall(emitter, fn.ret, fn.symbol, args)
	g.takeOstyEmitter(emitter)
	return true, nil
}

func (g *generator) emitUserCallStmt(call *ast.CallExpr) (bool, error) {
	id, ok := call.Fn.(*ast.Ident)
	if !ok {
		return false, nil
	}
	sig := g.functions[id.Name]
	if sig == nil {
		return false, nil
	}
	args, err := g.userCallArgs(sig, call)
	if err != nil {
		return true, err
	}
	emitter := g.toOstyEmitter()
	if sig.ret == "void" {
		emitter.body = append(emitter.body, fmt.Sprintf("  call void @%s(%s)", sig.name, llvmCallArgs(args)))
	} else {
		llvmCall(emitter, sig.ret, sig.name, args)
	}
	g.takeOstyEmitter(emitter)
	return true, nil
}

func (g *generator) userCallArgs(sig *fnSig, call *ast.CallExpr) ([]*LlvmValue, error) {
	if len(call.Args) != len(sig.params) {
		return nil, unsupportedf("call", "function %q argument count", sig.name)
	}
	args := make([]*LlvmValue, 0, len(call.Args))
	for i, arg := range call.Args {
		if arg.Name != "" || arg.Value == nil {
			return nil, unsupportedf("call", "function %q requires positional arguments", sig.name)
		}
		v, err := g.emitExpr(arg.Value)
		if err != nil {
			return nil, err
		}
		param := sig.params[i]
		if v.typ != param.typ {
			return nil, unsupportedf("type-system", "function %q arg %d type %s, want %s", sig.name, i+1, v.typ, param.typ)
		}
		args = append(args, toOstyValue(v))
	}
	return args, nil
}

func (g *generator) runtimeFFICallTarget(call *ast.CallExpr) (*runtimeFFIFunction, bool, error) {
	if call == nil {
		return nil, false, nil
	}
	field, ok := call.Fn.(*ast.FieldExpr)
	if !ok {
		return nil, false, nil
	}
	if field.IsOptional {
		return nil, true, unsupported("runtime-ffi", "optional runtime FFI calls are not supported")
	}
	alias, ok := field.X.(*ast.Ident)
	if !ok {
		return nil, false, nil
	}
	path, ok := g.runtimeFFIPaths[alias.Name]
	if !ok {
		return nil, false, nil
	}
	funcs := g.runtimeFFI[alias.Name]
	fn := funcs[field.Name]
	if fn == nil {
		return nil, true, unsupported("runtime-ffi", path+"."+field.Name)
	}
	if fn.unsupported != "" {
		return nil, true, unsupported("runtime-ffi", fn.path+"."+fn.sourceName+" signature: "+fn.unsupported)
	}
	return fn, true, nil
}

func (g *generator) runtimeFFICallArgs(fn *runtimeFFIFunction, callArgs []*ast.Arg) ([]*LlvmValue, error) {
	if len(callArgs) != len(fn.params) {
		return nil, unsupportedf("call", "runtime FFI %s.%s argument count", fn.path, fn.sourceName)
	}
	args := make([]*LlvmValue, 0, len(callArgs))
	for i, arg := range callArgs {
		if arg == nil || arg.Name != "" || arg.Value == nil {
			return nil, unsupportedf("call", "runtime FFI %s.%s requires positional arguments", fn.path, fn.sourceName)
		}
		v, err := g.emitExpr(arg.Value)
		if err != nil {
			return nil, err
		}
		param := fn.params[i]
		if v.typ != param.typ {
			return nil, unsupportedf("type-system", "runtime FFI %s.%s arg %d type %s, want %s", fn.path, fn.sourceName, i+1, v.typ, param.typ)
		}
		args = append(args, toOstyValue(v))
	}
	return args, nil
}

func (g *generator) declareRuntimeFFI(fn *runtimeFFIFunction) {
	if fn == nil {
		return
	}
	g.declareRuntimeSymbol(fn.symbol, fn.ret, fn.params)
}

func (g *generator) declareRuntimeSymbol(symbol, ret string, params []paramInfo) {
	if _, exists := g.runtimeDecls[symbol]; exists {
		return
	}
	g.runtimeDecls[symbol] = runtimeDecl{symbol: symbol, ret: ret, params: params}
	g.runtimeDeclOrder = append(g.runtimeDeclOrder, symbol)
}

func (g *generator) emitEnumVariantCall(call *ast.CallExpr) (value, bool, error) {
	ref, found, err := g.enumVariantCallTarget(call)
	if !found || err != nil {
		return value{}, found, err
	}
	if len(call.Args) != len(ref.variant.payloads) {
		return value{}, true, unsupportedf("call", "enum variant %q argument count", ref.variant.name)
	}
	if len(ref.variant.payloads) == 0 {
		out, err := g.enumVariantConstant(ref.enum, ref.variant)
		return out, true, err
	}
	if !ref.enum.hasPayload {
		return value{}, true, unsupportedf("expression", "enum %q has no payload layout", ref.enum.name)
	}
	arg := call.Args[0]
	if arg.Name != "" || arg.Value == nil {
		return value{}, true, unsupportedf("call", "enum variant %q requires positional payload", ref.variant.name)
	}
	payload, err := g.emitExpr(arg.Value)
	if err != nil {
		return value{}, true, err
	}
	if payload.typ != ref.variant.payloads[0] {
		return value{}, true, unsupportedf("type-system", "enum variant %q payload type %s, want %s", ref.variant.name, payload.typ, ref.variant.payloads[0])
	}
	out, err := g.emitEnumPayloadVariant(ref.enum, ref.variant, payload)
	return out, true, err
}

func (g *generator) enumVariantCallTarget(call *ast.CallExpr) (enumVariantRef, bool, error) {
	switch fn := call.Fn.(type) {
	case *ast.Ident:
		found, count := g.findBareEnumVariant(fn.Name)
		if count == 0 {
			return enumVariantRef{}, false, nil
		}
		if count > 1 {
			return enumVariantRef{}, true, unsupportedf("name", "ambiguous enum variant %q", fn.Name)
		}
		return found, true, nil
	case *ast.FieldExpr:
		found, ok := g.enumVariantByField(fn)
		return found, ok, nil
	default:
		return enumVariantRef{}, false, nil
	}
}

func (g *generator) render(defs []string) []byte {
	typeDefs := make([]string, 0, len(g.structs)+len(g.enumsByType))
	for _, info := range g.structs {
		fieldTypes := make([]string, 0, len(info.fields))
		for _, field := range info.fields {
			fieldTypes = append(fieldTypes, field.typ)
		}
		typeDefs = append(typeDefs, llvmStructTypeDef(info.name, fieldTypes))
	}
	for _, info := range g.enums {
		if info.hasPayload {
			typeDefs = append(typeDefs, llvmStructTypeDef(info.name, []string{"i64", info.payloadTyp}))
		}
	}
	runtimeDecls := g.runtimeDeclarationIR()
	if g.needsGCRuntime {
		runtimeDecls = append(llvmGcRuntimeDeclarations(), runtimeDecls...)
	}
	if len(runtimeDecls) > 0 {
		return []byte(llvmRenderModuleWithRuntimeDeclarations(g.sourcePath, g.target, typeDefs, g.stringDefs, runtimeDecls, defs))
	}
	return []byte(llvmRenderModuleWithGlobalsAndTypes(g.sourcePath, g.target, typeDefs, g.stringDefs, defs))
}

func (g *generator) runtimeDeclarationIR() []string {
	if len(g.runtimeDeclOrder) == 0 {
		return nil
	}
	out := make([]string, 0, len(g.runtimeDeclOrder))
	for _, symbol := range g.runtimeDeclOrder {
		decl, ok := g.runtimeDecls[symbol]
		if !ok {
			continue
		}
		paramTypes := make([]string, 0, len(decl.params))
		for _, param := range decl.params {
			paramTypes = append(paramTypes, param.typ)
		}
		out = append(out, fmt.Sprintf("declare %s @%s(%s)", decl.ret, decl.symbol, strings.Join(paramTypes, ", ")))
	}
	return out
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

func structTypeExprName(expr ast.Expr) (string, bool) {
	id, ok := expr.(*ast.Ident)
	if !ok || id.Name == "" {
		return "", false
	}
	return id.Name, true
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

func zeroLiteral(typ string) string {
	switch typ {
	case "double":
		return "0.0"
	case "ptr":
		return "null"
	default:
		return "0"
	}
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

func llvmType(t ast.Type, structs map[string]*structInfo, enums map[string]*enumInfo) (string, error) {
	switch tt := t.(type) {
	case *ast.NamedType:
		if len(tt.Path) != 1 {
			return "ptr", nil
		}
		if len(tt.Args) != 0 {
			return "ptr", nil
		}
		switch tt.Path[0] {
		case "Int":
			return "i64", nil
		case "Float":
			return "double", nil
		case "Bool":
			return "i1", nil
		case "String":
			return "ptr", nil
		case "Bytes", "Error":
			return "ptr", nil
		default:
			if info := structs[tt.Path[0]]; info != nil {
				return info.typ, nil
			}
			if info := enums[tt.Path[0]]; info != nil {
				return info.typ, nil
			}
			return "", unsupportedf("type-system", "type %q", strings.Join(tt.Path, "."))
		}
	case *ast.OptionalType, *ast.TupleType, *ast.FnType:
		return "ptr", nil
	default:
		return "", unsupportedf("type-system", "type %T", t)
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
