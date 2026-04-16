package llvmgen

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
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
// The implementation is generated from toolchain/llvmgen.osty so
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
func ClangLinkBinaryArgs(target string, objectPaths []string, binaryPath string) []string {
	return llvmClangLinkBinaryArgs(target, objectPaths, binaryPath)
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
	return generateASTFile(file, opts)
}

func generateASTFile(file *ast.File, opts Options) ([]byte, error) {
	if file == nil {
		return nil, unsupported("source-layout", "nil file")
	}
	if diag, ok := fileUnsupportedDiagnostic(file); ok {
		return nil, &UnsupportedError{Diagnostic: diag}
	}
	g := &generator{
		sourcePath:      filepath.ToSlash(llvmFirstNonEmpty(opts.SourcePath, "<unknown>")),
		target:          opts.Target,
		runtimeFFI:      map[string]map[string]*runtimeFFIFunction{},
		runtimeFFIPaths: map[string]string{},
		runtimeDecls:    map[string]runtimeDecl{},
		tupleTypes:      map[string]tupleTypeInfo{},
	}
	if len(file.Stmts) > 0 {
		if len(file.Decls) > 0 {
			return nil, unsupported("source-layout", "mixed script statements and declarations")
		}
		g.runtimeFFI = collectRuntimeFFI(file, nil, nil)
		g.runtimeFFIPaths = collectRuntimeFFIPaths(file)
		g.resultTypes = collectBuiltinResultTypes(file, nil, nil)
		g.testingAliases = collectStdTestingAliases(file)
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
	g.methods = decls.methodsByType
	g.structs = decls.structsOrdered
	g.structsByName = decls.structsByName
	g.structsByType = decls.structsByType
	g.enums = decls.enumsOrdered
	g.enumsByName = decls.enumsByName
	g.enumsByType = decls.enumsByType
	g.resultTypes = collectBuiltinResultTypes(file, decls.structsByName, decls.enumsByName)
	g.runtimeFFI = collectRuntimeFFI(file, decls.structsByName, decls.enumsByName)
	g.runtimeFFIPaths = collectRuntimeFFIPaths(file)
	g.testingAliases = collectStdTestingAliases(file)
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
		if use != nil && use.IsRuntimeFFI && !llvmIsKnownRuntimeFfiPath(use.RuntimePath) {
			return UnsupportedDiagnosticFor("runtime-ffi", use.RuntimePath), true
		}
	}
	return UnsupportedDiagnostic{}, false
}

type generator struct {
	sourcePath       string
	target           string
	functions        map[string]*fnSig
	methods          map[string]map[string]*fnSig
	structs          []*structInfo
	structsByName    map[string]*structInfo
	structsByType    map[string]*structInfo
	enums            []*enumInfo
	enumsByName      map[string]*enumInfo
	enumsByType      map[string]*enumInfo
	tupleTypes       map[string]tupleTypeInfo
	resultTypes      map[string]builtinResultType
	runtimeFFI       map[string]map[string]*runtimeFFIFunction
	runtimeFFIPaths  map[string]string
	testingAliases   map[string]bool
	runtimeDecls     map[string]runtimeDecl
	runtimeDeclOrder []string

	temp              int
	label             int
	stringID          int
	stringDefs        []*LlvmStringGlobal
	body              []string
	locals            []map[string]value
	returnType        string
	returnListElemTyp string
	currentBlock      string
	currentReachable  bool

	needsGCRuntime bool
	gcRootSlots    []value
	gcRootMarks    []int
	nextSafepoint  int
	hiddenLocalID  int
	loopStack      []loopContext
}

type loopContext struct {
	continueLabel string
	breakLabel    string
	scopeDepth    int
}

type scopeState struct {
	locals      []map[string]value
	gcRootSlots []value
	gcRootMarks []int
}

type declarations struct {
	functionsOrdered []*fnSig
	functionsByName  map[string]*fnSig
	methodsByType    map[string]map[string]*fnSig
	structsOrdered   []*structInfo
	structsByName    map[string]*structInfo
	structsByType    map[string]*structInfo
	enumsOrdered     []*enumInfo
	enumsByName      map[string]*enumInfo
	enumsByType      map[string]*enumInfo
}

type fnSig struct {
	name           string
	irName         string
	receiverType   string
	receiverMut    bool
	ret            string
	retListElemTyp string
	params         []paramInfo
	decl           *ast.FnDecl
}

type paramInfo struct {
	name        string
	typ         string
	irTyp       string
	listElemTyp string
	mutable     bool
	byRef       bool
}

type structInfo struct {
	name   string
	typ    string
	decl   *ast.StructDecl
	fields []fieldInfo
	byName map[string]fieldInfo
}

type fieldInfo struct {
	name        string
	typ         string
	index       int
	listElemTyp string
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
	name               string
	tag                int
	payloads           []string
	payloadListElemTyp string
}

type enumVariantRef struct {
	enum    *enumInfo
	variant variantInfo
}

type enumPatternInfo struct {
	variant            variantInfo
	payloadName        string
	payloadType        string
	payloadListElemTyp string
	hasPayloadBinding  bool
}

type tupleTypeInfo struct {
	typ              string
	elems            []string
	elemListElemTyps []string
}

type value struct {
	typ         string
	ref         string
	ptr         bool
	mutable     bool
	gcManaged   bool
	listElemTyp string
	rootPaths   [][]int
}

const (
	llvmGcRuntimeFrameSlotKind = 5
)

type runtimeFFIFunction struct {
	path        string
	sourceName  string
	symbol      string
	ret         string
	listElemTyp string
	params      []paramInfo
	unsupported string
}

type runtimeDecl struct {
	symbol string
	ret    string
	params []paramInfo
}

type builtinResultType struct {
	typ    string
	okTyp  string
	errTyp string
}

