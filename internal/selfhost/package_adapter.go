package selfhost

import (
	"fmt"
	"reflect"

	"github.com/osty/osty/internal/selfhost/api"
)

// Package-level type aliases re-export the shared boundary shapes from
// internal/selfhost/api for compatibility. New consumers should import
// internal/selfhost/api directly.
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
func CheckPackageStructured(input PackageCheckInput) (result CheckResult, err error) {
	defer recoverCheckResult(&result, "package")
	selfhostCheckMu.Lock()
	defer selfhostCheckMu.Unlock()

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
	result = adaptCheckResultWithTokenLayout(serializeCheckResult(cx), layout)
	return result, nil
}

// InspectPackageStructured runs the same selfhost package check as
// CheckPackageStructured, then asks the selfhost inspect pass to derive
// per-node observations from that authoritative check result. The Go host only
// adapts token spans to byte offsets; inference rule and hint policy stay in
// toolchain/inspect.osty.
func InspectPackageStructured(input PackageCheckInput) (records []api.InspectRecord, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			records = nil
			err = fmt.Errorf("selfhost inspect recovered from panic: %v", recovered)
		}
	}()
	selfhostCheckMu.Lock()
	defer selfhostCheckMu.Unlock()

	file, layout, err := selfhostBuildPackageAst(input.Files)
	if err != nil {
		return nil, err
	}
	if file == nil {
		return nil, nil
	}
	cx := newElabCx(file, emptyTyArena())
	selfhostInstallImportSurfaces(cx.env, input.Imports)
	elabFile(cx)
	checked := serializeCheckResult(cx)
	recs := inspectFromAstAndCheck(file, checked)
	return adaptInspectRecordsWithTokenLayout(recs, layout), nil
}

