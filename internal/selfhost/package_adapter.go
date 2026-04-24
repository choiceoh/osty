package selfhost

import (
	"fmt"
	"reflect"

	"github.com/osty/osty/internal/selfhost/api"
)

// Package-level type aliases re-export the shared boundary shapes from
// internal/selfhost/api. See selfhost/api/package.go for definitions.
type (
	PackageCheckFile         = api.PackageCheckFile
	PackageCheckGenericBound = api.PackageCheckGenericBound
	PackageCheckFn           = api.PackageCheckFn
	PackageCheckField        = api.PackageCheckField
	PackageCheckVariant      = api.PackageCheckVariant
	PackageCheckAlias        = api.PackageCheckAlias
	PackageCheckType         = api.PackageCheckType
	PackageCheckInterfaceExt = api.PackageCheckInterfaceExt
	PackageCheckImport       = api.PackageCheckImport
	PackageCheckInput        = api.PackageCheckInput
)

// CheckPackageStructured re-parses each input file via the self-host
// lexer + parser, merges the per-file AstArenas into a synthetic package
// arena, installs imported package surfaces directly into the checker
// env, and runs the typed checker. Source text is the sole AST ingress
// point — no *ast.File round-trip, no astbridge bumps.
func CheckPackageStructured(input PackageCheckInput) (CheckResult, error) {
	file, layout, err := selfhostBuildPackageAst(input.Files)
	if err != nil {
		return CheckResult{}, err
	}
	if file == nil {
		return CheckResult{}, nil
	}
	cx := newElabCx(file, emptyTyArena())
	selfhostInstallImportSurfaces(cx.env, input.Imports)
	elabFile(cx)
	result := adaptCheckResultWithTokenLayout(serializeCheckResult(cx), layout)
	selfhostAppendIntrinsicBodyGateForPackage(&result, input)
	return result, nil
}

type selfhostPackageTokenLayout struct {
	starts     []int
	ends       []int
	startLines []int
	startCols  []int
	// fileIdx[i] indexes into files for token i. -1 when the file carried no
	// display name (selfhostLayoutTokenPos then emits an empty filename and
	// the telemetry suffix drops back to `@Lnn:Cnn`).
	fileIdx []int
	files   []string
}

func selfhostBuildPackageAst(files []PackageCheckFile) (*AstFile, *selfhostPackageTokenLayout, error) {
	arena := emptyAstArena()
	layout := &selfhostPackageTokenLayout{}
	haveFile := false
	for _, file := range files {
		if len(file.Source) == 0 {
			continue
		}
		lexed := ostyLexSource(string(file.Source))
		parsed := astParseLexedSource(lexed)
		if parsed == nil || parsed.arena == nil {
			return nil, nil, fmt.Errorf("selfhost package adapter: parse produced no AST")
		}
		if len(parsed.arena.errors) > 0 {
			return nil, nil, fmt.Errorf("selfhost package adapter: parse errors: %s", astFormatErrors(parsed))
		}
		parsed = selfhostSemanticAstFile(parsed)
		tokenBase := len(layout.starts)
		fileIdx := -1
		if file.Name != "" {
			fileIdx = len(layout.files)
			layout.files = append(layout.files, file.Name)
		}
		selfhostAppendTokenLayout(layout, lexed, file.Base, fileIdx)
		selfhostMergeAstArena(arena, parsed.arena, tokenBase)
		haveFile = true
	}
	if !haveFile {
		return nil, layout, nil
	}
	return &AstFile{arena: arena}, layout, nil
}

func selfhostAppendTokenLayout(layout *selfhostPackageTokenLayout, lexed *OstyLexedSource, base int, fileIdx int) {
	if layout == nil || lexed == nil || lexed.stream == nil {
		return
	}
	rt := newRuneTable(lexed.source)
	// Grow all parallel slices in one amortised step — appends still happen
	// per token inside the loop, but the underlying array is sized for the
	// incoming stream up-front, so we avoid the repeated-realloc cost that
	// multi-MB merged packages would otherwise pay on every `append`.
	n := len(lexed.stream.tokens)
	newLen := len(layout.starts) + n
	layout.starts = selfhostGrowIntSlice(layout.starts, newLen)
	layout.ends = selfhostGrowIntSlice(layout.ends, newLen)
	layout.startLines = selfhostGrowIntSlice(layout.startLines, newLen)
	layout.startCols = selfhostGrowIntSlice(layout.startCols, newLen)
	layout.fileIdx = selfhostGrowIntSlice(layout.fileIdx, newLen)
	for _, tok := range lexed.stream.tokens {
		if tok == nil || tok.start == nil || tok.end == nil {
			layout.starts = append(layout.starts, base)
			layout.ends = append(layout.ends, base)
			layout.startLines = append(layout.startLines, 0)
			layout.startCols = append(layout.startCols, 0)
			layout.fileIdx = append(layout.fileIdx, fileIdx)
			continue
		}
		layout.starts = append(layout.starts, base+rt.byteOffset(tok.start.offset))
		layout.ends = append(layout.ends, base+rt.byteOffset(tok.end.offset))
		layout.startLines = append(layout.startLines, tok.start.line)
		layout.startCols = append(layout.startCols, tok.start.column)
		layout.fileIdx = append(layout.fileIdx, fileIdx)
	}
}

