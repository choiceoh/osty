package check

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/canonical"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/sourcemap"
	"github.com/osty/osty/internal/token"
	"github.com/osty/osty/internal/toolchain"
	"github.com/osty/osty/internal/types"
)

const nativeCheckerEnv = "OSTY_NATIVE_CHECKER_BIN"

// The checker targets an Osty-native request/response boundary. The preferred
// path uses an external executable, and the default host implementation falls
// back to the embedded selfhost checker when that binary is unavailable.
type nativeChecker interface {
	CheckSourceStructured([]byte) (nativeCheckResult, error)
}

type nativePackageChecker interface {
	CheckPackageStructured(selfhost.PackageCheckInput) (nativeCheckResult, error)
}

type nativeCheckRequest struct {
	Source string `json:"source"`
}

type nativeCheckSummary struct {
	Assignments     int                       `json:"assignments"`
	Accepted        int                       `json:"accepted"`
	Errors          int                       `json:"errors"`
	ErrorsByContext map[string]int            `json:"errorsByContext,omitempty"`
	ErrorDetails    map[string]map[string]int `json:"errorDetails,omitempty"`
}

type nativeCheckedNode struct {
	Node     int    `json:"node"`
	Kind     string `json:"kind"`
	TypeName string `json:"typeName"`
	Start    int    `json:"start"`
	End      int    `json:"end"`
}

type nativeCheckedBinding struct {
	Node     int    `json:"node"`
	Name     string `json:"name"`
	TypeName string `json:"typeName"`
	Mutable  bool   `json:"mutable"`
	Start    int    `json:"start"`
	End      int    `json:"end"`
}