type selfhostPackageTokenLayout struct {
	starts     []int
	ends       []int
	startLines []int
	startCols  []int
	endLines   []int
	endCols    []int
	// fileIdx[i] indexes into files for token i. -1 when the file carried no
	// display name (selfhostLayoutTokenPos then emits an empty filename and
	// the telemetry suffix drops back to `@Lnn:Cnn`).
	fileIdx []int
	files   []string
	fileIDs []string
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
		displayName := file.Path
		if displayName == "" {
			displayName = file.Name
		}
		if displayName != "" || file.SourceFileID != "" {
			fileIdx = len(layout.files)
			layout.files = append(layout.files, displayName)
			layout.fileIDs = append(layout.fileIDs, file.SourceFileID)
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
	layout.endLines = selfhostGrowIntSlice(layout.endLines, newLen)
	layout.endCols = selfhostGrowIntSlice(layout.endCols, newLen)
	layout.fileIdx = selfhostGrowIntSlice(layout.fileIdx, newLen)
	for _, tok := range lexed.stream.tokens {
		if tok == nil || tok.start == nil || tok.end == nil {
			layout.starts = append(layout.starts, base)
			layout.ends = append(layout.ends, base)
			layout.startLines = append(layout.startLines, 0)
			layout.startCols = append(layout.startCols, 0)
			layout.endLines = append(layout.endLines, 0)
			layout.endCols = append(layout.endCols, 0)
			layout.fileIdx = append(layout.fileIdx, fileIdx)
			continue
		}
		layout.starts = append(layout.starts, base+rt.byteOffset(tok.start.offset))
		layout.ends = append(layout.ends, base+rt.byteOffset(tok.end.offset))
		layout.startLines = append(layout.startLines, tok.start.line)
		layout.startCols = append(layout.startCols, tok.start.column)
		layout.endLines = append(layout.endLines, tok.end.line)
		layout.endCols = append(layout.endCols, tok.end.column)
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
		cloned.extra = selfhostShiftExtraForKind(node, nodeBase)
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

func selfhostShiftExtraForKind(node *AstNode, base int) int {
	if node == nil || node.extra < 0 {
		return -1
	}
	// `extra` is overloaded in the parser arena. Pattern nodes store small
	// enum tags there, while declaration-ish nodes store packed annotation node
	// indices. Shift only the annotation-bearing shapes during package merges.
	switch node.kind.(type) {
	case *AstNodeKind_AstNFnDecl,
		*AstNodeKind_AstNStructDecl,
		*AstNodeKind_AstNEnumDecl,
		*AstNodeKind_AstNInterfaceDecl,
		*AstNodeKind_AstNTypeAlias,
		*AstNodeKind_AstNLet,
		*AstNodeKind_AstNLetDecl,
		*AstNodeKind_AstNField_,
		*AstNodeKind_AstNVariant:
		return selfhostShiftNodeIndex(node.extra, base)
	default:
		return node.extra
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
	return adaptCheckResultWithTokenMapper(checked, checkResultTokenMapper{
		tokenPos: selfhostLayoutTokenPos(layout),
		offsets: func(startToken, endToken int) (int, int) {
			return checkNodeOffsetsWithTokenLayout(layout, startToken, endToken)
		},
		tokenRange: func(startToken, endToken int) (int, int, int, int, int, int) {
			return checkNodeRangeWithTokenLayout(layout, startToken, endToken)
		},
		file: func(tokenIdx int) string {
			return checkFilePathWithTokenLayout(layout, tokenIdx)
		},
		sourceID: func(tokenIdx int) string {
			return checkSourceFileIDWithTokenLayout(layout, tokenIdx)
		},
	})
}

func checkFilePathWithTokenLayout(layout *selfhostPackageTokenLayout, tokenIdx int) string {
	if layout == nil || tokenIdx < 0 || tokenIdx >= len(layout.fileIdx) {
		return ""
	}
	idx := layout.fileIdx[tokenIdx]
	if idx < 0 || idx >= len(layout.files) {
		return ""
	}
	return layout.files[idx]
}

func checkSourceFileIDWithTokenLayout(layout *selfhostPackageTokenLayout, tokenIdx int) string {
	if layout == nil || tokenIdx < 0 || tokenIdx >= len(layout.fileIdx) {
		return ""
	}
	idx := layout.fileIdx[tokenIdx]
	if idx < 0 || idx >= len(layout.fileIDs) {
		return ""
	}
	return layout.fileIDs[idx]
}

func checkNodeRangeWithTokenLayout(layout *selfhostPackageTokenLayout, startToken, endToken int) (start, end, startLine, startColumn, endLine, endColumn int) {
	start, end = checkNodeOffsetsWithTokenLayout(layout, startToken, endToken)
	startLine, startColumn, endLine, endColumn = checkTokenDisplayRangeWithTokenLayout(layout, startToken, endToken)
	return start, end, startLine, startColumn, endLine, endColumn
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

func checkTokenDisplayRangeWithTokenLayout(layout *selfhostPackageTokenLayout, startToken, endToken int) (startLine, startColumn, endLine, endColumn int) {
	if layout == nil || len(layout.startLines) == 0 {
		return 0, 0, 0, 0
	}
	if startToken < 0 {
		startToken = 0
	}
	if startToken >= len(layout.startLines) {
		startToken = len(layout.startLines) - 1
	}
	endIndex := endToken - 1
	if endIndex < startToken {
		endIndex = startToken
	}
	if endIndex >= len(layout.endLines) {
		endIndex = len(layout.endLines) - 1
	}
	if startToken < len(layout.startCols) {
		startLine = layout.startLines[startToken]
		startColumn = layout.startCols[startToken]
	}
	if endIndex >= 0 && endIndex < len(layout.endCols) {
		endLine = layout.endLines[endIndex]
		endColumn = layout.endCols[endIndex]
	}
	return startLine, startColumn, endLine, endColumn
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
		checkMarkImportAlias(env, imp.Alias)
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
			if ifaceTy := selfhostTypeReprToTy(env, ext.InterfaceTypeRepr, ext.InterfaceType); ifaceTy >= 0 {
				checkRegisterInterfaceExtends(env, &CheckInterfaceExt{owner: ext.Owner, ifaceTy: ifaceTy})
			}
		}
		for _, alias := range imp.Aliases {
			checkRegisterAlias(env, &CheckAliasSig{
				name:     alias.Name,
				ty:       selfhostTypeReprToTy(env, alias.TargetRepr, alias.Target),
				generics: append([]string(nil), alias.Generics...),
			})
		}
		for _, field := range imp.Fields {
			sig := &CheckFieldSig{
				owner:      field.Owner,
				name:       field.Name,
				ty:         selfhostTypeReprToTy(env, field.Type, field.TypeName),
				hasDefault: field.HasDefault,
			}
			selfhostSetCheckFieldExported(sig, field.Exported)
			checkRegisterField(env, sig)
		}
		for _, variant := range imp.Variants {
			fieldTys := selfhostTypeReprListToTys(env, variant.FieldTypeReprs, variant.FieldTypes)
			checkRegisterVariant(env, &CheckVariantSig{
				owner:    variant.Owner,
				name:     variant.Name,
				fieldTys: fieldTys,
				generics: append([]string(nil), variant.Generics...),
			})
		}
		for _, fn := range imp.Functions {
			paramTys := selfhostTypeReprListToTys(env, fn.ParamTypeReprs, fn.ParamTypes)
			receiverTy := -1
			if fn.ReceiverTypeRepr != nil || fn.ReceiverType != "" {
				receiverTy = selfhostTypeReprToTy(env, fn.ReceiverTypeRepr, fn.ReceiverType)
			}
			retTy := selfhostTypeReprToTy(env, fn.ReturnTypeRepr, fn.ReturnType)
			if fn.ReturnTypeRepr == nil && (fn.ReturnType == "" || fn.ReturnType == "()") {
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
			iface:   selfhostTypeReprToTy(env, bound.InterfaceTypeRepr, bound.InterfaceType),
		})
	}
	return out
}

func selfhostTypeReprListToTys(env *CheckEnv, reprs []TypeRepr, fallback []string) []int {
	n := len(reprs)
	if n == 0 {
		n = len(fallback)
	}
	out := make([]int, 0, n)
	for i := 0; i < n; i++ {
		var repr *TypeRepr
		if i < len(reprs) {
			repr = &reprs[i]
		}
		fallbackName := ""
		if i < len(fallback) {
			fallbackName = fallback[i]
		}
		out = append(out, selfhostTypeReprToTy(env, repr, fallbackName))
	}
	return out
}

func selfhostTypeReprToTy(env *CheckEnv, repr *TypeRepr, fallback string) int {
	if env == nil {
		return -1
	}
	if repr == nil {
		return selfhostTypeNameToTy(env, fallback)
	}
	switch repr.Kind {
	case "unit":
		return tUnit(env.tys)
	case "never":
		return tNever(env.tys)
	case "error":
		return tErr(env.tys)
	case "poison":
		return tPoison(env.tys)
	case "primitive":
		if prim := primKindFromName(repr.Name); !isInvalidPrimKind(prim) || repr.Name == "Invalid" {
			return tyPrim(env.tys, prim)
		}
		return tyNamed(env.tys, repr.Name, nil)
	case "named":
		return tyNamed(env.tys, repr.Name, selfhostTypeReprArgsToTys(env, repr.Args))
	case "optional":
		return tyOptional(env.tys, selfhostTypeReprToTy(env, repr.Return, ""))
	case "tuple":
		return tyTuple(env.tys, selfhostTypeReprArgsToTys(env, repr.Args))
	case "fn":
		ret := tUnit(env.tys)
		if repr.Return != nil {
			ret = selfhostTypeReprToTy(env, repr.Return, "")
		}
		return tyFn(env.tys, selfhostTypeReprArgsToTys(env, repr.Args), ret)
	case "typevar":
		if repr.Name == "" {
			return tErr(env.tys)
		}
		return tyNamed(env.tys, repr.Name, nil)
	case "self":
		return tySelf(env.tys, "")
	default:
		if repr.Name != "" {
			return tyNamed(env.tys, repr.Name, selfhostTypeReprArgsToTys(env, repr.Args))
		}
		return selfhostTypeNameToTy(env, fallback)
	}
}

func selfhostTypeReprArgsToTys(env *CheckEnv, reprs []TypeRepr) []int {
	out := make([]int, 0, len(reprs))
	for i := range reprs {
		out = append(out, selfhostTypeReprToTy(env, &reprs[i], ""))
	}
	return out
}

func isInvalidPrimKind(prim PrimKind) bool {
	_, ok := prim.(*PrimKind_PkInvalid)
	return ok
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