// selfhostGrowIntSlice reserves capacity for at least `want` total elements
// without changing the slice's logical length, so subsequent `append`s stay
// allocation-free for large per-file token batches.
func selfhostGrowIntSlice(xs []int, want int) []int {
	if cap(xs) >= want {
		return xs
	}
	grown := make([]int, len(xs), want)
	copy(grown, xs)
	return grown
}

// selfhostLayoutTokenPos returns a selfhostTokenPos backed by the per-file
// (filename, line, column) slices recorded during selfhostAppendTokenLayout.
// Mirrors selfhostStreamTokenPos for the package/workspace call paths where
// the lex stream is materialised per-file and then discarded. Filename is
// empty when the originating PackageCheckFile carried no Name, which lets
// the telemetry suffix drop back to `@Lnn:Cnn` without lying about the file.
func selfhostLayoutTokenPos(layout *selfhostPackageTokenLayout) selfhostTokenPos {
	if layout == nil {
		return nil
	}
	return func(tokenIdx int) (string, int, int, bool) {
		if tokenIdx < 0 || tokenIdx >= len(layout.startLines) || tokenIdx >= len(layout.startCols) {
			return "", 0, 0, false
		}
		line := layout.startLines[tokenIdx]
		col := layout.startCols[tokenIdx]
		if line <= 0 || col <= 0 {
			return "", 0, 0, false
		}
		file := ""
		if tokenIdx < len(layout.fileIdx) {
			if idx := layout.fileIdx[tokenIdx]; idx >= 0 && idx < len(layout.files) {
				file = layout.files[idx]
			}
		}
		return file, line, col, true
	}
}

func selfhostMergeAstArena(dst *AstArena, src *AstArena, tokenBase int) {
	if dst == nil || src == nil {
		return
	}
	nodeBase := len(dst.nodes)
	for _, node := range src.nodes {
		if node == nil {
			dst.nodes = append(dst.nodes, nil)
			continue
		}
		cloned := *node
		cloned.start = selfhostShiftTokenIndex(cloned.start, tokenBase)
		cloned.end = selfhostShiftTokenIndex(cloned.end, tokenBase)
		cloned.left = selfhostShiftNodeIndex(cloned.left, nodeBase)
		cloned.right = selfhostShiftNodeIndex(cloned.right, nodeBase)
		// Parser AST nodes reuse `extra` for enum-like metadata such as pattern
		// kinds and packed annotations, so it cannot be shifted blindly during a
		// multi-file merge the way child node references can.
		cloned.extra = node.extra
		cloned.children = selfhostShiftNodeList(cloned.children, nodeBase)
		cloned.children2 = selfhostShiftNodeList(cloned.children2, nodeBase)
		dst.nodes = append(dst.nodes, &cloned)
	}
	for _, decl := range src.decls {
		dst.decls = append(dst.decls, selfhostShiftNodeIndex(decl, nodeBase))
	}
	for _, parseErr := range src.errors {
		if parseErr == nil {
			dst.errors = append(dst.errors, nil)
			continue
		}
		cloned := *parseErr
		cloned.tokenIndex = selfhostShiftTokenIndex(cloned.tokenIndex, tokenBase)
		dst.errors = append(dst.errors, &cloned)
	}
}

func selfhostShiftNodeList(xs []int, base int) []int {
	if len(xs) == 0 {
		return nil
	}
	out := make([]int, len(xs))
	for i, x := range xs {
		out[i] = selfhostShiftNodeIndex(x, base)
	}
	return out
}

func selfhostShiftNodeIndex(idx, base int) int {
	if idx < 0 {
		return idx
	}
	return idx + base
}