type nativeCheckedSymbol struct {
	Node     int    `json:"node"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Owner    string `json:"owner"`
	TypeName string `json:"typeName"`
	Start    int    `json:"start"`
	End      int    `json:"end"`
}

type nativeCheckInstantiation struct {
	Node       int      `json:"node"`
	Callee     string   `json:"callee"`
	TypeArgs   []string `json:"typeArgs"`
	ResultType string   `json:"resultType"`
	Start      int      `json:"start"`
	End        int      `json:"end"`
}

type nativeCheckResult struct {
	Summary        nativeCheckSummary         `json:"summary"`
	TypedNodes     []nativeCheckedNode        `json:"typedNodes"`
	Bindings       []nativeCheckedBinding     `json:"bindings"`
	Symbols        []nativeCheckedSymbol      `json:"symbols"`
	Instantiations []nativeCheckInstantiation `json:"instantiations"`
}

type nativeCheckerExec struct {
	path string
}

func (e nativeCheckerExec) CheckSourceStructured(src []byte) (nativeCheckResult, error) {
	req := nativeCheckRequest{Source: string(src)}
	payload, err := json.Marshal(req)
	if err != nil {
		return nativeCheckResult{}, fmt.Errorf("marshal native checker request: %w", err)
	}
	cmd := exec.Command(e.path)
	cmd.Stdin = bytes.NewReader(payload)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = "<no output>"
		}
		return nativeCheckResult{}, fmt.Errorf("exec %s: %w (%s)", e.path, err, msg)
	}
	var checked nativeCheckResult
	if err := json.Unmarshal(out, &checked); err != nil {
		return nativeCheckResult{}, fmt.Errorf("decode native checker response: %w", err)
	}
	return checked, nil
}

type embeddedNativeChecker struct{}

func (embeddedNativeChecker) CheckSourceStructured(src []byte) (nativeCheckResult, error) {
	checked := selfhost.CheckSourceStructured(src)
	return adaptEmbeddedCheckResult(checked), nil
}

func (embeddedNativeChecker) CheckPackageStructured(input selfhost.PackageCheckInput) (nativeCheckResult, error) {
	checked, err := selfhost.CheckPackageStructured(input)
	if err != nil {
		return nativeCheckResult{}, err
	}
	return adaptEmbeddedCheckResult(checked), nil
}

func adaptEmbeddedCheckResult(checked selfhost.CheckResult) nativeCheckResult {
	result := nativeCheckResult{
		Summary: nativeCheckSummary{
			Assignments:     checked.Summary.Assignments,
			Accepted:        checked.Summary.Accepted,
			Errors:          checked.Summary.Errors,
			ErrorsByContext: cloneStringIntMap(checked.Summary.ErrorsByContext),
			ErrorDetails:    cloneErrorDetailMap(checked.Summary.ErrorDetails),
		},
		TypedNodes:     make([]nativeCheckedNode, 0, len(checked.TypedNodes)),
		Bindings:       make([]nativeCheckedBinding, 0, len(checked.Bindings)),
		Symbols:        make([]nativeCheckedSymbol, 0, len(checked.Symbols)),
		Instantiations: make([]nativeCheckInstantiation, 0, len(checked.Instantiations)),
	}
	for _, node := range checked.TypedNodes {
		result.TypedNodes = append(result.TypedNodes, nativeCheckedNode{
			Node:     node.Node,
			Kind:     node.Kind,
			TypeName: node.TypeName,
			Start:    node.Start,
			End:      node.End,
		})
	}
	for _, binding := range checked.Bindings {
		result.Bindings = append(result.Bindings, nativeCheckedBinding{
			Node:     binding.Node,
			Name:     binding.Name,
			TypeName: binding.TypeName,
			Mutable:  binding.Mutable,
			Start:    binding.Start,
			End:      binding.End,
		})
	}
	for _, symbol := range checked.Symbols {
		result.Symbols = append(result.Symbols, nativeCheckedSymbol{
			Node:     symbol.Node,
			Kind:     symbol.Kind,
			Name:     symbol.Name,
			Owner:    symbol.Owner,
			TypeName: symbol.TypeName,
			Start:    symbol.Start,
			End:      symbol.End,
		})
	}
	for _, inst := range checked.Instantiations {
		result.Instantiations = append(result.Instantiations, nativeCheckInstantiation{
			Node:       inst.Node,
			Callee:     inst.Callee,
			TypeArgs:   append([]string(nil), inst.TypeArgs...),
			ResultType: inst.ResultType,
			Start:      inst.Start,
			End:        inst.End,
		})
	}
	return result
}

var nativeCheckerFactory = defaultNativeChecker
var ensureManagedNativeChecker = func() (string, error) {
	return toolchain.EnsureNativeChecker(".")
}

func defaultNativeChecker() (nativeChecker, string) {
	path := strings.TrimSpace(os.Getenv(nativeCheckerEnv))
	if path != "" {
		resolved, err := exec.LookPath(path)
		if err != nil {
			return nil, fmt.Sprintf("%s=%q was not found", nativeCheckerEnv, path)
		}
		return nativeCheckerExec{path: resolved}, ""
	}
	managedPath, err := ensureManagedNativeChecker()
	if err != nil {
		return embeddedNativeChecker{}, fmt.Sprintf(
			"%s is not set and the managed toolchain checker could not be prepared: %v; falling back to the embedded checker",
			nativeCheckerEnv,
			err,
		)
	}
	return nativeCheckerExec{path: managedPath}, ""
}

type selfhostCheckedSource struct {
	source []byte
	files  []selfhostFileSegment
}

type selfhostFileSegment struct {
	file      *ast.File
	scope     *resolve.Scope
	refs      map[*ast.Ident]*resolve.Symbol
	base      int
	sourceMap *sourcemap.Map
}

func applyNativeFileResult(result *Result, file *ast.File, rr *resolve.Result, src []byte, stdlib resolve.StdlibProvider) {
	applySelfhostFileResult(result, file, rr, src, stdlib)
}

func applySelfhostFileResult(result *Result, file *ast.File, rr *resolve.Result, src []byte, stdlib resolve.StdlibProvider) {
	if result == nil {
		return
	}
	if len(src) == 0 {
		result.Diags = append(result.Diags, checkerUnavailableDiag(
			"file",
			"source bytes were not supplied to the native checker boundary",
		))
		return
	}
	runner, note := nativeCheckerFactory()
	if runner == nil {
		result.Diags = append(result.Diags, checkerUnavailableDiag(
			"file",
			"no Osty-native checker executable is configured",
			note,
		))
		return
	}
	checkedSrc := selfhostFileSource(file, rr, src, stdlib)
	if dump := os.Getenv("OSTY_NATIVE_CHECKER_SOURCE_DUMP"); dump != "" {
		_ = os.WriteFile(dump, checkedSrc.source, 0o644)
	}
	checked, err := runner.CheckSourceStructured(checkedSrc.source)
	if err != nil {
		result.Diags = append(result.Diags, checkerUnavailableDiag(
			"file",
			"the Osty-native checker executable failed",
			err.Error(),
		))
		return
	}
	result.Diags = nativeCheckerDiags(checkedSrc.source, checked)
	result.NativeCheckerTelemetry = nativeCheckerTelemetry(checked)
	overlaySelfhostResult(result, checkedSrc, checked)
}

func applyNativePackageResult(result *Result, pkg *resolve.Package, pr *resolve.PackageResult, ws *resolve.Workspace, stdlib resolve.StdlibProvider) {
	applySelfhostPackageResult(result, pkg, pr, ws, stdlib)
}

func applySelfhostPackageResult(result *Result, pkg *resolve.Package, _ *resolve.PackageResult, ws *resolve.Workspace, stdlib resolve.StdlibProvider) {
	if result == nil || pkg == nil {
		return
	}
	runner, note := nativeCheckerFactory()
	if runner == nil {
		result.Diags = append(result.Diags, checkerUnavailableDiag(
			"package",
			"no Osty-native checker executable is configured",
			note,
		))
		return
	}
	src := selfhostPackageSource(pkg, ws, stdlib)
	if dump := os.Getenv("OSTY_NATIVE_CHECKER_SOURCE_DUMP"); dump != "" {
		_ = os.WriteFile(dump, src.source, 0o644)
	}
	if len(src.source) == 0 {
		result.Diags = append(result.Diags, checkerUnavailableDiag(
			"package",
			"package source bytes were not available to the native checker boundary",
		))
		return
	}
	var (
		checked nativeCheckResult
		err     error
	)
	input := selfhostPackageCheckInput(pkg, ws, stdlib, src)
	switch r := runner.(type) {
	case nativePackageChecker:
		checked, err = r.CheckPackageStructured(input)
	case nativeCheckerExec:
		// The external checker protocol is still source-only; keep package and
		// workspace runs on the structured in-process path until that protocol
		// grows a package-native request shape.
		checked, err = embeddedNativeChecker{}.CheckPackageStructured(input)
	default:
		checked, err = runner.CheckSourceStructured(src.source)
	}
	if err != nil {
		result.Diags = append(result.Diags, checkerUnavailableDiag(
			"package",
			"the Osty-native checker executable failed",
			err.Error(),
		))
		return
	}
	result.Diags = nativeCheckerDiags(src.source, checked)
	result.NativeCheckerTelemetry = nativeCheckerTelemetry(checked)
	overlaySelfhostResult(result, src, checked)
}

func applyNativeWorkspaceResults(ws *resolve.Workspace, resolved map[string]*resolve.PackageResult, results map[string]*Result, stdlib resolve.StdlibProvider) {
	applySelfhostWorkspaceResults(ws, resolved, results, stdlib)
}

func applySelfhostWorkspaceResults(ws *resolve.Workspace, _ map[string]*resolve.PackageResult, results map[string]*Result, stdlib resolve.StdlibProvider) {
	if ws == nil {
		return
	}
	for path, result := range results {
		pkg := ws.Packages[path]
		if isProviderStdlibPackage(ws, path, pkg) {
			continue
		}
		applySelfhostPackageResult(result, pkg, nil, ws, stdlib)
	}
}

func nativeCheckerTelemetry(checked nativeCheckResult) *NativeCheckerTelemetry {
	if checked.Summary.Assignments == 0 && checked.Summary.Errors == 0 && len(checked.Summary.ErrorsByContext) == 0 {
		return nil
	}
	return &NativeCheckerTelemetry{
		Assignments:     checked.Summary.Assignments,
		Accepted:        checked.Summary.Accepted,
		Errors:          checked.Summary.Errors,
		ErrorsByContext: cloneStringIntMap(checked.Summary.ErrorsByContext),
		ErrorDetails:    cloneErrorDetailMap(checked.Summary.ErrorDetails),
	}
}

func cloneErrorDetailMap(src map[string]map[string]int) map[string]map[string]int {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]map[string]int, len(src))
	for ctx, inner := range src {
		out[ctx] = cloneStringIntMap(inner)
	}
	return out
}

func cloneStringIntMap(src map[string]int) map[string]int {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]int, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func nativeCheckerDiags(src []byte, checked nativeCheckResult) []*diag.Diagnostic {
	if checked.Summary.Errors == 0 {
		return nil
	}
	label := "native checker reported type errors"
	if checked.Summary.Errors == 1 {
		label = "native checker reported a type error"
	}
	return []*diag.Diagnostic{
		diag.New(diag.Error, fmt.Sprintf("%s: %d error(s)", label, checked.Summary.Errors)).
			Code(diag.CodeTypeMismatch).
			Primary(fileStartSpan(src), "native checker summary").
			Note(fmt.Sprintf(
				"native checker accepted %d of %d assignment/return/call checks",
				checked.Summary.Accepted,
				checked.Summary.Assignments,
			)).
			Build(),
	}
}

type selfhostSpanKey struct {
	start int
	end   int
}

type selfhostNameSpanKey struct {
	selfhostSpanKey
	name string
}

type selfhostSpanIndex struct {
	exprs       map[selfhostSpanKey]ast.Expr
	exprsByFrom map[int][]ast.Expr
	exprKeys    map[ast.Expr]selfhostSpanKey
	calls       map[selfhostSpanKey]*ast.CallExpr
	callsByFrom map[int][]*ast.CallExpr
	callKeys    map[*ast.CallExpr]selfhostSpanKey
	scopes      map[selfhostSpanKey]*resolve.Scope
	bindings    map[selfhostNameSpanKey]ast.Node
	symbols     map[selfhostNameSpanKey]*resolve.Symbol
}

func (idx *selfhostSpanIndex) bindNode(key selfhostNameSpanKey, n ast.Node) {
	if idx.bindings[key] != nil {
		return
	}
	idx.bindings[key] = n
}

func overlaySelfhostResult(result *Result, src selfhostCheckedSource, checked nativeCheckResult) {
	if result == nil {
		return
	}
	idx := buildSelfhostSpanIndex(src)
	for _, node := range checked.TypedNodes {
		key := selfhostSpanKey{start: node.Start, end: node.End}
		expr := idx.lookupExpr(key, node.Kind)
		if expr == nil {
			continue
		}
		t := parseSelfhostTypeName(node.TypeName, idx.scopeFor(key))
		if t == nil {
			continue
		}
		result.Types[expr] = t
	}
	for _, binding := range checked.Bindings {
		key := selfhostSpanKey{start: binding.Start, end: binding.End}
		t := parseSelfhostTypeName(binding.TypeName, idx.scopeFor(key))
		if t == nil {
			continue
		}
		nameKey := selfhostNameSpanKey{selfhostSpanKey: key, name: binding.Name}
		if n := idx.bindings[nameKey]; n != nil {
			result.LetTypes[n] = t
		}
		if sym := idx.symbols[nameKey]; sym != nil {
			result.SymTypes[sym] = t
		}
	}
	for _, symbol := range checked.Symbols {
		key := selfhostSpanKey{start: symbol.Start, end: symbol.End}
		t := parseSelfhostTypeName(symbol.TypeName, idx.scopeFor(key))
		if t == nil {
			continue
		}
		nameKey := selfhostNameSpanKey{selfhostSpanKey: key, name: symbol.Name}
		if sym := idx.symbols[nameKey]; sym != nil {
			result.SymTypes[sym] = t
		}
	}
	for _, inst := range checked.Instantiations {
		key := selfhostSpanKey{start: inst.Start, end: inst.End}
		call := idx.lookupCall(key)
		if call == nil || len(inst.TypeArgs) == 0 {
			continue
		}
		args := make([]types.Type, 0, len(inst.TypeArgs))
		for _, name := range inst.TypeArgs {
			if t := parseSelfhostTypeName(name, idx.scopeFor(key)); t != nil {
				args = append(args, t)
			}
		}
		if len(args) == len(inst.TypeArgs) {
			result.Instantiations[call] = args
		}
	}
}

func buildSelfhostSpanIndex(src selfhostCheckedSource) *selfhostSpanIndex {
	idx := &selfhostSpanIndex{
		exprs:       map[selfhostSpanKey]ast.Expr{},
		exprsByFrom: map[int][]ast.Expr{},
		exprKeys:    map[ast.Expr]selfhostSpanKey{},
		calls:       map[selfhostSpanKey]*ast.CallExpr{},
		callsByFrom: map[int][]*ast.CallExpr{},
		callKeys:    map[*ast.CallExpr]selfhostSpanKey{},
		scopes:      map[selfhostSpanKey]*resolve.Scope{},
		bindings:    map[selfhostNameSpanKey]ast.Node{},
		symbols:     map[selfhostNameSpanKey]*resolve.Symbol{},
	}
	for _, file := range src.files {
		if file.file == nil {
			continue
		}
		for _, decl := range file.file.Decls {
			idx.addNode(decl, file.base, file.scope, file.sourceMap)
		}
		for _, stmt := range file.file.Stmts {
			idx.addNode(stmt, file.base, file.scope, file.sourceMap)
		}
		idx.addScopeSubtree(file.scope, file.base, file.sourceMap)
		for _, sym := range file.refs {
			idx.addSymbol(sym, file.base, file.scope, file.sourceMap)
		}
	}
	return idx
}

func (idx *selfhostSpanIndex) scopeFor(key selfhostSpanKey) *resolve.Scope {
	if scope := idx.scopes[key]; scope != nil {
		return scope
	}
	var best *resolve.Scope
	bestSize := int(^uint(0) >> 1)
	for span, scope := range idx.scopes {
		if scope == nil {
			continue
		}
		if span.start > key.start || span.end < key.end {
			continue
		}
		size := span.end - span.start
		if size < bestSize {
			best = scope
			bestSize = size
		}
	}
	return best
}

func (idx *selfhostSpanIndex) addNode(n ast.Node, base int, scope *resolve.Scope, sm *sourcemap.Map) {
	if n == nil {
		return
	}
	// Nilable AST fields (e.g. FnDecl.Body for interface methods without a
	// default) arrive here as a non-nil ast.Node interface wrapping a nil
	// pointer. The `n == nil` guard above misses that; the type switch below
	// would then dereference the nil pointer.
	if rv := reflect.ValueOf(n); rv.Kind() == reflect.Ptr && rv.IsNil() {
		return
	}
	key, haveKey := spanKeyForNode(n, base, sm)
	if haveKey {
		if _, ok := idx.scopes[key]; !ok {
			idx.scopes[key] = scope
		}
		idx.addDeclaredSymbol(n, key, scope)
		if e, ok := n.(ast.Expr); ok {
			if _, have := idx.exprs[key]; !have {
				idx.exprs[key] = e
			}
			idx.exprsByFrom[key.start] = append(idx.exprsByFrom[key.start], e)
			idx.exprKeys[e] = key
			if c, ok := e.(*ast.CallExpr); ok {
				idx.calls[key] = c
				idx.callsByFrom[key.start] = append(idx.callsByFrom[key.start], c)
				idx.callKeys[c] = key
			}
		}
	}
	switch v := n.(type) {
	case *ast.FnDecl:
		idx.addNode(v.Recv, base, scope, sm)
		for _, g := range v.Generics {
			idx.addNode(g, base, scope, sm)
		}
		for _, p := range v.Params {
			idx.addNode(p, base, scope, sm)
		}
		idx.addNode(v.ReturnType, base, scope, sm)
		idx.addNode(v.Body, base, scope, sm)
	case *ast.StructDecl:
		for _, g := range v.Generics {
			idx.addNode(g, base, scope, sm)
		}
		for _, f := range v.Fields {
			idx.addNode(f, base, scope, sm)
		}
		for _, m := range v.Methods {
			idx.addNode(m, base, scope, sm)
		}
	case *ast.EnumDecl:
		for _, g := range v.Generics {
			idx.addNode(g, base, scope, sm)
		}
		for _, variant := range v.Variants {
			idx.addNode(variant, base, scope, sm)
		}
		for _, m := range v.Methods {
			idx.addNode(m, base, scope, sm)
		}
	case *ast.InterfaceDecl:
		for _, g := range v.Generics {
			idx.addNode(g, base, scope, sm)
		}
		for _, sup := range v.Extends {
			idx.addNode(sup, base, scope, sm)
		}
		for _, m := range v.Methods {
			idx.addNode(m, base, scope, sm)
		}
	case *ast.TypeAliasDecl:
		for _, g := range v.Generics {
			idx.addNode(g, base, scope, sm)
		}
		idx.addNode(v.Target, base, scope, sm)
	case *ast.LetDecl:
		idx.addNode(v.Type, base, scope, sm)
		idx.addNode(v.Value, base, scope, sm)
	case *ast.UseDecl:
		for _, d := range v.GoBody {
			idx.addNode(d, base, scope, sm)
		}
	case *ast.Field:
		idx.addNode(v.Type, base, scope, sm)
		idx.addNode(v.Default, base, scope, sm)
	case *ast.Variant:
		for _, f := range v.Fields {
			idx.addNode(f, base, scope, sm)
		}
	case *ast.Param:
		if haveKey && v.Name != "" {
			idx.bindNode(selfhostNameSpanKey{selfhostSpanKey: key, name: v.Name}, v)
		}
		idx.addNode(v.Pattern, base, scope, sm)
		idx.addNode(v.Type, base, scope, sm)
		idx.addNode(v.Default, base, scope, sm)
	case *ast.GenericParam:
		for _, con := range v.Constraints {
			idx.addNode(con, base, scope, sm)
		}
	case *ast.NamedType:
		for _, a := range v.Args {
			idx.addNode(a, base, scope, sm)
		}
	case *ast.OptionalType:
		idx.addNode(v.Inner, base, scope, sm)
	case *ast.TupleType:
		for _, elem := range v.Elems {
			idx.addNode(elem, base, scope, sm)
		}
	case *ast.FnType:
		for _, p := range v.Params {
			idx.addNode(p, base, scope, sm)
		}
		idx.addNode(v.ReturnType, base, scope, sm)
	case *ast.Block:
		for _, s := range v.Stmts {
			idx.addNode(s, base, scope, sm)
		}
	case *ast.LetStmt:
		if name := bindingPatternName(v.Pattern); name != "" {
			if patKey, ok := spanKeyForNode(v.Pattern, base, sm); ok {
				idx.bindNode(selfhostNameSpanKey{selfhostSpanKey: patKey, name: name}, v)
			}
		}
		idx.addNode(v.Pattern, base, scope, sm)
		idx.addNode(v.Type, base, scope, sm)
		idx.addNode(v.Value, base, scope, sm)
	case *ast.ExprStmt:
		idx.addNode(v.X, base, scope, sm)
	case *ast.AssignStmt:
		for _, t := range v.Targets {
			idx.addNode(t, base, scope, sm)
		}
		idx.addNode(v.Value, base, scope, sm)
	case *ast.ReturnStmt:
		idx.addNode(v.Value, base, scope, sm)
	case *ast.ChanSendStmt:
		idx.addNode(v.Channel, base, scope, sm)
		idx.addNode(v.Value, base, scope, sm)
	case *ast.DeferStmt:
		idx.addNode(v.X, base, scope, sm)
	case *ast.ForStmt:
		idx.addNode(v.Pattern, base, scope, sm)
		idx.addNode(v.Iter, base, scope, sm)
		idx.addNode(v.Body, base, scope, sm)
	case *ast.UnaryExpr:
		idx.addNode(v.X, base, scope, sm)
	case *ast.BinaryExpr:
		idx.addNode(v.Left, base, scope, sm)
		idx.addNode(v.Right, base, scope, sm)
	case *ast.QuestionExpr:
		idx.addNode(v.X, base, scope, sm)
	case *ast.CallExpr:
		idx.addNode(v.Fn, base, scope, sm)
		for _, a := range v.Args {
			idx.addNode(a, base, scope, sm)
		}
	case *ast.Arg:
		idx.addNode(v.Value, base, scope, sm)
	case *ast.FieldExpr:
		idx.addNode(v.X, base, scope, sm)
	case *ast.IndexExpr:
		idx.addNode(v.X, base, scope, sm)
		idx.addNode(v.Index, base, scope, sm)
	case *ast.TurbofishExpr:
		idx.addNode(v.Base, base, scope, sm)
		for _, a := range v.Args {
			idx.addNode(a, base, scope, sm)
		}
	case *ast.RangeExpr:
		idx.addNode(v.Start, base, scope, sm)
		idx.addNode(v.Stop, base, scope, sm)
	case *ast.ParenExpr:
		idx.addNode(v.X, base, scope, sm)
	case *ast.TupleExpr:
		for _, e := range v.Elems {
			idx.addNode(e, base, scope, sm)
		}
	case *ast.ListExpr:
		for _, e := range v.Elems {
			idx.addNode(e, base, scope, sm)
		}
	case *ast.MapExpr:
		for _, e := range v.Entries {
			idx.addNode(e, base, scope, sm)
		}
	case *ast.MapEntry:
		idx.addNode(v.Key, base, scope, sm)
		idx.addNode(v.Value, base, scope, sm)
	case *ast.StructLit:
		idx.addNode(v.Type, base, scope, sm)
		for _, f := range v.Fields {
			idx.addNode(f, base, scope, sm)
		}
		idx.addNode(v.Spread, base, scope, sm)
	case *ast.StructLitField:
		idx.addNode(v.Value, base, scope, sm)
	case *ast.IfExpr:
		idx.addNode(v.Pattern, base, scope, sm)
		idx.addNode(v.Cond, base, scope, sm)
		idx.addNode(v.Then, base, scope, sm)
		idx.addNode(v.Else, base, scope, sm)
	case *ast.MatchExpr:
		idx.addNode(v.Scrutinee, base, scope, sm)
		for _, a := range v.Arms {
			idx.addNode(a, base, scope, sm)
		}
	case *ast.MatchArm:
		idx.addNode(v.Pattern, base, scope, sm)
		idx.addNode(v.Guard, base, scope, sm)
		idx.addNode(v.Body, base, scope, sm)
	case *ast.ClosureExpr:
		for _, p := range v.Params {
			idx.addNode(p, base, scope, sm)
		}
		idx.addNode(v.ReturnType, base, scope, sm)
		idx.addNode(v.Body, base, scope, sm)
	case *ast.LiteralPat:
		idx.addNode(v.Literal, base, scope, sm)
	case *ast.IdentPat:
		if haveKey && v.Name != "" {
			idx.bindNode(selfhostNameSpanKey{selfhostSpanKey: key, name: v.Name}, v)
		}
	case *ast.TuplePat:
		for _, p := range v.Elems {
			idx.addNode(p, base, scope, sm)
		}
	case *ast.StructPat:
		for _, f := range v.Fields {
			idx.addNode(f, base, scope, sm)
		}
	case *ast.StructPatField:
		idx.addNode(v.Pattern, base, scope, sm)
	case *ast.VariantPat:
		for _, a := range v.Args {
			idx.addNode(a, base, scope, sm)
		}
	case *ast.RangePat:
		idx.addNode(v.Start, base, scope, sm)
		idx.addNode(v.Stop, base, scope, sm)
	case *ast.OrPat:
		for _, a := range v.Alts {
			idx.addNode(a, base, scope, sm)
		}
	case *ast.BindingPat:
		if haveKey && v.Name != "" {
			idx.bindNode(selfhostNameSpanKey{selfhostSpanKey: key, name: v.Name}, v)
		}
		idx.addNode(v.Pattern, base, scope, sm)
	}
}

func (idx *selfhostSpanIndex) addScopeSubtree(scope *resolve.Scope, base int, sm *sourcemap.Map) {
	if scope == nil {
		return
	}
	for _, sym := range scope.Symbols() {
		idx.addSymbol(sym, base, scope, sm)
	}
	for _, child := range scope.Children() {
		idx.addScopeSubtree(child, base, sm)
	}
}

func (idx *selfhostSpanIndex) addDeclaredSymbol(n ast.Node, key selfhostSpanKey, scope *resolve.Scope) {
	sym := declaredSymbolForNode(n, scope)
	if sym == nil {
		return
	}
	idx.symbols[selfhostNameSpanKey{selfhostSpanKey: key, name: sym.Name}] = sym
}

func declaredSymbolForNode(n ast.Node, scope *resolve.Scope) *resolve.Symbol {
	if scope == nil {
		return nil
	}
	pkgScope := scope.Parent()
	if pkgScope == nil {
		pkgScope = scope
	}
	switch v := n.(type) {
	case *ast.FnDecl:
		if v.Recv != nil || v.Name == "" {
			return nil
		}
		return lookupLocalDeclSymbol(pkgScope, v.Name, v)
	case *ast.StructDecl:
		return lookupLocalDeclSymbol(pkgScope, v.Name, v)
	case *ast.EnumDecl:
		return lookupLocalDeclSymbol(pkgScope, v.Name, v)
	case *ast.InterfaceDecl:
		return lookupLocalDeclSymbol(pkgScope, v.Name, v)
	case *ast.TypeAliasDecl:
		return lookupLocalDeclSymbol(pkgScope, v.Name, v)
	case *ast.LetDecl:
		return lookupLocalDeclSymbol(pkgScope, v.Name, v)
	case *ast.Variant:
		return lookupLocalDeclSymbol(pkgScope, v.Name, v)
	default:
		return nil
	}
}

func lookupLocalDeclSymbol(scope *resolve.Scope, name string, decl ast.Node) *resolve.Symbol {
	if scope == nil || name == "" || decl == nil {
		return nil
	}
	sym := scope.LookupLocal(name)
	if sym == nil || sym.Decl != decl {
		return nil
	}
	return sym
}

func (idx *selfhostSpanIndex) lookupExpr(key selfhostSpanKey, kind string) ast.Expr {
	if expr := idx.exprs[key]; expr != nil {
		return expr
	}
	var best ast.Expr
	bestSize := int(^uint(0) >> 1)
	for _, expr := range idx.exprsByFrom[key.start] {
		if kind != "" && selfhostExprKind(expr) != kind {
			continue
		}
		exprKey := idx.exprKeys[expr]
		if exprKey.end < key.end {
			continue
		}
		size := exprKey.end - exprKey.start
		if size < bestSize {
			best = expr
			bestSize = size
		}
	}
	if best != nil {
		return best
	}
	for expr, exprKey := range idx.exprKeys {
		if kind != "" && selfhostExprKind(expr) != kind {
			continue
		}
		if exprKey.start > key.start || exprKey.end < key.end {
			continue
		}
		size := exprKey.end - exprKey.start
		if size < bestSize {
			best = expr
			bestSize = size
		}
	}
	return best
}

func (idx *selfhostSpanIndex) lookupCall(key selfhostSpanKey) *ast.CallExpr {
	if call := idx.calls[key]; call != nil {
		return call
	}
	var best *ast.CallExpr
	bestSize := int(^uint(0) >> 1)
	for _, call := range idx.callsByFrom[key.start] {
		callKey := idx.callKeys[call]
		if callKey.end < key.end {
			continue
		}
		size := callKey.end - callKey.start
		if size < bestSize {
			best = call
			bestSize = size
		}
	}
	if best != nil {
		return best
	}
	for call, callKey := range idx.callKeys {
		if callKey.start > key.start || callKey.end < key.end {
			continue
		}
		size := callKey.end - callKey.start
		if size < bestSize {
			best = call
			bestSize = size
		}
	}
	return best
}

func selfhostExprKind(expr ast.Expr) string {
	switch expr.(type) {
	case *ast.Ident:
		return "Ident"
	case *ast.IntLit:
		return "IntLit"
	case *ast.FloatLit:
		return "FloatLit"
	case *ast.StringLit:
		return "StringLit"
	case *ast.BoolLit:
		return "BoolLit"
	case *ast.CharLit:
		return "CharLit"
	case *ast.ByteLit:
		return "ByteLit"
	case *ast.UnaryExpr:
		return "Unary"
	case *ast.BinaryExpr:
		return "Binary"
	case *ast.QuestionExpr:
		return "Question"
	case *ast.CallExpr:
		return "Call"
	case *ast.FieldExpr:
		return "Field"
	case *ast.IndexExpr:
		return "Index"
	case *ast.TurbofishExpr:
		return "Turbofish"
	case *ast.RangeExpr:
		return "Range"
	case *ast.ParenExpr:
		return "Paren"
	case *ast.TupleExpr:
		return "Tuple"
	case *ast.ListExpr:
		return "List"
	case *ast.MapExpr:
		return "Map"
	case *ast.StructLit:
		return "StructLit"
	case *ast.IfExpr:
		return "If"
	case *ast.MatchExpr:
		return "Match"
	case *ast.ClosureExpr:
		return "Closure"
	case *ast.Block:
		return "Block"
	default:
		return ""
	}
}

func (idx *selfhostSpanIndex) addSymbol(sym *resolve.Symbol, base int, scope *resolve.Scope, sm *sourcemap.Map) {
	if sym == nil || sym.Decl == nil || sym.Name == "" {
		return
	}
	key, ok := spanKeyForNode(sym.Decl, base, sm)
	if !ok {
		return
	}
	idx.symbols[selfhostNameSpanKey{selfhostSpanKey: key, name: sym.Name}] = sym
	if _, ok := idx.scopes[key]; !ok {
		idx.scopes[key] = scope
	}
}

func spanKeyForNode(n ast.Node, base int, sm *sourcemap.Map) (key selfhostSpanKey, ok bool) {
	if n == nil {
		return selfhostSpanKey{}, false
	}
	defer func() {
		if recover() != nil {
			key = selfhostSpanKey{}
			ok = false
		}
	}()
	start := n.Pos().Offset
	end := n.End().Offset
	if sm != nil {
		if generated, ok := sm.GeneratedSpanForOriginal(diag.Span{
			Start: n.Pos(),
			End:   n.End(),
		}); ok {
			start = generated.Start.Offset
			end = generated.End.Offset
		}
	}
	if end < start {
		return selfhostSpanKey{}, false
	}
	return selfhostSpanKey{
		start: base + start,
		end:   base + end,
	}, true
}

func bindingPatternName(p ast.Pattern) string {
	switch p := p.(type) {
	case *ast.IdentPat:
		return p.Name
	case *ast.BindingPat:
		return p.Name
	default:
		return ""
	}
}

func parseSelfhostTypeName(raw string, scope *resolve.Scope) types.Type {
	text := strings.TrimSpace(raw)
	switch text {
	case "", "Invalid", "Poison":
		return types.ErrorType
	case "()", "Unit":
		return types.Unit
	case "Never":
		return types.Never
	case "UntypedInt":
		return types.UntypedIntVal
	case "UntypedFloat":
		return types.UntypedFloatVal
	}
	if strings.HasSuffix(text, "?") {
		inner := parseSelfhostTypeName(strings.TrimSuffix(text, "?"), scope)
		return &types.Optional{Inner: inner}
	}
	if strings.HasPrefix(text, "fn(") {
		return parseSelfhostFnType(text, scope)
	}
	if strings.HasPrefix(text, "(") && strings.HasSuffix(text, ")") {
		inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(text, "("), ")"))
		if inner == "" {
			return types.Unit
		}
		parts := splitSelfhostTypeList(inner)
		if len(parts) == 1 {
			return parseSelfhostTypeName(parts[0], scope)
		}
		elems := make([]types.Type, 0, len(parts))
		for _, part := range parts {
			elems = append(elems, parseSelfhostTypeName(part, scope))
		}
		return &types.Tuple{Elems: elems}
	}
	head, argText, hasArgs := splitSelfhostGeneric(text)
	if p := types.PrimitiveByName(head); p != nil && !hasArgs {
		return p
	}
	if head == "Option" && hasArgs {
		args := splitSelfhostTypeList(argText)
		if len(args) == 1 {
			return &types.Optional{Inner: parseSelfhostTypeName(args[0], scope)}
		}
	}
	args := []types.Type(nil)
	if hasArgs {
		for _, part := range splitSelfhostTypeList(argText) {
			args = append(args, parseSelfhostTypeName(part, scope))
		}
	}
	sym := lookupSelfhostTypeSymbol(head, scope)
	if sym.Kind == resolve.SymGeneric {
		return &types.TypeVar{Sym: sym}
	}
	return &types.Named{Sym: sym, Args: args}
}

func parseSelfhostFnType(text string, scope *resolve.Scope) types.Type {
	open := strings.IndexByte(text, '(')
	if open < 0 {
		return types.ErrorType
	}
	close := matchingSelfhostParen(text, open)
	if close < 0 {
		return types.ErrorType
	}
	paramText := strings.TrimSpace(text[open+1 : close])
	var params []types.Type
	if paramText != "" {
		for _, part := range splitSelfhostTypeList(paramText) {
			params = append(params, parseSelfhostTypeName(part, scope))
		}
	}
	ret := types.Type(types.Unit)
	rest := strings.TrimSpace(text[close+1:])
	if strings.HasPrefix(rest, "->") {
		ret = parseSelfhostTypeName(strings.TrimSpace(strings.TrimPrefix(rest, "->")), scope)
	}
	return &types.FnType{Params: params, Return: ret}
}

func splitSelfhostGeneric(text string) (head, args string, ok bool) {
	depth := 0
	start := -1
	for i, r := range text {
		switch r {
		case '<':
			if depth == 0 {
				start = i
			}
			depth++
		case '>':
			depth--
			if depth == 0 && i == len(text)-1 && start >= 0 {
				return strings.TrimSpace(text[:start]), strings.TrimSpace(text[start+1 : i]), true
			}
		}
	}
	return strings.TrimSpace(text), "", false
}

func splitSelfhostTypeList(text string) []string {
	var out []string
	start := 0
	angle := 0
	paren := 0
	for i, r := range text {
		switch r {
		case '<':
			angle++
		case '>':
			if angle > 0 {
				angle--
			}
		case '(':
			paren++
		case ')':
			if paren > 0 {
				paren--
			}
		case ',':
			if angle == 0 && paren == 0 {
				part := strings.TrimSpace(text[start:i])
				if part != "" {
					out = append(out, part)
				}
				start = i + 1
			}
		}
	}
	if part := strings.TrimSpace(text[start:]); part != "" {
		out = append(out, part)
	}
	return out
}

func matchingSelfhostParen(text string, open int) int {
	depth := 0
	for i := open; i < len(text); i++ {
		switch text[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func lookupSelfhostTypeSymbol(head string, scope *resolve.Scope) *resolve.Symbol {
	name := strings.TrimSpace(head)
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		name = name[idx+1:]
	}
	if scope != nil {
		if sym := scope.Lookup(name); sym != nil {
			return sym
		}
	}
	if _, ok := scalarByName[name]; ok {
		return syntheticBuiltinSym(name)
	}
	switch name {
	case "List", "Map", "Set", "Option", "Result", "Error", "Equal", "Ordered", "Hashable", "Chan", "Channel", "Handle", "TaskGroup", "Iter":
		return syntheticBuiltinSym(name)
	}
	if name == "Self" || looksLikeSelfhostGeneric(name) {
		return &resolve.Symbol{Name: name, Kind: resolve.SymGeneric}
	}
	return &resolve.Symbol{Name: name, Kind: resolve.SymTypeAlias}
}

func looksLikeSelfhostGeneric(name string) bool {
	if name == "" {
		return false
	}
	if strings.ContainsAny(name, ".<>(), ") {
		return false
	}
	r := rune(name[0])
	return r >= 'A' && r <= 'Z' && len(name) == 1
}

func fileStartSpan(src []byte) diag.Span {
	start := token.Pos{Line: 1, Column: 1, Offset: 0}
	end := start
	if len(src) > 0 {
		end = token.Pos{Line: 1, Column: 2, Offset: 1}
	}
	return diag.Span{Start: start, End: end}
}

func selfhostFileSource(file *ast.File, rr *resolve.Result, src []byte, stdlib resolve.StdlibProvider) selfhostCheckedSource {
	var canonicalMap *sourcemap.Map
	if file != nil {
		if canonicalSrc, sm := canonical.SourceWithMap(src, file); len(canonicalSrc) > 0 && bytes.Equal(canonicalSrc, src) {
			canonicalMap = sm
		}
	}
	var b bytes.Buffer
	writeSelfhostImports(&b, nil, stdlib, fileUses(file))
	if b.Len() > 0 {
		b.WriteByte('\n')
	}
	base := b.Len()
	b.Write(src)
	if !bytes.HasSuffix(src, []byte("\n")) {
		b.WriteByte('\n')
	}
	var scope *resolve.Scope
	var refs map[*ast.Ident]*resolve.Symbol
	if rr != nil {
		scope = rr.FileScope
		refs = rr.Refs
	}
	return selfhostCheckedSource{
		source: b.Bytes(),
		files: []selfhostFileSegment{{
			file:      file,
			scope:     scope,
			refs:      refs,
			base:      base,
			sourceMap: canonicalMap,
		}},
	}
}

func selfhostPackageSource(pkg *resolve.Package, ws *resolve.Workspace, stdlib resolve.StdlibProvider) selfhostCheckedSource {
	var b bytes.Buffer
	writeSelfhostPackageImports(&b, pkg, ws, stdlib)
	var files []selfhostFileSegment
	for _, pf := range pkg.Files {
		src := pf.CheckerSource()
		if len(src) == 0 {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		base := b.Len()
		b.Write(src)
		if !bytes.HasSuffix(src, []byte("\n")) {
			b.WriteByte('\n')
		}
		files = append(files, selfhostFileSegment{
			file:      pf.File,
			scope:     pf.FileScope,
			refs:      pf.Refs,
			base:      base,
			sourceMap: pf.CanonicalMap,
		})
	}
	return selfhostCheckedSource{source: b.Bytes(), files: files}
}

func writeSelfhostPackageImports(b *bytes.Buffer, pkg *resolve.Package, ws *resolve.Workspace, stdlib resolve.StdlibProvider) {
	if pkg == nil {
		return
	}
	var uses []*ast.UseDecl
	for _, pf := range pkg.Files {
		if pf == nil || pf.File == nil {
			continue
		}
		uses = append(uses, pf.File.Uses...)
	}
	writeSelfhostImports(b, ws, stdlib, uses)
}

func writeSelfhostImports(b *bytes.Buffer, ws *resolve.Workspace, stdlib resolve.StdlibProvider, uses []*ast.UseDecl) {
	seen := map[string]bool{}
	for _, use := range uses {
		dotPath := strings.Join(use.Path, ".")
		target := (*resolve.Package)(nil)
		if ws != nil {
			target = ws.Packages[dotPath]
			if target == nil && ws.Stdlib != nil {
				target = ws.Stdlib.LookupPackage(dotPath)
			}
		}
		if target == nil && stdlib != nil {
			target = stdlib.LookupPackage(dotPath)
		}
		if target == nil {
			continue
		}
		alias := use.Alias
		if alias == "" && len(use.Path) > 0 {
			alias = use.Path[len(use.Path)-1]
		}
		if alias == "" || seen[alias] {
			continue
		}
		seen[alias] = true
		writeSelfhostPackageImport(b, alias, target)
	}
}

func fileUses(file *ast.File) []*ast.UseDecl {
	if file == nil {
		return nil
	}
	return file.Uses
}

func writeSelfhostPackageImport(b *bytes.Buffer, alias string, pkg *resolve.Package) {
	var body bytes.Buffer
	for _, pf := range pkg.Files {
		if pf == nil || pf.File == nil {
			continue
		}
		for _, decl := range pf.File.Decls {
			fn, ok := decl.(*ast.FnDecl)
			if !ok || !fn.Pub || fn.Recv != nil {
				continue
			}
			fmt.Fprintf(&body, "    fn %s", fn.Name)
			if generics := selfhostGenericParams(fn.Generics); generics != "" {
				body.WriteString(generics)
			}
			body.WriteByte('(')
			for i, param := range fn.Params {
				if i > 0 {
					body.WriteString(", ")
				}
				name := param.Name
				if name == "" {
					name = fmt.Sprintf("arg%d", i)
				}
				fmt.Fprintf(&body, "%s: %s", name, selfhostTypeSource(param.Type))
			}
			body.WriteByte(')')
			if ret := selfhostTypeSource(fn.ReturnType); ret != "()" {
				fmt.Fprintf(&body, " -> %s", ret)
			}
			body.WriteByte('\n')
		}
	}
	if body.Len() == 0 {
		return
	}
	fmt.Fprintf(b, "use go %q as %s {\n", alias, alias)
	b.Write(body.Bytes())
	b.WriteString("}\n")
}

// selfhostGenericParams formats a fn's generic parameter list for the
// boundary writer. Empty list returns "" (no `<>` emitted). Per
// LANG_SPEC §19.5, runtime intrinsic stubs declare generics like
// `fn read<T: Pod>(p: RawPtr) -> T`; without this the boundary
// dropped `<T: Pod>` and the native checker saw `T` as undeclared,
// producing `ErrType` at every turbofish call site.
func selfhostGenericParams(gps []*ast.GenericParam) string {
	if len(gps) == 0 {
		return ""
	}
	parts := make([]string, 0, len(gps))
	for _, gp := range gps {
		if gp == nil {
			continue
		}
		entry := gp.Name
		if len(gp.Constraints) > 0 {
			bounds := make([]string, 0, len(gp.Constraints))
			for _, c := range gp.Constraints {
				bounds = append(bounds, selfhostTypeSource(c))
			}
			entry += ": " + strings.Join(bounds, " + ")
		}
		parts = append(parts, entry)
	}
	if len(parts) == 0 {
		return ""
	}
	return "<" + strings.Join(parts, ", ") + ">"
}

func selfhostTypeSource(t ast.Type) string {
	switch x := t.(type) {
	case nil:
		return "()"
	case *ast.NamedType:
		name := strings.Join(x.Path, ".")
		if name == "" {
			name = "Invalid"
		}
		if len(x.Args) == 0 {
			return name
		}
		args := make([]string, 0, len(x.Args))
		for _, arg := range x.Args {
			args = append(args, selfhostTypeSource(arg))
		}
		return name + "<" + strings.Join(args, ", ") + ">"
	case *ast.OptionalType:
		return selfhostTypeSource(x.Inner) + "?"
	case *ast.TupleType:
		elems := make([]string, 0, len(x.Elems))
		for _, elem := range x.Elems {
			elems = append(elems, selfhostTypeSource(elem))
		}
		return "(" + strings.Join(elems, ", ") + ")"
	case *ast.FnType:
		params := make([]string, 0, len(x.Params))
		for _, param := range x.Params {
			params = append(params, selfhostTypeSource(param))
		}
		out := "fn(" + strings.Join(params, ", ") + ")"
		if x.ReturnType != nil {
			out += " -> " + selfhostTypeSource(x.ReturnType)
		}
		return out
	default:
		return "Invalid"
	}
}

var scalarByName = map[string]types.Type{
	"Int":     types.Int,
	"Int8":    types.Int8,
	"Int16":   types.Int16,
	"Int32":   types.Int32,
	"Int64":   types.Int64,
	"UInt8":   types.UInt8,
	"UInt16":  types.UInt16,
	"UInt32":  types.UInt32,
	"UInt64":  types.UInt64,
	"Byte":    types.Byte,
	"Float":   types.Float,
	"Float32": types.Float32,
	"Float64": types.Float64,
	"Bool":    types.Bool,
	"Char":    types.Char,
	"String":  types.String,
	"Bytes":   types.Bytes,
	"Never":   types.Never,
}

// syntheticBuiltinSym returns a process-wide Symbol that stands in for a
// builtin when the native checker response names a type outside the current
// resolver scope.
func syntheticBuiltinSym(name string) *resolve.Symbol {
	if sym, ok := syntheticBuiltinsRead(name); ok {
		return sym
	}
	sym := &resolve.Symbol{Name: name, Kind: resolve.SymBuiltin}
	syntheticBuiltinsStore(name, sym)
	return sym
}

var (
	syntheticBuiltinsMu sync.RWMutex
	syntheticBuiltins   = map[string]*resolve.Symbol{}
)

func syntheticBuiltinsRead(name string) (*resolve.Symbol, bool) {
	syntheticBuiltinsMu.RLock()
	defer syntheticBuiltinsMu.RUnlock()
	sym, ok := syntheticBuiltins[name]
	return sym, ok
}

func syntheticBuiltinsStore(name string, sym *resolve.Symbol) {
	syntheticBuiltinsMu.Lock()
	defer syntheticBuiltinsMu.Unlock()
	if _, ok := syntheticBuiltins[name]; ok {
		return
	}
	syntheticBuiltins[name] = sym
}