func collectDeclarations(file *ast.File) (*declarations, error) {
	out := &declarations{
		functionsByName: map[string]*fnSig{},
		methodsByType:   map[string]map[string]*fnSig{},
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
			info, err := collectEnum(d, out.structsByName)
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
		if err := collectStructFields(info, out.structsByName, out.enumsByName); err != nil {
			return nil, err
		}
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FnDecl)
		if !ok {
			continue
		}
		sig, err := signatureOf(fn, "", out.structsByName, out.enumsByName)
		if err != nil {
			return nil, err
		}
		if _, exists := out.functionsByName[sig.name]; exists {
			return nil, unsupportedf("source-layout", "duplicate function %q", sig.name)
		}
		out.functionsOrdered = append(out.functionsOrdered, sig)
		out.functionsByName[sig.name] = sig
	}
	for _, info := range out.structsOrdered {
		if err := collectMethodDeclarations(out, info.name, info.typ, info.decl.Methods, out.structsByName, out.enumsByName); err != nil {
			return nil, err
		}
	}
	for _, info := range out.enumsOrdered {
		if err := collectMethodDeclarations(out, info.name, info.typ, info.decl.Methods, out.structsByName, out.enumsByName); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func collectMethodDeclarations(out *declarations, ownerName, ownerType string, methods []*ast.FnDecl, structs map[string]*structInfo, enums map[string]*enumInfo) error {
	if out == nil {
		return unsupported("source-layout", "nil declarations")
	}
	for _, fn := range methods {
		sig, err := signatureOf(fn, ownerName, structs, enums)
		if err != nil {
			return err
		}
		methodsByName := out.methodsByType[ownerType]
		if methodsByName == nil {
			methodsByName = map[string]*fnSig{}
			out.methodsByType[ownerType] = methodsByName
		}
		if _, exists := methodsByName[sig.name]; exists {
			return unsupportedf("source-layout", "duplicate method %q on %q", sig.name, ownerName)
		}
		out.functionsOrdered = append(out.functionsOrdered, sig)
		methodsByName[sig.name] = sig
	}
	return nil
}

func collectRuntimeFFI(file *ast.File, structs map[string]*structInfo, enums map[string]*enumInfo) map[string]map[string]*runtimeFFIFunction {
	out := map[string]map[string]*runtimeFFIFunction{}
	if file == nil {
		return out
	}
	for _, use := range file.Uses {
		if use == nil || !use.IsRuntimeFFI || !llvmIsKnownRuntimeFfiPath(use.RuntimePath) {
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
		if use == nil || !use.IsRuntimeFFI || !llvmIsKnownRuntimeFfiPath(use.RuntimePath) {
			continue
		}
		if alias := runtimeFFIAlias(use); alias != "" {
			out[alias] = use.RuntimePath
		}
	}
	return out
}

func collectStdTestingAliases(file *ast.File) map[string]bool {
	out := map[string]bool{}
	if file == nil {
		return out
	}
	for _, use := range file.Uses {
		if use == nil || len(use.Path) != 2 || use.Path[0] != "std" || use.Path[1] != "testing" {
			continue
		}
		alias := use.Alias
		if alias == "" {
			alias = "testing"
		}
		if alias != "" {
			out[alias] = true
		}
	}
	return out
}

func collectBuiltinResultTypes(file *ast.File, structs map[string]*structInfo, enums map[string]*enumInfo) map[string]builtinResultType {
	out := map[string]builtinResultType{}
	if file == nil {
		return out
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FnDecl:
			for _, param := range d.Params {
				if param == nil {
					continue
				}
				collectBuiltinResultTypeFromAST(out, param.Type, structs, enums)
			}
			collectBuiltinResultTypeFromAST(out, d.ReturnType, structs, enums)
		case *ast.StructDecl:
			for _, field := range d.Fields {
				if field == nil {
					continue
				}
				collectBuiltinResultTypeFromAST(out, field.Type, structs, enums)
			}
		case *ast.EnumDecl:
			for _, variant := range d.Variants {
				if variant == nil {
					continue
				}
				for _, field := range variant.Fields {
					collectBuiltinResultTypeFromAST(out, field, structs, enums)
				}
			}
		case *ast.LetDecl:
			collectBuiltinResultTypeFromAST(out, d.Type, structs, enums)
		}
	}
	return out
}

func collectBuiltinResultTypeFromAST(out map[string]builtinResultType, t ast.Type, structs map[string]*structInfo, enums map[string]*enumInfo) {
	if out == nil || t == nil {
		return
	}
	switch tt := t.(type) {
	case *ast.NamedType:
		if len(tt.Path) == 1 && tt.Path[0] == "Result" && len(tt.Args) == 2 {
			okTyp, err := llvmType(tt.Args[0], structs, enums)
			if err == nil {
				errTyp, err2 := llvmType(tt.Args[1], structs, enums)
				if err2 == nil {
					info := builtinResultType{
						typ:    llvmResultTypeName(okTyp, errTyp),
						okTyp:  okTyp,
						errTyp: errTyp,
					}
					out[info.typ] = info
				}
			}
		}
		for _, arg := range tt.Args {
			collectBuiltinResultTypeFromAST(out, arg, structs, enums)
		}
	case *ast.OptionalType:
		collectBuiltinResultTypeFromAST(out, tt.Inner, structs, enums)
	case *ast.TupleType:
		for _, elem := range tt.Elems {
			collectBuiltinResultTypeFromAST(out, elem, structs, enums)
		}
	case *ast.FnType:
		for _, param := range tt.Params {
			collectBuiltinResultTypeFromAST(out, param, structs, enums)
		}
		collectBuiltinResultTypeFromAST(out, tt.ReturnType, structs, enums)
	}
}

func llvmBuiltinAggregateName(prefix string, parts ...string) string {
	names := make([]string, 0, len(parts)+1)
	names = append(names, prefix)
	for _, part := range parts {
		names = append(names, llvmBuiltinAggregatePart(part))
	}
	return "%" + strings.Join(names, ".")
}

func llvmBuiltinAggregatePart(part string) string {
	if part == "" {
		return "void"
	}
	var b strings.Builder
	for i := 0; i < len(part); i++ {
		c := part[i]
		if c == '_' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9') {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('_')
	}
	if b.Len() == 0 {
		return "value"
	}
	return b.String()
}

func llvmResultTypeName(okTyp, errTyp string) string {
	return llvmBuiltinAggregateName("Result", okTyp, errTyp)
}

func llvmTupleTypeName(elemTypes []string) string {
	return llvmBuiltinAggregateName("Tuple", elemTypes...)
}

func runtimeFFISignature(path string, fn *ast.FnDecl, structs map[string]*structInfo, enums map[string]*enumInfo) *runtimeFFIFunction {
	out := &runtimeFFIFunction{
		path:       path,
		sourceName: fn.Name,
		symbol:     runtimeFFISymbol(path, fn.Name),
	}
	if msg := llvmRuntimeFfiHeaderUnsupported(fn.Recv != nil, len(fn.Generics)); msg != "" {
		out.unsupported = msg
		return out
	}
	if fn.ReturnType == nil {
		out.ret = "void"
	} else {
		ret, err := llvmRuntimeABIType(fn.ReturnType, structs, enums)
		if err != nil {
			out.unsupported = llvmRuntimeFfiReturnUnsupported(unsupportedMessage(err))
			return out
		}
		out.ret = ret
		if listElemTyp, ok, err := llvmListElementType(fn.ReturnType, structs, enums); err != nil {
			out.unsupported = llvmRuntimeFfiReturnUnsupported(unsupportedMessage(err))
			return out
		} else if ok {
			out.listElemTyp = listElemTyp
		}
	}
	for _, p := range fn.Params {
		if p == nil {
			out.unsupported = llvmRuntimeFfiParamUnsupported("", true, false, "")
			return out
		}
		if p.Pattern != nil || p.Default != nil {
			out.unsupported = llvmRuntimeFfiParamUnsupported("", false, true, "")
			return out
		}
		name := llvmSignatureParamName(p.Name, len(out.params))
		typ, err := llvmRuntimeABIType(p.Type, structs, enums)
		if err != nil {
			out.unsupported = llvmRuntimeFfiParamUnsupported(name, false, false, unsupportedMessage(err))
			return out
		}
		info := paramInfo{name: name, typ: typ}
		if listElemTyp, ok, err := llvmListElementType(p.Type, structs, enums); err != nil {
			out.unsupported = llvmRuntimeFfiParamUnsupported(name, false, false, unsupportedMessage(err))
			return out
		} else if ok {
			info.listElemTyp = listElemTyp
		}
		out.params = append(out.params, info)
	}
	return out
}

func runtimeFFIAlias(use *ast.UseDecl) string {
	if use == nil {
		return ""
	}
	lastPath := ""
	if len(use.Path) > 0 {
		lastPath = use.Path[len(use.Path)-1]
	}
	return llvmRuntimeFfiAlias(use.Alias, lastPath, use.RuntimePath)
}

func runtimeFFISymbol(path, name string) string {
	return llvmRuntimeFfiSymbol(path, name)
}

func llvmRuntimeABIType(t ast.Type, structs map[string]*structInfo, enums map[string]*enumInfo) (string, error) {
	switch tt := t.(type) {
	case nil:
		return "void", nil
	case *ast.NamedType:
		name := ""
		structType := ""
		enumType := ""
		if len(tt.Path) == 1 {
			name = tt.Path[0]
			if info := structs[name]; info != nil {
				structType = info.typ
			}
			if info := enums[name]; info != nil {
				enumType = info.typ
			}
		}
		return llvmRuntimeAbiNamedType(name, len(tt.Path), len(tt.Args), structType, enumType), nil
	case *ast.OptionalType, *ast.TupleType, *ast.FnType:
		return "ptr", nil
	default:
		return "", unsupportedf("type-system", "runtime ABI type %T", t)
	}
}

func llvmListElementType(t ast.Type, structs map[string]*structInfo, enums map[string]*enumInfo) (string, bool, error) {
	named, ok := t.(*ast.NamedType)
	if !ok {
		return "", false, nil
	}
	name := ""
	if len(named.Path) == 1 {
		name = named.Path[0]
	}
	if !llvmIsRuntimeAbiListType(name, len(named.Path), len(named.Args)) {
		return "", false, nil
	}
	elemTyp, err := llvmRuntimeABIType(named.Args[0], structs, enums)
	if err != nil {
		return "", true, err
	}
	return elemTyp, true, nil
}

func collectStructShell(decl *ast.StructDecl) (*structInfo, error) {
	if decl == nil {
		return nil, unsupported("source-layout", "nil struct")
	}
	if diag := llvmNominalDeclHeaderDiagnostic("struct", decl.Name, llvmIsIdent(decl.Name), len(decl.Generics), len(decl.Methods)); diag.kind != "" {
		return nil, unsupported(diag.kind, diag.message)
	}
	return &structInfo{
		name:   decl.Name,
		typ:    llvmStructTypeName(decl.Name),
		decl:   decl,
		byName: map[string]fieldInfo{},
	}, nil
}

func collectStructFields(info *structInfo, structs map[string]*structInfo, enums map[string]*enumInfo) error {
	if info == nil || info.decl == nil {
		return unsupported("source-layout", "nil struct")
	}
	for i, field := range info.decl.Fields {
		if field == nil {
			return unsupportedf("source-layout", "struct %q has nil field", info.name)
		}
		if diag := llvmStructFieldDiagnostic(info.name, field.Name, llvmIsIdent(field.Name), field.Default != nil, false, false, ""); diag.kind != "" {
			return unsupported(diag.kind, diag.message)
		}
		if _, exists := info.byName[field.Name]; exists {
			diag := llvmStructFieldDiagnostic(info.name, field.Name, true, false, true, false, "")
			return unsupported(diag.kind, diag.message)
		}
		typ, err := llvmType(field.Type, structs, enums)
		if err != nil {
			diag := llvmStructFieldDiagnostic(info.name, field.Name, true, false, false, false, unsupportedMessage(err))
			return unsupported(diag.kind, diag.message)
		}
		if typ == info.typ {
			diag := llvmStructFieldDiagnostic(info.name, field.Name, true, false, false, true, "")
			return unsupported(diag.kind, diag.message)
		}
		fieldInfo := fieldInfo{name: field.Name, typ: typ, index: i}
		if listElemTyp, ok, err := llvmListElementType(field.Type, structs, enums); err != nil {
			diag := llvmStructFieldDiagnostic(info.name, field.Name, true, false, false, false, unsupportedMessage(err))
			return unsupported(diag.kind, diag.message)
		} else if ok {
			fieldInfo.listElemTyp = listElemTyp
		}
		info.fields = append(info.fields, fieldInfo)
		info.byName[field.Name] = fieldInfo
	}
	return nil
}

func collectEnum(decl *ast.EnumDecl, structs map[string]*structInfo) (*enumInfo, error) {
	if decl == nil {
		return nil, unsupported("source-layout", "nil enum")
	}
	if diag := llvmNominalDeclHeaderDiagnostic("enum", decl.Name, llvmIsIdent(decl.Name), len(decl.Generics), len(decl.Methods)); diag.kind != "" {
		return nil, unsupported(diag.kind, diag.message)
	}
	info := &enumInfo{
		name:     decl.Name,
		typ:      llvmEnumStorageType(decl.Name, false),
		decl:     decl,
		variants: map[string]variantInfo{},
	}
	for i, variant := range decl.Variants {
		if variant == nil {
			return nil, unsupportedf("source-layout", "enum %q has nil variant", decl.Name)
		}
		if diag := llvmEnumVariantHeaderDiagnostic(decl.Name, variant.Name, llvmIsIdent(variant.Name), len(variant.Fields), false); diag.kind != "" {
			return nil, unsupported(diag.kind, diag.message)
		}
		payloads := make([]string, 0, len(variant.Fields))
		payloadListElemTyp := ""
		if len(variant.Fields) == 1 {
			typ, err := llvmEnumPayloadType(variant.Fields[0])
			if err != nil {
				diag := llvmEnumPayloadDiagnostic(decl.Name, variant.Name, unsupportedMessage(err), "", "")
				return nil, unsupported(diag.kind, diag.message)
			}
			if info.payloadTyp == "" {
				info.payloadTyp = typ
			} else if info.payloadTyp != typ {
				diag := llvmEnumPayloadDiagnostic(decl.Name, variant.Name, "", info.payloadTyp, typ)
				return nil, unsupported(diag.kind, diag.message)
			}
			payloads = append(payloads, typ)
			if listElemTyp, ok, err := llvmListElementType(variant.Fields[0], structs, nil); err != nil {
				diag := llvmEnumPayloadDiagnostic(decl.Name, variant.Name, unsupportedMessage(err), "", "")
				return nil, unsupported(diag.kind, diag.message)
			} else if ok {
				payloadListElemTyp = listElemTyp
			}
			info.hasPayload = true
		}
		if _, exists := info.variants[variant.Name]; exists {
			diag := llvmEnumVariantHeaderDiagnostic(decl.Name, variant.Name, true, len(variant.Fields), true)
			return nil, unsupported(diag.kind, diag.message)
		}
		info.variants[variant.Name] = variantInfo{
			name:               variant.Name,
			tag:                i,
			payloads:           payloads,
			payloadListElemTyp: payloadListElemTyp,
		}
	}
	info.typ = llvmEnumStorageType(info.name, info.hasPayload)
	return info, nil
}

func llvmEnumPayloadType(t ast.Type) (string, error) {
	named, ok := t.(*ast.NamedType)
	if !ok {
		return "", unsupported("type-system", "LLVM enum payloads currently support Int or Float only")
	}
	name := ""
	if len(named.Path) == 1 {
		name = named.Path[0]
	}
	if typ := llvmEnumPayloadNamedType(name, len(named.Path), len(named.Args)); typ != "" {
		return typ, nil
	}
	return "", unsupported("type-system", "LLVM enum payloads currently support Int, Float, or String only")
}

func signatureOf(fn *ast.FnDecl, ownerName string, structs map[string]*structInfo, enums map[string]*enumInfo) (*fnSig, error) {
	if fn == nil {
		return nil, unsupported("source-layout", "nil function")
	}
	if diag := llvmFunctionHeaderDiagnostic(
		fn.Name,
		llvmIsIdent(fn.Name),
		false,
		len(fn.Generics),
		fn.Body != nil,
		fn.Name == "main",
		len(fn.Params),
		fn.ReturnType != nil,
	); diag.kind != "" {
		return nil, unsupported(diag.kind, diag.message)
	}
	sig := &fnSig{name: fn.Name, irName: fn.Name, decl: fn}
	if fn.Recv != nil {
		ownerType, ok := llvmMethodOwnerType(ownerName, structs, enums)
		if !ok {
			return nil, unsupportedf("type-system", "unknown method receiver owner %q", ownerName)
		}
		sig.irName = llvmMethodIRName(ownerName, fn.Name)
		sig.receiverType = ownerType
		sig.receiverMut = fn.Recv.Mut
		sig.params = append(sig.params, paramInfo{
			name:    "self",
			typ:     ownerType,
			irTyp:   llvmMethodReceiverIRType(ownerType, fn.Recv.Mut),
			mutable: fn.Recv.Mut,
			byRef:   fn.Recv.Mut,
		})
	}
	if fn.Name == "main" {
		return sig, nil
	}
	if fn.ReturnType == nil {
		sig.ret = "void"
	} else {
		ret, err := llvmType(fn.ReturnType, structs, enums)
		if err != nil {
			diag := llvmFunctionReturnDiagnostic(fn.Name, unsupportedMessage(err))
			return nil, unsupported(diag.kind, diag.message)
		}
		sig.ret = ret
	}
	for _, p := range fn.Params {
		if diag := llvmFunctionParamDiagnostic(fn.Name, p.Name, p.Pattern != nil || p.Name == "", false, true, ""); diag.kind != "" {
			return nil, unsupported(diag.kind, diag.message)
		}
		if p.Default != nil {
			diag := llvmFunctionParamDiagnostic(fn.Name, p.Name, false, true, true, "")
			return nil, unsupported(diag.kind, diag.message)
		}
		if !llvmIsIdent(p.Name) {
			diag := llvmFunctionParamDiagnostic(fn.Name, p.Name, false, false, false, "")
			return nil, unsupported(diag.kind, diag.message)
		}
		typ, err := llvmType(p.Type, structs, enums)
		if err != nil {
			diag := llvmFunctionParamDiagnostic(fn.Name, p.Name, false, false, true, unsupportedMessage(err))
			return nil, unsupported(diag.kind, diag.message)
		}
		info := paramInfo{name: p.Name, typ: typ}
		if listElemTyp, ok, err := llvmListElementType(p.Type, structs, enums); err != nil {
			diag := llvmFunctionParamDiagnostic(fn.Name, p.Name, false, false, true, unsupportedMessage(err))
			return nil, unsupported(diag.kind, diag.message)
		} else if ok {
			info.listElemTyp = listElemTyp
		}
		sig.params = append(sig.params, info)
	}
	if listElemTyp, ok, err := llvmListElementType(fn.ReturnType, structs, enums); err != nil {
		return nil, unsupportedf("type-system", "function %q return type: %s", fn.Name, unsupportedMessage(err))
	} else if ok {
		sig.retListElemTyp = listElemTyp
	}
	return sig, nil
}

func (g *generator) emitScriptMain(stmts []ast.Stmt) (string, error) {
	g.beginFunction()
	if err := g.emitBlock(stmts); err != nil {
		return "", err
	}
	if g.currentReachable {
		emitter := g.toOstyEmitter()
		g.releaseGCRoots(emitter)
		llvmReturnI32Zero(emitter)
		g.takeOstyEmitter(emitter)
	}
	return g.renderFunction("i32", "main", nil), nil
}

func (g *generator) emitMainFunction(sig *fnSig) (string, error) {
	g.beginFunction()
	if err := g.emitBlock(sig.decl.Body.Stmts); err != nil {
		return "", err
	}
	if g.currentReachable {
		emitter := g.toOstyEmitter()
		g.releaseGCRoots(emitter)
		llvmReturnI32Zero(emitter)
		g.takeOstyEmitter(emitter)
	}
	return g.renderFunction("i32", "main", nil), nil
}

func (g *generator) emitUserFunction(sig *fnSig) (string, error) {
	g.beginFunction()
	g.returnType = sig.ret
	g.returnListElemTyp = sig.retListElemTyp
	for _, p := range sig.params {
		v := value{typ: p.typ, ref: "%" + p.name, listElemTyp: p.listElemTyp}
		if p.listElemTyp != "" {
			v.gcManaged = true
		}
		v.rootPaths = g.rootPathsForType(p.typ)
		if p.byRef {
			v.ptr = true
			v.mutable = p.mutable
			g.bindLocal(p.name, v)
			continue
		}
		g.bindNamedLocal(p.name, v, p.mutable)
	}
	if sig.ret == "void" {
		if err := g.emitBlock(sig.decl.Body.Stmts); err != nil {
			return "", err
		}
		if g.currentReachable {
			emitter := g.toOstyEmitter()
			g.releaseGCRoots(emitter)
			emitter.body = append(emitter.body, "  ret void")
			g.takeOstyEmitter(emitter)
		}
	} else {
		if err := g.emitReturningBlock(sig.decl.Body.Stmts, sig.ret, sig.retListElemTyp); err != nil {
			return "", err
		}
	}
	return g.renderFunction(sig.ret, sig.irName, sig.params), nil
}

func (g *generator) beginFunction() {
	g.temp = 0
	g.label = 0
	g.body = nil
	g.locals = []map[string]value{{}}
	g.returnType = ""
	g.returnListElemTyp = ""
	g.gcRootSlots = nil
	g.gcRootMarks = []int{0}
	g.nextSafepoint = 1
	g.hiddenLocalID = 0
	g.currentBlock = "entry"
	g.currentReachable = true
	g.loopStack = nil
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

func (g *generator) emitGCSafepoint(emitter *LlvmEmitter) {
	g.declareRuntimeSymbol("osty.gc.safepoint_v1", "void", []paramInfo{
		{typ: "i64"},
		{typ: "ptr"},
		{typ: "i64"},
	})
	id := g.nextSafepoint
	g.nextSafepoint++
	roots := g.visibleSafepointRoots()
	if len(roots) == 0 {
		emitter.body = append(emitter.body, fmt.Sprintf(
			"  call void @osty.gc.safepoint_v1(i64 %d, ptr null, i64 0)",
			id,
		))
		g.needsGCRuntime = true
		return
	}
	slotsPtr := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = alloca ptr, i64 %d", slotsPtr, len(roots)))
	for i, root := range roots {
		slotPtr := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = getelementptr ptr, ptr %s, i64 %d", slotPtr, slotsPtr, i))
		emitter.body = append(emitter.body, fmt.Sprintf("  store ptr %s, ptr %s", g.safepointRootAddress(emitter, root), slotPtr))
	}
	emitter.body = append(emitter.body, fmt.Sprintf(
		"  call void @osty.gc.safepoint_v1(i64 %d, ptr %s, i64 %d)",
		id,
		slotsPtr,
		len(roots),
	))
	g.needsGCRuntime = true
}

func (g *generator) emitReturningBlock(stmts []ast.Stmt, retType, retListElemTyp string) error {
	if len(stmts) == 0 {
		return unsupported("function-signature", "function body has no return value")
	}
	for i, stmt := range stmts {
		if i != len(stmts)-1 {
			if err := g.emitStmt(stmt); err != nil {
				return err
			}
			if !g.currentReachable {
				return nil
			}
			continue
		}
		switch s := stmt.(type) {
		case *ast.ReturnStmt:
			if s.Value == nil {
				return unsupported("function-signature", "bare return in value-returning function")
			}
			v, err := g.emitExprWithListHint(s.Value, retListElemTyp)
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
			g.leaveBlock()
			return nil
		case *ast.ExprStmt:
			v, err := g.emitExprWithListHint(s.X, retListElemTyp)
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
			g.leaveBlock()
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
		if !g.currentReachable {
			break
		}
	}
	return nil
}

func (g *generator) emitStmt(stmt ast.Stmt) error {
	if !g.currentReachable {
		return nil
	}
	switch s := stmt.(type) {
	case *ast.Block:
		return g.emitScopedStmtBlock(s.Stmts)
	case *ast.LetStmt:
		return g.emitLet(s)
	case *ast.AssignStmt:
		return g.emitAssign(s)
	case *ast.ForStmt:
		return g.emitFor(s)
	case *ast.ReturnStmt:
		return g.emitReturn(s)
	case *ast.BreakStmt:
		return g.emitBreak()
	case *ast.ContinueStmt:
		return g.emitContinue()
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
	if stmt.Value == nil {
		return unsupported("statement", "let has no value")
	}
	hintedListElemTyp := ""
	if stmt.Type != nil {
		if listElemTyp, ok, err := llvmListElementType(stmt.Type, g.structsByName, g.enumsByName); err != nil {
			return err
		} else if ok {
			hintedListElemTyp = listElemTyp
		}
	}
	v, err := g.emitExprWithListHint(stmt.Value, hintedListElemTyp)
	if err != nil {
		return err
	}
	if stmt.Type != nil {
		typ, err := llvmType(stmt.Type, g.structsByName, g.enumsByName)
		if err != nil {
			return err
		}
		if typ != v.typ {
			return unsupportedf("type-system", "let pattern type %s, value %s", typ, v.typ)
		}
	}
	return g.bindLetPattern(stmt.Pattern, v, stmt.Mut)
}

func (g *generator) emitAssign(stmt *ast.AssignStmt) error {
	if stmt.Op != token.ASSIGN {
		return unsupportedf("statement", "compound assignment %q", stmt.Op)
	}
	if len(stmt.Targets) != 1 {
		return unsupported("statement", "multi-target assignment")
	}
	target, ok := stmt.Targets[0].(*ast.Ident)
	if ok {
		slot, ok := g.lookupLocal(target.Name)
		if !ok {
			return unsupportedf("name", "assignment to unknown identifier %q", target.Name)
		}
		if !slot.mutable {
			return unsupportedf("statement", "assignment to immutable identifier %q", target.Name)
		}
		v, err := g.emitExprWithListHint(stmt.Value, slot.listElemTyp)
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
	field, ok := stmt.Targets[0].(*ast.FieldExpr)
	if !ok {
		return unsupportedf("statement", "assignment target %T", stmt.Targets[0])
	}
	return g.emitFieldAssign(field, stmt.Value)
}

func (g *generator) emitFieldAssign(target *ast.FieldExpr, rhs ast.Expr) error {
	if target == nil {
		return unsupported("statement", "nil field assignment target")
	}
	if target.IsOptional {
		return unsupported("statement", "optional field assignment is not supported")
	}
	baseIdent, ok := target.X.(*ast.Ident)
	if !ok {
		return unsupportedf("statement", "field assignment base %T", target.X)
	}
	slot, ok := g.lookupLocal(baseIdent.Name)
	if !ok {
		return unsupportedf("name", "assignment to unknown identifier %q", baseIdent.Name)
	}
	if !slot.ptr || !slot.mutable {
		return unsupportedf("statement", "assignment to immutable field %q.%s", baseIdent.Name, target.Name)
	}
	info := g.structsByType[slot.typ]
	if info == nil {
		return unsupportedf("type-system", "field assignment on %s", slot.typ)
	}
	field, ok := info.byName[target.Name]
	if !ok {
		return unsupportedf("expression", "struct %q has no field %q", info.name, target.Name)
	}
	v, err := g.emitExprWithListHint(rhs, field.listElemTyp)
	if err != nil {
		return err
	}
	if v.typ != field.typ {
		return unsupportedf("type-system", "field assignment %q.%s type %s, value %s", baseIdent.Name, target.Name, field.typ, v.typ)
	}
	current, err := g.loadIfPointer(slot)
	if err != nil {
		return err
	}
	emitter := g.toOstyEmitter()
	tmp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf(
		"  %s = insertvalue %s %s, %s %s, %d",
		tmp,
		current.typ,
		current.ref,
		v.typ,
		v.ref,
		field.index,
	))
	llvmStore(emitter, toOstyValue(slot), toOstyValue(value{typ: current.typ, ref: tmp}))
	g.takeOstyEmitter(emitter)
	return nil
}

func (g *generator) emitFor(stmt *ast.ForStmt) error {
	if stmt.IsForLet {
		return g.emitForLet(stmt)
	}
	if stmt.Body == nil {
		return unsupported("control-flow", "for has no body")
	}
	if stmt.Pattern == nil {
		return g.emitWhileFor(stmt)
	}
	iterName, err := identPatternName(stmt.Pattern)
	if err != nil {
		return err
	}
	if iterTyp, elemTyp, ok := g.staticExprType(stmt.Iter); ok && iterTyp == "ptr" && elemTyp != "" {
		return g.emitListFor(stmt, iterName, elemTyp)
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
	g.enterBlock(loop.bodyLabel)
	continueLabel := g.nextNamedLabel("for.cont")
	g.pushLoop(loopContext{
		continueLabel: continueLabel,
		breakLabel:    loop.endLabel,
		scopeDepth:    len(g.locals),
	})
	scopeDepth := len(g.locals)
	g.pushScope()
	g.bindLocal(iterName, value{typ: "i64", ref: loop.current})
	if err := g.emitBlock(stmt.Body.Stmts); err != nil {
		if len(g.locals) > scopeDepth {
			g.popScope()
		}
		g.popLoop()
		return err
	}
	if len(g.locals) > scopeDepth {
		g.popScope()
	}
	g.popLoop()
	if g.currentReachable {
		g.branchTo(continueLabel)
	}
	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", continueLabel))
	g.emitGCSafepoint(emitter)
	llvmRangeEnd(emitter, loop)
	g.takeOstyEmitter(emitter)
	g.enterBlock(loop.endLabel)
	return nil
}

func (g *generator) emitWhileFor(stmt *ast.ForStmt) error {
	emitter := g.toOstyEmitter()
	condLabel := llvmNextLabel(emitter, "for.cond")
	bodyLabel := llvmNextLabel(emitter, "for.body")
	continueLabel := llvmNextLabel(emitter, "for.cont")
	endLabel := llvmNextLabel(emitter, "for.end")
	emitter.body = append(emitter.body, fmt.Sprintf("  br label %%%s", condLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", condLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(condLabel)

	if stmt.Iter == nil {
		emitter = g.toOstyEmitter()
		emitter.body = append(emitter.body, fmt.Sprintf("  br label %%%s", bodyLabel))
		emitter.body = append(emitter.body, fmt.Sprintf("%s:", bodyLabel))
		g.takeOstyEmitter(emitter)
	} else {
		cond, err := g.emitExpr(stmt.Iter)
		if err != nil {
			return err
		}
		if cond.typ != "i1" {
			return unsupportedf("type-system", "for condition type %s, want i1", cond.typ)
		}
		emitter = g.toOstyEmitter()
		emitter.body = append(emitter.body, fmt.Sprintf(
			"  br i1 %s, label %%%s, label %%%s",
			toOstyValue(cond).name,
			bodyLabel,
			endLabel,
		))
		emitter.body = append(emitter.body, fmt.Sprintf("%s:", bodyLabel))
		g.takeOstyEmitter(emitter)
	}
	g.enterBlock(bodyLabel)

	g.pushLoop(loopContext{
		continueLabel: continueLabel,
		breakLabel:    endLabel,
		scopeDepth:    len(g.locals),
	})
	scopeDepth := len(g.locals)
	g.pushScope()
	if err := g.emitBlock(stmt.Body.Stmts); err != nil {
		if len(g.locals) > scopeDepth {
			g.popScope()
		}
		g.popLoop()
		return err
	}
	if len(g.locals) > scopeDepth {
		g.popScope()
	}
	g.popLoop()
	if g.currentReachable {
		g.branchTo(continueLabel)
	}

	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", continueLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(continueLabel)
	emitter = g.toOstyEmitter()
	g.emitGCSafepoint(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  br label %%%s", condLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", endLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(endLabel)
	return nil
}

func (g *generator) emitForLet(stmt *ast.ForStmt) error {
	if stmt.Body == nil {
		return unsupported("control-flow", "for has no body")
	}
	if stmt.Pattern == nil {
		return unsupported("control-flow", "for-let requires a pattern")
	}
	if stmt.Iter == nil {
		return unsupported("control-flow", "for-let requires an iterator expression")
	}
	emitter := g.toOstyEmitter()
	condLabel := llvmNextLabel(emitter, "for.cond")
	bodyLabel := llvmNextLabel(emitter, "for.body")
	continueLabel := llvmNextLabel(emitter, "for.cont")
	endLabel := llvmNextLabel(emitter, "for.end")
	emitter.body = append(emitter.body, fmt.Sprintf("  br label %%%s", condLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", condLabel))
	g.takeOstyEmitter(emitter)

	scrutinee, err := g.emitExpr(stmt.Iter)
	if err != nil {
		return err
	}
	cond, bind, err := g.ifLetCondition(stmt.Pattern, scrutinee)
	if err != nil {
		return err
	}
	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, fmt.Sprintf("  br i1 %s, label %%%s, label %%%s", cond.name, bodyLabel, endLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", bodyLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(bodyLabel)

	g.pushLoop(loopContext{
		continueLabel: continueLabel,
		breakLabel:    endLabel,
		scopeDepth:    len(g.locals),
	})
	scopeDepth := len(g.locals)
	g.pushScope()
	if bind != nil {
		if err := bind(); err != nil {
			if len(g.locals) > scopeDepth {
				g.popScope()
			}
			g.popLoop()
			return err
		}
	}
	if err := g.emitBlock(stmt.Body.Stmts); err != nil {
		if len(g.locals) > scopeDepth {
			g.popScope()
		}
		g.popLoop()
		return err
	}
	if len(g.locals) > scopeDepth {
		g.popScope()
	}
	g.popLoop()
	if g.currentReachable {
		g.branchTo(continueLabel)
	}

	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", continueLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(continueLabel)
	emitter = g.toOstyEmitter()
	g.emitGCSafepoint(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  br label %%%s", condLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", endLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(endLabel)
	return nil
}

func (g *generator) emitReturn(stmt *ast.ReturnStmt) error {
	if stmt == nil {
		return unsupported("statement", "nil return")
	}
	var ret value
	var err error
	switch {
	case stmt.Value == nil:
		if g.returnType != "" && g.returnType != "void" {
			return unsupported("function-signature", "bare return in value-returning function")
		}
	case g.returnType == "" || g.returnType == "void":
		return unsupported("function-signature", "return with value in void-returning function")
	default:
		ret, err = g.emitExprWithListHint(stmt.Value, g.returnListElemTyp)
		if err != nil {
			return err
		}
		if ret.typ != g.returnType {
			return unsupportedf("type-system", "return type %s, want %s", ret.typ, g.returnType)
		}
	}
	emitter := g.toOstyEmitter()
	g.releaseGCRoots(emitter)
	switch {
	case stmt.Value == nil && g.returnType == "":
		llvmReturnI32Zero(emitter)
	case stmt.Value == nil && g.returnType == "void":
		emitter.body = append(emitter.body, "  ret void")
	default:
		llvmReturn(emitter, toOstyValue(ret))
	}
	g.takeOstyEmitter(emitter)
	g.leaveBlock()
	return nil
}

func (g *generator) emitExprStmt(expr ast.Expr) error {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return unsupported("statement", "only println calls are supported as expression statements")
	}
	if emitted, err := g.emitTestingCallStmt(call); emitted || err != nil {
		return err
	}
	if emitted, err := g.emitListMethodCallStmt(call); emitted || err != nil {
		return err
	}
	if emitted, err := g.emitRuntimeFFICallStmt(call); emitted || err != nil {
		return err
	}
	if emitted, err := g.emitUserCallStmt(call); emitted || err != nil {
		return err
	}
	return g.emitPrintln(call)
}

func (g *generator) emitTestingCallStmt(call *ast.CallExpr) (bool, error) {
	method, ok := g.testingCallMethod(call)
	if !ok {
		return false, nil
	}
	switch method {
	case "assert":
		if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return true, unsupported("call", "testing.assert requires one positional argument")
		}
		cond, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return true, err
		}
		return true, g.emitTestingAssertion(cond, g.testingFailureMessage(call, "assert"))
	case "assertEq":
		return true, g.emitTestingCompare(call, token.EQ, "assertEq")
	case "assertNe":
		return true, g.emitTestingCompare(call, token.NEQ, "assertNe")
	case "fail":
		if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return true, unsupported("call", "testing.fail requires one positional argument")
		}
		g.emitTestingAbort(g.testingFailureMessage(call, "fail"))
		return true, nil
	case "context":
		return true, g.emitTestingContextStmt(call)
	case "expectOk":
		_, err := g.emitTestingExpect(call, false)
		return true, err
	case "expectError":
		_, err := g.emitTestingExpect(call, true)
		return true, err
	default:
		return true, unsupportedf("call", "testing.%s is not supported by LLVM yet", method)
	}
}

func (g *generator) testingCallMethod(call *ast.CallExpr) (string, bool) {
	if call == nil {
		return "", false
	}
	field, ok := call.Fn.(*ast.FieldExpr)
	if !ok || field.IsOptional {
		return "", false
	}
	alias, ok := field.X.(*ast.Ident)
	if !ok || alias == nil || !g.testingAliases[alias.Name] {
		return "", false
	}
	return field.Name, true
}

func (g *generator) emitTestingCompare(call *ast.CallExpr, op token.Kind, name string) error {
	if len(call.Args) != 2 {
		return unsupportedf("call", "testing.%s requires two positional arguments", name)
	}
	for _, arg := range call.Args {
		if arg == nil || arg.Name != "" || arg.Value == nil {
			return unsupportedf("call", "testing.%s requires positional arguments", name)
		}
	}
	left, err := g.emitExpr(call.Args[0].Value)
	if err != nil {
		return err
	}
	right, err := g.emitExpr(call.Args[1].Value)
	if err != nil {
		return err
	}
	cond, err := g.emitCompare(op, left, right)
	if err != nil {
		return err
	}
	return g.emitTestingAssertion(cond, g.testingFailureMessage(call, name))
}

func (g *generator) emitTestingExpect(call *ast.CallExpr, wantErr bool) (value, error) {
	method := "expectOk"
	wantTag := "0"
	payloadIndex := 1
	if wantErr {
		method = "expectError"
		wantTag = "1"
		payloadIndex = 2
	}
	if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
		return value{}, unsupportedf("call", "testing.%s requires one positional argument", method)
	}
	result, err := g.emitExpr(call.Args[0].Value)
	if err != nil {
		return value{}, err
	}
	info, ok := g.resultTypes[result.typ]
	if !ok {
		return value{}, unsupportedf("type-system", "testing.%s requires a Result<T, E> value", method)
	}
	payloadType := info.okTyp
	if wantErr {
		payloadType = info.errTyp
	}
	emitter := g.toOstyEmitter()
	tag := llvmExtractValue(emitter, toOstyValue(result), "i64", 0)
	cond := llvmCompare(emitter, "eq", tag, toOstyValue(value{typ: "i64", ref: wantTag}))
	okLabel := llvmNextLabel(emitter, "test.expect.ok")
	failLabel := llvmNextLabel(emitter, "test.expect.fail")
	emitter.body = append(emitter.body, fmt.Sprintf("  br i1 %s, label %%%s, label %%%s", cond.name, okLabel, failLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", failLabel))
	g.emitTestingAbortWithEmitter(emitter, g.testingFailureMessage(call, method), okLabel)
	payload := llvmExtractValue(emitter, toOstyValue(result), payloadType, payloadIndex)
	g.takeOstyEmitter(emitter)
	out := fromOstyValue(payload)
	out.rootPaths = g.rootPathsForType(out.typ)
	return out, nil
}

func (g *generator) emitTestingContextStmt(call *ast.CallExpr) error {
	if len(call.Args) != 2 || call.Args[0] == nil || call.Args[1] == nil || call.Args[0].Name != "" || call.Args[1].Name != "" || call.Args[1].Value == nil {
		return unsupported("call", "testing.context requires a message and a zero-arg closure")
	}
	closure, ok := call.Args[1].Value.(*ast.ClosureExpr)
	if !ok {
		return unsupported("call", "testing.context requires a closure body")
	}
	if len(closure.Params) != 0 || closure.ReturnType != nil || closure.Body == nil {
		return unsupported("call", "testing.context requires a zero-arg closure with inferred unit return")
	}
	g.pushScope()
	defer g.popScope()
	return g.emitTestingClosureBody(closure.Body)
}

func (g *generator) emitTestingClosureBody(body ast.Expr) error {
	switch expr := body.(type) {
	case *ast.Block:
		return g.emitBlock(expr.Stmts)
	case *ast.IfExpr:
		return g.emitIfStmt(expr)
	default:
		return g.emitExprStmt(expr)
	}
}

func (g *generator) emitTestingAssertion(cond value, message string) error {
	if cond.typ != "i1" {
		return unsupportedf("type-system", "testing assertion condition type %s, want i1", cond.typ)
	}
	emitter := g.toOstyEmitter()
	okLabel := llvmNextLabel(emitter, "test.ok")
	failLabel := llvmNextLabel(emitter, "test.fail")
	emitter.body = append(emitter.body, fmt.Sprintf("  br i1 %s, label %%%s, label %%%s", cond.ref, okLabel, failLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", failLabel))
	g.emitTestingAbortWithEmitter(emitter, message, okLabel)
	g.takeOstyEmitter(emitter)
	g.currentBlock = okLabel
	return nil
}

func (g *generator) emitTestingAbort(message string) {
	emitter := g.toOstyEmitter()
	deadLabel := llvmNextLabel(emitter, "test.dead")
	g.emitTestingAbortWithEmitter(emitter, message, deadLabel)
	g.takeOstyEmitter(emitter)
	g.currentBlock = deadLabel
}

func (g *generator) emitTestingAbortWithEmitter(emitter *LlvmEmitter, message string, nextLabel string) {
	text := llvmStringLiteral(emitter, message)
	llvmPrintlnString(emitter, text)
	g.declareRuntimeSymbol("exit", "void", []paramInfo{{typ: "i32"}})
	emitter.body = append(emitter.body, "  call void @exit(i32 1)")
	emitter.body = append(emitter.body, "  unreachable")
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", nextLabel))
}

func (g *generator) testingFailureMessage(call *ast.CallExpr, name string) string {
	source := g.sourcePath
	if source == "" {
		source = "<test>"
	} else if abs, err := filepath.Abs(source); err == nil {
		source = abs
	}
	line := 0
	if call != nil {
		line = call.Pos().Line
	}
	return fmt.Sprintf("testing.%s failed at %s:%d", name, source, line)
}

func (g *generator) emitIfStmt(expr *ast.IfExpr) error {
	if expr.IsIfLet {
		return g.emitIfLetStmt(expr)
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
	g.enterBlock(labels.thenLabel)
	baseState := g.captureScopeState()
	if err := g.emitScopedStmtBlock(expr.Then.Stmts); err != nil {
		return err
	}
	thenReachable := g.currentReachable
	if thenReachable {
		g.branchTo(labels.endLabel)
	}
	g.restoreScopeState(baseState)
	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", labels.elseLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(labels.elseLabel)
	if expr.Else != nil {
		if err := g.emitElse(expr.Else); err != nil {
			return err
		}
	}
	elseReachable := g.currentReachable
	if elseReachable {
		g.branchTo(labels.endLabel)
	}
	g.restoreScopeState(baseState)
	if thenReachable || elseReachable {
		emitter = g.toOstyEmitter()
		emitter.body = append(emitter.body, fmt.Sprintf("%s:", labels.endLabel))
		g.takeOstyEmitter(emitter)
		g.enterBlock(labels.endLabel)
		return nil
	}
	g.leaveBlock()
	return nil
}

func (g *generator) emitIfLetStmt(expr *ast.IfExpr) error {
	if expr.Then == nil {
		return unsupported("control-flow", "if has no then block")
	}
	scrutinee, err := g.emitExpr(expr.Cond)
	if err != nil {
		return err
	}
	cond, bind, err := g.ifLetCondition(expr.Pattern, scrutinee)
	if err != nil {
		return err
	}
	emitter := g.toOstyEmitter()
	labels := llvmIfStart(emitter, cond)
	g.takeOstyEmitter(emitter)
	g.enterBlock(labels.thenLabel)
	baseState := g.captureScopeState()
	scopeDepth := len(g.locals)
	g.pushScope()
	if bind != nil {
		if err := bind(); err != nil {
			if len(g.locals) > scopeDepth {
				g.popScope()
			}
			return err
		}
	}
	if err := g.emitBlock(expr.Then.Stmts); err != nil {
		if len(g.locals) > scopeDepth {
			g.popScope()
		}
		return err
	}
	if len(g.locals) > scopeDepth {
		g.popScope()
	}
	thenReachable := g.currentReachable
	if thenReachable {
		g.branchTo(labels.endLabel)
	}
	g.restoreScopeState(baseState)
	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", labels.elseLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(labels.elseLabel)
	if expr.Else != nil {
		if err := g.emitElse(expr.Else); err != nil {
			return err
		}
	}
	elseReachable := g.currentReachable
	if elseReachable {
		g.branchTo(labels.endLabel)
	}
	g.restoreScopeState(baseState)
	if thenReachable || elseReachable {
		emitter = g.toOstyEmitter()
		emitter.body = append(emitter.body, fmt.Sprintf("%s:", labels.endLabel))
		g.takeOstyEmitter(emitter)
		g.enterBlock(labels.endLabel)
		return nil
	}
	g.leaveBlock()
	return nil
}

func (g *generator) ifLetCondition(pattern ast.Pattern, scrutinee value) (*LlvmValue, func() error, error) {
	if pattern == nil {
		return nil, nil, unsupported("control-flow", "if-let requires a pattern")
	}
	if _, ok := pattern.(*ast.WildcardPat); ok {
		return toOstyValue(value{typ: "i1", ref: "true"}), func() error { return nil }, nil
	}
	if info := g.enumsByType[scrutinee.typ]; info != nil && info.hasPayload {
		matched, ok, err := g.matchPayloadEnumPattern(info, pattern)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			return nil, nil, unsupported("control-flow", "if-let pattern must be an enum variant")
		}
		emitter := g.toOstyEmitter()
		tag := llvmExtractValue(emitter, toOstyValue(scrutinee), "i64", 0)
		cond := llvmCompare(emitter, "eq", tag, toOstyValue(value{typ: "i64", ref: strconv.Itoa(matched.variant.tag)}))
		g.takeOstyEmitter(emitter)
		return cond, func() error {
			return g.bindPayloadEnumPattern(scrutinee, matched)
		}, nil
	}
	tag, ok, err := g.matchEnumTag(pattern)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, unsupported("control-flow", "if-let pattern must be an enum variant")
	}
	emitter := g.toOstyEmitter()
	cond := llvmCompare(emitter, "eq", toOstyValue(scrutinee), toOstyValue(value{typ: "i64", ref: strconv.Itoa(tag)}))
	g.takeOstyEmitter(emitter)
	return cond, func() error { return nil }, nil
}

func (g *generator) emitElse(expr ast.Expr) error {
	switch e := expr.(type) {
	case *ast.Block:
		return g.emitScopedStmtBlock(e.Stmts)
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
	if !llvmIsAsciiStringText(text) {
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
	case *ast.TupleExpr:
		return g.emitTupleExpr(e)
	case *ast.ListExpr:
		return g.emitListExprWithHint(e, "")
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
	loaded := fromOstyValue(out)
	loaded.gcManaged = v.gcManaged
	loaded.listElemTyp = v.listElemTyp
	loaded.rootPaths = cloneRootPaths(v.rootPaths)
	return loaded, nil
}

func (g *generator) registerTupleType(elemTypes []string, elemListElemTyps []string) tupleTypeInfo {
	info := tupleTypeInfo{
		typ:              llvmTupleTypeName(elemTypes),
		elems:            append([]string(nil), elemTypes...),
		elemListElemTyps: append([]string(nil), elemListElemTyps...),
	}
	if g.tupleTypes == nil {
		g.tupleTypes = map[string]tupleTypeInfo{}
	}
	if existing, ok := g.tupleTypes[info.typ]; ok {
		if len(existing.elemListElemTyps) == 0 && len(info.elemListElemTyps) != 0 {
			existing.elemListElemTyps = append([]string(nil), info.elemListElemTyps...)
			g.tupleTypes[info.typ] = existing
		}
		return existing
	}
	g.tupleTypes[info.typ] = info
	return info
}

func (g *generator) emitTupleExpr(expr *ast.TupleExpr) (value, error) {
	if expr == nil {
		return value{}, unsupported("expression", "nil tuple literal")
	}
	fields := make([]*LlvmValue, 0, len(expr.Elems))
	elemTypes := make([]string, 0, len(expr.Elems))
	elemListElemTyps := make([]string, 0, len(expr.Elems))
	for _, elem := range expr.Elems {
		v, err := g.emitExpr(elem)
		if err != nil {
			return value{}, err
		}
		fields = append(fields, toOstyValue(v))
		elemTypes = append(elemTypes, v.typ)
		elemListElemTyps = append(elemListElemTyps, v.listElemTyp)
	}
	info := g.registerTupleType(elemTypes, elemListElemTyps)
	emitter := g.toOstyEmitter()
	out := llvmStructLiteral(emitter, info.typ, fields)
	g.takeOstyEmitter(emitter)
	tupleValue := fromOstyValue(out)
	tupleValue.rootPaths = g.rootPathsForType(info.typ)
	return tupleValue, nil
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
		if !llvmIsIdent(field.Name) {
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
			v, err = g.emitExprWithListHint(litField.Value, field.listElemTyp)
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
	litValue := fromOstyValue(out)
	litValue.rootPaths = g.rootPathsForType(info.typ)
	return litValue, nil
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
	loaded := fromOstyValue(out)
	loaded.listElemTyp = field.listElemTyp
	loaded.gcManaged = field.listElemTyp != ""
	loaded.rootPaths = g.rootPathsForType(field.typ)
	return loaded, nil
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
		return g.emitEnumPayloadVariant(info, variant, value{typ: info.payloadTyp, ref: llvmZeroLiteral(info.payloadTyp)})
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
	enumValue := fromOstyValue(out)
	enumValue.rootPaths = g.rootPathsForType(info.typ)
	return enumValue, nil
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
	case token.BITNOT:
		if v.typ != "i64" {
			return value{}, unsupportedf("type-system", "bitwise not on %s", v.typ)
		}
		emitter := g.toOstyEmitter()
		out := llvmBinaryI64(emitter, "xor", toOstyValue(v), llvmIntLiteral(-1))
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
	if llvmIsCompareOp(e.Op.String()) {
		return g.emitCompare(e.Op, left, right)
	}
	if e.Op == token.AND || e.Op == token.OR {
		return g.emitLogical(e.Op, left, right)
	}
	if left.typ == "double" && right.typ == "double" {
		op := llvmFloatBinaryInstruction(e.Op.String())
		if op == "" {
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
	op := llvmIntBinaryInstruction(e.Op.String())
	if op == "" {
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
	inst := llvmLogicalInstruction(op.String())
	if inst == "" {
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
		pred := llvmIntComparePredicate(op.String())
		if pred == "" {
			g.takeOstyEmitter(emitter)
			return value{}, unsupportedf("expression", "comparison operator %q", op)
		}
		out = llvmCompare(emitter, pred, toOstyValue(left), toOstyValue(right))
	case "double":
		pred := llvmFloatComparePredicate(op.String())
		if pred == "" {
			g.takeOstyEmitter(emitter)
			return value{}, unsupportedf("expression", "comparison operator %q", op)
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

func (g *generator) emitIfExprValue(expr *ast.IfExpr) (value, error) {
	if expr.IsIfLet {
		return g.emitIfLetExprValue(expr)
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

func (g *generator) emitIfLetExprValue(expr *ast.IfExpr) (value, error) {
	if expr.Then == nil {
		return value{}, unsupported("control-flow", "if expression has no then block")
	}
	if expr.Else == nil {
		return value{}, unsupported("control-flow", "if expression has no else branch")
	}
	scrutinee, err := g.emitExpr(expr.Cond)
	if err != nil {
		return value{}, err
	}
	cond, bind, err := g.ifLetCondition(expr.Pattern, scrutinee)
	if err != nil {
		return value{}, err
	}
	emitter := g.toOstyEmitter()
	labels := llvmIfExprStart(emitter, cond)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.thenLabel

	g.pushScope()
	if bind != nil {
		if err := bind(); err != nil {
			g.popScope()
			return value{}, err
		}
	}
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
	out := value{typ: thenValue.typ, ref: tmp}
	if thenValue.listElemTyp != "" && thenValue.listElemTyp == elseValue.listElemTyp {
		out.listElemTyp = thenValue.listElemTyp
		out.gcManaged = thenValue.gcManaged || elseValue.gcManaged
	}
	out.rootPaths = g.rootPathsForType(out.typ)
	return out, nil
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
	hasGuard := false
	for _, arm := range expr.Arms {
		if arm == nil {
			return value{}, unsupported("expression", "nil match arm")
		}
		if arm.Guard != nil {
			hasGuard = true
		}
	}
	if hasGuard {
		return g.emitGuardedMatchExprValue(scrutinee, expr.Arms)
	}
	if scrutinee.typ == "i64" {
		return g.emitTagEnumMatchExprValue(scrutinee, expr.Arms)
	}
	if info := g.enumsByType[scrutinee.typ]; info != nil && info.hasPayload {
		return g.emitPayloadEnumMatchExprValue(scrutinee, info, expr.Arms)
	}
	return value{}, unsupportedf("type-system", "match scrutinee type %s, want enum tag", scrutinee.typ)
}

func (g *generator) emitTagEnumMatchExprValue(scrutinee value, arms []*ast.MatchArm) (value, error) {
	if len(arms) == 2 {
		return g.emitTagEnumMatchIfExprValue(scrutinee, arms[0], arms[1])
	}
	selectSafe := true
	for _, arm := range arms {
		if !matchArmBodyIsSelectSafe(arm.Body) {
			selectSafe = false
			break
		}
	}
	if selectSafe {
		return g.emitTagEnumMatchSelectValue(scrutinee, arms)
	}
	return g.emitTagEnumMatchChainValue(scrutinee, arms)
}

func (g *generator) emitTagEnumMatchSelectValue(scrutinee value, arms []*ast.MatchArm) (value, error) {
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

func (g *generator) emitTagEnumMatchChainValue(scrutinee value, arms []*ast.MatchArm) (value, error) {
	if len(arms) == 0 {
		return value{}, unsupported("expression", "match with no arms")
	}
	arm := arms[0]
	if arm == nil {
		return value{}, unsupported("expression", "nil match arm")
	}
	if len(arms) == 1 {
		if _, catchAll := arm.Pattern.(*ast.WildcardPat); !catchAll {
			if _, ok, err := g.matchEnumTag(arm.Pattern); err != nil {
				return value{}, err
			} else if !ok {
				return value{}, unsupported("expression", "match arm must be a payload-free enum variant")
			}
		}
		return g.emitMatchArmBodyValue(arm.Body)
	}
	if _, catchAll := arm.Pattern.(*ast.WildcardPat); catchAll {
		return value{}, unsupported("expression", "wildcard match arm must be last")
	}
	tag, ok, err := g.matchEnumTag(arm.Pattern)
	if err != nil {
		return value{}, err
	}
	if !ok {
		return value{}, unsupported("expression", "match arm must be a payload-free enum variant")
	}
	emitter := g.toOstyEmitter()
	cond := llvmCompare(emitter, "eq", toOstyValue(scrutinee), toOstyValue(value{typ: "i64", ref: strconv.Itoa(tag)}))
	labels := llvmIfExprStart(emitter, cond)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.thenLabel

	thenValue, err := g.emitMatchArmBodyValue(arm.Body)
	if err != nil {
		return value{}, err
	}
	thenPred := g.currentBlock
	emitter = g.toOstyEmitter()
	llvmIfExprElse(emitter, labels)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.elseLabel

	elseValue, err := g.emitTagEnumMatchChainValue(scrutinee, arms[1:])
	if err != nil {
		return value{}, err
	}
	elsePred := g.currentBlock
	if thenValue.typ != elseValue.typ {
		return value{}, unsupportedf("type-system", "match arm types %s/%s", thenValue.typ, elseValue.typ)
	}
	return g.emitIfExprPhi(labels, thenPred, elsePred, thenValue, elseValue)
}

func (g *generator) emitGuardedMatchExprValue(scrutinee value, arms []*ast.MatchArm) (value, error) {
	if len(arms) == 0 {
		return value{}, unsupported("expression", "match with no arms")
	}
	arm := arms[0]
	if arm == nil {
		return value{}, unsupported("expression", "nil match arm")
	}
	if len(arms) == 1 {
		return g.emitFinalMatchArmValue(scrutinee, arm)
	}
	if _, catchAll := arm.Pattern.(*ast.WildcardPat); catchAll && arm.Guard == nil {
		return value{}, unsupported("expression", "wildcard match arm must be last")
	}
	cond, bind, err := g.ifLetCondition(arm.Pattern, scrutinee)
	if err != nil {
		return value{}, err
	}
	emitter := g.toOstyEmitter()
	labels := llvmIfExprStart(emitter, cond)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.thenLabel

	thenValue, err := g.emitGuardedMatchArmThenValue(scrutinee, arm, arms[1:], bind)
	if err != nil {
		return value{}, err
	}
	thenPred := g.currentBlock
	emitter = g.toOstyEmitter()
	llvmIfExprElse(emitter, labels)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.elseLabel

	elseValue, err := g.emitGuardedMatchExprValue(scrutinee, arms[1:])
	if err != nil {
		return value{}, err
	}
	elsePred := g.currentBlock
	if thenValue.typ != elseValue.typ {
		return value{}, unsupportedf("type-system", "match arm types %s/%s", thenValue.typ, elseValue.typ)
	}
	return g.emitIfExprPhi(labels, thenPred, elsePred, thenValue, elseValue)
}

func (g *generator) emitGuardedMatchArmThenValue(scrutinee value, arm *ast.MatchArm, rest []*ast.MatchArm, bind func() error) (value, error) {
	g.pushScope()
	defer g.popScope()
	if bind != nil {
		if err := bind(); err != nil {
			return value{}, err
		}
	}
	if arm.Guard == nil {
		return g.emitMatchArmBodyValue(arm.Body)
	}
	guard, err := g.emitExpr(arm.Guard)
	if err != nil {
		return value{}, err
	}
	if guard.typ != "i1" {
		return value{}, unsupportedf("type-system", "match guard type %s, want i1", guard.typ)
	}
	if len(rest) == 0 {
		return value{}, unsupported("control-flow", "final guarded match arm requires an unguarded fallback arm")
	}
	emitter := g.toOstyEmitter()
	labels := llvmIfExprStart(emitter, toOstyValue(guard))
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.thenLabel

	thenValue, err := g.emitMatchArmBodyValue(arm.Body)
	if err != nil {
		return value{}, err
	}
	thenPred := g.currentBlock
	emitter = g.toOstyEmitter()
	llvmIfExprElse(emitter, labels)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.elseLabel

	elseValue, err := g.emitGuardedMatchExprValue(scrutinee, rest)
	if err != nil {
		return value{}, err
	}
	elsePred := g.currentBlock
	if thenValue.typ != elseValue.typ {
		return value{}, unsupportedf("type-system", "match arm types %s/%s", thenValue.typ, elseValue.typ)
	}
	return g.emitIfExprPhi(labels, thenPred, elsePred, thenValue, elseValue)
}

func (g *generator) emitFinalMatchArmValue(scrutinee value, arm *ast.MatchArm) (value, error) {
	if arm == nil {
		return value{}, unsupported("expression", "nil match arm")
	}
	if arm.Guard != nil {
		return value{}, unsupported("control-flow", "final guarded match arm requires an unguarded fallback arm")
	}
	_, bind, err := g.ifLetCondition(arm.Pattern, scrutinee)
	if err != nil {
		return value{}, err
	}
	g.pushScope()
	defer g.popScope()
	if bind != nil {
		if err := bind(); err != nil {
			return value{}, err
		}
	}
	return g.emitMatchArmBodyValue(arm.Body)
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
	out := value{typ: thenValue.typ, ref: tmp}
	if thenValue.listElemTyp != "" && thenValue.listElemTyp == elseValue.listElemTyp {
		out.listElemTyp = thenValue.listElemTyp
		out.gcManaged = thenValue.gcManaged || elseValue.gcManaged
	}
	out.rootPaths = g.rootPathsForType(out.typ)
	return out, nil
}

func (g *generator) emitExprWithListHint(expr ast.Expr, listElemTyp string) (value, error) {
	if list, ok := expr.(*ast.ListExpr); ok {
		return g.emitListExprWithHint(list, listElemTyp)
	}
	return g.emitExpr(expr)
}

func (g *generator) usesAggregateListABI(elemTyp string) bool {
	switch elemTyp {
	case "", "i64", "i1", "double", "ptr":
		return false
	}
	return len(g.rootPathsForType(elemTyp)) == 0
}

func (g *generator) emitAggregateByteSize(emitter *LlvmEmitter, typ string) value {
	sizePtr := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = getelementptr %s, ptr null, i32 1", sizePtr, typ))
	size := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = ptrtoint ptr %s to i64", size, sizePtr))
	return value{typ: "i64", ref: size}
}

func (g *generator) emitAggregateScratchSlot(emitter *LlvmEmitter, typ, initial string) value {
	slot := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = alloca %s", slot, typ))
	emitter.body = append(emitter.body, fmt.Sprintf("  store %s %s, ptr %s", typ, initial, slot))
	return value{typ: typ, ref: slot, ptr: true}
}

func (g *generator) emitListAggregatePush(listValue, elem value) error {
	if len(g.rootPathsForType(elem.typ)) != 0 {
		return unsupportedf("type-system", "list element type %s with managed aggregate fields", elem.typ)
	}
	g.declareRuntimeSymbol(listRuntimePushBytesSymbol(), "void", []paramInfo{{typ: "ptr"}, {typ: "ptr"}, {typ: "i64"}})
	emitter := g.toOstyEmitter()
	slot := g.emitAggregateScratchSlot(emitter, elem.typ, elem.ref)
	size := g.emitAggregateByteSize(emitter, elem.typ)
	emitter.body = append(emitter.body, fmt.Sprintf(
		"  call void @%s(%s)",
		listRuntimePushBytesSymbol(),
		llvmCallArgs([]*LlvmValue{toOstyValue(listValue), toOstyValue(value{typ: "ptr", ref: slot.ref}), toOstyValue(size)}),
	))
	g.takeOstyEmitter(emitter)
	return nil
}

func (g *generator) emitListAggregateGet(listValue value, index value, elemTyp string) (value, error) {
	if len(g.rootPathsForType(elemTyp)) != 0 {
		return value{}, unsupportedf("type-system", "list element type %s with managed aggregate fields", elemTyp)
	}
	g.declareRuntimeSymbol(listRuntimeGetBytesSymbol(), "void", []paramInfo{{typ: "ptr"}, {typ: "i64"}, {typ: "ptr"}, {typ: "i64"}})
	emitter := g.toOstyEmitter()
	slot := g.emitAggregateScratchSlot(emitter, elemTyp, "zeroinitializer")
	size := g.emitAggregateByteSize(emitter, elemTyp)
	emitter.body = append(emitter.body, fmt.Sprintf(
		"  call void @%s(%s)",
		listRuntimeGetBytesSymbol(),
		llvmCallArgs([]*LlvmValue{toOstyValue(listValue), toOstyValue(index), toOstyValue(value{typ: "ptr", ref: slot.ref}), toOstyValue(size)}),
	))
	out := llvmLoad(emitter, toOstyValue(slot))
	g.takeOstyEmitter(emitter)
	loaded := fromOstyValue(out)
	loaded.rootPaths = g.rootPathsForType(elemTyp)
	return loaded, nil
}

func (g *generator) emitListExprWithHint(expr *ast.ListExpr, hintedElemTyp string) (value, error) {
	if expr == nil {
		return value{}, unsupported("expression", "nil list literal")
	}
	g.pushScope()
	defer g.popScope()
	elemTyp := hintedElemTyp
	emittedElems := make([]value, 0, len(expr.Elems))
	for _, elem := range expr.Elems {
		v, err := g.emitExpr(elem)
		if err != nil {
			return value{}, err
		}
		if elemTyp == "" {
			elemTyp = v.typ
		}
		if v.typ != elemTyp {
			return value{}, unsupportedf("type-system", "list literal element type %s, want %s", v.typ, elemTyp)
		}
		emittedElems = append(emittedElems, g.protectManagedTemporary("list.elem", v))
	}
	if elemTyp == "" {
		return value{}, unsupported("expression", "empty list literal requires an explicit List<T> type")
	}
	useAggregateABI := g.usesAggregateListABI(elemTyp)
	if !useAggregateABI && elemTyp != "ptr" && elemTyp != "i64" && elemTyp != "i1" && elemTyp != "double" {
		return value{}, unsupportedf("type-system", "list literal element type %s requires scalar tuples or primitive values", elemTyp)
	}
	g.declareRuntimeSymbol(listRuntimeNewSymbol(), "ptr", nil)
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "ptr", listRuntimeNewSymbol(), nil)
	g.takeOstyEmitter(emitter)
	listValue := fromOstyValue(out)
	listValue.gcManaged = true
	listValue.listElemTyp = elemTyp
	if len(emittedElems) == 0 {
		return listValue, nil
	}
	pushSymbol := ""
	if !useAggregateABI {
		pushSymbol = listRuntimePushSymbol(elemTyp)
		g.declareRuntimeSymbol(pushSymbol, "void", []paramInfo{{typ: "ptr"}, {typ: elemTyp}})
	}
	for _, elem := range emittedElems {
		loaded, err := g.loadIfPointer(elem)
		if err != nil {
			return value{}, err
		}
		if useAggregateABI {
			if err := g.emitListAggregatePush(listValue, loaded); err != nil {
				return value{}, err
			}
			continue
		}
		emitter = g.toOstyEmitter()
		emitter.body = append(emitter.body, fmt.Sprintf(
			"  call void @%s(%s)",
			pushSymbol,
			llvmCallArgs([]*LlvmValue{toOstyValue(listValue), toOstyValue(loaded)}),
		))
		g.takeOstyEmitter(emitter)
	}
	return listValue, nil
}

func (g *generator) emitListMethodCall(call *ast.CallExpr) (value, bool, error) {
	field, elemTyp, found := g.listMethodInfo(call)
	if !found {
		return value{}, false, nil
	}
	if field.Name != "len" {
		return value{}, false, nil
	}
	if len(call.Args) != 0 {
		return value{}, true, unsupported("call", "list.len requires no arguments")
	}
	base, err := g.emitExpr(field.X)
	if err != nil {
		return value{}, true, err
	}
	if base.typ != "ptr" || elemTyp == "" {
		return value{}, true, unsupportedf("type-system", "list receiver type %s", base.typ)
	}
	g.declareRuntimeSymbol(listRuntimeLenSymbol(), "i64", []paramInfo{{typ: "ptr"}})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "i64", listRuntimeLenSymbol(), []*LlvmValue{toOstyValue(base)})
	g.takeOstyEmitter(emitter)
	return fromOstyValue(out), true, nil
}

func (g *generator) emitListMethodCallStmt(call *ast.CallExpr) (bool, error) {
	field, elemTyp, found := g.listMethodInfo(call)
	if !found {
		return false, nil
	}
	if field.Name != "push" {
		return false, nil
	}
	if len(call.Args) != 1 || call.Args[0].Name != "" || call.Args[0].Value == nil {
		return true, unsupported("call", "list.push requires one positional argument")
	}
	g.pushScope()
	defer g.popScope()
	emitter := g.toOstyEmitter()
	g.emitGCSafepoint(emitter)
	g.takeOstyEmitter(emitter)
	base, err := g.emitExpr(field.X)
	if err != nil {
		return true, err
	}
	if base.typ != "ptr" || elemTyp == "" {
		return true, unsupportedf("type-system", "list receiver type %s", base.typ)
	}
	base = g.protectManagedTemporary("list.base", base)
	arg, err := g.emitExpr(call.Args[0].Value)
	if err != nil {
		return true, err
	}
	if arg.typ != elemTyp {
		return true, unsupportedf("type-system", "list.push arg type %s, want %s", arg.typ, elemTyp)
	}
	baseValue, err := g.loadIfPointer(base)
	if err != nil {
		return true, err
	}
	argValue, err := g.loadIfPointer(arg)
	if err != nil {
		return true, err
	}
	if g.usesAggregateListABI(elemTyp) {
		return true, g.emitListAggregatePush(baseValue, argValue)
	}
	if elemTyp != "ptr" && elemTyp != "i64" && elemTyp != "i1" && elemTyp != "double" {
		return true, unsupportedf("type-system", "list.push element type %s requires scalar tuples or primitive values", elemTyp)
	}
	pushSymbol := listRuntimePushSymbol(elemTyp)
	g.declareRuntimeSymbol(pushSymbol, "void", []paramInfo{{typ: "ptr"}, {typ: elemTyp}})
	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, fmt.Sprintf(
		"  call void @%s(%s)",
		pushSymbol,
		llvmCallArgs([]*LlvmValue{toOstyValue(baseValue), toOstyValue(argValue)}),
	))
	g.takeOstyEmitter(emitter)
	return true, nil
}

func (g *generator) emitListFor(stmt *ast.ForStmt, iterName, elemTyp string) error {
	g.pushScope()
	defer g.popScope()
	iterable, err := g.emitExpr(stmt.Iter)
	if err != nil {
		return err
	}
	if iterable.typ != "ptr" || elemTyp == "" {
		return unsupportedf("type-system", "for-in iterable type %s", iterable.typ)
	}
	useAggregateABI := g.usesAggregateListABI(elemTyp)
	if !useAggregateABI && elemTyp != "ptr" && elemTyp != "i64" && elemTyp != "i1" && elemTyp != "double" {
		return unsupportedf("type-system", "for-in element type %s requires scalar tuples or primitive values", elemTyp)
	}
	iterable = g.protectManagedTemporary("for.iter", iterable)
	g.declareRuntimeSymbol(listRuntimeLenSymbol(), "i64", []paramInfo{{typ: "ptr"}})
	iterableValue, err := g.loadIfPointer(iterable)
	if err != nil {
		return err
	}
	emitter := g.toOstyEmitter()
	lenValue := llvmCall(emitter, "i64", listRuntimeLenSymbol(), []*LlvmValue{toOstyValue(iterableValue)})
	loop := llvmRangeStart(emitter, iterName+"_idx", llvmIntLiteral(0), lenValue, false)
	g.takeOstyEmitter(emitter)
	g.enterBlock(loop.bodyLabel)
	continueLabel := g.nextNamedLabel("for.cont")
	g.pushLoop(loopContext{
		continueLabel: continueLabel,
		breakLabel:    loop.endLabel,
		scopeDepth:    len(g.locals),
	})
	scopeDepth := len(g.locals)
	g.pushScope()
	iterableValue, err = g.loadIfPointer(iterable)
	if err != nil {
		if len(g.locals) > scopeDepth {
			g.popScope()
		}
		g.popLoop()
		return err
	}
	indexValue := value{typ: "i64", ref: loop.current}
	if useAggregateABI {
		item, err := g.emitListAggregateGet(iterableValue, indexValue, elemTyp)
		if err != nil {
			g.popScope()
			return err
		}
		g.bindLocal(iterName, item)
	} else {
		getSymbol := listRuntimeGetSymbol(elemTyp)
		g.declareRuntimeSymbol(getSymbol, elemTyp, []paramInfo{{typ: "ptr"}, {typ: "i64"}})
		emitter = g.toOstyEmitter()
		item := llvmCall(emitter, elemTyp, getSymbol, []*LlvmValue{toOstyValue(iterableValue), llvmI64(loop.current)})
		g.takeOstyEmitter(emitter)
		loaded := fromOstyValue(item)
		loaded.rootPaths = g.rootPathsForType(elemTyp)
		g.bindLocal(iterName, loaded)
	}
	if err := g.emitBlock(stmt.Body.Stmts); err != nil {
		if len(g.locals) > scopeDepth {
			g.popScope()
		}
		g.popLoop()
		return err
	}
	if len(g.locals) > scopeDepth {
		g.popScope()
	}
	g.popLoop()
	if g.currentReachable {
		g.branchTo(continueLabel)
	}
	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", continueLabel))
	g.emitGCSafepoint(emitter)
	llvmRangeEnd(emitter, loop)
	g.takeOstyEmitter(emitter)
	g.enterBlock(loop.endLabel)
	return nil
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

func (g *generator) emitPayloadEnumMatchExprValue(scrutinee value, info *enumInfo, arms []*ast.MatchArm) (value, error) {
	if len(arms) == 0 {
		return value{}, unsupported("expression", "match with no arms")
	}
	first := arms[0]
	firstPattern, ok, err := g.matchPayloadEnumPattern(info, first.Pattern)
	if err != nil {
		return value{}, err
	}
	if !ok {
		return value{}, unsupportedf("expression", "first match arm must be an enum %q variant", info.name)
	}
	if len(arms) == 1 {
		g.pushScope()
		defer g.popScope()
		if err := g.bindPayloadEnumPattern(scrutinee, firstPattern); err != nil {
			return value{}, err
		}
		return g.emitMatchArmBodyValue(first.Body)
	}
	second := arms[1]
	var elseValue value
	var elsePred string
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

	if len(arms) > 2 {
		elseValue, err = g.emitPayloadEnumMatchExprValue(scrutinee, info, arms[1:])
		if err != nil {
			return value{}, err
		}
		elsePred = g.currentBlock
	} else {
		g.pushScope()
		if secondHasPattern {
			if err := g.bindPayloadEnumPattern(scrutinee, secondPattern); err != nil {
				g.popScope()
				return value{}, err
			}
		}
		elseValue, err = g.emitMatchArmBodyValue(second.Body)
		g.popScope()
		if err != nil {
			return value{}, err
		}
		elsePred = g.currentBlock
	}
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
	payloadValue := fromOstyValue(payload)
	payloadValue.listElemTyp = pattern.payloadListElemTyp
	payloadValue.gcManaged = pattern.payloadListElemTyp != ""
	g.bindNamedLocal(pattern.payloadName, payloadValue, false)
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
		out.payloadListElemTyp = variant.payloadListElemTyp
		switch arg := p.Args[0].(type) {
		case *ast.IdentPat:
			if !llvmIsIdent(arg.Name) {
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
	if v, found, err := g.emitTestingValueCall(call); found || err != nil {
		return v, err
	}
	if v, found, err := g.emitBuiltinResultConstructor(call); found || err != nil {
		return v, err
	}
	if v, found, err := g.emitEnumVariantCall(call); found || err != nil {
		return v, err
	}
	if v, found, err := g.emitListMethodCall(call); found || err != nil {
		return v, err
	}
	if v, found, err := g.emitRuntimeFFICall(call); found || err != nil {
		return v, err
	}
	sig, receiverExpr, found, err := g.userCallTarget(call)
	if err != nil {
		return value{}, err
	}
	if !found {
		if id, ok := call.Fn.(*ast.Ident); ok && id.Name == "println" {
			return value{}, unsupported("call", "println is only supported as a statement")
		}
		return value{}, unsupportedf("call", "call target %T", call.Fn)
	}
	if sig.ret == "" {
		return value{}, unsupportedf("call", "function %q has no return value", sig.name)
	}
	if sig.ret == "void" {
		return value{}, unsupportedf("call", "function %q has no return value", sig.name)
	}
	emitter := g.toOstyEmitter()
	g.emitGCSafepoint(emitter)
	g.takeOstyEmitter(emitter)
	g.pushScope()
	args, err := g.userCallArgs(sig, receiverExpr, call)
	if err != nil {
		g.popScope()
		return value{}, err
	}
	emitter = g.toOstyEmitter()
	out := llvmCall(emitter, sig.ret, sig.irName, args)
	g.takeOstyEmitter(emitter)
	g.popScope()
	ret := fromOstyValue(out)
	ret.listElemTyp = sig.retListElemTyp
	ret.gcManaged = sig.retListElemTyp != ""
	ret.rootPaths = g.rootPathsForType(sig.ret)
	return ret, nil
}

func (g *generator) emitTestingValueCall(call *ast.CallExpr) (value, bool, error) {
	method, ok := g.testingCallMethod(call)
	if !ok {
		return value{}, false, nil
	}
	switch method {
	case "expectOk":
		v, err := g.emitTestingExpect(call, false)
		return v, true, err
	case "expectError":
		v, err := g.emitTestingExpect(call, true)
		return v, true, err
	default:
		return value{}, false, nil
	}
}

func (g *generator) emitBuiltinResultConstructor(call *ast.CallExpr) (value, bool, error) {
	id, ok := call.Fn.(*ast.Ident)
	if !ok || (id.Name != "Ok" && id.Name != "Err") {
		return value{}, false, nil
	}
	info, ok := g.currentBuiltinResultType()
	if !ok {
		return value{}, true, unsupportedf("call", "%s requires a concrete Result<T, E> context", id.Name)
	}
	if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
		return value{}, true, unsupportedf("call", "%s requires one positional argument", id.Name)
	}
	payloadIndex := 1
	payloadType := info.okTyp
	tag := "0"
	if id.Name == "Err" {
		payloadIndex = 2
		payloadType = info.errTyp
		tag = "1"
	}
	payload, err := g.emitExpr(call.Args[0].Value)
	if err != nil {
		return value{}, true, err
	}
	if payload.typ != payloadType {
		return value{}, true, unsupportedf("type-system", "%s payload type %s, want %s", id.Name, payload.typ, payloadType)
	}
	emitter := g.toOstyEmitter()
	fields := []*LlvmValue{
		toOstyValue(value{typ: "i64", ref: tag}),
		toOstyValue(llvmZeroValue(info.okTyp)),
		toOstyValue(llvmZeroValue(info.errTyp)),
	}
	fields[payloadIndex] = toOstyValue(payload)
	out := llvmStructLiteral(emitter, info.typ, fields)
	g.takeOstyEmitter(emitter)
	result := fromOstyValue(out)
	result.rootPaths = g.rootPathsForType(result.typ)
	return result, true, nil
}

func (g *generator) currentBuiltinResultType() (builtinResultType, bool) {
	if info, ok := g.resultTypes[g.returnType]; ok {
		return info, true
	}
	if len(g.resultTypes) == 1 {
		for _, info := range g.resultTypes {
			return info, true
		}
	}
	return builtinResultType{}, false
}

func llvmZeroValue(typ string) value {
	ref := llvmZeroLiteral(typ)
	if typ != "ptr" && typ != "i64" && typ != "i1" && typ != "double" {
		ref = "zeroinitializer"
	}
	return value{typ: typ, ref: ref}
}

func (g *generator) emitRuntimeFFICall(call *ast.CallExpr) (value, bool, error) {
	fn, found, err := g.runtimeFFICallTarget(call)
	if !found || err != nil {
		return value{}, found, err
	}
	if fn.ret == "void" {
		return value{}, true, unsupportedf("call", "runtime FFI %s.%s has no return value", fn.path, fn.sourceName)
	}
	emitter := g.toOstyEmitter()
	g.emitGCSafepoint(emitter)
	g.takeOstyEmitter(emitter)
	g.pushScope()
	args, err := g.runtimeFFICallArgs(fn, call.Args)
	if err != nil {
		g.popScope()
		return value{}, true, err
	}
	g.declareRuntimeFFI(fn)
	emitter = g.toOstyEmitter()
	out := llvmCall(emitter, fn.ret, fn.symbol, args)
	g.takeOstyEmitter(emitter)
	g.popScope()
	ret := fromOstyValue(out)
	ret.listElemTyp = fn.listElemTyp
	ret.gcManaged = fn.listElemTyp != ""
	ret.rootPaths = g.rootPathsForType(fn.ret)
	return ret, true, nil
}

func (g *generator) emitRuntimeFFICallStmt(call *ast.CallExpr) (bool, error) {
	fn, found, err := g.runtimeFFICallTarget(call)
	if !found || err != nil {
		return found, err
	}
	emitter := g.toOstyEmitter()
	g.emitGCSafepoint(emitter)
	g.takeOstyEmitter(emitter)
	g.pushScope()
	args, err := g.runtimeFFICallArgs(fn, call.Args)
	if err != nil {
		g.popScope()
		return true, err
	}
	g.declareRuntimeFFI(fn)
	if fn.ret == "void" {
		g.body = append(g.body, fmt.Sprintf("  call void @%s(%s)", fn.symbol, llvmCallArgs(args)))
		g.popScope()
		return true, nil
	}
	emitter = g.toOstyEmitter()
	llvmCall(emitter, fn.ret, fn.symbol, args)
	g.takeOstyEmitter(emitter)
	g.popScope()
	return true, nil
}

func (g *generator) emitUserCallStmt(call *ast.CallExpr) (bool, error) {
	sig, receiverExpr, found, err := g.userCallTarget(call)
	if err != nil {
		return true, err
	}
	if !found {
		return false, nil
	}
	emitter := g.toOstyEmitter()
	g.emitGCSafepoint(emitter)
	g.takeOstyEmitter(emitter)
	g.pushScope()
	args, err := g.userCallArgs(sig, receiverExpr, call)
	if err != nil {
		g.popScope()
		return true, err
	}
	emitter = g.toOstyEmitter()
	if sig.ret == "void" {
		emitter.body = append(emitter.body, fmt.Sprintf("  call void @%s(%s)", sig.irName, llvmCallArgs(args)))
	} else {
		llvmCall(emitter, sig.ret, sig.irName, args)
	}
	g.takeOstyEmitter(emitter)
	g.popScope()
	return true, nil
}

func (g *generator) userCallArgs(sig *fnSig, receiverExpr ast.Expr, call *ast.CallExpr) ([]*LlvmValue, error) {
	expectedArgs := len(sig.params)
	if receiverExpr != nil {
		expectedArgs--
	}
	if len(call.Args) != expectedArgs {
		return nil, unsupportedf("call", "function %q argument count", sig.name)
	}
	args := make([]*LlvmValue, 0, len(sig.params))
	paramIndex := 0
	if receiverExpr != nil {
		receiver, err := g.userCallReceiverArg(sig, sig.params[0], receiverExpr)
		if err != nil {
			return nil, err
		}
		args = append(args, receiver)
		paramIndex = 1
	}
	values := make([]value, 0, len(call.Args))
	for i, arg := range call.Args {
		if arg.Name != "" || arg.Value == nil {
			return nil, unsupportedf("call", "function %q requires positional arguments", sig.name)
		}
		param := sig.params[paramIndex+i]
		v, err := g.emitExprWithListHint(arg.Value, param.listElemTyp)
		if err != nil {
			return nil, err
		}
		if v.typ != param.typ {
			return nil, unsupportedf("type-system", "function %q arg %d type %s, want %s", sig.name, i+1, v.typ, param.typ)
		}
		values = append(values, g.protectManagedTemporary(sig.name+".arg", v))
	}
	for _, v := range values {
		loaded, err := g.loadIfPointer(v)
		if err != nil {
			return nil, err
		}
		args = append(args, toOstyValue(loaded))
	}
	return args, nil
}

func (g *generator) userCallReceiverArg(sig *fnSig, param paramInfo, receiverExpr ast.Expr) (*LlvmValue, error) {
	if param.byRef {
		id, ok := receiverExpr.(*ast.Ident)
		if !ok {
			return nil, unsupportedf("call", "mut receiver for %q must be a local binding", sig.name)
		}
		slot, ok := g.lookupLocal(id.Name)
		if !ok {
			return nil, unsupportedf("name", "unknown receiver binding %q", id.Name)
		}
		if !slot.ptr || slot.typ != param.typ {
			return nil, unsupportedf("type-system", "receiver for %q must be mutable %s", sig.name, param.typ)
		}
		return &LlvmValue{typ: "ptr", name: slot.ref}, nil
	}
	v, err := g.emitExpr(receiverExpr)
	if err != nil {
		return nil, err
	}
	if v.typ != param.typ {
		return nil, unsupportedf("type-system", "receiver for %q type %s, want %s", sig.name, v.typ, param.typ)
	}
	protected := g.protectManagedTemporary(sig.name+".self", v)
	loaded, err := g.loadIfPointer(protected)
	if err != nil {
		return nil, err
	}
	return toOstyValue(loaded), nil
}

func (g *generator) userCallTarget(call *ast.CallExpr) (*fnSig, ast.Expr, bool, error) {
	if call == nil {
		return nil, nil, false, nil
	}
	switch fn := call.Fn.(type) {
	case *ast.Ident:
		sig := g.functions[fn.Name]
		if sig == nil {
			return nil, nil, false, nil
		}
		return sig, nil, true, nil
	case *ast.FieldExpr:
		if fn.IsOptional {
			return nil, nil, false, unsupported("call", "optional method calls are not supported")
		}
		baseTyp, _, ok := g.staticExprType(fn.X)
		if !ok {
			return nil, nil, false, nil
		}
		methods := g.methods[baseTyp]
		if methods == nil {
			return nil, nil, false, nil
		}
		sig := methods[fn.Name]
		if sig == nil {
			return nil, nil, false, nil
		}
		return sig, fn.X, true, nil
	default:
		return nil, nil, false, nil
	}
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
	values := make([]value, 0, len(callArgs))
	for i, arg := range callArgs {
		if arg == nil || arg.Name != "" || arg.Value == nil {
			return nil, unsupportedf("call", "runtime FFI %s.%s requires positional arguments", fn.path, fn.sourceName)
		}
		param := fn.params[i]
		v, err := g.emitExprWithListHint(arg.Value, param.listElemTyp)
		if err != nil {
			return nil, err
		}
		if v.typ != param.typ {
			return nil, unsupportedf("type-system", "runtime FFI %s.%s arg %d type %s, want %s", fn.path, fn.sourceName, i+1, v.typ, param.typ)
		}
		values = append(values, g.protectManagedTemporary(fn.symbol+".arg", v))
	}
	args := make([]*LlvmValue, 0, len(values))
	for _, v := range values {
		loaded, err := g.loadIfPointer(v)
		if err != nil {
			return nil, err
		}
		args = append(args, toOstyValue(loaded))
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
	typeDefs := make([]string, 0, len(g.structs)+len(g.enumsByType)+len(g.tupleTypes)+len(g.resultTypes))
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
	if len(g.tupleTypes) != 0 {
		names := make([]string, 0, len(g.tupleTypes))
		for name := range g.tupleTypes {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			info := g.tupleTypes[name]
			typeDefs = append(typeDefs, llvmStructTypeDef(strings.TrimPrefix(info.typ, "%"), info.elems))
		}
	}
	if len(g.resultTypes) != 0 {
		names := make([]string, 0, len(g.resultTypes))
		for name := range g.resultTypes {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			info := g.resultTypes[name]
			typeDefs = append(typeDefs, llvmStructTypeDef(strings.TrimPrefix(info.typ, "%"), []string{"i64", info.okTyp, info.errTyp}))
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

func toLLVMParams(params []paramInfo) []*LlvmParam {
	out := make([]*LlvmParam, 0, len(params))
	for _, p := range params {
		out = append(out, llvmParam(p.name, llvmParamIRType(p)))
	}
	return out
}

func (g *generator) enterBlock(label string) {
	g.currentBlock = label
	g.currentReachable = true
}

func (g *generator) leaveBlock() {
	g.currentBlock = ""
	g.currentReachable = false
}

func (g *generator) branchTo(label string) {
	emitter := g.toOstyEmitter()
	emitter.body = append(emitter.body, fmt.Sprintf("  br label %%%s", label))
	g.takeOstyEmitter(emitter)
	g.leaveBlock()
}

func (g *generator) nextNamedLabel(prefix string) string {
	emitter := g.toOstyEmitter()
	label := llvmNextLabel(emitter, prefix)
	g.takeOstyEmitter(emitter)
	return label
}

func (g *generator) emitScopedStmtBlock(stmts []ast.Stmt) error {
	scopeDepth := len(g.locals)
	g.pushScope()
	if err := g.emitBlock(stmts); err != nil {
		if len(g.locals) > scopeDepth {
			g.popScope()
		}
		return err
	}
	if len(g.locals) > scopeDepth {
		g.popScope()
	}
	return nil
}

func (g *generator) captureScopeState() scopeState {
	locals := append([]map[string]value(nil), g.locals...)
	gcRootSlots := append([]value(nil), g.gcRootSlots...)
	gcRootMarks := append([]int(nil), g.gcRootMarks...)
	return scopeState{
		locals:      locals,
		gcRootSlots: gcRootSlots,
		gcRootMarks: gcRootMarks,
	}
}

func (g *generator) restoreScopeState(state scopeState) {
	g.locals = append([]map[string]value(nil), state.locals...)
	g.gcRootSlots = append([]value(nil), state.gcRootSlots...)
	g.gcRootMarks = append([]int(nil), state.gcRootMarks...)
}

func (g *generator) pushScope() {
	g.locals = append(g.locals, map[string]value{})
	g.gcRootMarks = append(g.gcRootMarks, len(g.gcRootSlots))
}

func (g *generator) popScope() {
	mark := 0
	if len(g.gcRootMarks) != 0 {
		mark = g.gcRootMarks[len(g.gcRootMarks)-1]
		g.gcRootMarks = g.gcRootMarks[:len(g.gcRootMarks)-1]
	}
	if mark < len(g.gcRootSlots) {
		if g.currentReachable {
			emitter := g.toOstyEmitter()
			for i := len(g.gcRootSlots) - 1; i >= mark; i-- {
				llvmGcRootRelease(emitter, toOstyValue(g.gcRootSlots[i]))
			}
			g.takeOstyEmitter(emitter)
		}
		g.gcRootSlots = g.gcRootSlots[:mark]
	}
	g.locals = g.locals[:len(g.locals)-1]
}

func (g *generator) bindNamedLocal(name string, v value, mutable bool) {
	if mutable || (v.typ == "ptr" && v.gcManaged) || len(v.rootPaths) != 0 {
		emitter := g.toOstyEmitter()
		slot := llvmMutableLetSlot(emitter, name, toOstyValue(v))
		slotValue := fromOstyValue(slot)
		slotValue.gcManaged = v.gcManaged
		slotValue.listElemTyp = v.listElemTyp
		slotValue.mutable = mutable
		slotValue.rootPaths = cloneRootPaths(v.rootPaths)
		g.bindGCRootIfManagedPointer(emitter, slotValue)
		g.takeOstyEmitter(emitter)
		g.bindLocal(name, slotValue)
		return
	}
	v.mutable = false
	g.bindLocal(name, v)
}

type gcSafepointRoot struct {
	slot value
	path []int
}

func cloneRootPaths(paths [][]int) [][]int {
	if len(paths) == 0 {
		return nil
	}
	out := make([][]int, 0, len(paths))
	for _, path := range paths {
		next := append([]int(nil), path...)
		out = append(out, next)
	}
	return out
}

func prependRootIndex(index int, paths [][]int) [][]int {
	if len(paths) == 0 {
		return nil
	}
	out := make([][]int, 0, len(paths))
	for _, path := range paths {
		next := make([]int, 0, len(path)+1)
		next = append(next, index)
		next = append(next, path...)
		out = append(out, next)
	}
	return out
}

func (g *generator) rootPathsForType(typ string) [][]int {
	return g.rootPathsForTypeSeen(typ, map[string]bool{})
}

func (g *generator) rootPathsForTypeSeen(typ string, seen map[string]bool) [][]int {
	if typ == "" || typ == "ptr" || typ == "i64" || typ == "i1" || typ == "double" {
		return nil
	}
	if seen[typ] {
		return nil
	}
	if info := g.structsByType[typ]; info != nil {
		seen[typ] = true
		var out [][]int
		for _, field := range info.fields {
			if field.listElemTyp != "" {
				out = append(out, []int{field.index})
				continue
			}
			out = append(out, prependRootIndex(field.index, g.rootPathsForTypeSeen(field.typ, seen))...)
		}
		delete(seen, typ)
		return out
	}
	if info := g.enumsByType[typ]; info != nil && info.hasPayload {
		seen[typ] = true
		defer delete(seen, typ)
		if info.payloadTyp == "ptr" {
			return [][]int{{1}}
		}
		return prependRootIndex(1, g.rootPathsForTypeSeen(info.payloadTyp, seen))
	}
	if info, ok := g.resultTypes[typ]; ok {
		seen[typ] = true
		defer delete(seen, typ)
		var out [][]int
		if info.okTyp == "ptr" {
			out = append(out, []int{1})
		} else {
			out = append(out, prependRootIndex(1, g.rootPathsForTypeSeen(info.okTyp, seen))...)
		}
		if info.errTyp == "ptr" {
			out = append(out, []int{2})
		} else {
			out = append(out, prependRootIndex(2, g.rootPathsForTypeSeen(info.errTyp, seen))...)
		}
		return out
	}
	if info, ok := g.tupleTypes[typ]; ok {
		seen[typ] = true
		defer delete(seen, typ)
		var out [][]int
		for i, elemTyp := range info.elems {
			if i < len(info.elemListElemTyps) && info.elemListElemTyps[i] != "" {
				out = append(out, []int{i})
				continue
			}
			out = append(out, prependRootIndex(i, g.rootPathsForTypeSeen(elemTyp, seen))...)
		}
		return out
	}
	return nil
}

func (g *generator) visibleSafepointRoots() []gcSafepointRoot {
	seen := map[string]struct{}{}
	out := []gcSafepointRoot{}
	for i := len(g.locals) - 1; i >= 0; i-- {
		names := make([]string, 0, len(g.locals[i]))
		for name := range g.locals[i] {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if _, exists := seen[name]; exists {
				continue
			}
			seen[name] = struct{}{}
			slot := g.locals[i][name]
			if !slot.ptr {
				continue
			}
			if slot.typ == "ptr" && slot.gcManaged {
				out = append(out, gcSafepointRoot{slot: slot})
			}
			for _, path := range slot.rootPaths {
				out = append(out, gcSafepointRoot{
					slot: slot,
					path: append([]int(nil), path...),
				})
			}
		}
	}
	return out
}

func (g *generator) safepointRootAddress(emitter *LlvmEmitter, root gcSafepointRoot) string {
	if len(root.path) == 0 {
		return root.slot.ref
	}
	addr := root.slot.ref
	currentType := root.slot.typ
	for _, index := range root.path {
		fieldPtr := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, fmt.Sprintf(
			"  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d",
			fieldPtr,
			currentType,
			addr,
			index,
		))
		nextType, ok := g.aggregateFieldType(currentType, index)
		if !ok {
			return addr
		}
		addr = fieldPtr
		currentType = nextType
	}
	return addr
}

func (g *generator) aggregateFieldType(typ string, index int) (string, bool) {
	if info := g.structsByType[typ]; info != nil {
		for _, field := range info.fields {
			if field.index == index {
				return field.typ, true
			}
		}
		return "", false
	}
	if info := g.enumsByType[typ]; info != nil && info.hasPayload {
		switch index {
		case 0:
			return "i64", true
		case 1:
			return info.payloadTyp, true
		}
	}
	if info, ok := g.tupleTypes[typ]; ok {
		if index < 0 || index >= len(info.elems) {
			return "", false
		}
		return info.elems[index], true
	}
	return "", false
}

func (g *generator) bindLocal(name string, v value) {
	g.locals[len(g.locals)-1][name] = v
}

func (g *generator) pushLoop(loop loopContext) {
	g.loopStack = append(g.loopStack, loop)
}

func (g *generator) popLoop() {
	if len(g.loopStack) == 0 {
		return
	}
	g.loopStack = g.loopStack[:len(g.loopStack)-1]
}

func (g *generator) currentLoop() (loopContext, bool) {
	if len(g.loopStack) == 0 {
		return loopContext{}, false
	}
	return g.loopStack[len(g.loopStack)-1], true
}

func (g *generator) unwindScopesTo(scopeDepth int) {
	for len(g.locals) > scopeDepth {
		g.popScope()
	}
}

func (g *generator) emitBreak() error {
	loop, ok := g.currentLoop()
	if !ok {
		return unsupported("control-flow", "break outside of loop")
	}
	g.unwindScopesTo(loop.scopeDepth)
	g.branchTo(loop.breakLabel)
	return nil
}

func (g *generator) emitContinue() error {
	loop, ok := g.currentLoop()
	if !ok {
		return unsupported("control-flow", "continue outside of loop")
	}
	g.unwindScopesTo(loop.scopeDepth)
	g.branchTo(loop.continueLabel)
	return nil
}

func (g *generator) nextHiddenLocalName(prefix string) string {
	name := fmt.Sprintf("$%s.%d", prefix, g.hiddenLocalID)
	g.hiddenLocalID++
	return name
}

func (g *generator) needsSafepointProtection(v value) bool {
	if v.ptr {
		return false
	}
	return (v.typ == "ptr" && v.gcManaged) || len(v.rootPaths) != 0
}

func (g *generator) protectManagedTemporary(prefix string, v value) value {
	if !g.needsSafepointProtection(v) {
		return v
	}
	name := g.nextHiddenLocalName(prefix)
	g.bindNamedLocal(name, v, false)
	protected, ok := g.lookupLocal(name)
	if !ok {
		return v
	}
	return protected
}

func (g *generator) lookupLocal(name string) (value, bool) {
	for i := len(g.locals) - 1; i >= 0; i-- {
		if v, ok := g.locals[i][name]; ok {
			return v, true
		}
	}
	return value{}, false
}

func (g *generator) bindLetPattern(pattern ast.Pattern, v value, mutable bool) error {
	switch p := pattern.(type) {
	case nil:
		return unsupported("statement", "let requires a pattern")
	case *ast.WildcardPat:
		if mutable {
			return unsupported("statement", "wildcard let patterns cannot be mutable")
		}
		return nil
	case *ast.IdentPat:
		if p.Name == "" {
			return unsupported("statement", "empty let binding name")
		}
		if !llvmIsIdent(p.Name) {
			return unsupportedf("name", "let name %q", p.Name)
		}
		g.bindNamedLocal(p.Name, v, mutable)
		return nil
	case *ast.TuplePat:
		if mutable {
			return unsupported("statement", "tuple let patterns cannot be mutable yet")
		}
		info, ok := g.tupleTypes[v.typ]
		if !ok {
			return unsupportedf("type-system", "tuple pattern on %s", v.typ)
		}
		if len(p.Elems) != len(info.elems) {
			return unsupportedf("statement", "tuple pattern arity %d, value %d", len(p.Elems), len(info.elems))
		}
		for i, elemPat := range p.Elems {
			elemValue, err := g.extractTupleElement(v, info, i)
			if err != nil {
				return err
			}
			if err := g.bindLetPattern(elemPat, elemValue, false); err != nil {
				return err
			}
		}
		return nil
	default:
		return unsupported("statement", "only identifier, wildcard, and tuple let patterns are supported")
	}
}

func (g *generator) extractTupleElement(tuple value, info tupleTypeInfo, index int) (value, error) {
	if index < 0 || index >= len(info.elems) {
		return value{}, unsupportedf("expression", "tuple index %d out of range", index)
	}
	emitter := g.toOstyEmitter()
	out := llvmExtractValue(emitter, toOstyValue(tuple), info.elems[index], index)
	g.takeOstyEmitter(emitter)
	elem := fromOstyValue(out)
	if index < len(info.elemListElemTyps) && info.elemListElemTyps[index] != "" {
		elem.listElemTyp = info.elemListElemTyps[index]
		elem.gcManaged = true
	}
	elem.rootPaths = g.rootPathsForType(info.elems[index])
	return elem, nil
}

func identPatternName(p ast.Pattern) (string, error) {
	id, ok := p.(*ast.IdentPat)
	if !ok || id.Name == "" {
		return "", unsupported("statement", "only identifier let patterns are supported")
	}
	if !llvmIsIdent(id.Name) {
		return "", unsupportedf("name", "let name %q", id.Name)
	}
	return id.Name, nil
}

func (g *generator) staticExprType(expr ast.Expr) (string, string, bool) {
	switch e := expr.(type) {
	case *ast.IntLit:
		return "i64", "", true
	case *ast.FloatLit:
		return "double", "", true
	case *ast.BoolLit:
		return "i1", "", true
	case *ast.StringLit:
		return "ptr", "", true
	case *ast.Ident:
		if v, ok := g.lookupLocal(e.Name); ok {
			return v.typ, v.listElemTyp, true
		}
	case *ast.ParenExpr:
		return g.staticExprType(e.X)
	case *ast.TupleExpr:
		elemTypes := make([]string, 0, len(e.Elems))
		for _, elem := range e.Elems {
			typ, _, ok := g.staticExprType(elem)
			if !ok {
				return "", "", false
			}
			elemTypes = append(elemTypes, typ)
		}
		return llvmTupleTypeName(elemTypes), "", true
	case *ast.ListExpr:
		if elemTyp, ok := g.staticListLiteralElementType(e); ok {
			return "ptr", elemTyp, true
		}
	case *ast.CallExpr:
		if retTyp, found, ok := g.staticListMethodResult(e); found {
			return retTyp, "", ok
		}
		if id, ok := e.Fn.(*ast.Ident); ok {
			if sig := g.functions[id.Name]; sig != nil && sig.ret != "" && sig.ret != "void" {
				return sig.ret, sig.retListElemTyp, true
			}
		}
		if field, ok := e.Fn.(*ast.FieldExpr); ok && !field.IsOptional {
			if baseTyp, _, ok := g.staticExprType(field.X); ok {
				if methods := g.methods[baseTyp]; methods != nil {
					if sig := methods[field.Name]; sig != nil && sig.ret != "" && sig.ret != "void" {
						return sig.ret, sig.retListElemTyp, true
					}
				}
			}
		}
		if fn, found, err := g.runtimeFFICallTarget(e); found && err == nil && fn.ret != "" && fn.ret != "void" {
			return fn.ret, fn.listElemTyp, true
		}
	case *ast.FieldExpr:
		if e.IsOptional {
			return "", "", false
		}
		baseTyp, _, ok := g.staticExprType(e.X)
		if !ok {
			return "", "", false
		}
		if info := g.structsByType[baseTyp]; info != nil {
			if field, ok := info.byName[e.Name]; ok {
				return field.typ, field.listElemTyp, true
			}
		}
	}
	return "", "", false
}

func (g *generator) staticListLiteralElementType(expr *ast.ListExpr) (string, bool) {
	if expr == nil || len(expr.Elems) == 0 {
		return "", false
	}
	elemTyp, _, ok := g.staticExprType(expr.Elems[0])
	if !ok {
		return "", false
	}
	for _, elem := range expr.Elems[1:] {
		typ, _, ok := g.staticExprType(elem)
		if !ok || typ != elemTyp {
			return "", false
		}
	}
	return elemTyp, true
}

func (g *generator) staticListMethodResult(call *ast.CallExpr) (string, bool, bool) {
	field, _, found := g.listMethodInfo(call)
	if !found {
		return "", false, false
	}
	switch field.Name {
	case "len":
		return "i64", true, true
	default:
		return "", true, false
	}
}

func (g *generator) listMethodInfo(call *ast.CallExpr) (*ast.FieldExpr, string, bool) {
	if call == nil {
		return nil, "", false
	}
	field, ok := call.Fn.(*ast.FieldExpr)
	if !ok || field.IsOptional {
		return nil, "", false
	}
	switch field.Name {
	case "len", "push":
	default:
		return nil, "", false
	}
	baseTyp, elemTyp, ok := g.staticExprType(field.X)
	if !ok || baseTyp != "ptr" || elemTyp == "" {
		return nil, "", false
	}
	return field, elemTyp, true
}

func listRuntimeNewSymbol() string {
	return "osty_rt_list_new"
}

func listRuntimeLenSymbol() string {
	return "osty_rt_list_len"
}

func listRuntimePushBytesSymbol() string {
	return "osty_rt_list_push_bytes_v1"
}

func listRuntimeGetBytesSymbol() string {
	return "osty_rt_list_get_bytes_v1"
}

func listRuntimePushSymbol(elemTyp string) string {
	return "osty_rt_list_push_" + listRuntimeSymbolSuffix(elemTyp)
}

func listRuntimeGetSymbol(elemTyp string) string {
	return "osty_rt_list_get_" + listRuntimeSymbolSuffix(elemTyp)
}

func listRuntimeSymbolSuffix(typ string) string {
	switch typ {
	case "i64", "i1", "ptr":
		return typ
	case "double":
		return "f64"
	}
	var b strings.Builder
	for i := 0; i < len(typ); i++ {
		c := typ[i]
		if c == '_' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9') {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('_')
	}
	if b.Len() == 0 {
		return "ptr"
	}
	return b.String()
}

func llvmType(t ast.Type, structs map[string]*structInfo, enums map[string]*enumInfo) (string, error) {
	switch tt := t.(type) {
	case *ast.NamedType:
		if len(tt.Path) == 1 && tt.Path[0] == "Result" && len(tt.Args) == 2 {
			okTyp, err := llvmType(tt.Args[0], structs, enums)
			if err != nil {
				return "", err
			}
			errTyp, err := llvmType(tt.Args[1], structs, enums)
			if err != nil {
				return "", err
			}
			return llvmResultTypeName(okTyp, errTyp), nil
		}
		name := ""
		structType := ""
		enumType := ""
		if len(tt.Path) == 1 {
			name = tt.Path[0]
			if info := structs[name]; info != nil {
				structType = info.typ
			}
			if info := enums[name]; info != nil {
				enumType = info.typ
			}
		}
		if typ := llvmNamedType(name, len(tt.Path), len(tt.Args), structType, enumType); typ != "" {
			return typ, nil
		}
		return "", unsupportedf("type-system", "type %q", strings.Join(tt.Path, "."))
	case *ast.OptionalType, *ast.FnType:
		return "ptr", nil
	case *ast.TupleType:
		elemTypes := make([]string, 0, len(tt.Elems))
		for _, elem := range tt.Elems {
			elemTyp, err := llvmType(elem, structs, enums)
			if err != nil {
				return "", err
			}
			elemTypes = append(elemTypes, elemTyp)
		}
		return llvmTupleTypeName(elemTypes), nil
	default:
		return "", unsupportedf("type-system", "type %T", t)
	}
}

func llvmMethodOwnerType(ownerName string, structs map[string]*structInfo, enums map[string]*enumInfo) (string, bool) {
	if info := structs[ownerName]; info != nil {
		return info.typ, true
	}
	if info := enums[ownerName]; info != nil {
		return info.typ, true
	}
	return "", false
}

func llvmMethodIRName(ownerName, methodName string) string {
	return sanitizeLLVMName(ownerName) + "__" + sanitizeLLVMName(methodName)
}

func llvmMethodReceiverIRType(ownerType string, mutable bool) string {
	if mutable {
		return "ptr"
	}
	return ownerType
}

func llvmParamIRType(param paramInfo) string {
	if param.irTyp != "" {
		return param.irTyp
	}
	return param.typ
}

func sanitizeLLVMName(name string) string {
	if name == "" {
		return "anon"
	}
	var b strings.Builder
	for i, c := range name {
		if c == '_' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || (i > 0 && '0' <= c && c <= '9') {
			b.WriteRune(c)
			continue
		}
		if i == 0 && '0' <= c && c <= '9' {
			b.WriteByte('_')
			b.WriteRune(c)
			continue
		}
		b.WriteByte('_')
	}
	if b.Len() == 0 {
		return "anon"
	}
	return b.String()
}