func selfhostShiftTokenIndex(idx, base int) int {
	if idx < 0 {
		return idx
	}
	return idx + base
}

func adaptCheckResultWithTokenLayout(checked *FrontCheckResult, layout *selfhostPackageTokenLayout) CheckResult {
	if checked == nil {
		return CheckResult{}
	}
	result := CheckResult{
		Summary:        adaptCheckSummaryWithContext(checked, selfhostLayoutTokenPos(layout)),
		TypedNodes:     make([]CheckedNode, 0, len(checked.typedNodes)),
		Bindings:       make([]CheckedBinding, 0, len(checked.bindings)),
		Symbols:        make([]CheckedSymbol, 0, len(checked.symbols)),
		Instantiations: make([]CheckInstantiation, 0, len(checked.instantiations)),
		Diagnostics:    make([]CheckDiagnosticRecord, 0, len(checked.diagnostics)),
	}
	for _, node := range checked.typedNodes {
		if node == nil {
			continue
		}
		start, end := checkNodeOffsetsWithTokenLayout(layout, node.start, node.end)
		result.TypedNodes = append(result.TypedNodes, CheckedNode{
			Node:     node.node,
			Kind:     node.kind,
			TypeName: node.typeName,
			Start:    start,
			End:      end,
		})
	}
	for _, binding := range checked.bindings {
		if binding == nil {
			continue
		}
		start, end := checkNodeOffsetsWithTokenLayout(layout, binding.start, binding.end)
		result.Bindings = append(result.Bindings, CheckedBinding{
			Node:     binding.node,
			Name:     binding.name,
			TypeName: binding.typeName,
			Mutable:  binding.mutable,
			Start:    start,
			End:      end,
		})
	}
	for _, symbol := range checked.symbols {
		if symbol == nil {
			continue
		}
		start, end := checkNodeOffsetsWithTokenLayout(layout, symbol.start, symbol.end)
		result.Symbols = append(result.Symbols, CheckedSymbol{
			Node:     symbol.node,
			Kind:     symbol.kind,
			Name:     symbol.name,
			Owner:    symbol.owner,
			TypeName: symbol.typeName,
			Start:    start,
			End:      end,
		})
	}
	for _, inst := range checked.instantiations {
		if inst == nil {
			continue
		}
		start, end := checkNodeOffsetsWithTokenLayout(layout, inst.start, inst.end)
		result.Instantiations = append(result.Instantiations, CheckInstantiation{
			Node:       inst.node,
			Callee:     inst.callee,
			TypeArgs:   append([]string(nil), inst.typeArgs...),
			ResultType: inst.resultType,
			Start:      start,
			End:        end,
		})
	}
	for _, d := range checked.diagnostics {
		if d == nil {
			continue
		}
		start, end := checkNodeOffsetsWithTokenLayout(layout, d.start, d.end)
		result.Diagnostics = append(result.Diagnostics, CheckDiagnosticRecord{
			Code:     d.code,
			Severity: diagnosticSeverityName(d.severity),
			Message:  d.message,
			Start:    start,
			End:      end,
			File:     "",
			Notes:    append([]string(nil), d.notes...),
		})
	}
	return result
}

func checkNodeOffsetsWithTokenLayout(layout *selfhostPackageTokenLayout, startToken, endToken int) (int, int) {
	if layout == nil || len(layout.starts) == 0 {
		return 0, 0
	}
	if startToken < 0 {
		startToken = 0
	}
	if startToken >= len(layout.starts) {
		startToken = len(layout.starts) - 1
	}
	endIndex := endToken - 1
	if endIndex < startToken {
		endIndex = startToken
	}
	if endIndex >= len(layout.ends) {
		endIndex = len(layout.ends) - 1
	}
	start := layout.starts[startToken]
	end := layout.ends[endIndex]
	if end < start {
		end = start
	}
	return start, end
}

func selfhostInstallImportSurfaces(env *CheckEnv, imports []PackageCheckImport) {
	if env == nil {
		return
	}
	for _, imp := range imports {
		if imp.Alias == "" {
			continue
		}
		checkBind(env, imp.Alias, tyNamed(env.tys, imp.Alias, nil))
		for _, iface := range imp.RegisterAsIface {
			if iface != "" {
				checkRegisterInterface(env, iface)
			}
		}
		for _, decl := range imp.TypeDecls {
			checkRegisterType(env, &CheckTypeSig{
				name:          decl.Name,
				generics:      append([]string(nil), decl.Generics...),
				genericBounds: selfhostMaterializeBounds(env, decl.GenericBounds),
				kind:          decl.Kind,
			})
		}
		for _, ext := range imp.InterfaceExts {
			if ifaceTy := selfhostTypeNameToTy(env, ext.InterfaceType); ifaceTy >= 0 {
				checkRegisterInterfaceExtends(env, &CheckInterfaceExt{owner: ext.Owner, ifaceTy: ifaceTy})
			}
		}
		for _, alias := range imp.Aliases {
			checkRegisterAlias(env, &CheckAliasSig{
				name:     alias.Name,
				ty:       selfhostTypeNameToTy(env, alias.Target),
				generics: append([]string(nil), alias.Generics...),
			})
		}
		for _, field := range imp.Fields {
			sig := &CheckFieldSig{
				owner:      field.Owner,
				name:       field.Name,
				ty:         selfhostTypeNameToTy(env, field.TypeName),
				hasDefault: field.HasDefault,
			}
			selfhostSetCheckFieldExported(sig, field.Exported)
			checkRegisterField(env, sig)
		}
		for _, variant := range imp.Variants {
			fieldTys := make([]int, 0, len(variant.FieldTypes))
			for _, tyName := range variant.FieldTypes {
				fieldTys = append(fieldTys, selfhostTypeNameToTy(env, tyName))
			}
			checkRegisterVariant(env, &CheckVariantSig{
				owner:    variant.Owner,
				name:     variant.Name,
				fieldTys: fieldTys,
				generics: append([]string(nil), variant.Generics...),
			})
		}
		for _, fn := range imp.Functions {
			paramTys := make([]int, 0, len(fn.ParamTypes))
			for _, tyName := range fn.ParamTypes {
				paramTys = append(paramTys, selfhostTypeNameToTy(env, tyName))
			}
			receiverTy := -1
			if fn.ReceiverType != "" {
				receiverTy = selfhostTypeNameToTy(env, fn.ReceiverType)
			}
			retTy := selfhostTypeNameToTy(env, fn.ReturnType)
			if fn.ReturnType == "" || fn.ReturnType == "()" {
				retTy = tUnit(env.tys)
			}
			checkRegisterFn(env, &CheckFnSig{
				name:          fn.Name,
				owner:         fn.Owner,
				receiverTy:    receiverTy,
				retTy:         retTy,
				paramNames:    append([]string(nil), fn.ParamNames...),
				paramTys:      paramTys,
				generics:      append([]string(nil), fn.Generics...),
				genericBounds: selfhostMaterializeBounds(env, fn.GenericBounds),
			})
			if fn.HasBody {
				checkMarkFnHasBody(env, fn.Name, fn.Owner)
			}
		}
	}
}

// selfhostSetCheckFieldExported bridges the checked-in generated.go shape
// during regen: older generated snapshots do not yet carry the `exported`
// field on CheckFieldSig, so package compilation would fail if we named the
// field directly in a composite literal. Reflection keeps the pre-regen
// package buildable and becomes a no-op once the generated type lags behind.
func selfhostSetCheckFieldExported(sig *CheckFieldSig, exported bool) {
	if sig == nil {
		return
	}
	rv := reflect.ValueOf(sig)
	if !rv.IsValid() || rv.Kind() != reflect.Pointer || rv.IsNil() {
		return
	}
	elem := rv.Elem()
	if !elem.IsValid() || elem.Kind() != reflect.Struct {
		return
	}
	field := elem.FieldByName("exported")
	if !field.IsValid() || !field.CanSet() || field.Kind() != reflect.Bool {
		return
	}
	field.SetBool(exported)
}

func selfhostMaterializeBounds(env *CheckEnv, bounds []PackageCheckGenericBound) []*CheckGenericBound {
	out := make([]*CheckGenericBound, 0, len(bounds))
	for _, bound := range bounds {
		out = append(out, &CheckGenericBound{
			tyParam: bound.TyParam,
			iface:   selfhostTypeNameToTy(env, bound.InterfaceType),
		})
	}
	return out
}

func selfhostTypeNameToTy(env *CheckEnv, typeName string) int {
	if env == nil {
		return -1
	}
	if typeName == "" {
		return tErr(env.tys)
	}
	if typeName == "()" {
		return tUnit(env.tys)
	}
	if typeName == "Invalid" || typeName == "Poison" {
		return tErr(env.tys)
	}
	return tyFromString(env.tys, typeName)
}
