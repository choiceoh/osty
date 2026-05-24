package ir

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/selfhost/api"
	"github.com/osty/osty/internal/stdlib"
	"github.com/osty/osty/internal/token"
	"github.com/osty/osty/internal/types"
)

// LowerFnDecl lowers a single Osty function declaration to its IR form.
//
// Unlike Lower, which consumes a whole file and its resolver/checker
// results, LowerFnDecl is the entry point for lowering one fn body in
// isolation — for example an Osty-bodied stdlib function being injected
// alongside a user module. The caller supplies the resolve and check
// results that cover fn's body; both may be nil when unavailable, with
// the same degraded behavior as Lower (identifier kinds default to
// IdentUnknown and expression types fall back to ErrTypeVal).
//
// Returns the lowered FnDecl plus any non-fatal lowering issues. A nil
// input returns (nil, nil).
func LowerFnDecl(pkgName string, fn *ast.FnDecl, res *resolve.Result, chk *check.Result) (*FnDecl, []error) {
	if fn == nil {
		return nil, nil
	}
	l := &lowerer{
		pkgName:      pkgName,
		res:          res,
		chk:          chk,
		typeRefByPtr: buildSingleFileTypeRefMap(res),
		refByPtr:     buildSingleFileRefMap(res),
	}
	out := l.lowerFnDecl(fn)
	return out, l.issues
}

// LowerLetDecl lowers a single `pub let NAME = value` declaration.
// Mirrors LowerFnDecl in shape — the body-injection pipeline uses it
// to pull in stdlib globals that injected fn bodies reference.
//
// Returns the lowered LetDecl plus any non-fatal lowering issues. A
// nil input returns (nil, nil).
func LowerLetDecl(pkgName string, ld *ast.LetDecl, res *resolve.Result, chk *check.Result) (*LetDecl, []error) {
	if ld == nil {
		return nil, nil
	}
	l := &lowerer{
		pkgName:      pkgName,
		res:          res,
		chk:          chk,
		typeRefByPtr: buildSingleFileTypeRefMap(res),
		refByPtr:     buildSingleFileRefMap(res),
	}
	out := l.lowerLetDecl(ld)
	return out, l.issues
}

// Lower converts a type-checked Osty file into an independent IR Module.
//
// pkgName is the module's package name (e.g. "main"). res and chk may be
// nil when the caller has no resolver/checker output available — in that
// case expression types fall back to ErrTypeVal and identifier kinds are
// left as IdentUnknown, so backends that need either should pass real
// data.
//
// The returned []error is the set of non-fatal lowering issues
// encountered (unsupported constructs, missing type info); callers are
// free to ignore them or surface them via their diagnostic machinery.
// The returned Module is always non-nil — it just contains ErrorStmt /
// ErrorExpr nodes in positions that failed.
func Lower(pkgName string, file *ast.File, res *resolve.Result, chk *check.Result) (*Module, []error) {
	l := &lowerer{
		pkgName:      pkgName,
		file:         file,
		res:          res,
		chk:          chk,
		typeRefByPtr: buildSingleFileTypeRefMap(res),
		refByPtr:     buildSingleFileRefMap(res),
	}
	return l.run()
}

// LowerPackage lowers every file in a resolved package into a single
// Module. Top-level declarations from each file are concatenated in
// `pkg.Files` discovery order (lexicographic by path) so the merged
// module's `Decls` slice mirrors the package's source layout
// deterministically.
//
// pkgName names the resulting module — typically `pkg.Name` or "main"
// for binary packages. chk is the package-level check.Result (which
// already covers every file's expressions); each file's per-file
// resolve handles (`pf.Refs`, `pf.TypeRefs`, `pf.FileScope`) are
// reconstructed into a `resolve.Result` on the fly so the lowerer's
// existing per-file machinery keeps working.
//
// The Module's Span comes from the first file's span; non-fatal issues
// from every file are concatenated. A nil package returns a nil module
// and a single descriptive error so callers can distinguish "no work"
// from "successful empty lower". An empty Files slice returns an empty
// (but valid) module so the validator and downstream emitters can run.
func LowerPackage(pkgName string, pkg *resolve.Package, chk *check.Result) (*Module, []error) {
	if pkg == nil {
		return nil, []error{fmt.Errorf("ir.LowerPackage: nil package")}
	}
	mod := &Module{Package: pkgName}
	var issues []error
	// Build the package-wide pointer-keyed `TypeRefsByID` /
	// `RefsByID` shadows once so every per-file lowerer shares the
	// same view. Cross-file recovery paths (`recoverFnDeclReturnType`
	// and siblings) reach foreign-file `*ast.NamedType` /
	// `*ast.Ident` nodes whose `NodeID` collides with unrelated
	// nodes in the lowerer's current file; the pointer-keyed maps
	// disambiguate them by node identity rather than ID.
	endMaps := beginIRPhase("ir.LowerPackage.buildMaps")
	typeRefByPtr := buildPkgTypeRefMap(pkg)
	refByPtr := buildPkgRefMap(pkg)
	endMaps()
	endLoop := beginIRPhase("ir.LowerPackage.fileLoop")
	// Per-file lowering is independent — each lowerer allocates its own
	// `bindingPatTypes` / `fieldTypeCache` / `native` / `issues` state,
	// shares only read-only views of `chk`, `typeRefByPtr`, `refByPtr`,
	// and writes to its own `*Module` that we concatenate in source order
	// below. That makes the loop safe to fan out across worker goroutines.
	// On the toolchain install-self build (118 files) this trades
	// ~11.2s of serial CPU for parallel runs at GOMAXPROCS cap.
	//
	// Deterministic output order is preserved by writing into per-index
	// slots in `results` and concatenating in package-file order, so
	// downstream `ir.Monomorphize` / `Optimize` / `Validate` see the same
	// decl order as before. Single-file packages (`len(pkg.Files) <= 1`)
	// keep the serial path — the goroutine overhead would dominate.
	results := make([]fileLowerResult, len(pkg.Files))
	workers := runtime.GOMAXPROCS(0)
	if workers < 1 {
		workers = 1
	}
	if workers > len(pkg.Files) {
		workers = len(pkg.Files)
	}
	if workers <= 1 || len(pkg.Files) <= 1 {
		for i, pf := range pkg.Files {
			results[i] = lowerOneFile(pkgName, pf, chk, typeRefByPtr, refByPtr)
		}
	} else {
		var wg sync.WaitGroup
		sem := make(chan struct{}, workers)
		for i, pf := range pkg.Files {
			i, pf := i, pf
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				results[i] = lowerOneFile(pkgName, pf, chk, typeRefByPtr, refByPtr)
			}()
		}
		wg.Wait()
	}
	for i, r := range results {
		if r.mod == nil {
			continue
		}
		if i == 0 || mod.SpanV == (Span{}) {
			mod.SpanV = r.mod.SpanV
		}
		mod.Decls = append(mod.Decls, r.mod.Decls...)
		mod.Script = append(mod.Script, r.mod.Script...)
		issues = append(issues, r.issues...)
	}
	endLoop()
	return mod, issues
}

type fileLowerResult struct {
	mod    *Module
	issues []error
}

// lowerOneFile runs the per-file lowerer for one PackageFile and
// returns its module + non-fatal issues. Pulled out of LowerPackage so
// the parallel worker pool can call it without inlining a closure that
// captures loop-iteration variables. Caller is responsible for
// preserving order when merging results.
func lowerOneFile(pkgName string, pf *resolve.PackageFile, chk *check.Result, typeRefByPtr map[*ast.NamedType]*resolve.Symbol, refByPtr map[*ast.Ident]*resolve.Symbol) fileLowerResult {
	if pf == nil {
		return fileLowerResult{}
	}
	file := pf.EnsureFile()
	if file == nil {
		return fileLowerResult{}
	}
	res := &resolve.Result{
		RefsByID:      pf.RefsByID,
		TypeRefsByID:  pf.TypeRefsByID,
		RefIdents:     pf.RefIdents,
		TypeRefIdents: pf.TypeRefIdents,
		FileScope:     pf.FileScope,
	}
	l := &lowerer{
		pkgName:      pkgName,
		file:         file,
		res:          res,
		chk:          chk,
		typeRefByPtr: typeRefByPtr,
		refByPtr:     refByPtr,
	}
	mod, issues := l.run()
	return fileLowerResult{mod: mod, issues: issues}
}

// lowerer holds per-file state for one Lower call.
type lowerer struct {
	pkgName string
	file    *ast.File
	res     *resolve.Result
	chk     *check.Result

	// issues collects non-fatal issues.
	issues []error

	// bindingPatTypes records the inferred IR-side type for `let
	// p = <expr>` bindings keyed by the binding pattern node. The
	// embedded selfhost checker doesn't populate `Types[ident]` /
	// `SymTypes[sym]` for value-bound idents whose Symbol.Decl is
	// the IdentPat (rather than the LetStmt), so `lowerIdent`'s
	// last-resort fallback consults this map by walking the symbol
	// to its IdentPat and looking up the type the lowerer recorded
	// when it processed the LetStmt earlier in the same function.
	bindingPatTypes map[*ast.IdentPat]Type

	// bindingPatTypesByOffset mirrors `bindingPatTypes` keyed by the
	// IdentPat's source-position offset. Backend-emit paths reparse
	// the merged package source (`cmd/osty/main.go::parseGenEmitFile`)
	// before lowering, producing a NEW AST whose IdentPat pointers
	// don't match the OLD AST that the resolve.Result was built
	// against. The pointer-keyed map then misses every lookup whose
	// `sym.Decl` is from the old AST. Falling back to position offset
	// re-aligns the two ASTs across the reparse boundary — the merged
	// source text is identical, so offsets are stable.
	bindingPatTypesByOffset map[int]Type

	// fieldTypeCache memoises (typeName, fieldName) → field IR Type
	// resolutions for `recoverFieldType`. Without the memo, every
	// field access on a partial struct re-walks the file's decls
	// looking for the matching StructDecl.
	fieldTypeCache map[fieldKey]Type

	// native caches structured selfhost checker facts for this lowering pass.
	native nativeCheckCache

	// typeRefByPtr is a pointer-keyed shadow of `res.TypeRefsByID`.
	// `TypeRefsByID` is per-file and keyed by `ast.NodeID`, which is
	// only unique within one `*ast.File`. When the lowerer crosses
	// into a foreign file's AST (e.g. via
	// `recoverFnDeclReturnType`'s `sym.Decl.(*ast.FnDecl).ReturnType`
	// walk), the ID-keyed lookup against the current file's
	// `res.TypeRefsByID` collides with whatever node happens to
	// share the same `NodeID` in the current file. The
	// pointer-keyed map keys by the `*ast.NamedType` itself, so
	// foreign-file NamedTypes route to the correct symbol regardless
	// of the lowerer's "current file" context. Populated by
	// `LowerPackage` and per-file `Lower` from each file's
	// `pf.TypeRefIdents` / `pf.TypeRefsByID` pair.
	typeRefByPtr map[*ast.NamedType]*resolve.Symbol

	// refByPtr is the `RefsByID` twin of `typeRefByPtr`. Same
	// rationale: `RefsByID` is keyed by file-local `NodeID`, so a
	// recovery path that follows a `sym.Decl` into a foreign file
	// and then walks the foreign body / signature for `*ast.Ident`
	// references collides on shared IDs across files. The bridge
	// already pairs each file's `*ast.Ident` with the symbol it
	// resolves to (via `pf.RefIdents` + `pf.RefsByID`); we shadow
	// that with pointer-identity here so cross-file walks read the
	// right symbol regardless of the lowerer's current `res`.
	refByPtr map[*ast.Ident]*resolve.Symbol
}

// ==== Top level ====

func (l *lowerer) run() (*Module, []error) {
	mod := &Module{
		Package: l.pkgName,
		SpanV:   l.fileSpan(),
	}
	for _, u := range l.file.Uses {
		if lowered := l.lowerDecl(u); lowered != nil {
			mod.Decls = append(mod.Decls, lowered)
		}
	}
	for _, d := range l.file.Decls {
		if lowered := l.lowerDecl(d); lowered != nil {
			mod.Decls = append(mod.Decls, lowered)
		}
	}
	for _, s := range l.file.Stmts {
		mod.Script = append(mod.Script, l.lowerStmt(s))
	}
	return mod, l.issues
}

func (l *lowerer) fileSpan() Span {
	if l.file == nil {
		return Span{}
	}
	return Span{Start: posFromToken(l.file.PosV), End: posFromToken(l.file.EndV)}
}

// note records a non-fatal issue.
func (l *lowerer) note(format string, args ...any) {
	l.issues = append(l.issues, fmt.Errorf(format, args...))
}

// ==== Declarations ====

func (l *lowerer) lowerDecl(d ast.Decl) Decl {
	switch d := d.(type) {
	case *ast.FnDecl:
		// Methods on types are materialised inside their owning struct
		// or enum declaration. Skip them at the top level; the owner's
		// lowering picks them up through the AST's Methods slice.
		if d.Recv != nil {
			return nil
		}
		return l.lowerFnDecl(d)
	case *ast.StructDecl:
		return l.lowerStructDecl(d)
	case *ast.EnumDecl:
		return l.lowerEnumDecl(d)
	case *ast.LetDecl:
		return l.lowerLetDecl(d)
	case *ast.UseDecl:
		return l.lowerUseDecl(d)
	case *ast.InterfaceDecl:
		return l.lowerInterfaceDecl(d)
	case *ast.TypeAliasDecl:
		return l.lowerTypeAliasDecl(d)
	}
	l.note("unsupported top-level decl %T", d)
	return nil
}

func (l *lowerer) lowerFnDecl(fn *ast.FnDecl) *FnDecl {
	_, vecWidth, vecScalable, vecPredicate := extractVectorizeArgs(fn.Annotations)
	unrollEnable, unrollCount := extractUnrollArgs(fn.Annotations)
	noVec := hasNamedAnnotation(fn.Annotations, "no_vectorize")
	out := &FnDecl{
		Name:         fn.Name,
		Return:       l.lowerType(fn.ReturnType),
		ReceiverMut:  fn.Recv != nil && fn.Recv.Mut,
		Exported:     fn.Pub,
		SpanV:        nodeSpan(fn),
		ExportSymbol: extractExportSymbol(fn.Annotations),
		CABI:         hasNamedAnnotation(fn.Annotations, "c_abi"),
		IsIntrinsic:  hasNamedAnnotation(fn.Annotations, "intrinsic"),
		NoAlloc:      hasNamedAnnotation(fn.Annotations, "no_alloc"),
		// v0.6 A5.2: vectorize is default-on. `#[no_vectorize]` is
		// the sole way to opt out.
		Vectorize:          !noVec,
		NoVectorize:        noVec,
		VectorizeWidth:     vecWidth,
		VectorizeScalable:  vecScalable,
		VectorizePredicate: vecPredicate,
		Parallel:           hasNamedAnnotation(fn.Annotations, "parallel"),
		Unroll:             unrollEnable,
		UnrollCount:        unrollCount,
		InlineMode:         extractInlineMode(fn.Annotations),
		Hot:                hasNamedAnnotation(fn.Annotations, "hot"),
		Cold:               hasNamedAnnotation(fn.Annotations, "cold"),
		TargetFeatures:     extractTargetFeatures(fn.Annotations),
		Pure:               hasNamedAnnotation(fn.Annotations, "pure"),
	}
	out.NoaliasAll, out.NoaliasParams = extractNoaliasArgs(fn.Annotations)
	if out.Return == nil {
		out.Return = TUnit
	}
	for _, gp := range fn.Generics {
		out.Generics = append(out.Generics, l.lowerTypeParam(gp, fn.Name))
	}
	for _, p := range fn.Params {
		out.Params = append(out.Params, l.lowerParam(p))
	}
	if fn.Body != nil {
		out.Body = l.lowerBlock(fn.Body)
	}
	// Return-position closure inference: `fn f() -> fn(Int) -> Int { |x| x + 1 }`
	// — the trailing-expression closure inherits its expected
	// signature from the declared return type. Without this,
	// the closure's `Params[i].Type` stays nil and the body's
	// Idents reading those params lower with ErrTypeVal.
	// `lowerClosure` already handles the equivalent let-with-
	// annotation case via `out.T = l.exprType(c)` when the
	// checker captures it, but the return-position case is
	// usually missing because the checker doesn't run on the
	// builtin-routed Closure node.
	if fnT, ok := out.Return.(*FnType); ok && fnT != nil {
		backfillTrailingClosure(out.Body, fnT)
	}
	return out
}

// backfillTrailingClosure refines a function body's trailing
// closure expression with an expected fn signature. The trailing
// closure can live in either `Block.Result` (when the body's
// final expression is captured as the block's implicit result)
// or the last `ExprStmt` of `Block.Stmts` (when the parser
// preserved it as a statement-position trailing expression — the
// common case for single-expr `fn` bodies). Both shapes lower to
// the same closure value; both need the backfill.
func backfillTrailingClosure(body *Block, expected *FnType) {
	if body == nil || expected == nil {
		return
	}
	if body.Result != nil {
		backfillExprIfClosure(body.Result, expected)
	}
	if n := len(body.Stmts); n > 0 {
		switch s := body.Stmts[n-1].(type) {
		case *ExprStmt:
			if s != nil {
				backfillExprIfClosure(s.X, expected)
			}
		case *IfStmt:
			// Trailing `if/else` at statement position used as
			// the implicit return — both branches' trailing
			// closures take the same expected fn type.
			if s == nil {
				return
			}
			backfillTrailingClosure(s.Then, expected)
			backfillTrailingClosure(s.Else, expected)
		case *MatchStmt:
			if s == nil {
				return
			}
			for _, arm := range s.Arms {
				if arm == nil || arm.Body == nil {
					continue
				}
				backfillTrailingClosure(arm.Body, expected)
			}
		case *ReturnStmt:
			if s == nil {
				return
			}
			backfillExprIfClosure(s.Value, expected)
		}
	}
}

// backfillExprIfClosure descends into expression shapes that
// preserve a single trailing value (Closure, IfExpr, MatchExpr,
// BlockExpr) and runs `backfillClosure` on any reached Closure
// node. Used by `backfillTrailingClosure` to propagate the
// expected fn signature into closures nested inside branch
// expressions, e.g.:
//
//	fn pick(b: Bool) -> fn(Int) -> Int {
//	    if b { |x| x + 1 } else { |x| x - 1 }
//	}
//
// Without the recursive walk, each branch's trailing closure
// stays with un-typed params and the lifted MIR fn signatures
// poison their bodies.
func backfillExprIfClosure(e Expr, expected *FnType) {
	if e == nil || expected == nil {
		return
	}
	switch x := e.(type) {
	case *Closure:
		if x != nil {
			backfillClosure(x, expected)
		}
	case *IfExpr:
		if x == nil {
			return
		}
		backfillTrailingClosure(x.Then, expected)
		backfillTrailingClosure(x.Else, expected)
		if x.T == nil || x.T == ErrTypeVal {
			x.T = expected
		}
	case *MatchExpr:
		if x == nil {
			return
		}
		for _, arm := range x.Arms {
			if arm == nil || arm.Body == nil {
				continue
			}
			backfillTrailingClosure(arm.Body, expected)
		}
		if x.T == nil || x.T == ErrTypeVal {
			x.T = expected
		}
	case *BlockExpr:
		if x == nil || x.Block == nil {
			return
		}
		backfillTrailingClosure(x.Block, expected)
		if x.T == nil || x.T == ErrTypeVal {
			x.T = expected
		}
	case *IfLetExpr:
		if x == nil {
			return
		}
		backfillTrailingClosure(x.Then, expected)
		backfillTrailingClosure(x.Else, expected)
		if x.T == nil || x.T == ErrTypeVal {
			x.T = expected
		}
	}
}

// extractInlineMode reads the v0.6 A8 `#[inline]` family off the
// annotation list and returns the corresponding FnDecl.InlineMode
// value. Bare `#[inline]` → InlineSoft; `#[inline(always)]` →
// InlineAlways; `#[inline(never)]` → InlineNever; absent →
// InlineNone.
func extractInlineMode(annots []*ast.Annotation) int {
	for _, a := range annots {
		if a == nil || a.Name != "inline" {
			continue
		}
		if len(a.Args) == 0 {
			return InlineSoft
		}
		for _, arg := range a.Args {
			if arg == nil {
				continue
			}
			switch arg.Key {
			case "always":
				return InlineAlways
			case "never":
				return InlineNever
			}
		}
		return InlineSoft
	}
	return InlineNone
}

// extractNoaliasArgs reads `#[noalias]` / `#[noalias(p1, p2)]` and
// returns (allParams, []paramName). Bare form → (true, nil).
// Arg-list form → (false, names). Absent → (false, nil).
func extractNoaliasArgs(annots []*ast.Annotation) (bool, []string) {
	for _, a := range annots {
		if a == nil || a.Name != "noalias" {
			continue
		}
		if len(a.Args) == 0 {
			return true, nil
		}
		names := make([]string, 0, len(a.Args))
		for _, arg := range a.Args {
			if arg == nil || arg.Key == "" {
				continue
			}
			names = append(names, arg.Key)
		}
		return false, names
	}
	return false, nil
}

// extractTargetFeatures reads `#[target_feature(...)]` and returns
// each bare-identifier argument in source order, skipping empty
// keys. The resolver has already rejected malformed arg shapes, so
// we can assume well-formed names here.
func extractTargetFeatures(annots []*ast.Annotation) []string {
	var out []string
	for _, a := range annots {
		if a == nil || a.Name != "target_feature" {
			continue
		}
		for _, arg := range a.Args {
			if arg == nil || arg.Key == "" {
				continue
			}
			out = append(out, arg.Key)
		}
	}
	return out
}

// extractExportSymbol reads the `#[export("name")]` annotation from a
// declaration's annotation list (LANG_SPEC §19.6). Returns the empty
// string when the annotation is absent or malformed; the resolver's
// arg validator (`checkExportArgs` in internal/resolve) is the
// authoritative place that rejects a malformed `#[export]`, so at
// this point in the pipeline we only pick up the well-formed cases.
// hasNamedAnnotation reports whether `annots` contains an annotation
// with the given name. Used by `lowerFnDecl` for bare-flag annotations
// (`#[c_abi]`, `#[no_alloc]`, `#[intrinsic]`) where presence alone is
// the signal — argument shape is the resolver's responsibility.
func hasNamedAnnotation(annots []*ast.Annotation, name string) bool {
	for _, a := range annots {
		if a != nil && a.Name == name {
			return true
		}
	}
	return false
}

// extractVectorizeArgs reads `#[vectorize(...)]` metadata from the
// annotation list and returns (enable, width, scalable, predicate).
// `enable` tracks presence of the annotation regardless of args; the
// three other flags are set from the arg list the resolver already
// validated, so we can assume well-formed shape here.
func extractVectorizeArgs(annots []*ast.Annotation) (enable bool, width int, scalable, predicate bool) {
	for _, a := range annots {
		if a == nil || a.Name != "vectorize" {
			continue
		}
		enable = true
		for _, arg := range a.Args {
			if arg == nil {
				continue
			}
			switch arg.Key {
			case "scalable":
				scalable = true
			case "predicate":
				predicate = true
			case "width":
				if lit, ok := arg.Value.(*ast.IntLit); ok {
					if v, ok := parseAnnotationInt(lit.Text); ok {
						width = v
					}
				}
			}
		}
	}
	return
}

// extractUnrollArgs reads `#[unroll]` / `#[unroll(count = N)]` from
// the annotation list and returns (enable, count). `count == 0` means
// the bare form; a positive value means the fixed factor.
func extractUnrollArgs(annots []*ast.Annotation) (enable bool, count int) {
	for _, a := range annots {
		if a == nil || a.Name != "unroll" {
			continue
		}
		enable = true
		for _, arg := range a.Args {
			if arg == nil || arg.Key != "count" {
				continue
			}
			if lit, ok := arg.Value.(*ast.IntLit); ok {
				if v, ok := parseAnnotationInt(lit.Text); ok {
					count = v
				}
			}
		}
	}
	return
}

// parseAnnotationInt parses a decimal/hex/octal/binary integer literal
// text (with optional underscore separators) into an int. Returns
// (value, true) only for non-negative values that fit in int.
func parseAnnotationInt(text string) (int, bool) {
	text = strings.ReplaceAll(text, "_", "")
	base := 10
	switch {
	case strings.HasPrefix(text, "0x"), strings.HasPrefix(text, "0X"):
		base = 16
		text = text[2:]
	case strings.HasPrefix(text, "0o"), strings.HasPrefix(text, "0O"):
		base = 8
		text = text[2:]
	case strings.HasPrefix(text, "0b"), strings.HasPrefix(text, "0B"):
		base = 2
		text = text[2:]
	}
	if text == "" {
		return 0, false
	}
	v, err := strconv.ParseInt(text, base, 64)
	if err != nil || v < 0 || v > int64(^uint(0)>>1) {
		return 0, false
	}
	return int(v), true
}

func extractExportSymbol(annots []*ast.Annotation) string {
	for _, a := range annots {
		if a == nil || a.Name != "export" {
			continue
		}
		if len(a.Args) != 1 {
			continue
		}
		arg := a.Args[0]
		if arg == nil || arg.Key != "" || arg.Value == nil {
			continue
		}
		lit, ok := arg.Value.(*ast.StringLit)
		if !ok {
			continue
		}
		var buf []byte
		for _, p := range lit.Parts {
			if !p.IsLit {
				// Interpolation is a resolver error; ignore here.
				return ""
			}
			buf = append(buf, p.Lit...)
		}
		return string(buf)
	}
	return ""
}

// jsonFieldOptions extracts `#[json(...)]` metadata from a struct
// field's annotation list. Returns (key, skip, optional) where an
// empty key means "use the field name". The resolver has already
// validated argument shape (E0407) and that `optional` only attaches
// to Option-typed fields (E0408), so this helper accepts any
// argument form silently — malformed inputs cannot reach IR.
func jsonFieldOptions(annots []*ast.Annotation) (key string, skip, optional bool) {
	for _, a := range annots {
		if a == nil || a.Name != "json" {
			continue
		}
		for _, arg := range a.Args {
			switch arg.Key {
			case "key":
				if s, ok := annotationStringLit(arg.Value); ok {
					key = s
				}
			case "skip":
				skip = true
			case "optional":
				optional = true
			}
		}
	}
	return key, skip, optional
}

// jsonVariantOptions extracts `#[json(...)]` metadata from an enum
// variant's annotation list. Returns (tag, skip); empty tag means
// "use the variant name". Only `key` and `skip` are defined for
// variants — `optional` is a field-level knob.
func jsonVariantOptions(annots []*ast.Annotation) (tag string, skip bool) {
	for _, a := range annots {
		if a == nil || a.Name != "json" {
			continue
		}
		for _, arg := range a.Args {
			switch arg.Key {
			case "key":
				if s, ok := annotationStringLit(arg.Value); ok {
					tag = s
				}
			case "skip":
				skip = true
			}
		}
	}
	return tag, skip
}

// annotationStringLit concatenates the literal parts of a string
// literal `ast.Expr`. Interpolation segments force a false return
// since the resolver forbids non-literal annotation arguments.
func annotationStringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.StringLit)
	if !ok {
		return "", false
	}
	var buf []byte
	for _, p := range lit.Parts {
		if !p.IsLit {
			return "", false
		}
		buf = append(buf, p.Lit...)
	}
	return string(buf), true
}

func (l *lowerer) lowerParam(p *ast.Param) *Param {
	out := &Param{
		Type:  l.lowerType(p.Type),
		SpanV: nodeSpan(p),
	}
	if p.Name != "" {
		out.Name = p.Name
	} else if p.Pattern != nil {
		out.Pattern = l.lowerPattern(p.Pattern)
	}
	if p.Default != nil {
		out.Default = l.lowerExpr(p.Default)
	}
	return out
}

func (l *lowerer) lowerTypeParam(gp *ast.GenericParam, owner string) *TypeParam {
	out := &TypeParam{Name: gp.Name, SpanV: nodeSpan(gp)}
	for _, c := range gp.Constraints {
		if t := l.lowerType(c); t != nil {
			out.Bounds = append(out.Bounds, t)
		}
	}
	return out
}

func (l *lowerer) lowerStructDecl(sd *ast.StructDecl) *StructDecl {
	out := &StructDecl{
		Name:     sd.Name,
		Exported: sd.Pub,
		SpanV:    nodeSpan(sd),
		Pod:      hasNamedAnnotation(sd.Annotations, "pod"),
		ReprC:    hasNamedAnnotation(sd.Annotations, "repr"),
	}
	for _, gp := range sd.Generics {
		out.Generics = append(out.Generics, l.lowerTypeParam(gp, sd.Name))
	}
	for _, f := range sd.Fields {
		field := &Field{
			Name:     f.Name,
			Type:     l.lowerType(f.Type),
			Exported: f.Pub,
			SpanV:    nodeSpan(f),
		}
		if f.Default != nil {
			field.Default = l.lowerExpr(f.Default)
		}
		field.JSONKey, field.JSONSkip, field.JSONOptional = jsonFieldOptions(f.Annotations)
		out.Fields = append(out.Fields, field)
	}
	for _, m := range sd.Methods {
		out.Methods = append(out.Methods, l.lowerFnDecl(m))
	}
	info := check.ClassifyBuilderDerive(sd)
	out.BuilderDerivable, out.BuilderRequiredFields = info.Derivable, info.Required
	return out
}

func (l *lowerer) lowerEnumDecl(ed *ast.EnumDecl) *EnumDecl {
	out := &EnumDecl{
		Name:     ed.Name,
		Exported: ed.Pub,
		SpanV:    nodeSpan(ed),
	}
	for _, gp := range ed.Generics {
		out.Generics = append(out.Generics, l.lowerTypeParam(gp, ed.Name))
	}
	for _, v := range ed.Variants {
		variant := &Variant{Name: v.Name, SpanV: nodeSpan(v)}
		for _, ty := range v.Fields {
			variant.Payload = append(variant.Payload, l.lowerType(ty))
		}
		variant.JSONTag, variant.JSONSkip = jsonVariantOptions(v.Annotations)
		out.Variants = append(out.Variants, variant)
	}
	for _, m := range ed.Methods {
		out.Methods = append(out.Methods, l.lowerFnDecl(m))
	}
	return out
}

func (l *lowerer) lowerLetDecl(ld *ast.LetDecl) *LetDecl {
	out := &LetDecl{
		Name:     ld.Name,
		Mut:      ld.Mut,
		Exported: ld.Pub,
		SpanV:    nodeSpan(ld),
	}
	if ld.Type != nil {
		out.Type = l.lowerType(ld.Type)
	}
	if ld.Value != nil {
		out.Value = l.lowerExpr(ld.Value)
		if out.Type == nil {
			out.Type = out.Value.Type()
		}
	}
	if !usableRecoveredType(out.Type) {
		if t := l.nativeSymbolTypeForNode(ld, ld.Name); usableRecoveredType(t) {
			out.Type = t
		}
	}
	return out
}

// ==== Types ====

// lowerType converts an AST Type to IR Type. Returns nil when the input
// is nil (caller substitutes TUnit when that matters).
func (l *lowerer) lowerType(t ast.Type) Type {
	if t == nil {
		return nil
	}
	switch t := t.(type) {
	case *ast.NamedType:
		return l.lowerNamedType(t)
	case *ast.OptionalType:
		return &OptionalType{Inner: l.lowerType(t.Inner)}
	case *ast.TupleType:
		elems := make([]Type, len(t.Elems))
		for i, e := range t.Elems {
			elems[i] = l.lowerType(e)
		}
		return &TupleType{Elems: elems}
	case *ast.FnType:
		params := make([]Type, len(t.Params))
		for i, p := range t.Params {
			params[i] = l.lowerType(p)
		}
		ret := l.lowerType(t.ReturnType)
		if ret == nil {
			ret = TUnit
		}
		return &FnType{Params: params, Return: ret}
	}
	l.note("unsupported type node %T", t)
	return ErrTypeVal
}

// lowerStdlibType lowers a type AST that originated from a stdlib stub.
// Stub node IDs collide with the current file's resolver mappings, so
// we temporarily disable resolver lookup and fall back to pure AST
// shape-based lowering.
func (l *lowerer) lowerStdlibType(t ast.Type) Type {
	saved := l.res
	l.res = nil
	defer func() { l.res = saved }()
	return l.lowerType(t)
}

// lowerNamedType resolves a NamedType to either a primitive, an IR
// NamedType (with Builtin flag populated from the resolver when
// available), or a TypeVar for generic parameter references.
func (l *lowerer) lowerNamedType(nt *ast.NamedType) Type {
	name := nt.Path[len(nt.Path)-1]
	pkg := ""
	if len(nt.Path) > 1 {
		pkg = joinDottedPath(nt.Path[:len(nt.Path)-1])
	}

	// Primitive scalars short-circuit — only when bare (no qualifier, no
	// type args).
	if pkg == "" && len(nt.Args) == 0 {
		if p := primitiveByName(name); p != nil {
			return p
		}
	}

	args := make([]Type, len(nt.Args))
	for i, a := range nt.Args {
		args[i] = l.lowerType(a)
	}

	// Consult the resolver for the head symbol so we can classify
	// builtins vs user declarations vs generic parameters. The
	// pointer-keyed `typeRef` lookup is safe across files (see
	// `typeRefByPtr`), so the previous defensive name-match guard
	// added in PR #1919 is no longer needed — cross-file leaks via
	// `recoverFnDeclReturnType` and siblings used to surface as
	// wrong-name symbols here, but the package-wide pointer map
	// disambiguates them by node identity.
	sym := l.typeRef(nt)
	if sym != nil {
		symName := sym.Name
		if pkg != "" && len(nt.Path) > 0 && symName == nt.Path[0] {
			symName = name
		}
		switch sym.Kind {
		case resolve.SymBuiltin:
			if len(nt.Args) == 0 {
				if p := primitiveByName(symName); p != nil {
					return p
				}
			}
			return &NamedType{Package: pkg, Name: symName, Args: args, Builtin: true}
		case resolve.SymGeneric:
			return &TypeVar{Name: symName, Owner: ""}
		case resolve.SymTypeAlias:
			// Unwrap the alias at IR construction time. Without this,
			// downstream passes (MIR generator's typeSupported,
			// mono's substitution) see the user-declared name as an
			// opaque NamedType with no layout, and fail with
			// `unsupported local type <alias>`. Aliases are pure
			// syntactic sugar in Osty (§3.A type system), so the
			// target type is semantically identical — follow it.
			if aliasDecl, ok := sym.Decl.(*ast.TypeAliasDecl); ok && aliasDecl != nil && aliasDecl.Target != nil {
				return l.lowerType(aliasDecl.Target)
			}
		}
		return &NamedType{Package: pkg, Name: symName, Args: args}
	}

	// No resolver data available — best effort on the source name.
	if pkg == "" {
		switch name {
		case "List", "Map", "Set", "Option", "Result":
			return &NamedType{Package: "", Name: name, Args: args, Builtin: true}
		}
	}
	return &NamedType{Package: pkg, Name: name, Args: args}
}

// joinDottedPath joins a non-empty string slice with '.'.
func joinDottedPath(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += "." + p
	}
	return out
}

func (l *lowerer) typeRef(nt *ast.NamedType) *resolve.Symbol {
	if nt == nil {
		return nil
	}
	// `l.res == nil` is the "bypass the resolver entirely" signal
	// (see `lowerStdlibType`, which sets it temporarily so stdlib
	// stub nodes lower via pure AST-shape classification). Honour
	// that contract by skipping the pointer-keyed map too —
	// otherwise a stdlib stub nt that happens to be registered in
	// `typeRefByPtr` (e.g. when the outer lowerer was built from a
	// stdlib `*resolve.Result`) would still receive a resolver
	// answer.
	if l.res == nil {
		return nil
	}
	// Prefer the pointer-keyed map when populated: it is immune to
	// `NodeID` collisions across files, so foreign-file NamedTypes
	// reached via `sym.Decl` walks (see `recoverFnDeclReturnType`)
	// resolve to the correct symbol even though the lowerer's
	// current `res` covers a different file.
	if l.typeRefByPtr != nil {
		if sym, ok := l.typeRefByPtr[nt]; ok {
			return sym
		}
	}
	return l.res.TypeRefsByID[nt.ID]
}

// buildPkgTypeRefMap assembles a pointer-keyed `*ast.NamedType` →
// `*resolve.Symbol` map from every file in pkg. The map short-
// circuits the ID-keyed lookup against the lowerer's current
// `res.TypeRefsByID`, which fails when a foreign-file recovery
// (`recoverFnDeclReturnType` and siblings) reaches a NamedType
// whose `NodeID` collides with an unrelated node in the lowerer's
// current file. Each `pf` carries `TypeRefIdents` as the canonical
// list of resolved NamedTypes alongside `TypeRefsByID`; we walk
// that slice once and pair every node pointer with the symbol
// recorded for it.
func buildPkgTypeRefMap(pkg *resolve.Package) map[*ast.NamedType]*resolve.Symbol {
	if pkg == nil {
		return nil
	}
	out := map[*ast.NamedType]*resolve.Symbol{}
	for _, pf := range pkg.Files {
		if pf == nil {
			continue
		}
		for _, nt := range pf.TypeRefIdents {
			if nt == nil {
				continue
			}
			if sym := pf.TypeRefsByID[nt.ID]; sym != nil {
				out[nt] = sym
			}
		}
	}
	return out
}

// buildSingleFileTypeRefMap is the single-file analogue of
// `buildPkgTypeRefMap`. Same shape, fed directly from a
// `*resolve.Result` rather than a Package.
func buildSingleFileTypeRefMap(res *resolve.Result) map[*ast.NamedType]*resolve.Symbol {
	if res == nil {
		return nil
	}
	out := map[*ast.NamedType]*resolve.Symbol{}
	for _, nt := range res.TypeRefIdents {
		if nt == nil {
			continue
		}
		if sym := res.TypeRefsByID[nt.ID]; sym != nil {
			out[nt] = sym
		}
	}
	return out
}

// ref returns the resolver symbol bound to id, preferring the
// pointer-keyed `refByPtr` shadow over the ID-keyed
// `res.RefsByID` so cross-file recovery paths (a `sym.Decl` walk
// that lands in a foreign file's AST and then encounters a
// foreign-file `*ast.Ident`) do not collide on shared `NodeID`s
// with unrelated idents in the lowerer's current file. The
// `l.res == nil` short-circuit honours `lowerStdlibType`'s "bypass
// the resolver entirely" contract, mirroring `typeRef`.
func (l *lowerer) ref(id *ast.Ident) *resolve.Symbol {
	if id == nil || l.res == nil {
		return nil
	}
	if l.refByPtr != nil {
		if sym, ok := l.refByPtr[id]; ok {
			return sym
		}
	}
	return l.res.RefsByID[id.ID]
}

// buildPkgRefMap is the `*ast.Ident` twin of `buildPkgTypeRefMap`:
// walks every file's `RefIdents` once and pairs each resolved
// ident's pointer with the symbol the bridge recorded for it.
// Used by `LowerPackage` so cross-file `sym.Decl` walks read the
// foreign ident's resolver answer rather than colliding on
// node-ID inside the lowerer's current file.
func buildPkgRefMap(pkg *resolve.Package) map[*ast.Ident]*resolve.Symbol {
	if pkg == nil {
		return nil
	}
	out := map[*ast.Ident]*resolve.Symbol{}
	for _, pf := range pkg.Files {
		if pf == nil {
			continue
		}
		for _, id := range pf.RefIdents {
			if id == nil {
				continue
			}
			if sym := pf.RefsByID[id.ID]; sym != nil {
				out[id] = sym
			}
		}
	}
	return out
}

// buildSingleFileRefMap is the single-file analogue of
// `buildPkgRefMap`. Same shape, fed directly from a
// `*resolve.Result` rather than a Package.
func buildSingleFileRefMap(res *resolve.Result) map[*ast.Ident]*resolve.Symbol {
	if res == nil {
		return nil
	}
	out := map[*ast.Ident]*resolve.Symbol{}
	for _, id := range res.RefIdents {
		if id == nil {
			continue
		}
		if sym := res.RefsByID[id.ID]; sym != nil {
			out[id] = sym
		}
	}
	return out
}

// primitiveByName maps a scalar type name to the IR singleton.
func primitiveByName(name string) *PrimType {
	switch name {
	case "Int":
		return TInt
	case "Int8":
		return TInt8
	case "Int16":
		return TInt16
	case "Int32":
		return TInt32
	case "Int64":
		return TInt64
	case "UInt8":
		return TUInt8
	case "UInt16":
		return TUInt16
	case "UInt32":
		return TUInt32
	case "UInt64":
		return TUInt64
	case "Byte":
		return TByte
	case "Float":
		return TFloat
	case "Float32":
		return TFloat32
	case "Float64":
		return TFloat64
	case "Bool":
		return TBool
	case "Char":
		return TChar
	case "String":
		return TString
	case "Bytes":
		return TBytes
	case "RawPtr":
		return TRawPtr
	}
	return nil
}

// fromCheckerType converts a checker types.Type into an IR Type. Used
// when lowering expressions where the checker's inferred type is the
// authoritative source.
func (l *lowerer) fromCheckerType(t types.Type) Type {
	if t == nil {
		return nil
	}
	switch t := t.(type) {
	case *types.Primitive:
		if p := primitiveByKind(t.Kind); p != nil {
			return p
		}
		return ErrTypeVal
	case *types.Untyped:
		return l.fromCheckerType(t.Default())
	case *types.Tuple:
		elems := make([]Type, len(t.Elems))
		for i, e := range t.Elems {
			elems[i] = l.fromCheckerType(e)
		}
		return &TupleType{Elems: elems}
	case *types.Optional:
		return &OptionalType{Inner: l.fromCheckerType(t.Inner)}
	case *types.FnType:
		params := make([]Type, len(t.Params))
		for i, p := range t.Params {
			params[i] = l.fromCheckerType(p)
		}
		ret := l.fromCheckerType(t.Return)
		if ret == nil {
			ret = TUnit
		}
		return &FnType{Params: params, ParamNames: t.ParamNames, Return: ret}
	case *types.Named:
		name := "?"
		builtin := false
		pkg := ""
		if t.Sym != nil {
			name = t.Sym.Name
			builtin = t.Sym.Kind == resolve.SymBuiltin
			if t.Sym.Package != nil {
				pkg = t.Sym.Package.Name
			}
			// Unwrap type aliases at checker → IR boundary. Mirrors the
			// same unwrap in lowerNamedType (AST → IR): without it, any
			// expression whose checker-inferred type is a user alias
			// (e.g. `CheckName = String`) produces a NamedType that
			// MIR's typeSupported rejects with
			// `unsupported local type <alias>`. Aliases are pure
			// syntactic sugar — the target type is semantically
			// identical.
			if t.Sym.Kind == resolve.SymTypeAlias {
				if aliasDecl, ok := t.Sym.Decl.(*ast.TypeAliasDecl); ok && aliasDecl != nil && aliasDecl.Target != nil {
					return l.lowerType(aliasDecl.Target)
				}
			}
		}
		args := make([]Type, len(t.Args))
		for i, a := range t.Args {
			args[i] = l.fromCheckerType(a)
		}
		return &NamedType{Package: pkg, Name: name, Args: args, Builtin: builtin}
	case *types.TypeVar:
		name := "?"
		if t.Sym != nil {
			name = t.Sym.Name
		}
		return &TypeVar{Name: name}
	case *types.Error:
		return ErrTypeVal
	}
	l.note("unsupported checker type %T", t)
	return ErrTypeVal
}

func primitiveByKind(k types.PrimitiveKind) *PrimType {
	switch k {
	case types.PInt:
		return TInt
	case types.PInt8:
		return TInt8
	case types.PInt16:
		return TInt16
	case types.PInt32:
		return TInt32
	case types.PInt64:
		return TInt64
	case types.PUInt8:
		return TUInt8
	case types.PUInt16:
		return TUInt16
	case types.PUInt32:
		return TUInt32
	case types.PUInt64:
		return TUInt64
	case types.PByte:
		return TByte
	case types.PFloat:
		return TFloat
	case types.PFloat32:
		return TFloat32
	case types.PFloat64:
		return TFloat64
	case types.PBool:
		return TBool
	case types.PChar:
		return TChar
	case types.PString:
		return TString
	case types.PBytes:
		return TBytes
	case types.PRawPtr:
		return TRawPtr
	case types.PUnit:
		return TUnit
	case types.PNever:
		return TNever
	}
	return nil
}

// ==== Blocks and statements ====

func (l *lowerer) lowerBlock(b *ast.Block) *Block {
	out := &Block{SpanV: nodeSpan(b)}
	if len(b.Stmts) == 0 {
		return out
	}
	// The block's "result" is the final expression statement, if any,
	// and if its type is not unit. This matches the checker's view of
	// block-as-expression and lets backends omit an extra Go statement
	// when no value is implicitly returned.
	last := b.Stmts[len(b.Stmts)-1]
	lead := b.Stmts[:len(b.Stmts)-1]
	for _, s := range lead {
		out.Stmts = append(out.Stmts, l.lowerStmt(s))
	}
	if es, ok := last.(*ast.ExprStmt); ok && l.expressionYieldsValue(es.X) {
		out.Result = l.lowerExpr(es.X)
		return out
	}
	out.Stmts = append(out.Stmts, l.lowerStmt(last))
	return out
}

// expressionYieldsValue reports whether treating the expression as a
// block-final implicit result is appropriate: the checker assigned it
// a non-unit type, or — when the checker didn't cover this surface
// — its syntactic shape unambiguously evaluates to a value.
func (l *lowerer) expressionYieldsValue(e ast.Expr) bool {
	if l.chk == nil {
		return false
	}
	if t := l.exprType(e); usableRecoveredType(t) {
		if expressionTypeYieldsValue(t) {
			return true
		}
	}
	if t := l.bindingTypeFromAST(e); expressionTypeYieldsValue(t) {
		return true
	}
	switch e.(type) {
	case *ast.StructLit, *ast.IntLit, *ast.FloatLit, *ast.StringLit,
		*ast.CharLit, *ast.BoolLit, *ast.ListExpr, *ast.MapExpr,
		*ast.TupleExpr, *ast.RangeExpr:
		return true
	case *ast.BinaryExpr, *ast.UnaryExpr, *ast.FieldExpr, *ast.IndexExpr,
		*ast.QuestionExpr, *ast.TurbofishExpr:
		// These expression shapes always yield a value in Osty (they
		// have no statement form). When SemanticDB type lookup misses
		// (post-#1645, both byID and the no-op byKey lookup fail for
		// these kinds) the type-driven branch above returns nil and we
		// rely on syntactic shape to keep the trailing expression as
		// the block's Result rather than a discarded ExprStmt. Without
		// this case `fn f(p: Point) -> Int { p.x + p.y }` lowers with
		// `Body.Result = nil` and MIR emits an UnreachableTerm, which
		// breaks stage0 patterns P15-P19, P23 and TestStage0RealMIRBaseline.
		return true
	case *ast.ParenExpr:
		if px, ok := e.(*ast.ParenExpr); ok && px.X != nil {
			return l.expressionYieldsValue(px.X)
		}
		return false
	case *ast.IfExpr:
		return astIfLooksLikeValueExpr(e)
	case *ast.MatchExpr:
		return astMatchLooksLikeValueExpr(e)
	case *ast.Ident:
		if astIdentLooksLikeValueConstructor(e) {
			return true
		}
		// Trailing bare ident (`xs` after building a list, `x`
		// returning a parameter): a binding/parameter reference is
		// always a value. Without this branch
		//   fn build() -> List<Int> { let mut xs = []; xs.push(1); xs }
		// drops the trailing `xs` to ExprStmt because the type-driven
		// path can't see `xs`'s inferred type (`let mut xs = []` is
		// type-inferred and post-#1645 the SemanticDB lookup misses on
		// AST Ident nodes whose ID doesn't match the selfhost arena id).
		// Limit to bindings/parameters — top-level fn references stay
		// in the constructor-only branch above to avoid promoting
		// `let f = helper; f` style indirect-call boilerplate.
		if id, ok := e.(*ast.Ident); ok && id != nil && l.res != nil {
			if sym := l.ref(id); sym != nil {
				switch sym.Kind {
				case resolve.SymLet, resolve.SymParam:
					return true
				}
			}
		}
		return false
	case *ast.CallExpr:
		// CallExpr return type may be unit (e.g. `println(...)`),
		// in which case the trailing call should stay an ExprStmt
		// rather than promote to the block's Result — promoting it
		// changes the MIR shape (Stmt-positioned IntrinsicInstr vs
		// expression-positioned), breaking stage0 patterns that key
		// on `println`-as-stmt.
		if astCallLooksLikeValueConstructor(e) {
			return true
		}
		// Method calls (`recv.name(...)`) on user-defined types: look
		// up the method's declared ReturnType via the resolver to
		// decide. Non-unit returns promote, unit-returning side-effect
		// calls (e.g. `ch.send(x)`) remain Stmts. Without this branch
		// `fn show(t: Tag) -> String { t.label() }` would lose the
		// trailing call (Body.Result=nil → MIR UnreachableTerm).
		if call, ok := e.(*ast.CallExpr); ok && call != nil {
			if fx, ok := call.Fn.(*ast.FieldExpr); ok && fx != nil {
				if rt := l.userMethodReturnTypeFromAST(fx); rt != nil && expressionTypeYieldsValue(rt) {
					return true
				}
				// Builtin container / stdlib intrinsic methods: when
				// the user-method path bails out because the receiver
				// is a builtin (`List<T>`, `Map<K,V>`, `String`,
				// `Bytes`), consult the same intrinsic table the
				// MethodCall recovery uses. Without this, trailing
				// `xs.filter(|x| ...)`, `xs.map(...)`, `s.toUpper()`
				// etc. are dropped to ExprStmt and the function
				// returns nothing — MIR UnreachableTerm.
				if rt := l.builtinMethodReturnTypeFromAST(fx); rt != nil && expressionTypeYieldsValue(rt) {
					return true
				}
				// Closure-dependent methods (`xs.fold(init, fn)`,
				// `o.map(fn)`): result type comes from an argument
				// rather than the receiver alone.
				if rt := l.closureDependentMethodReturnTypeFromAST(fx, call.Args); rt != nil && expressionTypeYieldsValue(rt) {
					return true
				}
				// `use go "X" as alias { fn Y(...) -> T }` FFI call:
				// `alias.Y(...)`. Receiver is a UseDecl alias symbol;
				// look up the listed fn's return type in the UseDecl
				// body. Without this, trailing `strings.ToUpper(...)`
				// in `fn banner(name: String) -> String { strings.
				// ToUpper(strings.Repeat(name, 2)) }` drops to
				// ExprStmt → MIR UnreachableTerm.
				if rt := l.useAliasFnReturnTypeFromAST(fx); rt != nil && expressionTypeYieldsValue(rt) {
					return true
				}
				// Enum-qualified variant call (`Color.Red(255)`,
				// `Value.IntVal(n)`): receiver Ident resolves to a
				// `SymEnum`, field name is one of the enum's variants.
				// The call returns the enum's nominal type. Without
				// this, `pub fn newInt(n: Int) -> Value { Value.IntVal(n) }`
				// drops the trailing variant call to ExprStmt → MIR
				// UnreachableTerm.
				if rt := l.enumVariantCallReturnTypeFromAST(fx); rt != nil && expressionTypeYieldsValue(rt) {
					return true
				}
				// `use std.strings` module call: `strings.toLower(s)`.
				// Receiver Ident resolves to `SymPackage` whose Decl
				// is a `*ast.UseDecl` with Path = ["std", "strings", ...].
				// Look up the fn declaration in the stdlib registry.
				if rt := l.stdModuleFnReturnTypeFromAST(fx); rt != nil && expressionTypeYieldsValue(rt) {
					return true
				}
			}
			// Free-fn calls (`helper()`): promote when the resolved
			// declaration has a non-unit return type. We restrict this
			// to top-level fn decls because:
			//   - let-bound closures (`let g = |n| n + 1; g(x)`) are
			//     correct to promote but altering this site for them
			//     measurably regresses stage0 toolchain coverage
			//     (some toolchain helpers rely on the current
			//     Stmt-positioned shape).
			//   - free-fn return types are stable (no inference), so
			//     `expressionTypeYieldsValue` decides reliably.
			if id, ok := call.Fn.(*ast.Ident); ok && id != nil {
				if rt := l.freeFnReturnTypeFromAST(id); rt != nil && expressionTypeYieldsValue(rt) {
					return true
				}
			}
		}
		return false
	}
	return false
}

// freeFnReturnTypeFromAST resolves the callee identifier of a free-fn
// call (`helper()`) or a parameter whose declared type is a function
// type (`fn apply(f: fn(Int) -> Int, x: Int) -> Int { f(x) }`) to its
// declared return type via the resolver. Used by
// `expressionYieldsValue` to decide whether a trailing call promotes
// to the block's Result. Closure / let-bound function callees are
// intentionally excluded — they parse and resolve but their bodies
// are sometimes Stmt-positioned in the toolchain, and promoting them
// regresses stage0 audit coverage.
func (l *lowerer) freeFnReturnTypeFromAST(id *ast.Ident) Type {
	if l == nil || id == nil || l.res == nil {
		return nil
	}
	sym := l.ref(id)
	if sym == nil {
		return nil
	}
	switch d := sym.Decl.(type) {
	case *ast.FnDecl:
		if d.ReturnType == nil {
			return TUnit
		}
		return l.lowerType(d.ReturnType)
	case *ast.Param:
		// Function-typed parameter — return the FnType's declared
		// return so `f(x)` at trailing position promotes.
		if ft, ok := d.Type.(*ast.FnType); ok && ft != nil {
			if ft.ReturnType == nil {
				return TUnit
			}
			return l.lowerType(ft.ReturnType)
		}
	case *ast.IdentPat:
		// Let-bound closure: `let f = || P { n: 1 }; f()`. Walk the
		// owning LetStmt to find the closure expression and lower its
		// declared / inferred return type. Limited to closures with an
		// explicit `-> T` return annotation OR a body whose tail shape
		// unambiguously yields a value (literal / struct-lit / binary
		// op). Without this, trailing `f()` calls in non-unit-return
		// fns drop to ExprStmt → MIR UnreachableTerm. Earlier rounds
		// excluded this path due to stage0 audit regressions; the
		// conservative form (only promotes when the closure body
		// shape is value-yielding) avoids those regressions.
		if cl := l.findLetClosureByPattern(d); cl != nil {
			if cl.ReturnType != nil {
				if lt := l.lowerType(cl.ReturnType); lt != nil && lt != ErrTypeVal {
					return lt
				}
			}
			switch cl.Body.(type) {
			case *ast.StructLit, *ast.TupleExpr, *ast.ListExpr, *ast.MapExpr,
				*ast.BinaryExpr, *ast.UnaryExpr, *ast.IntLit, *ast.FloatLit,
				*ast.StringLit, *ast.CharLit, *ast.BoolLit:
				return &NamedType{Name: "?closure_result"}
			}
		}
	}
	return nil
}

// closureDependentMethodReturnTypeFromAST handles the methods whose
// return type is derived from a function-typed argument rather than
// the receiver alone. Covers the common shapes:
//
//   - `xs.fold(init, fn)` → typeof(init)
//   - `xs.map(fn)`        → List<typeof(fn(x))>, approximated as List<?>
//   - `o.map(fn)`         → Option<typeof(fn(x))>, approximated as Option<?>
//
// Returns nil when the call doesn't match a known shape. Used by
// `expressionYieldsValue` to decide promotion at trailing position.
// For `.map` we return a sentinel non-unit type so promotion fires
// without committing to a wrong inner type; MIR/backend then run
// their own recovery on the closure body.
func (l *lowerer) closureDependentMethodReturnTypeFromAST(fx *ast.FieldExpr, args []*ast.Arg) Type {
	if l == nil || fx == nil {
		return nil
	}
	recvType := l.resolveExprStaticType(fx.X)
	if recvType == nil || recvType == ErrTypeVal {
		return nil
	}
	switch fx.Name {
	case "fold", "reduce":
		// `xs.fold(init, fn)`: result type = type of init.
		// `xs.reduce(fn)`: result type = element type (skip for now,
		// would need receiver introspection).
		if fx.Name == "fold" && len(args) >= 1 && args[0] != nil && args[0].Value != nil {
			if t := l.lowerExpr(args[0].Value).Type(); t != nil && t != ErrTypeVal {
				return t
			}
		}
	case "map", "flatMap":
		// `xs.map(fn)` / `o.map(fn)`: promotion-only — wrap a
		// `?closure_result` sentinel in the appropriate container so
		// `expressionTypeYieldsValue` reports true.
		switch r := recvType.(type) {
		case *NamedType:
			if r.Builtin && r.Name == "List" {
				return &NamedType{Name: "List", Builtin: true, Args: []Type{&NamedType{Name: "?closure_result"}}}
			}
			if r.Builtin && r.Name == "Result" && len(r.Args) == 2 {
				return &NamedType{Name: "Result", Builtin: true, Args: []Type{&NamedType{Name: "?closure_result"}, r.Args[1]}}
			}
		case *OptionalType:
			return &OptionalType{Inner: &NamedType{Name: "?closure_result"}}
		}
	case "zip":
		// `xs.zip(ys)`: List<T>.zip(List<U>) → List<(T, U)>.
		nt, ok := recvType.(*NamedType)
		if !ok || !nt.Builtin || nt.Name != "List" || len(nt.Args) != 1 || len(args) < 1 || args[0] == nil || args[0].Value == nil {
			return nil
		}
		other := l.lowerExpr(args[0].Value).Type()
		ont, ok := other.(*NamedType)
		if !ok || !ont.Builtin || ont.Name != "List" || len(ont.Args) != 1 {
			return nil
		}
		return &NamedType{Name: "List", Builtin: true, Args: []Type{
			&TupleType{Elems: []Type{nt.Args[0], ont.Args[0]}},
		}}
	case "mapOr":
		// `o.mapOr(default, fn)`: result type = type of default.
		if len(args) >= 1 && args[0] != nil && args[0].Value != nil {
			if t := l.lowerExpr(args[0].Value).Type(); t != nil && t != ErrTypeVal {
				return t
			}
		}
	case "mapErr":
		// `r.mapErr(fn)` on Result<T, E> → Result<T, ?closure_result>.
		if nt, ok := recvType.(*NamedType); ok && nt.Builtin && nt.Name == "Result" && len(nt.Args) == 2 {
			return &NamedType{Name: "Result", Builtin: true, Args: []Type{
				nt.Args[0], &NamedType{Name: "?closure_result"},
			}}
		}
	}
	return nil
}

// builtinMethodReturnTypeFromAST mirrors `userMethodReturnTypeFromAST`
// for receivers whose static type is a builtin container or primitive
// (`List<T>`, `Map<K,V>`, `Set<T>`, `String`, `Bytes`,
// `Option<T>`, `Result<T,E>`). It defers to the existing
// `recoverMethodReturnTypeFromType` intrinsic table so trailing
// `xs.filter(...)`, `s.toUpper()`, `opt.unwrap()` etc. promote to
// the block's Result instead of being dropped to ExprStmt. Receiver
// is resolved via `resolveExprStaticType`, which also handles
// chained method calls (`x.foo().bar()`).
func (l *lowerer) builtinMethodReturnTypeFromAST(fx *ast.FieldExpr) Type {
	if l == nil || fx == nil {
		return nil
	}
	recvType := l.resolveExprStaticType(fx.X)
	if recvType == nil || recvType == ErrTypeVal {
		return nil
	}
	return recoverMethodReturnTypeFromType(fx.Name, recvType)
}

// resolveExprStaticType returns a best-effort static type for the
// expression, using resolver + AST decl shapes when SemanticDB lookups
// miss. Covers the receiver shapes commonly used as method-call
// receivers: bare Ident, FieldExpr (struct field access), and
// CallExpr (method-chain / free-fn return type). Returns nil when the
// type can't be recovered.
func (l *lowerer) resolveExprStaticType(e ast.Expr) Type {
	if l == nil || e == nil || l.res == nil {
		return nil
	}
	switch x := e.(type) {
	case *ast.Ident:
		sym := l.ref(x)
		if sym == nil {
			return nil
		}
		if t := l.nativeBindingTypeForSymbol(sym); t != nil && t != ErrTypeVal {
			return t
		}
		if t := l.nativeSymbolType(sym); t != nil && t != ErrTypeVal {
			return t
		}
		if p, ok := sym.Decl.(*ast.Param); ok && p.Type != nil {
			if t := l.lowerType(p.Type); t != nil && t != ErrTypeVal {
				return t
			}
		}
		// Let-stmt binding with explicit Type annotation.
		if ip, ok := sym.Decl.(*ast.IdentPat); ok {
			if ls := l.findLetStmtByPattern(ip); ls != nil && ls.Type != nil {
				if t := l.lowerType(ls.Type); t != nil && t != ErrTypeVal {
					return t
				}
			}
		}
	case *ast.CallExpr:
		// Method-chain receiver: `recv.foo(...).bar()`. The outer
		// `.bar()` needs `foo(...)`'s return type to look up `bar`.
		// Delegate to userMethodReturnTypeFromAST / builtinMethodReturn-
		// TypeFromAST / freeFnReturnTypeFromAST based on the callee
		// shape. Without this, every chained method call (which is
		// the dominant shape in toolchain code like
		// `self.tryAllocate(k, n).unwrap()`) fails receiver type
		// resolution → promotion misses → MIR UnreachableTerm.
		if fx, ok := x.Fn.(*ast.FieldExpr); ok && fx != nil {
			if t := l.userMethodReturnTypeFromAST(fx); t != nil && t != ErrTypeVal {
				return t
			}
			if t := l.builtinMethodReturnTypeFromAST(fx); t != nil && t != ErrTypeVal {
				return t
			}
			if t := l.useAliasFnReturnTypeFromAST(fx); t != nil && t != ErrTypeVal {
				return t
			}
			if t := l.closureDependentMethodReturnTypeFromAST(fx, x.Args); t != nil && t != ErrTypeVal {
				return t
			}
		}
		if id, ok := x.Fn.(*ast.Ident); ok && id != nil {
			if t := l.freeFnReturnTypeFromAST(id); t != nil && t != ErrTypeVal {
				return t
			}
		}
	case *ast.FieldExpr:
		// `r.field` chain: recover the field's declared type from the
		// receiver's struct decl.
		recv := l.resolveExprStaticType(x.X)
		if nt, ok := recv.(*NamedType); ok && nt != nil && !nt.Builtin {
			if sd := l.structDeclByName(nt.Name); sd != nil {
				for _, f := range sd.Fields {
					if f == nil || f.Name != x.Name {
						continue
					}
					if t := l.lowerType(f.Type); t != nil && t != ErrTypeVal {
						return t
					}
				}
			}
		}
	}
	return nil
}

// findLetClosureByPattern walks the current file looking for a
// LetStmt whose IdentPat matches `pat` and whose value is a
// ClosureExpr. Used by `freeFnReturnTypeFromAST` to peek at the
// closure's declared / inferred return type for promotion decisions.
func (l *lowerer) findLetClosureByPattern(pat *ast.IdentPat) *ast.ClosureExpr {
	if pat == nil || l.file == nil {
		return nil
	}
	var found *ast.ClosureExpr
	var walk func(ast.Node)
	walk = func(n ast.Node) {
		if n == nil || found != nil {
			return
		}
		if ls, ok := n.(*ast.LetStmt); ok && ls != nil {
			if ip, ok := ls.Pattern.(*ast.IdentPat); ok && ip == pat {
				if cl, ok := ls.Value.(*ast.ClosureExpr); ok {
					found = cl
				}
				return
			}
		}
		switch x := n.(type) {
		case *ast.File:
			for _, d := range x.Decls {
				walk(d)
			}
		case *ast.FnDecl:
			if x.Body != nil {
				walk(x.Body)
			}
		case *ast.Block:
			for _, s := range x.Stmts {
				walk(s)
			}
		case *ast.ExprStmt:
			walk(x.X)
		}
	}
	walk(l.file)
	return found
}

// findLetStmtByPattern walks the current file for a LetStmt whose
// IdentPat matches `pat`. Used to recover a let binding's declared
// type when the SemanticDB lookup misses.
func (l *lowerer) findLetStmtByPattern(pat *ast.IdentPat) *ast.LetStmt {
	if pat == nil || l.file == nil {
		return nil
	}
	var found *ast.LetStmt
	var walk func(ast.Node)
	walk = func(n ast.Node) {
		if n == nil || found != nil {
			return
		}
		if ls, ok := n.(*ast.LetStmt); ok && ls != nil {
			if ip, ok := ls.Pattern.(*ast.IdentPat); ok && ip == pat {
				found = ls
				return
			}
		}
		switch x := n.(type) {
		case *ast.File:
			for _, d := range x.Decls {
				walk(d)
			}
		case *ast.FnDecl:
			if x.Body != nil {
				walk(x.Body)
			}
		case *ast.Block:
			for _, s := range x.Stmts {
				walk(s)
			}
		case *ast.ExprStmt:
			walk(x.X)
		}
	}
	walk(l.file)
	return found
}

// stdModuleFnReturnTypeFromAST resolves `module.fn(...)` calls where
// `module` is a `use std.X` import alias. Looks up the fn's declared
// return type via the existing `lookupUseDeclFnReturn` /
// `lookupPackageFnReturn` chain, with a stdlib-registry fallback for
// single-file contexts where `sym.Package` isn't populated.
func (l *lowerer) stdModuleFnReturnTypeFromAST(fx *ast.FieldExpr) Type {
	if l == nil || fx == nil || l.res == nil {
		return nil
	}
	id, ok := fx.X.(*ast.Ident)
	if !ok || id == nil {
		return nil
	}
	sym := l.ref(id)
	if sym == nil || sym.Kind != resolve.SymPackage {
		return nil
	}
	ud, ok := sym.Decl.(*ast.UseDecl)
	if !ok || ud == nil {
		return nil
	}
	if t := l.lookupUseDeclFnReturn(ud, fx.Name); t != nil && t != ErrTypeVal {
		return t
	}
	if sym.Package != nil {
		if t := l.lookupPackageFnReturn(sym.Package, fx.Name); t != nil && t != ErrTypeVal {
			return t
		}
	}
	// stdlib registry direct lookup.
	if reg := stdlib.LoadCached(); reg != nil && len(ud.Path) >= 2 && ud.Path[0] == "std" {
		modKey := strings.Join(ud.Path[1:], ".")
		if fn := reg.LookupFnDecl(modKey, fx.Name); fn != nil {
			if fn.ReturnType == nil {
				return TUnit
			}
			return l.lowerType(fn.ReturnType)
		}
	}
	return nil
}

// enumVariantCallReturnTypeFromAST resolves `Enum.Variant(...)` calls
// to the enum's nominal type. Receiver Ident must resolve to a
// `SymEnum` whose declaration lists `Variant` among its variants.
// Returns nil when the call doesn't match the enum-qualified shape.
func (l *lowerer) enumVariantCallReturnTypeFromAST(fx *ast.FieldExpr) Type {
	if l == nil || fx == nil || l.res == nil {
		return nil
	}
	id, ok := fx.X.(*ast.Ident)
	if !ok || id == nil {
		return nil
	}
	sym := l.ref(id)
	if sym == nil || sym.Kind != resolve.SymEnum {
		return nil
	}
	if !l.isVariantOfEnum(sym, fx.Name) {
		return nil
	}
	return &NamedType{Name: sym.Name}
}

// useAliasFnReturnTypeFromAST resolves an `alias.Fn(...)` call whose
// receiver is a `use go "..." as alias { fn Fn(...) -> T }` import
// alias. Returns the declared return type from the UseDecl body, or
// nil when the receiver isn't a use-alias or no matching fn is found.
func (l *lowerer) useAliasFnReturnTypeFromAST(fx *ast.FieldExpr) Type {
	if l == nil || fx == nil || l.res == nil {
		return nil
	}
	id, ok := fx.X.(*ast.Ident)
	if !ok || id == nil {
		return nil
	}
	sym := l.ref(id)
	if sym == nil {
		return nil
	}
	ud, ok := sym.Decl.(*ast.UseDecl)
	if !ok || ud == nil {
		return nil
	}
	return l.lookupUseDeclFnReturn(ud, fx.Name)
}

// userMethodReturnTypeFromAST resolves the receiver expression of a
// `recv.method(...)` call to its user-defined struct/enum decl and
// returns the named method's lowered return type. Returns nil when the
// receiver is not a nominal user type or the method is absent.
func (l *lowerer) userMethodReturnTypeFromAST(fx *ast.FieldExpr) Type {
	if l == nil || fx == nil {
		return nil
	}
	if l.res == nil {
		return nil
	}
	// Receiver may be an Ident (`x.method()`), a chained call
	// (`x.foo().bar()`), or another FieldExpr (`x.y.bar()`).
	// `resolveExprStaticType` handles all three.
	recvType := l.resolveExprStaticType(fx.X)
	if recvType == nil || recvType == ErrTypeVal {
		return nil
	}
	nt, ok := recvType.(*NamedType)
	if !ok || nt == nil || nt.Builtin {
		return nil
	}
	if sd := l.structDeclByName(nt.Name); sd != nil {
		for _, m := range sd.Methods {
			if m == nil || m.Name != fx.Name {
				continue
			}
			if m.ReturnType == nil {
				return TUnit
			}
			return l.lowerType(m.ReturnType)
		}
	}
	if ed := l.enumDeclByName(nt.Name); ed != nil {
		for _, m := range ed.Methods {
			if m == nil || m.Name != fx.Name {
				continue
			}
			if m.ReturnType == nil {
				return TUnit
			}
			return l.lowerType(m.ReturnType)
		}
	}
	return nil
}

func astIdentLooksLikeValueConstructor(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	if !ok || id == nil || id.Name == "" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(id.Name)
	return unicode.IsUpper(r)
}

func astCallLooksLikeValueConstructor(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok || call == nil {
		return false
	}
	return astIdentLooksLikeValueConstructor(call.Fn)
}

func astIfLooksLikeValueExpr(e ast.Expr) bool {
	ife, ok := e.(*ast.IfExpr)
	if !ok || ife == nil {
		return false
	}
	if ife.Then == nil || len(ife.Then.Stmts) == 0 {
		return false
	}
	if !astBlockTailLooksLikeValueConstructor(ife.Then) {
		return false
	}
	// `if let Some(x) = opt { x } else { -1 }` is a perfectly valid
	// value expression — same shape as a regular `if cond { ... } else
	// { ... }`. The earlier `IsIfLet → false` short-circuit was
	// over-restrictive; rely on branch-shape analysis to decide whether
	// the whole expression yields a value. Without this, trailing
	// `if let` at block-final position drops to ExprStmt → MIR
	// UnreachableTerm.
	return astElseLooksLikeValueConstructor(ife.Else)
}

// astMatchLooksLikeValueExpr reports whether a trailing `match ... { ... }`
// at the block-final position should be lowered as the block's Result
// (a value expression) rather than as a MatchStmt. Mirrors
// `astIfLooksLikeValueExpr` but walks every arm — if any arm yields a
// value, the match itself yields a value. Without `chk.Types`
// (#1645-zeroed legacy map) the type-driven branch in
// `expressionYieldsValue` misses match expressions entirely, so the
// trailing match becomes a MatchStmt with no result wired back to the
// function return → MIR UnreachableTerm → stage0 declines.
func astMatchLooksLikeValueExpr(e ast.Expr) bool {
	m, ok := e.(*ast.MatchExpr)
	if !ok || m == nil || len(m.Arms) == 0 {
		return false
	}
	for _, arm := range m.Arms {
		if arm == nil || arm.Body == nil {
			continue
		}
		// Arm body is either a Block or a bare expression. Both reduce
		// to "is the tail expression a value-yielding shape?".
		if blk, ok := arm.Body.(*ast.Block); ok {
			if astBlockTailLooksLikeValueConstructor(blk) {
				return true
			}
			continue
		}
		switch arm.Body.(type) {
		case *ast.TupleExpr, *ast.ListExpr, *ast.MapExpr, *ast.StructLit,
			*ast.RangeExpr, *ast.IntLit, *ast.FloatLit, *ast.StringLit,
			*ast.CharLit, *ast.BoolLit, *ast.BinaryExpr, *ast.UnaryExpr,
			*ast.FieldExpr, *ast.IndexExpr, *ast.QuestionExpr,
			*ast.TurbofishExpr:
			return true
		case *ast.Ident:
			if astIdentLooksLikeValueConstructor(arm.Body) {
				return true
			}
		case *ast.CallExpr:
			if astCallLooksLikeValueConstructor(arm.Body) {
				return true
			}
		}
	}
	return false
}

func astElseLooksLikeValueConstructor(e ast.Expr) bool {
	switch x := e.(type) {
	case nil:
		return false
	case *ast.Block:
		return astBlockTailLooksLikeValueConstructor(x)
	case *ast.IfExpr:
		return astIfLooksLikeValueExpr(x)
	default:
		// `else <expr>` forms; accept value constructors.
		return astIdentLooksLikeValueConstructor(x) || astCallLooksLikeValueConstructor(x)
	}
}

func astBlockTailLooksLikeValueConstructor(b *ast.Block) bool {
	if b == nil || len(b.Stmts) == 0 {
		return false
	}
	last, ok := b.Stmts[len(b.Stmts)-1].(*ast.ExprStmt)
	if !ok || last == nil {
		return false
	}
	if astIdentLooksLikeValueConstructor(last.X) || astCallLooksLikeValueConstructor(last.X) {
		return true
	}
	// Without `chk.Types` (zeroed by #1645) we can't ask the checker
	// whether `(a, b)` / `x + y` / `obj.field` is the block's value, so
	// classify by shape. Mirrors the additions in `expressionYieldsValue`
	// — same rationale (always-yields-value syntactic shapes in Osty).
	switch x := last.X.(type) {
	case *ast.TupleExpr, *ast.ListExpr, *ast.MapExpr, *ast.StructLit, *ast.RangeExpr,
		*ast.IntLit, *ast.FloatLit, *ast.StringLit, *ast.CharLit, *ast.BoolLit,
		*ast.BinaryExpr, *ast.UnaryExpr, *ast.FieldExpr, *ast.IndexExpr,
		*ast.QuestionExpr, *ast.TurbofishExpr, *ast.Ident:
		// `*ast.Ident` covers `if cond { x } else { -1 }` where `x` is a
		// binding/parameter reference. Bare ident at block-tail is
		// almost always a value (function references are a theoretical
		// false positive but harmless — they're FnType-valued).
		return true
	case *ast.IfExpr:
		// Nested `if … { … } else { … }` at the block's tail — recurse
		// so the enclosing block is still classified as value-yielding
		// when every nested arm itself yields a value. Without this
		// case, `if outer { if inner { a } else { b } } else { c }`
		// drops the outer expression to an IfStmt and the function's
		// return value never gets wired up. Audit-driven discovery:
		// `frontStringContentStart` in toolchain/frontend.osty has
		// exactly this shape (`if kind == FrontRawString { if triple
		// { start + 4 } else { start + 2 } } else if … else …`).
		return astIfLooksLikeValueExpr(x)
	case *ast.MatchExpr:
		// Same rationale as IfExpr: a trailing `match` at the block's
		// tail yields a value when at least one arm does. Mirrors the
		// post-#1645 syntactic-shape classification path for match.
		return astMatchLooksLikeValueExpr(x)
	}
	return false
}

func expressionTypeYieldsValue(t Type) bool {
	if !usableRecoveredType(t) {
		return false
	}
	if isPrim(t, PrimUnit) || isPrim(t, PrimNever) {
		return false
	}
	return true
}

func (l *lowerer) lowerStmt(s ast.Stmt) Stmt {
	switch s := s.(type) {
	case *ast.Block:
		return l.lowerBlock(s)
	case *ast.LetStmt:
		return l.lowerLetStmt(s)
	case *ast.ExprStmt:
		switch x := s.X.(type) {
		case *ast.IfExpr:
			if !x.IsIfLet {
				return l.lowerIfStmt(x)
			}
		case *ast.MatchExpr:
			return l.lowerMatchStmt(x)
		}
		x := l.lowerExpr(s.X)
		return &ExprStmt{X: x, SpanV: Span{Start: posFromToken(s.Pos()), End: posFromToken(s.End())}}
	case *ast.ReturnStmt:
		out := &ReturnStmt{SpanV: nodeSpan(s)}
		if s.Value != nil {
			out.Value = l.lowerExpr(s.Value)
		}
		return out
	case *ast.BreakStmt:
		out := &BreakStmt{Label: s.Label, SpanV: nodeSpan(s)}
		if s.Value != nil {
			out.Value = l.lowerExpr(s.Value)
		}
		return out
	case *ast.ContinueStmt:
		return &ContinueStmt{Label: s.Label, SpanV: nodeSpan(s)}
	case *ast.AssignStmt:
		return l.lowerAssignStmt(s)
	case *ast.ForStmt:
		return l.lowerForStmt(s)
	case *ast.DeferStmt:
		return l.lowerDeferStmt(s)
	case *ast.ChanSendStmt:
		return &ChanSendStmt{
			Channel: l.lowerExpr(s.Channel),
			Value:   l.lowerExpr(s.Value),
			SpanV:   nodeSpan(s),
		}
	}
	// Fall through: if it's actually an if used at statement position,
	// the parser wrapped it in an ExprStmt already; we only get here
	// for deferred constructs.
	l.note("unsupported statement %T at %v", s, s.Pos())
	return &ErrorStmt{Note: fmt.Sprintf("%T", s), SpanV: nodeSpan(s)}
}

func (l *lowerer) lowerLetStmt(s *ast.LetStmt) Stmt {
	out := &LetStmt{
		Mut:   s.Mut,
		SpanV: nodeSpan(s),
	}
	if name, ok := simpleBindName(s.Pattern); ok {
		out.Name = name
	} else {
		out.Pattern = l.lowerPattern(s.Pattern)
	}
	if s.Type != nil {
		out.Type = l.lowerType(s.Type)
	}
	if s.Value != nil {
		out.Value = l.lowerExpr(s.Value)
		if out.Type == nil {
			out.Type = out.Value.Type()
		}
		// Closure-from-annotation backfill: `let f: fn(Int) ->
		// Int = |x| x + 1` carries the closure's expected
		// signature in the LetStmt's type annotation. Propagate
		// it back into the Closure value so its un-annotated
		// param/Return slots resolve before the body's Idents
		// poison downstream lowering.
		if cl, ok := out.Value.(*Closure); ok && cl != nil {
			if fnT, ok := out.Type.(*FnType); ok && fnT != nil {
				backfillClosure(cl, fnT)
				if out.Value.Type() == nil || out.Value.Type() == ErrTypeVal {
					out.Type = cl.T
				}
			}
		}
	}
	if !usableRecoveredType(out.Type) {
		if name, ok := simpleBindName(s.Pattern); ok {
			if t := l.nativeBindingType(s.Pattern, name); usableRecoveredType(t) {
				out.Type = t
			}
		}
	}
	// Record the inferred binding-pattern type so later references
	// to the same name in this function can recover their type via
	// the resolver Symbol → IdentPat → recorded type chain. The
	// embedded selfhost checker doesn't always populate `Types[lit]`
	// for the let RHS, so when the IR-side `out.Type` is poisoned we
	// fall back to deriving the type from the AST shape directly
	// (StructLit head ident → `&NamedType{Name: ...}`). Only the
	// bare `let x = <expr>` shape is recorded; richer patterns
	// (tuple / struct destructure) carry a different per-element
	// shape and would need a structured recovery pass.
	if ip, ok := s.Pattern.(*ast.IdentPat); ok && ip != nil {
		recorded := out.Type
		if recorded == nil || recorded == ErrTypeVal {
			recorded = l.bindingTypeFromAST(s.Value)
		}
		if recorded != nil && recorded != ErrTypeVal {
			if l.bindingPatTypes == nil {
				l.bindingPatTypes = map[*ast.IdentPat]Type{}
			}
			l.bindingPatTypes[ip] = recorded
			if l.bindingPatTypesByOffset == nil {
				l.bindingPatTypesByOffset = map[int]Type{}
			}
			l.bindingPatTypesByOffset[ip.Pos().Offset] = recorded
		}
	}
	return out
}

// bindingTypeFromAST derives an IR Type from a value expression when
// the checker hasn't populated the per-node Types map. It intentionally
// stays on shapes whose type is recoverable from syntax plus resolver
// anchors: named literals, declared fn returns, branch tails, field
// declarations, and collection index element types.
func (l *lowerer) bindingTypeFromAST(e ast.Expr) Type {
	switch n := e.(type) {
	case nil:
		return nil
	case *ast.IntLit:
		return TInt
	case *ast.FloatLit:
		return TFloat
	case *ast.BoolLit:
		return TBool
	case *ast.CharLit:
		return TChar
	case *ast.ByteLit:
		return TByte
	case *ast.BytesLit:
		return TBytes
	case *ast.StringLit:
		return TString
	case *ast.Ident:
		return l.bindingIdentType(n)
	case *ast.StructLit:
		if n == nil {
			return nil
		}
		switch h := n.Type.(type) {
		case *ast.Ident:
			if h.Name != "" {
				return &NamedType{Name: h.Name}
			}
		case *ast.FieldExpr:
			if h.Name != "" {
				return &NamedType{Name: h.Name}
			}
		}
	case *ast.CallExpr:
		if t := l.callReturnTypeFromAST(n); usableRecoveredType(t) {
			return t
		}
	case *ast.ListExpr:
		if n == nil || len(n.Elems) == 0 {
			return nil
		}
		if elem := l.bindingTypeFromAST(n.Elems[0]); usableRecoveredType(elem) {
			return &NamedType{Name: "List", Args: []Type{elem}, Builtin: true}
		}
	case *ast.IfExpr:
		if t := l.ifTypeFromAST(n); usableRecoveredType(t) {
			return t
		}
	case *ast.FieldExpr:
		if t := l.fieldTypeFromAST(n); usableRecoveredType(t) {
			return t
		}
	case *ast.IndexExpr:
		if t := l.indexTypeFromAST(n); usableRecoveredType(t) {
			return t
		}
	case *ast.ParenExpr:
		return l.bindingTypeFromAST(n.X)
	}
	return nil
}

func (l *lowerer) bindingIdentType(id *ast.Ident) Type {
	if id == nil {
		return nil
	}
	if t := l.exprType(id); usableRecoveredType(t) {
		return t
	}
	sym := l.symbol(id)
	if sym == nil {
		return nil
	}
	if t := l.nativeBindingTypeForSymbol(sym); usableRecoveredType(t) {
		return t
	}
	if t := l.nativeSymbolType(sym); usableRecoveredType(t) {
		return t
	}
	if l.chk != nil {
		if st := l.chk.SymTypes[sym]; st != nil {
			if t := l.fromCheckerType(st); usableRecoveredType(t) {
				return t
			}
		}
	}
	if t := l.identTypeFromDecl(sym.Decl); usableRecoveredType(t) {
		return t
	}
	if l.bindingPatTypes != nil {
		if ip, ok := sym.Decl.(*ast.IdentPat); ok {
			if t := l.bindingPatTypes[ip]; usableRecoveredType(t) {
				return t
			}
		}
	}
	// Offset-keyed companion fallback: the pointer-keyed lookup
	// above misses across the `parseGenEmitFile` reparse boundary
	// (sym.Decl points at the pre-reparse AST, populate cached the
	// post-reparse AST). The merged source text is identical, so
	// the IdentPat's source offset uniquely identifies the binding
	// across both ASTs.
	if l.bindingPatTypesByOffset != nil && sym.Decl != nil {
		if _, ok := sym.Decl.(*ast.IdentPat); ok {
			if t := l.bindingPatTypesByOffset[sym.Decl.Pos().Offset]; usableRecoveredType(t) {
				return t
			}
		}
	}
	return nil
}

func (l *lowerer) callReturnTypeFromAST(e *ast.CallExpr) Type {
	if e == nil {
		return nil
	}
	fn := e.Fn
	if tf, ok := fn.(*ast.TurbofishExpr); ok {
		fn = tf.Base
	}
	switch f := fn.(type) {
	case *ast.Ident:
		if sym := l.symbol(f); sym != nil {
			if sym.Kind == resolve.SymVariant {
				return l.variantOwnerTypeFromSymbol(sym)
			}
			if sym.Kind == resolve.SymBuiltin && isPreludeVariantName(sym.Name) {
				return l.preludeVariantTypeFromCall(sym.Name, e)
			}
		}
		return l.recoverFnDeclReturnType(f)
	case *ast.FieldExpr:
		if t := l.variantTypeFromQualifiedField(f); usableRecoveredType(t) {
			return t
		}
		receiverType := l.exprType(f.X)
		if !usableRecoveredType(receiverType) {
			receiverType = l.bindingTypeFromAST(f.X)
		}
		return recoverMethodReturnTypeFromType(f.Name, receiverType)
	}
	return nil
}

func (l *lowerer) preludeVariantTypeFromCall(name string, e *ast.CallExpr) Type {
	switch name {
	case "Some":
		if arg := firstArgTypeFromAST(l, e); usableRecoveredType(arg) {
			return &NamedType{Name: "Option", Args: []Type{arg}, Builtin: true}
		}
	case "Ok":
		if arg := firstArgTypeFromAST(l, e); usableRecoveredType(arg) {
			return &NamedType{Name: "Result", Args: []Type{arg, &NamedType{Name: "Error", Builtin: true}}, Builtin: true}
		}
	case "Err":
		if arg := firstArgTypeFromAST(l, e); usableRecoveredType(arg) {
			return &NamedType{Name: "Result", Args: []Type{ErrTypeVal, arg}, Builtin: true}
		}
	}
	return nil
}

func firstArgTypeFromAST(l *lowerer, e *ast.CallExpr) Type {
	if l == nil || e == nil || len(e.Args) == 0 || e.Args[0] == nil {
		return nil
	}
	return l.bindingTypeFromAST(e.Args[0].Value)
}

func (l *lowerer) ifTypeFromAST(e *ast.IfExpr) Type {
	if e == nil {
		return nil
	}
	thenType := l.blockTypeFromAST(e.Then)
	elseType := l.elseTypeFromAST(e.Else)
	if usableRecoveredType(thenType) {
		return thenType
	}
	if usableRecoveredType(elseType) {
		return elseType
	}
	return nil
}

func (l *lowerer) blockTypeFromAST(b *ast.Block) Type {
	if b == nil || len(b.Stmts) == 0 {
		return nil
	}
	if es, ok := b.Stmts[len(b.Stmts)-1].(*ast.ExprStmt); ok && es != nil {
		return l.bindingTypeFromAST(es.X)
	}
	return nil
}

func (l *lowerer) elseTypeFromAST(e ast.Expr) Type {
	switch alt := e.(type) {
	case nil:
		return nil
	case *ast.Block:
		return l.blockTypeFromAST(alt)
	default:
		return l.bindingTypeFromAST(alt)
	}
}

func (l *lowerer) fieldTypeFromAST(e *ast.FieldExpr) Type {
	if e == nil {
		return nil
	}
	if t := l.variantTypeFromQualifiedField(e); usableRecoveredType(t) {
		return t
	}
	receiverType := l.exprType(e.X)
	if !usableRecoveredType(receiverType) {
		receiverType = l.bindingTypeFromAST(e.X)
	}
	return l.recoverFieldType(receiverType, e.Name)
}

func (l *lowerer) indexTypeFromAST(e *ast.IndexExpr) Type {
	if e == nil {
		return nil
	}
	baseType := l.exprType(e.X)
	if !usableRecoveredType(baseType) {
		baseType = l.bindingTypeFromAST(e.X)
	}
	return recoverIndexTypeFromType(baseType)
}

func usableRecoveredType(t Type) bool {
	return t != nil && t != ErrTypeVal && !hasPoisonedTypeArg(t)
}

// simpleBindName returns (name, true) when the pattern is just a bare
// IdentPattern.
func simpleBindName(p ast.Pattern) (string, bool) {
	ip, ok := p.(*ast.IdentPat)
	if !ok {
		return "", false
	}
	return ip.Name, true
}

func (l *lowerer) lowerAssignStmt(s *ast.AssignStmt) Stmt {
	out := &AssignStmt{
		Op:    assignOp(s.Op),
		Value: l.lowerExpr(s.Value),
		SpanV: nodeSpan(s),
	}
	for _, t := range s.Targets {
		out.Targets = append(out.Targets, l.lowerExpr(t))
	}
	return out
}

func (l *lowerer) lowerForStmt(s *ast.ForStmt) Stmt {
	body := l.lowerBlock(s.Body)
	// Classify: infinite | while | for-in (range or iterator).
	if s.Pattern == nil && s.Iter == nil {
		return &ForStmt{Kind: ForInfinite, Label: s.Label, Body: body, SpanV: nodeSpan(s)}
	}
	if s.Pattern == nil && s.Iter != nil {
		return &ForStmt{Kind: ForWhile, Label: s.Label, Cond: l.lowerExpr(s.Iter), Body: body, SpanV: nodeSpan(s)}
	}
	var loopVar string
	var loopPat Pattern
	if name, ok := simpleBindName(s.Pattern); ok {
		loopVar = name
	} else {
		loopPat = l.lowerPattern(s.Pattern)
	}
	// for x in a..b is a numeric range loop.
	if r, ok := s.Iter.(*ast.RangeExpr); ok && r.Start != nil && r.Stop != nil {
		return &ForStmt{
			Kind:      ForRange,
			Label:     s.Label,
			Var:       loopVar,
			Pattern:   loopPat,
			Start:     l.lowerExpr(r.Start),
			End:       l.lowerExpr(r.Stop),
			Inclusive: r.Inclusive,
			Body:      body,
			SpanV:     nodeSpan(s),
		}
	}
	return &ForStmt{
		Kind:    ForIn,
		Label:   s.Label,
		Var:     loopVar,
		Pattern: loopPat,
		Iter:    l.lowerExpr(s.Iter),
		Body:    body,
		SpanV:   nodeSpan(s),
	}
}

func (l *lowerer) lowerIfStmt(e *ast.IfExpr) Stmt {
	return &IfStmt{
		Cond:  l.lowerExpr(e.Cond),
		Then:  l.lowerBlock(e.Then),
		Else:  l.lowerElseStmt(e.Else),
		SpanV: nodeSpan(e),
	}
}

func (l *lowerer) lowerElseStmt(alt ast.Expr) *Block {
	switch alt := alt.(type) {
	case nil:
		return nil
	case *ast.Block:
		return l.lowerBlock(alt)
	case *ast.IfExpr:
		if !alt.IsIfLet {
			stmt := l.lowerIfStmt(alt)
			return &Block{Stmts: []Stmt{stmt}, SpanV: nodeSpan(alt)}
		}
		lowered := l.lowerIfExpr(alt)
		return &Block{
			Stmts: []Stmt{&ExprStmt{X: lowered, SpanV: lowered.At()}},
			SpanV: nodeSpan(alt),
		}
	default:
		lowered := l.lowerExpr(alt)
		return &Block{
			Stmts: []Stmt{&ExprStmt{X: lowered, SpanV: lowered.At()}},
			SpanV: lowered.At(),
		}
	}
}

// assignOp maps a token kind to the IR AssignOp.
func assignOp(k token.Kind) AssignOp {
	switch k {
	case token.ASSIGN:
		return AssignEq
	case token.PLUSEQ:
		return AssignAdd
	case token.MINUSEQ:
		return AssignSub
	case token.STAREQ:
		return AssignMul
	case token.SLASHEQ:
		return AssignDiv
	case token.PERCENTEQ:
		return AssignMod
	case token.BITANDEQ:
		return AssignAnd
	case token.BITOREQ:
		return AssignOr
	case token.BITXOREQ:
		return AssignXor
	case token.SHLEQ:
		return AssignShl
	case token.SHREQ:
		return AssignShr
	}
	return AssignEq
}

// ==== Expressions ====

func (l *lowerer) lowerExpr(e ast.Expr) Expr {
	if e == nil {
		l.note("unsupported nil expression")
		return &ErrorExpr{Note: "nil expr", T: ErrTypeVal}
	}
	switch e := e.(type) {
	case *ast.IntLit:
		t := l.exprType(e)
		if t == ErrTypeVal {
			// Default int-literal type when the checker didn't
			// populate Types[e] — covers literals nested inside
			// contexts the checker skips (string interp parts,
			// match arm bodies, etc.). Without this, a
			// stray `+ 1` poisons the enclosing BinaryExpr to
			// ErrType, which cascades to every consumer of the
			// match / block result.
			t = TInt
		}
		return &IntLit{Text: e.Text, T: t, SpanV: nodeSpan(e)}
	case *ast.FloatLit:
		t := l.exprType(e)
		if t == ErrTypeVal {
			t = TFloat
		}
		return &FloatLit{Text: e.Text, T: t, SpanV: nodeSpan(e)}
	case *ast.BoolLit:
		return &BoolLit{Value: e.Value, SpanV: nodeSpan(e)}
	case *ast.CharLit:
		return &CharLit{Value: e.Value, SpanV: nodeSpan(e)}
	case *ast.ByteLit:
		return &ByteLit{Value: e.Value, SpanV: nodeSpan(e)}
	case *ast.BytesLit:
		return &BytesLit{Value: e.Value, SpanV: nodeSpan(e)}
	case *ast.StringLit:
		return l.lowerStringLit(e)
	case *ast.Ident:
		return l.lowerIdent(e)
	case *ast.ParenExpr:
		return l.lowerExpr(e.X)
	case *ast.UnaryExpr:
		return l.lowerUnary(e)
	case *ast.BinaryExpr:
		return l.lowerBinary(e)
	case *ast.CallExpr:
		return l.lowerCall(e)
	case *ast.ListExpr:
		return l.lowerList(e)
	case *ast.Block:
		blk := l.lowerBlock(e)
		t := l.exprType(e)
		if t == nil {
			if blk.Result != nil {
				t = blk.Result.Type()
			} else {
				t = TUnit
			}
		}
		return &BlockExpr{Block: blk, T: t, SpanV: nodeSpan(e)}
	case *ast.IfExpr:
		return l.lowerIfExpr(e)
	case *ast.MatchExpr:
		return l.lowerMatchExpr(e)
	case *ast.FieldExpr:
		return l.lowerFieldExpr(e)
	case *ast.IndexExpr:
		loweredX := l.lowerExpr(e.X)
		loweredIndex := l.lowerExpr(e.Index)
		t := l.exprType(e)
		if t == ErrTypeVal || t == nil {
			if rec := recoverIndexType(loweredX); rec != ErrTypeVal {
				t = rec
			}
		}
		if !usableRecoveredType(t) {
			if rec := l.bindingTypeFromAST(e); usableRecoveredType(rec) {
				t = rec
			}
		}
		return &IndexExpr{
			X:     loweredX,
			Index: loweredIndex,
			T:     t,
			SpanV: nodeSpan(e),
		}
	case *ast.StructLit:
		return l.lowerStructLit(e)
	case *ast.TupleExpr:
		if len(e.Elems) == 1 {
			return l.lowerExpr(e.Elems[0])
		}
		out := &TupleLit{T: l.exprType(e), SpanV: nodeSpan(e)}
		for _, el := range e.Elems {
			out.Elems = append(out.Elems, l.lowerExpr(el))
		}
		// Recover the tuple type from the lowered element types when the
		// checker didn't supply one. Tuples are fully determined by
		// their elements, so this is always safe.
		if out.T == nil || out.T == ErrTypeVal || hasPoisonedTypeArg(out.T) {
			elems := make([]Type, len(out.Elems))
			ok := true
			for i, el := range out.Elems {
				et := el.Type()
				if et == nil || et == ErrTypeVal {
					ok = false
					break
				}
				elems[i] = et
			}
			if ok {
				out.T = &TupleType{Elems: elems}
			}
		}
		return out
	case *ast.MapExpr:
		return l.lowerMapLit(e)
	case *ast.RangeExpr:
		out := &RangeLit{Inclusive: e.Inclusive, T: l.exprType(e), SpanV: nodeSpan(e)}
		if e.Start != nil {
			out.Start = l.lowerExpr(e.Start)
		}
		if e.Stop != nil {
			out.End = l.lowerExpr(e.Stop)
		}
		return out
	case *ast.QuestionExpr:
		return &QuestionExpr{
			X:     l.lowerExpr(e.X),
			T:     l.exprType(e),
			SpanV: nodeSpan(e),
		}
	case *ast.ClosureExpr:
		return l.lowerClosure(e)
	case *ast.TurbofishExpr:
		return l.lowerTurbofish(e)
	case *ast.LoopExpr:
		return l.lowerLoopExpr(e)
	}
	l.note("unsupported expression %T at %v", e, e.Pos())
	return &ErrorExpr{Note: fmt.Sprintf("%T", e), T: ErrTypeVal, SpanV: nodeSpan(e)}
}

func (l *lowerer) exprType(e ast.Expr) Type {
	if l.chk == nil {
		return ErrTypeVal
	}
	if t := l.nativeCheckedType(e); t != nil && t != ErrTypeVal {
		return t
	}
	t := l.chk.Types[e]
	if t == nil {
		return ErrTypeVal
	}
	return l.fromCheckerType(t)
}

func (l *lowerer) lowerStringLit(s *ast.StringLit) Expr {
	out := &StringLit{IsRaw: s.IsRaw, IsTriple: s.IsTriple, SpanV: nodeSpan(s)}
	for _, p := range s.Parts {
		if p.IsLit {
			out.Parts = append(out.Parts, StringPart{IsLit: true, Lit: p.Lit})
			continue
		}
		// A non-literal part with a nil expression is produced by the
		// parser's error-recovery path when a `{...}` interpolation
		// slot couldn't be parsed cleanly. Downstream consumers
		// (mir.Lower, the MIR emitter) assume every non-lit part has
		// a real Expr, so treat the hole as an empty string literal
		// here rather than letting lowerExpr crash on a nil switch.
		if p.Expr == nil {
			out.Parts = append(out.Parts, StringPart{IsLit: true, Lit: ""})
			continue
		}
		out.Parts = append(out.Parts, StringPart{Expr: l.lowerExpr(p.Expr)})
	}
	return out
}

func (l *lowerer) lowerIdent(id *ast.Ident) Expr {
	out := &Ident{Name: id.Name, SpanV: nodeSpan(id), T: ErrTypeVal}
	var sym *resolve.Symbol
	if l.res != nil {
		if s := l.ref(id); s != nil {
			sym = s
			out.Kind = identKind(s)
		}
	}
	if l.chk != nil {
		if t := l.exprType(id); usableRecoveredType(t) {
			out.T = t
		} else if sym != nil {
			if t := l.nativeSymbolType(sym); usableRecoveredType(t) {
				out.T = t
			} else if l.chk.SymTypes != nil {
				if st := l.chk.SymTypes[sym]; st != nil {
					out.T = l.fromCheckerType(st)
				}
			}
		}
	}
	// Last-resort operand-based recovery: when neither the per-node
	// Types map nor the SymTypes map covers this ident (the native
	// checker skips expressions nested inside string interpolation
	// parts, for instance), pull the declared type straight off the
	// resolved symbol's declaration node. This now also covers bare
	// enum variants (`let k = HirSwitchUnknown`) by recovering the
	// variant's containing enum.
	//
	// Same recovery fires when the recorded type carries poisoned
	// type-args (`Map<<error>, <error>>`) — the checker pinned a
	// shape but lost the K/V details. The decl-side recovery can
	// often pull a complete type off the LetStmt.Value (a MapLit
	// whose K/V types `lowerMapLit` filled from the entries).
	if sym != nil && (out.T == nil || out.T == ErrTypeVal || hasPoisonedTypeArg(out.T)) {
		if t := l.identTypeFromDecl(sym.Decl); t != nil && !hasPoisonedTypeArg(t) {
			out.T = t
		}
	}
	// AST-wide static-type recovery: `resolveExprStaticType(id)`
	// consults the resolver + struct/enum decls. This catches let
	// bindings whose initialiser type was missed by both
	// `bindingPatTypes` (e.g. RHS is a chained method call whose
	// inner type only resolves via the AST helpers) and the
	// per-decl shapes above. Without this, ident references in
	// examples/ai_demo_*.osty bodies stay at `<error>` and cascade
	// through every enclosing expression.
	if out.T == nil || out.T == ErrTypeVal || hasPoisonedTypeArg(out.T) {
		if t := l.resolveExprStaticType(id); t != nil && t != ErrTypeVal && !hasPoisonedTypeArg(t) {
			out.T = t
		}
	}
	return out
}

// identTypeFromDecl extracts the declared type from a symbol's
// introducing declaration. Handles the subset of decl shapes that a
// plain ident can resolve to: function / closure param (with or
// without destructuring pattern), an immutable / mutable `let`
// binding carrying an explicit type annotation, and enum variants
// whose owner enum can be found from the current file.
func (l *lowerer) identTypeFromDecl(decl ast.Node) Type {
	switch d := decl.(type) {
	case *ast.Param:
		if d == nil {
			return nil
		}
		return l.lowerType(d.Type)
	case *ast.LetStmt:
		if d == nil {
			return nil
		}
		if t := l.lowerType(d.Type); t != nil && !hasPoisonedTypeArg(t) {
			return t
		}
		// Annotation absent / poisoned. Recover off the initialiser
		// expression. The checker may have left `Types[d.Value]`
		// poisoned (`{"a": 1}` reaches lower.go with `Types[MapExpr]
		// = Map<<error>, <error>>`), so we re-lower the AST
		// expression to pick up `lowerMapLit`'s entry-driven KV
		// recovery (or `lowerListLit`'s element recovery). The
		// re-lowered Expr's Type() reflects the recovered shape.
		if d.Value != nil {
			if t := l.exprType(d.Value); t != nil && !hasPoisonedTypeArg(t) {
				return t
			}
			lowered := l.lowerExpr(d.Value)
			if lowered != nil {
				if t := lowered.Type(); t != nil && !hasPoisonedTypeArg(t) {
					return t
				}
			}
		}
		return nil
	case *ast.LetDecl:
		if d == nil {
			return nil
		}
		if t := l.lowerType(d.Type); t != nil && !hasPoisonedTypeArg(t) {
			return t
		}
		return nil
	case *ast.IdentPat:
		// IdentPat is the most common shape resolve.Symbol records
		// for `let x = ...` bindings. The lowerLetStmt pass records
		// the inferred binding type into `bindingPatTypes[ip]` after
		// running entry-driven recovery on the initialiser, so look
		// it up first. This is what unblocks `let m = {"a": 1}`
		// downstream uses — the IdentPat is the resolver's Decl
		// target, not the wrapping LetStmt.
		if d == nil {
			return nil
		}
		if l.bindingPatTypes != nil {
			if t, ok := l.bindingPatTypes[d]; ok && t != nil && !hasPoisonedTypeArg(t) {
				return t
			}
		}
		return nil
	case *ast.Variant:
		return l.enumTypeForVariantDecl(d)
	case *ast.Receiver:
		// `self` inside a method body. The resolver records the
		// `self` ident with `Decl = *ast.Receiver`, which carries no
		// type info on its own. Walk the file's struct/enum decls
		// looking for the method that owns this receiver, then
		// return the owner's nominal type. Without this, every
		// `self` reference inside a struct method body lowers as
		// `Ident.T = <error>` and cascades through every enclosing
		// expression — especially `self.method()` chains in
		// examples/gc/lib.osty style code.
		return l.selfReceiverType(d)
	}
	return nil
}

// selfReceiverType locates the struct/enum declaration that owns the
// given Receiver node and returns its nominal type, or nil when the
// receiver can't be matched to any decl in the current file.
func (l *lowerer) selfReceiverType(recv *ast.Receiver) Type {
	if l == nil || recv == nil || l.file == nil {
		return nil
	}
	for _, decl := range l.file.Decls {
		switch d := decl.(type) {
		case *ast.StructDecl:
			for _, m := range d.Methods {
				if m != nil && m.Recv == recv {
					return &NamedType{Name: d.Name}
				}
			}
		case *ast.EnumDecl:
			for _, m := range d.Methods {
				if m != nil && m.Recv == recv {
					return &NamedType{Name: d.Name}
				}
			}
		}
	}
	return nil
}

func (l *lowerer) symbol(id *ast.Ident) *resolve.Symbol {
	return l.ref(id)
}

func identKind(sym *resolve.Symbol) IdentKind {
	if sym == nil {
		return IdentUnknown
	}
	switch sym.Kind {
	case resolve.SymLet:
		if _, ok := sym.Decl.(*ast.LetDecl); ok {
			return IdentGlobal
		}
		return IdentLocal
	case resolve.SymParam:
		return IdentParam
	case resolve.SymFn:
		return IdentFn
	case resolve.SymVariant:
		return IdentVariant
	case resolve.SymStruct, resolve.SymEnum, resolve.SymInterface, resolve.SymTypeAlias:
		return IdentTypeName
	case resolve.SymBuiltin:
		return IdentBuiltin
	}
	return IdentUnknown
}

func (l *lowerer) lowerUnary(e *ast.UnaryExpr) Expr {
	op, ok := unaryOp(e.Op)
	if !ok {
		l.note("unsupported unary op %v at %v", e.Op, e.Pos())
		return &ErrorExpr{Note: "unary op", T: ErrTypeVal, SpanV: nodeSpan(e)}
	}
	x := l.lowerExpr(e.X)
	t := l.exprType(e)
	// Type recovery: when the checker is silent, fall back to the
	// operand's type. Unary minus / plus / bitwise-not preserve the
	// numeric type; `!` always yields Bool. Without this, `-n` where
	// `n: Int` leaves `UnaryExpr.T = <error>` and any wrapping `if -n
	// > 0 { ... } else { ... }` propagates the poison through to the
	// IfExpr's Result.
	if t == nil || t == ErrTypeVal || hasPoisonedTypeArg(t) {
		switch op {
		case UnNot:
			t = TBool
		case UnNeg, UnPlus, UnBitNot:
			if xt := x.Type(); xt != nil && xt != ErrTypeVal && !hasPoisonedTypeArg(xt) {
				t = xt
			}
		}
	}
	return &UnaryExpr{Op: op, X: x, T: t, SpanV: nodeSpan(e)}
}

func unaryOp(k token.Kind) (UnOp, bool) {
	switch k {
	case token.MINUS:
		return UnNeg, true
	case token.PLUS:
		return UnPlus, true
	case token.NOT:
		return UnNot, true
	case token.BITNOT:
		return UnBitNot, true
	}
	return 0, false
}

func (l *lowerer) lowerBinary(e *ast.BinaryExpr) Expr {
	if e.Op == token.QQ {
		left := l.lowerExpr(e.Left)
		right := l.lowerExpr(e.Right)
		t := l.exprType(e)
		if t == ErrTypeVal {
			t = recoverCoalesceType(left, right)
		}
		return &CoalesceExpr{
			Left:  left,
			Right: right,
			T:     t,
			SpanV: nodeSpan(e),
		}
	}
	op, ok := binaryOp(e.Op)
	if !ok {
		l.note("unsupported binary op %v at %v", e.Op, e.Pos())
		return &ErrorExpr{Note: "binary op", T: ErrTypeVal, SpanV: nodeSpan(e)}
	}
	left := l.lowerExpr(e.Left)
	right := l.lowerExpr(e.Right)
	t := l.exprType(e)
	if t == ErrTypeVal {
		t = recoverBinaryType(op, left, right)
	}
	return &BinaryExpr{
		Op:    op,
		Left:  left,
		Right: right,
		T:     t,
		SpanV: nodeSpan(e),
	}
}

func recoverCoalesceType(left, right Expr) Type {
	if left == nil || right == nil {
		return ErrTypeVal
	}
	lt := left.Type()
	rt := right.Type()
	if lt == nil || lt == ErrTypeVal {
		return ErrTypeVal
	}
	var inner Type
	switch x := lt.(type) {
	case *OptionalType:
		inner = x.Inner
	case *NamedType:
		if (x.Name == "Option" || x.Name == "Maybe") && len(x.Args) >= 1 {
			inner = x.Args[0]
		}
	}
	if inner == nil || inner == ErrTypeVal {
		return ErrTypeVal
	}
	if rt == nil || rt == ErrTypeVal {
		return inner
	}
	if inner.String() == rt.String() {
		return inner
	}
	if numericResult(inner, rt) != ErrTypeVal {
		return numericResult(inner, rt)
	}
	return inner
}

// recoverBinaryType derives a binary expression's result type from its
// operand types when the checker did not populate Types[e]. This is a
// best-effort fallback that keeps arithmetic and comparison chains off
// ErrType when the operands themselves are well-typed — without it, a
// single missing TypedNode at the checker boundary poisons every
// enclosing expression and blocks MIR-direct lowering.
//
// The recovery rules mirror the native elaborator (toolchain/elab.osty
// `binOpResultType`) for the subset where result type is derivable from
// operand types without further context.
func recoverBinaryType(op BinOp, left, right Expr) Type {
	if left == nil || right == nil {
		return ErrTypeVal
	}
	lt := left.Type()
	rt := right.Type()
	if lt == ErrTypeVal || rt == ErrTypeVal || lt == nil || rt == nil {
		return ErrTypeVal
	}
	switch op {
	case BinEq, BinNeq, BinLt, BinLeq, BinGt, BinGeq:
		return TBool
	case BinAnd, BinOr:
		return TBool
	case BinAdd:
		// String + String → String (spec §4.6). Otherwise numeric.
		if isPrim(lt, PrimString) && isPrim(rt, PrimString) {
			return TString
		}
		return numericResult(lt, rt)
	case BinSub, BinMul, BinDiv, BinMod:
		return numericResult(lt, rt)
	case BinBitAnd, BinBitOr, BinBitXor, BinShl, BinShr:
		// Bitwise ops preserve the non-untyped operand type.
		if isIntegral(lt) {
			return lt
		}
		if isIntegral(rt) {
			return rt
		}
	}
	return ErrTypeVal
}

// recoverIndexType derives an index expression's element type from
// the base receiver's type when the checker did not populate
// Types[e]. Mirrors the other operand-based recovery helpers
// (recoverBinaryType / recoverBlockType / recoverCallReturnType)
// and targets the concrete shapes `xs[i]` appears in across the
// toolchain:
//
//   - List<T>[i]      → T    (direct element access, the dominant case)
//   - Map<K, V>[k]    → V    (index form that panics on miss; matches
//     the native backend's intrinsic dispatch)
//   - Bytes[i]        → Byte
//   - String[i]       → Char (semantically a code-point read, though
//     real Osty source uses .chars() / .bytes()
//     and almost never String[i] directly)
//
// Returns ErrTypeVal when the base itself is un-typed or non-indexable
// — leaving the cascade behaviour from before the recovery.
func recoverIndexType(base Expr) Type {
	if base == nil {
		return ErrTypeVal
	}
	return recoverIndexTypeFromType(base.Type())
}

func recoverIndexTypeFromType(bt Type) Type {
	if bt == nil || bt == ErrTypeVal {
		return ErrTypeVal
	}
	switch t := bt.(type) {
	case *NamedType:
		if t.Builtin {
			switch t.Name {
			case "List":
				if len(t.Args) == 1 && t.Args[0] != nil {
					return t.Args[0]
				}
			case "Map":
				if len(t.Args) == 2 && t.Args[1] != nil {
					return t.Args[1]
				}
			}
		}
	case *PrimType:
		switch t.Kind {
		case PrimBytes:
			return &PrimType{Kind: PrimByte}
		case PrimString:
			return &PrimType{Kind: PrimChar}
		}
	}
	return ErrTypeVal
}

func isPrim(t Type, k PrimKind) bool {
	if p, ok := t.(*PrimType); ok {
		return p.Kind == k
	}
	return false
}

func isIntegral(t Type) bool {
	p, ok := t.(*PrimType)
	if !ok {
		return false
	}
	switch p.Kind {
	case PrimInt, PrimInt8, PrimInt16, PrimInt32, PrimInt64,
		PrimUInt8, PrimUInt16, PrimUInt32, PrimUInt64, PrimByte:
		return true
	}
	return false
}

func isFloat(t Type) bool {
	p, ok := t.(*PrimType)
	if !ok {
		return false
	}
	switch p.Kind {
	case PrimFloat, PrimFloat32, PrimFloat64:
		return true
	}
	return false
}

// numericResult mirrors `binNumericCommon` in the native elaborator:
// float dominates; otherwise prefer a concrete Int over untyped-int.
func numericResult(lt, rt Type) Type {
	if isFloat(lt) || isFloat(rt) {
		// Prefer the concrete Float type over a polymorphic literal.
		if isPrim(lt, PrimFloat) || isPrim(rt, PrimFloat) {
			return TFloat
		}
		if isFloat(lt) {
			return lt
		}
		return rt
	}
	if !isIntegral(lt) || !isIntegral(rt) {
		return ErrTypeVal
	}
	// Concrete Int wins over the default lane.
	if isPrim(lt, PrimInt) || isPrim(rt, PrimInt) {
		return TInt
	}
	return lt
}

func binaryOp(k token.Kind) (BinOp, bool) {
	switch k {
	case token.PLUS:
		return BinAdd, true
	case token.MINUS:
		return BinSub, true
	case token.STAR:
		return BinMul, true
	case token.SLASH:
		return BinDiv, true
	case token.PERCENT:
		return BinMod, true
	case token.EQ:
		return BinEq, true
	case token.NEQ:
		return BinNeq, true
	case token.LT:
		return BinLt, true
	case token.LEQ:
		return BinLeq, true
	case token.GT:
		return BinGt, true
	case token.GEQ:
		return BinGeq, true
	case token.AND:
		return BinAnd, true
	case token.OR:
		return BinOr, true
	case token.BITAND:
		return BinBitAnd, true
	case token.BITOR:
		return BinBitOr, true
	case token.BITXOR:
		return BinBitXor, true
	case token.SHL:
		return BinShl, true
	case token.SHR:
		return BinShr, true
	}
	return 0, false
}

func (l *lowerer) lowerCall(e *ast.CallExpr) Expr {
	// Detect a print-family intrinsic on a bare identifier.
	if id, ok := e.Fn.(*ast.Ident); ok {
		if k, isIntrinsic := intrinsicByName(id.Name); isIntrinsic {
			out := &IntrinsicCall{Kind: k, SpanV: nodeSpan(e)}
			for _, a := range e.Args {
				lowered := l.lowerArg(a)
				// Auto-`.toString()` for non-primitive print args.
				// `println(p)` for a struct / enum value walls at
				// the LLVM emit path with `LLVM000 println of
				// non-primitive <Type>` because emitPrintlnLike
				// only knows about primitives. Wrap the arg in an
				// explicit toString() method call so the user-side
				// or auto-derived toString impl handles the boxing
				// — same shape as the existing string-interp
				// boxing path that already works for `"{p}"`.
				if printlnLikeKind(k) && shouldAutoToString(lowered.Value) {
					lowered.Value = &MethodCall{
						Receiver: lowered.Value,
						Name:     "toString",
						T:        TString,
						SpanV:    lowered.Value.At(),
					}
				}
				out.Args = append(out.Args, lowered)
			}
			return out
		}
		// Check if this is a variant constructor: e.g. Some(42), Ok(x).
		if sym := l.symbol(id); sym != nil {
			if sym.Kind == resolve.SymVariant {
				return l.lowerVariantCall(e, "", sym.Name)
			}
			if sym.Kind == resolve.SymBuiltin && isPreludeVariantName(sym.Name) {
				return l.lowerVariantCall(e, "", sym.Name)
			}
		}
		// Stdlib body lowering sometimes runs with file-owned resolve
		// data projected out of the cached registry. If a prelude
		// constructor ref is absent from that lightweight projection,
		// keep the source-level meaning instead of letting Ok/Err/Some
		// degrade into unresolved function calls.
		if isPreludeVariantName(id.Name) {
			return l.lowerVariantCall(e, "", id.Name)
		}
	}
	// Strip a turbofish wrapper to retain its type arguments.
	var typeArgs []Type
	fn := e.Fn
	if tf, ok := fn.(*ast.TurbofishExpr); ok {
		for _, a := range tf.Args {
			typeArgs = append(typeArgs, l.lowerType(a))
		}
		fn = tf.Base
	}
	// Method call: x.name(args).
	if fx, ok := fn.(*ast.FieldExpr); ok {
		if lowered := l.tryLowerBuilderChain(e, fx); lowered != nil {
			return lowered
		}
		if id, ok := fx.X.(*ast.Ident); ok {
			if sym := l.symbol(id); sym != nil {
				if sym.Kind == resolve.SymEnum || sym.Kind == resolve.SymStruct {
					if l.isVariantOfEnum(sym, fx.Name) {
						return l.lowerVariantCall(e, sym.Name, fx.Name)
					}
				}
				// Module-qualified call: `use std.strings` makes
				// `strings.compare(...)` a free-function call on the
				// stdlib module, not a method dispatch on a value.
				// Preserving the qualified FieldExpr shape lets
				// backends rewrite the callsite to a mangled symbol
				// when the module body is injected (see
				// backend.RewriteStdlibCallsites). Without this branch
				// the call would fall into lowerMethodCall and emit a
				// MethodCall node, which backends currently cannot
				// dispatch.
				if sym.Kind == resolve.SymPackage {
					return l.lowerQualifiedCall(e, fx, typeArgs)
				}
			}
		}
		return l.lowerMethodCall(e, fx, typeArgs)
	}
	// Fall back to the checker's monomorphisation record when no
	// turbofish was written but the callee is generic.
	if len(typeArgs) == 0 {
		typeArgs = l.instantiationArgs(e)
	}
	callee := l.lowerExpr(fn)
	t := l.exprType(e)
	if t == ErrTypeVal || t == nil || hasPoisonedTypeArg(t) {
		// Prefer the callee's FnType.Return when the call's own
		// type is missing or carries a poisoned type-arg (the
		// embedded checker sometimes records `Result<Int, Error>`
		// as `Result<Int, <error>>` because the inner `Error`
		// reference doesn't resolve at the native-checker
		// boundary). The callee's FnType is seeded from the
		// resolver's symbol table for top-level fns and tends to
		// carry a fully-resolved return shape.
		if recovered := recoverCallReturnType(callee); recovered != nil && recovered != ErrTypeVal && !hasPoisonedTypeArg(recovered) {
			t = recovered
		}
		// Final fallback for bare-Ident callees whose FnType is
		// also poisoned: re-lower the resolved fn declaration's AST
		// return type. The resolver's Symbol.Decl points at the
		// originating ast.FnDecl, whose ReturnType node carries
		// fully-syntactic names — re-running lowerType on it
		// produces a fresh, non-poisoned IR Type even when the
		// checker's typed-node table dropped the inner reference.
		if t == nil || t == ErrTypeVal || hasPoisonedTypeArg(t) {
			if id, ok := fn.(*ast.Ident); ok {
				if rec := l.recoverFnDeclReturnType(id); rec != nil && rec != ErrTypeVal && !hasPoisonedTypeArg(rec) {
					t = rec
				}
			}
		}
		if t == nil || t == ErrTypeVal || hasPoisonedTypeArg(t) {
			if rec := l.bindingTypeFromAST(e); usableRecoveredType(rec) {
				t = rec
			}
		}
	}
	out := &CallExpr{
		Callee:   callee,
		TypeArgs: typeArgs,
		T:        t,
		SpanV:    nodeSpan(e),
	}
	for _, a := range e.Args {
		out.Args = append(out.Args, l.lowerArg(a))
	}
	l.backfillClosureArgsFromCallExpr(out, e)
	return out
}

// backfillClosureArgsFromCallExpr propagates the callee's
// expected param types into Closure-shaped args. The fn type
// resolution prefers the Callee Ident's T when usable; falls
// back to looking up the AST FnDecl for bare-Ident callees
// (e.g. same-module user fns whose Ident.T didn't survive
// checker-side resolution).
//
// Mirrors `backfillClosureArgsFromMethodCall` for the user-fn /
// free-fn call shape: `apply(|x| x * 2, 5)` where
// `apply: fn(fn(Int) -> Int, Int) -> Int` needs the closure arg
// to learn its param/return types from the callee's FnType.
func (l *lowerer) backfillClosureArgsFromCallExpr(c *CallExpr, astCall *ast.CallExpr) {
	if c == nil {
		return
	}
	hasClosureArg := false
	for _, a := range c.Args {
		if _, ok := a.Value.(*Closure); ok {
			hasClosureArg = true
			break
		}
	}
	if !hasClosureArg {
		return
	}
	fnT := l.resolveCalleeFnType(c.Callee, astCall)
	if fnT == nil {
		return
	}
	for i := range c.Args {
		if i >= len(fnT.Params) {
			break
		}
		cl, ok := c.Args[i].Value.(*Closure)
		if !ok || cl == nil {
			continue
		}
		expected, ok := fnT.Params[i].(*FnType)
		if !ok || expected == nil {
			continue
		}
		backfillClosure(cl, expected)
	}
}

// resolveCalleeFnType picks the FnType for a CallExpr's callee.
// Prefers the lowered Callee's T when it's a FnType; falls back
// to the AST callee's resolved fn declaration so same-module
// references whose Ident.T didn't make the round trip from the
// resolver still resolve.
func (l *lowerer) resolveCalleeFnType(callee Expr, astCall *ast.CallExpr) *FnType {
	if callee == nil {
		return nil
	}
	if fnT, ok := callee.Type().(*FnType); ok && fnT != nil {
		return fnT
	}
	if astCall == nil {
		return nil
	}
	id, ok := astCall.Fn.(*ast.Ident)
	if !ok || id == nil {
		return nil
	}
	if l.res != nil && l.res.RefsByID != nil {
		if sym := l.ref(id); sym != nil && sym.Decl != nil {
			if fn, ok := sym.Decl.(*ast.FnDecl); ok && fn != nil {
				return l.fnTypeFromAST(fn)
			}
		}
	}
	if l.file != nil {
		for _, decl := range l.file.Decls {
			if fn, ok := decl.(*ast.FnDecl); ok && fn != nil && fn.Name == id.Name {
				return l.fnTypeFromAST(fn)
			}
		}
	}
	return nil
}

// fnTypeFromAST builds a FnType from a resolved ast.FnDecl using
// the existing `lowerType` machinery. Returns nil if the decl is
// nil or every param/return slot resolves to ErrTypeVal.
func (l *lowerer) fnTypeFromAST(fn *ast.FnDecl) *FnType {
	if fn == nil {
		return nil
	}
	out := &FnType{}
	for _, p := range fn.Params {
		if p == nil {
			out.Params = append(out.Params, ErrTypeVal)
			continue
		}
		out.Params = append(out.Params, l.lowerType(p.Type))
	}
	out.Return = l.lowerType(fn.ReturnType)
	if out.Return == nil {
		out.Return = TUnit
	}
	return out
}

type builderLowerSetter struct {
	name string
	arg  *ast.Arg
}

func (l *lowerer) tryLowerBuilderChain(call *ast.CallExpr, build *ast.FieldExpr) Expr {
	if call == nil || build == nil || build.Name != "build" || build.IsOptional || len(call.Args) != 0 {
		return nil
	}
	var setters []builderLowerSetter
	cursor := build.X
	for {
		inner, ok := cursor.(*ast.CallExpr)
		if !ok {
			return nil
		}
		fe, ok := inner.Fn.(*ast.FieldExpr)
		if !ok || fe.IsOptional {
			return nil
		}
		if fe.Name == "builder" {
			if len(inner.Args) != 0 {
				return nil
			}
			id, ok := fe.X.(*ast.Ident)
			if !ok {
				return nil
			}
			sd := l.structDeclByIdent(id)
			if sd == nil {
				return nil
			}
			if !check.ClassifyBuilderDerive(sd).Derivable {
				return nil
			}
			return l.lowerBuilderStructLit(call, sd, nil, setters)
		}
		if fe.Name == "toBuilder" {
			if len(inner.Args) != 0 {
				return nil
			}
			// Lower the receiver up front so we can use the IR expr's
			// type as a fallback when AST-side struct-decl lookup
			// fails — the embedded selfhost checker doesn't always
			// populate `Types[ident]` for value-bound idents (`let p
			// = Point{...}` followed by `p.toBuilder()`). The lowered
			// IR Ident's `T` field is filled by `lowerIdent`'s
			// `bindingPatTypes` fallback whenever the binding's
			// resolver Symbol points at an `IdentPat` recorded by
			// `lowerLetStmt`.
			recv := l.lowerExpr(fe.X)
			sd := l.structDeclFromReceiver(fe.X)
			if sd == nil && recv != nil {
				sd = l.structDeclByType(recv.Type())
			}
			if sd == nil {
				return nil
			}
			if !check.ClassifyBuilderDerive(sd).Derivable {
				return nil
			}
			return l.lowerBuilderStructLit(call, sd, recv, setters)
		}
		if len(inner.Args) != 1 {
			return nil
		}
		arg := inner.Args[0]
		if arg == nil || arg.Name != "" || arg.Value == nil {
			return nil
		}
		setters = append(setters, builderLowerSetter{name: fe.Name, arg: arg})
		cursor = fe.X
	}
}

func (l *lowerer) structDeclByIdent(id *ast.Ident) *ast.StructDecl {
	if id == nil {
		return nil
	}
	if sym := l.symbol(id); sym != nil {
		if sd, ok := sym.Decl.(*ast.StructDecl); ok {
			return sd
		}
	}
	return l.structDeclByName(id.Name)
}

func (l *lowerer) structDeclFromReceiver(e ast.Expr) *ast.StructDecl {
	if e == nil {
		return nil
	}
	switch n := e.(type) {
	case *ast.StructLit:
		switch head := n.Type.(type) {
		case *ast.Ident:
			return l.structDeclByIdent(head)
		case *ast.FieldExpr:
			return l.structDeclByName(head.Name)
		}
	case *ast.ParenExpr:
		return l.structDeclFromReceiver(n.X)
	case *ast.Ident:
		// Type-name idents resolve directly via the resolver symbol.
		if sd := l.structDeclByIdent(n); sd != nil {
			return sd
		}
		// Value-bound idents (let p = Point{...}) carry their inferred
		// type on the per-node Types map or on the resolver Symbol's
		// SymTypes entry. Mirror the lowerIdent fallback chain so
		// `p.toBuilder()` resolves to Point's StructDecl when one of
		// those maps covers the receiver.
		if sd := l.structDeclByType(l.exprType(e)); sd != nil {
			return sd
		}
		if l.chk != nil {
			if sym := l.symbol(n); sym != nil {
				if sd := l.structDeclByType(l.nativeBindingTypeForSymbol(sym)); sd != nil {
					return sd
				}
				if sd := l.structDeclByType(l.nativeSymbolType(sym)); sd != nil {
					return sd
				}
				if st := l.chk.SymTypes[sym]; st != nil {
					if sd := l.structDeclByType(l.fromCheckerType(st)); sd != nil {
						return sd
					}
				}
				// Walk to the declared type on the symbol's
				// declaration node. Covers `let p: Point = ...` and
				// `fn f(p: Point)` with explicit annotations.
				if sd := l.structDeclFromSymbolDecl(sym); sd != nil {
					return sd
				}
				// Final fallback: consult `bindingPatTypes` populated
				// by `lowerLetStmt` for the case `let p = Point
				// {...}` where the binding's `Decl` points at the
				// IdentPat (without an explicit Type node). Read here
				// rather than in `lowerIdent` to keep the per-Ident
				// type-fill path untouched — downstream MIR
				// mut-receiver write-back logic depends on the
				// existing ErrTypeVal-poisoned receiver shape and
				// regresses when value-side idents start carrying
				// real types via the per-node `T` field.
				if l.bindingPatTypes != nil {
					if ip, ok := sym.Decl.(*ast.IdentPat); ok {
						if t := l.bindingPatTypes[ip]; t != nil {
							if sd := l.structDeclByType(t); sd != nil {
								return sd
							}
						}
					}
				}
			}
		}
		return nil
	}
	return l.structDeclByType(l.exprType(e))
}

// structDeclFromSymbolDecl walks the declaration node carried by a
// resolved Symbol and extracts the struct decl behind the binding's
// declared / inferred type. Mirrors the LetStmt / Param branch logic
// the AST checker uses to seed the `Types` map; callers fall back to
// this when neither the per-node Types map nor SymTypes covers the
// receiver's identifier.
func (l *lowerer) structDeclFromSymbolDecl(sym *resolve.Symbol) *ast.StructDecl {
	if sym == nil || sym.Decl == nil {
		return nil
	}
	if sd := l.structDeclByType(l.nativeBindingTypeForSymbol(sym)); sd != nil {
		return sd
	}
	if sd := l.structDeclByType(l.nativeSymbolType(sym)); sd != nil {
		return sd
	}
	switch d := sym.Decl.(type) {
	case *ast.LetStmt:
		if d.Type != nil {
			return l.structDeclFromTypeNode(d.Type)
		}
		return l.structDeclFromReceiver(d.Value)
	case *ast.LetDecl:
		if d.Type != nil {
			return l.structDeclFromTypeNode(d.Type)
		}
		return l.structDeclFromReceiver(d.Value)
	case *ast.Param:
		if d.Type != nil {
			return l.structDeclFromTypeNode(d.Type)
		}
	}
	return nil
}

// structDeclFromTypeNode unwraps an `ast.Type` written in source (e.g.
// the `Point` in `let p: Point = ...`) and returns its StructDecl.
// Only the bare `Ident` and `FieldExpr` (qualified) shapes are
// handled — those are what the receiver chain needs; richer type
// expressions (Optional, List<T>, etc.) don't resolve to a struct.
func (l *lowerer) structDeclFromTypeNode(t ast.Type) *ast.StructDecl {
	switch n := t.(type) {
	case *ast.NamedType:
		if len(n.Path) == 1 {
			return l.structDeclByName(n.Path[0])
		}
		if len(n.Path) > 0 {
			return l.structDeclByName(n.Path[len(n.Path)-1])
		}
	}
	return nil
}

func (l *lowerer) structDeclByType(t Type) *ast.StructDecl {
	nt, ok := t.(*NamedType)
	if !ok || nt == nil || nt.Builtin {
		return nil
	}
	return l.structDeclByName(nt.Name)
}

func (l *lowerer) structDeclByName(name string) *ast.StructDecl {
	if name == "" {
		return nil
	}
	if l.file != nil {
		for _, decl := range l.file.Decls {
			if sd, ok := decl.(*ast.StructDecl); ok && sd != nil && sd.Name == name {
				return sd
			}
		}
	}
	if l.res != nil && l.res.FileScope != nil {
		if sym := l.res.FileScope.Lookup(name); sym != nil {
			if sd, ok := sym.Decl.(*ast.StructDecl); ok {
				return sd
			}
		}
	}
	return nil
}

func (l *lowerer) lowerBuilderStructLit(
	call *ast.CallExpr,
	sd *ast.StructDecl,
	spread Expr,
	setters []builderLowerSetter,
) Expr {
	if call == nil || sd == nil {
		return nil
	}
	byName := make(map[string]*ast.Arg, len(setters))
	for _, setter := range setters {
		if setter.arg == nil {
			continue
		}
		if _, exists := byName[setter.name]; exists {
			continue
		}
		byName[setter.name] = setter.arg
	}
	out := &StructLit{
		TypeName: sd.Name,
		T:        l.exprType(call),
		Spread:   spread,
		SpanV:    nodeSpan(call),
	}
	for _, field := range sd.Fields {
		if field == nil || !field.Pub {
			continue
		}
		arg := byName[field.Name]
		if arg == nil || arg.Value == nil {
			continue
		}
		out.Fields = append(out.Fields, StructLitField{
			Name:  field.Name,
			Value: l.lowerExpr(arg.Value),
			SpanV: Span{Start: posFromToken(arg.Pos()), End: posFromToken(arg.End())},
		})
	}
	return out
}

// hasPoisonedTypeArg reports whether `t` carries an `<error>` /
// `ErrTypeVal` somewhere in its type-argument tree. The embedded
// selfhost checker sometimes records `Result<Int, Error>` with the
// second arg dropped to `<error>` because the inner `Error` lookup
// missed at the native-checker boundary; downstream MIR / LLVM
// rendering then produces opaque suffixes (`%Result.i64.opaque`
// instead of `%Result.i64.Error`) that fail LLVM verification when
// the Aggregate's slot type was rendered from the function's
// non-poisoned return type. Callers gate their stdlib-signature
// recovery on this so partial poisoning still routes through the
// recoverMethodReturnType / recoverCallReturnType chain.
func hasPoisonedTypeArg(t Type) bool {
	if t == nil || t == ErrTypeVal {
		return false
	}
	switch x := t.(type) {
	case *NamedType:
		for _, a := range x.Args {
			if a == ErrTypeVal {
				return true
			}
			// PrimInvalid placeholder ({Kind: 0}) is what the
			// checker leaves on type args when an upstream inference
			// path bailed but didn't surface ErrType — `let m =
			// {"a": 1}` ends up with `Map<*PrimType{0}, *PrimType{0}>`.
			// Treat it as poisoned so recovery paths fire.
			if pt, ok := a.(*PrimType); ok && pt.Kind == PrimInvalid {
				return true
			}
			if hasPoisonedTypeArg(a) {
				return true
			}
		}
	case *OptionalType:
		if x.Inner == ErrTypeVal {
			return true
		}
		if pt, ok := x.Inner.(*PrimType); ok && pt.Kind == PrimInvalid {
			return true
		}
		return hasPoisonedTypeArg(x.Inner)
	case *TupleType:
		for _, e := range x.Elems {
			if e == ErrTypeVal {
				return true
			}
			if hasPoisonedTypeArg(e) {
				return true
			}
		}
	case *FnType:
		for _, p := range x.Params {
			if p == ErrTypeVal {
				return true
			}
			if hasPoisonedTypeArg(p) {
				return true
			}
		}
		if x.Return == ErrTypeVal {
			return true
		}
		return hasPoisonedTypeArg(x.Return)
	}
	return false
}

// recoverFnDeclReturnType walks the resolver Symbol behind a callee
// Ident, fetches the originating ast.FnDecl, and re-lowers its
// declared return type via `lowerType`. Used as the last-resort
// fallback when both the call's own checker entry and the callee
// Ident's FnType carry poisoned (`<error>`) type args — the AST node
// still has the fully-syntactic source form, so a fresh round-trip
// through `lowerType` produces a non-poisoned IR shape.
func (l *lowerer) recoverFnDeclReturnType(id *ast.Ident) Type {
	if id == nil || l.res == nil {
		return nil
	}
	sym := l.ref(id)
	if sym == nil || sym.Decl == nil {
		return nil
	}
	fn, ok := sym.Decl.(*ast.FnDecl)
	if !ok || fn == nil || fn.ReturnType == nil {
		return nil
	}
	return l.lowerType(fn.ReturnType)
}

// recoverCallReturnType pulls the return type off the callee's FnType
// when the checker did not record a type for the call expression. The
// resolver seeds symbol types for top-level fns and `use`-imported
// functions, which propagate to the callee Ident during lowerIdent,
// so the FnType is usually present even when the call's own TypedNode
// is missing at the native-checker boundary.
func recoverCallReturnType(callee Expr) Type {
	if callee == nil {
		return ErrTypeVal
	}
	ct := callee.Type()
	if ct == nil || ct == ErrTypeVal {
		return ErrTypeVal
	}
	if f, ok := ct.(*FnType); ok && f.Return != nil {
		return f.Return
	}
	return ErrTypeVal
}

// recoverMethodCallType patches the one method-call shape that the
// generic callee-return recovery cannot see: `recv.downcast::<T>()`.
// The IR method form stores only the receiver + method name, so the
// synthetic checker signature (`Error.downcast::<T>() -> T?`) is not
// available as a first-class FnType on the lowered node. When the
// checker/native-checker boundary drops the call's own type but still
// records the turbofish args, recover the spec-mandated `T?` surface.
func recoverMethodCallType(name string, typeArgs []Type) Type {
	if name == "downcast" && len(typeArgs) == 1 && typeArgs[0] != nil && typeArgs[0] != ErrTypeVal {
		return &OptionalType{Inner: typeArgs[0]}
	}
	return ErrTypeVal
}

// lowerArg lowers a single call argument, preserving its keyword name
// when present.
func (l *lowerer) lowerArg(a *ast.Arg) Arg {
	return Arg{
		Name:  a.Name,
		Value: l.lowerExpr(a.Value),
		SpanV: Span{Start: posFromToken(a.Pos()), End: posFromToken(a.End())},
	}
}

// instantiationArgs returns the concrete type-argument list the
// checker recorded for this call site (monomorphisation info), or nil
// when the checker did not annotate it.
func (l *lowerer) instantiationArgs(e *ast.CallExpr) []Type {
	if l.chk == nil || e == nil {
		return nil
	}
	if args := l.nativeInstantiationArgs(e); len(args) > 0 {
		return args
	}
	if l.chk.InstantiationsByID == nil {
		return nil
	}
	raw, ok := l.chk.InstantiationsByID[e.ID]
	if !ok || len(raw) == 0 {
		return nil
	}
	out := make([]Type, 0, len(raw))
	for _, ta := range raw {
		out = append(out, l.fromCheckerType(ta))
	}
	return out
}

// isVariantOfEnum reports whether variantName is a declared variant on
// the enum named by sym. Consults the checker's type description table
// when available.
func (l *lowerer) isVariantOfEnum(sym *resolve.Symbol, variantName string) bool {
	if sym == nil || sym.Kind != resolve.SymEnum || sym.Decl == nil {
		return false
	}
	ed, ok := sym.Decl.(*ast.EnumDecl)
	if !ok {
		return false
	}
	for _, v := range ed.Variants {
		if v.Name == variantName {
			return true
		}
	}
	return false
}

func (l *lowerer) variantTypeFromQualifiedField(fx *ast.FieldExpr) Type {
	if fx == nil {
		return nil
	}
	id, ok := fx.X.(*ast.Ident)
	if !ok {
		return nil
	}
	sym := l.symbol(id)
	if sym == nil || sym.Kind != resolve.SymEnum || !l.isVariantOfEnum(sym, fx.Name) {
		return nil
	}
	return l.enumTypeFromSymbol(sym)
}

func (l *lowerer) variantOwnerTypeFromSymbol(sym *resolve.Symbol) Type {
	if sym == nil {
		return nil
	}
	if v, ok := sym.Decl.(*ast.Variant); ok {
		return l.enumTypeForVariantDecl(v)
	}
	return nil
}

func (l *lowerer) enumTypeFromSymbol(sym *resolve.Symbol) Type {
	if sym == nil || sym.Kind != resolve.SymEnum {
		return nil
	}
	if ed, ok := sym.Decl.(*ast.EnumDecl); ok && ed != nil {
		return enumDeclType(ed)
	}
	if sym.Name == "" {
		return nil
	}
	return &NamedType{Name: sym.Name}
}

func (l *lowerer) enumTypeForVariantDecl(v *ast.Variant) Type {
	ed := l.enumDeclForVariant(v)
	if ed == nil {
		return nil
	}
	return enumDeclType(ed)
}

func enumDeclType(ed *ast.EnumDecl) Type {
	if ed == nil || ed.Name == "" {
		return nil
	}
	args := make([]Type, 0, len(ed.Generics))
	for _, gp := range ed.Generics {
		if gp == nil || gp.Name == "" {
			continue
		}
		args = append(args, &TypeVar{Name: gp.Name})
	}
	return &NamedType{Name: ed.Name, Args: args}
}

func (l *lowerer) enumDeclForVariant(v *ast.Variant) *ast.EnumDecl {
	if v == nil || l.file == nil {
		return nil
	}
	for _, decl := range l.file.Decls {
		ed, ok := decl.(*ast.EnumDecl)
		if !ok || ed == nil {
			continue
		}
		for _, variant := range ed.Variants {
			if variant == v || (variant != nil && variant.Name == v.Name) {
				return ed
			}
		}
	}
	return nil
}

// lowerMethodCall lowers `receiver.name(args)` into an IR MethodCall,
// preserving turbofish type arguments.
// lowerQualifiedCall lowers `module.fn(args)` — where `module` resolves
// to a `use`-imported package alias — as a CallExpr whose callee is a
// FieldExpr, preserving the `(module, fn)` pair for downstream passes
// (stdlib reachability scan, callsite rewriting during stdlib body
// injection). The FieldExpr shape is deliberately chosen to match how a
// caller-constructed IR module would write the same call; a single
// consumer shape keeps ir.Reach and backend.RewriteStdlibCallsites
// uniform.
func (l *lowerer) lowerQualifiedCall(e *ast.CallExpr, fx *ast.FieldExpr, typeArgs []Type) Expr {
	if len(typeArgs) == 0 {
		typeArgs = l.instantiationArgs(e)
	}
	callee := &FieldExpr{
		X:     l.lowerExpr(fx.X),
		Name:  fx.Name,
		T:     l.exprType(fx),
		SpanV: nodeSpan(fx),
	}
	t := l.exprType(e)
	if t == ErrTypeVal {
		t = recoverCallReturnType(callee)
	}
	// The checker doesn't register `use X { fn Y(...) -> R }`
	// member signatures on the package symbol, so `host.Y` ends up
	// as <error> in both the checker types map and the callee's
	// FnType. Fall back to reading the UseDecl body (for inline FFI
	// signatures) or the resolved package scope (for stdlib /
	// workspace modules) directly: the AST already has the signature,
	// we just need to lower it. Without this, MIR's typeSupported
	// rejects the synthetic result temp with `unsupported local type
	// <error>` for every runtime / stdlib module call site.
	if t == ErrTypeVal || t == nil {
		if id, ok := fx.X.(*ast.Ident); ok {
			if sym := l.symbol(id); sym != nil && sym.Kind == resolve.SymPackage {
				if ud, ok := sym.Decl.(*ast.UseDecl); ok && ud != nil {
					if ret := l.lookupUseDeclFnReturn(ud, fx.Name); ret != nil {
						t = ret
					}
				}
				if (t == ErrTypeVal || t == nil) && sym.Package != nil {
					if ret := l.lookupPackageFnReturn(sym.Package, fx.Name); ret != nil {
						t = ret
					}
				}
				// stdlib registry fallback: when sym.Package is nil (common for
				// bare `use std.X` in single-file test contexts), query the
				// loaded stdlib registry directly via the module key derived
				// from the UseDecl path segments after "std".
				if (t == ErrTypeVal || t == nil) && stdlib.LoadCached() != nil {
					if ud, ok := sym.Decl.(*ast.UseDecl); ok && ud != nil && len(ud.Path) >= 2 && ud.Path[0] == "std" {
						modKey := strings.Join(ud.Path[1:], ".")
						if fn := stdlib.LoadCached().LookupFnDecl(modKey, fx.Name); fn != nil {
							if fn.ReturnType == nil {
								t = TUnit
							} else {
								// Stdlib stub AST node IDs collide with the
								// current file's resolver mappings, so bypass
								// l.res entirely when lowering the return type.
								t = l.lowerStdlibType(fn.ReturnType)
							}
						}
					}
				}
			}
		}
	}
	out := &CallExpr{
		Callee:   callee,
		TypeArgs: typeArgs,
		T:        t,
		SpanV:    nodeSpan(e),
	}
	for _, a := range e.Args {
		out.Args = append(out.Args, l.lowerArg(a))
	}
	return out
}

// lookupUseDeclFnReturn scans a `use X { ... }` body for an fn named
// fnName and returns its lowered return type (TUnit when the source
// declared no return type). Returns nil when no matching fn is found.
// This is the fallback path used by lowerQualifiedCall when the
// checker left the call's result type as <error>.
func (l *lowerer) lookupUseDeclFnReturn(ud *ast.UseDecl, fnName string) Type {
	if ud == nil {
		return nil
	}
	for _, d := range ud.GoBody {
		fn, ok := d.(*ast.FnDecl)
		if !ok || fn == nil || fn.Name != fnName {
			continue
		}
		if fn.ReturnType == nil {
			return TUnit
		}
		return l.lowerType(fn.ReturnType)
	}
	return nil
}

// lookupPackageFnReturn finds a top-level public fn named fnName in
// the resolved package and returns its lowered return type. Used by
// lowerQualifiedCall as the fallback when the UseDecl is a bare
// `use std.strings as X` (no inline FFI body) — the fn lives in the
// package's PkgScope, and its AST is on the resolved package file.
func (l *lowerer) lookupPackageFnReturn(pkg *resolve.Package, fnName string) Type {
	if pkg == nil {
		return nil
	}
	for _, pf := range pkg.Files {
		if pf == nil || pf.File == nil {
			continue
		}
		for _, decl := range pf.File.Decls {
			fn, ok := decl.(*ast.FnDecl)
			if !ok || fn == nil || fn.Name != fnName {
				continue
			}
			if fn.ReturnType == nil {
				return TUnit
			}
			return l.lowerType(fn.ReturnType)
		}
	}
	return nil
}

func (l *lowerer) lowerMethodCall(e *ast.CallExpr, fx *ast.FieldExpr, typeArgs []Type) Expr {
	if len(typeArgs) == 0 {
		typeArgs = l.instantiationArgs(e)
	}
	recv := l.lowerExpr(fx.X)
	t := l.exprType(e)
	// TypeVar-leaked method-result types ride a separate recovery
	// arm. They're not "poisoned" in the broader sense — the body of
	// a generic fn legitimately carries TypeVars before
	// monomorphization — but at a *user-side* method call site, a
	// result like `m.get(1)` returning `V?` instead of the
	// substituted `String?` means the call's substitution slipped
	// past the checker. Routing those through the same recovery as
	// poisoned types lets us pull the concrete arg off the
	// receiver's type without disturbing the legitimate generic-
	// body case (TypeVars in injected stdlib bodies are not method
	// call sites — they're FnDecl-level signatures handled by the
	// monomorpher).
	leaksTypeVar := containsTypeVar(t)
	if t == ErrTypeVal || t == nil || hasPoisonedTypeArg(t) || leaksTypeVar {
		if recovered := recoverMethodCallType(fx.Name, typeArgs); recovered != ErrTypeVal {
			t = recovered
		}
		// Recover from builtin method signatures when the checker
		// left the call type unpopulated or recorded it with poisoned
		// type args. Covers the common shapes from List / Map / Set /
		// String / Bytes: `.len()`, `.isEmpty()`, `.contains(x)`,
		// `.startsWith(s)`, etc., and `.toInt()` / `.toFloat()` on
		// String which produce a `Result<T, Error>` whose `Error`
		// arg the embedded checker sometimes records as `<error>`.
		// Without these, one method call with a checker-skipped
		// receiver poisons every enclosing expression and blocks
		// MIR / propagates `<error>` into Match scrutinee shapes.
		if recovered := recoverMethodReturnType(fx.Name, recv); recovered != nil {
			if t == nil || t == ErrTypeVal || hasPoisonedTypeArg(t) || containsTypeVar(t) {
				t = recovered
			}
		}
		// User-defined method recovery: look up the method on the
		// receiver's struct/enum declaration via the resolver. The
		// stdlib intrinsic table above only covers builtin containers
		// + String + Bytes; without this branch a call like
		// `b.capacity()` (user-method on `struct Buf`) lowers with
		// T=<error> post-#1645 (the checker's per-node Types map is
		// no longer populated and the byID/byKey native lookups miss),
		// which silently poisons every enclosing expression.
		if t == nil || t == ErrTypeVal || hasPoisonedTypeArg(t) || containsTypeVar(t) {
			if recovered := l.recoverUserMethodReturnType(fx.Name, recv); recovered != nil {
				t = recovered
			}
		}
		// AST-side receiver recovery: when the IR receiver carries
		// `<error>` (typical for `self` inside methods, or method
		// chains where the inner call's T didn't resolve), recover
		// the receiver's static type via `resolveExprStaticType` and
		// retry the intrinsic / user-method tables with the better
		// type. Critical for examples/gc-style code where every
		// method body is `self.tryFoo(...).unwrap()`.
		if t == nil || t == ErrTypeVal || hasPoisonedTypeArg(t) || containsTypeVar(t) {
			if astRecvT := l.resolveExprStaticType(fx.X); astRecvT != nil && astRecvT != ErrTypeVal {
				if recovered := recoverMethodReturnTypeFromType(fx.Name, astRecvT); recovered != nil {
					t = recovered
				}
				if t == nil || t == ErrTypeVal || hasPoisonedTypeArg(t) || containsTypeVar(t) {
					// User method via NamedType receiver.
					if nt, ok := astRecvT.(*NamedType); ok && nt != nil && !nt.Builtin {
						if sd := l.structDeclByName(nt.Name); sd != nil {
							for _, m := range sd.Methods {
								if m == nil || m.Name != fx.Name {
									continue
								}
								if m.ReturnType == nil {
									t = TUnit
									break
								}
								if lt := l.lowerType(m.ReturnType); lt != nil && lt != ErrTypeVal {
									t = lt
									break
								}
							}
						}
					}
				}
			}
		}
		// Use-alias FFI method recovery: `strings.ToUpper(name)` parses
		// as `MethodCall{Receiver: Ident("strings"), Name: "ToUpper"}`.
		// The receiver Ident resolves to a `*ast.UseDecl` symbol, not a
		// nominal value type, so the intrinsic / user-method paths above
		// bail out. Look up the fn signature in the UseDecl body.
		if t == nil || t == ErrTypeVal || hasPoisonedTypeArg(t) || containsTypeVar(t) {
			if rt := l.useAliasFnReturnTypeFromAST(fx); rt != nil && rt != ErrTypeVal {
				t = rt
			}
		}
	}
	out := &MethodCall{
		Receiver: recv,
		Name:     fx.Name,
		TypeArgs: typeArgs,
		T:        t,
		SpanV:    nodeSpan(e),
	}
	for _, a := range e.Args {
		out.Args = append(out.Args, l.lowerArg(a))
	}
	// Closure-arg backfill: the checker doesn't always thread the
	// expected closure signature (`fn(T) -> R`) down into a
	// closure literal passed to a stdlib higher-order method
	// (`xs.filter(|n| ...)` / `opt.map(|x| ...)` / `r.map(|n|
	// ...)`). The closure arrives at IR with `Params[i].Type =
	// nil` and `Return = ()`, which poisons every expression in
	// its body — `n > 0` becomes `<error>` because `n`'s type
	// can't be resolved.
	//
	// Recover the expected fn type from the receiver type +
	// method name and backfill the closure's param/return slots
	// in place. The synthesis itself is shared with the free-fn
	// CallExpr path.
	l.backfillClosureArgsFromMethodCall(out)
	return out
}

// backfillClosureArgsFromMethodCall walks the args of a stdlib
// higher-order method call and fills missing closure param /
// return types from the canonical signature derived off the
// receiver. No-op if the receiver isn't a recognised generic
// builtin (List/Option/Result/Iter) or the method isn't one of
// the recognised higher-order shapes.
func (l *lowerer) backfillClosureArgsFromMethodCall(mc *MethodCall) {
	if mc == nil || mc.Receiver == nil {
		return
	}
	recvT := mc.Receiver.Type()
	if recvT == nil || recvT == ErrTypeVal {
		return
	}
	// Iterate args; only Closure args need backfill.
	for i := range mc.Args {
		cl, ok := mc.Args[i].Value.(*Closure)
		if !ok || cl == nil {
			continue
		}
		expected := expectedClosureFnTypeForBuiltin(recvT, mc.Name, i, len(mc.Args))
		if expected == nil {
			continue
		}
		// Some methods have closure params whose types are
		// inferable from *other* args at the call site —
		// `fold<A>(init: A, |acc: A, n: T| ...)` is the
		// canonical example, where A comes from the init arg's
		// type. Patch missing param/return slots in `expected`
		// using the surrounding arg shape before backfilling.
		refineExpectedFromOtherArgs(expected, recvT, mc.Name, mc.Args, i)
		backfillClosure(cl, expected)
	}
	// Recover the MethodCall's own T from the receiver type +
	// method name + (now-resolved) closure return types. Without
	// this, `name.map(|s| s).filter(...)` leaves
	// `Option__map(...)` typed `<error>` even though both args
	// resolved, and the next chained `.filter(...)` reads a
	// poisoned receiver. The recovery is bounded to the same
	// stdlib higher-order method table the closure backfill
	// uses, so chained `map/filter/andThen/...` chains lower
	// without their middle nodes being marked as ErrType.
	if mc.T == nil || mc.T == ErrTypeVal || containsClosureResultSentinel(mc.T) || containsTypeVar(mc.T) || hasPoisonedTypeArg(mc.T) {
		if recovered := recoverHigherOrderMethodReturnType(recvT, mc.Name, mc.Args); recovered != nil {
			mc.T = recovered
		}
	}
	// flatMap-specific: when the checker leaves TypeArgs empty or
	// poisoned, monomorph mangles `R` as `?` and clang rejects the
	// `_Z…flatMapIl?E…` symbol. Derive `[T, R]` from the receiver +
	// recovered return so monomorph specializes.
	if mc.Name == "flatMap" && mc.T != nil && mc.T != ErrTypeVal {
		fillFlatMapTypeArgs(mc, recvT)
	}
}

// containsClosureResultSentinel reports whether t carries the
// `?closure_result` placeholder the AST-side surface-shape recovery
// inserts when `xs.map(fn) / xs.flatMap(fn)` cannot resolve the
// closure return at parse time. The sentinel is a NamedType, not a
// TypeVar or ErrType, so the usual recovery conditions miss it.
func containsClosureResultSentinel(t Type) bool {
	if t == nil {
		return false
	}
	if nt, ok := t.(*NamedType); ok && nt != nil {
		if nt.Name == "?closure_result" {
			return true
		}
		for _, a := range nt.Args {
			if containsClosureResultSentinel(a) {
				return true
			}
		}
	}
	return false
}

// fillFlatMapTypeArgs sets `mc.TypeArgs = [R]` for a flatMap call when
// the recovered return type carries a concrete element. The convention
// at the MethodCall stage is method-local TypeArgs only —
// `RewriteStdlibMethodCallsites` prepends the receiver's owner args
// when it rewrites the call into the injected free-fn shape, so
// duplicating the owner T here pushes the arity to 3 vs the free fn's
// [T, R] generics list and the monomorph request bails. Other methods
// are intentionally left alone — broader fill triggers monomorph
// regressions on scan/groupBy.
func fillFlatMapTypeArgs(mc *MethodCall, recvT Type) {
	if mc == nil || mc.T == nil {
		return
	}
	nt, ok := mc.T.(*NamedType)
	if !ok || nt == nil || nt.Name != "List" || len(nt.Args) < 1 {
		return
	}
	r := nt.Args[0]
	if r == nil || r == ErrTypeVal || hasPoisonedTypeArg(r) || containsTypeVar(r) {
		return
	}
	rNt, ok2 := recvT.(*NamedType)
	if !ok2 || rNt == nil || !rNt.Builtin || len(rNt.Args) < 1 {
		return
	}
	t := rNt.Args[0]
	if t == nil || t == ErrTypeVal || hasPoisonedTypeArg(t) || containsTypeVar(t) {
		return
	}
	if len(mc.TypeArgs) > 0 {
		clean := true
		for _, ta := range mc.TypeArgs {
			if ta == nil || ta == ErrTypeVal || hasPoisonedTypeArg(ta) || containsTypeVar(ta) {
				clean = false
				break
			}
		}
		if clean {
			return
		}
	}
	mc.TypeArgs = []Type{r}
}

// recoverHigherOrderMethodReturnType derives the return type of
// `recv.method(args...)` for stdlib higher-order methods on
// List / Option / Result whose checker-side return-type inference
// often fails when the call's first arg is an inferred closure.
// Mirrors the dispatch in `expectedClosureFnTypeForBuiltin` so a
// matching method here implies the call's return shape is
// well-defined by the type table:
//
//   - List<T>.map<R>(fn(T) -> R) -> List<R>
//   - List<T>.filter(fn(T) -> Bool) -> List<T>
//   - Option<T>.map<U>(fn(T) -> U) -> U?
//   - Option<T>.andThen<U>(fn(T) -> U?) -> U?
//   - Option<T>.filter(fn(T) -> Bool) -> T?
//   - Result<T,E>.map<U>(fn(T) -> U) -> Result<U, E>
//   - etc.
//
// Returns nil for shapes outside the table — callers leave the
// MethodCall's T as ErrType so downstream recovery doesn't paper
// over a real type-checker gap.
func recoverHigherOrderMethodReturnType(recvT Type, method string, args []Arg) Type {
	if recvT == nil {
		return nil
	}
	switch r := recvT.(type) {
	case *OptionalType:
		if r == nil || r.Inner == nil {
			return nil
		}
		return recoverOptionHigherOrderReturn(r.Inner, method, args)
	case *NamedType:
		if r == nil || !r.Builtin {
			return nil
		}
		switch r.Name {
		case "List", "Iter":
			if len(r.Args) < 1 {
				return nil
			}
			return recoverListHigherOrderReturn(r.Name, r.Args[0], method, args)
		case "Option", "Maybe":
			if len(r.Args) < 1 {
				return nil
			}
			return recoverOptionHigherOrderReturn(r.Args[0], method, args)
		case "Result":
			if len(r.Args) < 2 {
				return nil
			}
			return recoverResultHigherOrderReturn(r.Args[0], r.Args[1], method, args)
		case "Map":
			if len(r.Args) < 2 {
				return nil
			}
			return recoverMapHigherOrderReturn(r.Args[0], r.Args[1], method, args)
		case "Set":
			if len(r.Args) < 1 {
				return nil
			}
			return recoverSetHigherOrderReturn(r.Args[0], method, args)
		}
	}
	return nil
}

// recoverMapHigherOrderReturn derives the return type of
// `Map<K, V>.<method>(args...)` for the stdlib surface that
// surrounds the closure-arg backfill. Mirrors
// `expectedClosureFnTypeForMap` but for the OUTGOING type.
func recoverMapHigherOrderReturn(keyT, valT Type, method string, args []Arg) Type {
	if keyT == nil || valT == nil {
		return nil
	}
	switch method {
	case "update", "forEach", "retainIf", "clear":
		return TUnit
	case "getOrInsertWith", "getOrInsert":
		return valT
	case "mapValues":
		if len(args) == 1 {
			r := closureReturnType(args[0].Value)
			if r == nil {
				r = valT
			}
			return &NamedType{Name: "Map", Args: []Type{keyT, r}, Builtin: true}
		}
	case "filter", "mergeWith":
		return &NamedType{Name: "Map", Args: []Type{keyT, valT}, Builtin: true}
	case "any", "all":
		return TBool
	case "count":
		return TInt
	case "len":
		return TInt
	case "isEmpty":
		return TBool
	}
	return nil
}

// recoverSetHigherOrderReturn covers the stdlib `Set<T>` surface
// methods that take a closure or otherwise need return-type
// recovery for chain propagation.
func recoverSetHigherOrderReturn(elem Type, method string, args []Arg) Type {
	if elem == nil {
		return nil
	}
	switch method {
	case "forEach", "retainIf", "clear":
		return TUnit
	case "filter":
		return &NamedType{Name: "Set", Args: []Type{elem}, Builtin: true}
	case "any", "all":
		return TBool
	case "len":
		return TInt
	case "isEmpty":
		return TBool
	}
	return nil
}

func recoverListHigherOrderReturn(listName string, elem Type, method string, args []Arg) Type {
	if elem == nil {
		return nil
	}
	switch method {
	case "map":
		if len(args) == 1 {
			r := closureReturnType(args[0].Value)
			if r == nil {
				r = elem
			}
			return &NamedType{Name: listName, Args: []Type{r}, Builtin: true}
		}
	case "filter":
		if len(args) == 1 {
			return &NamedType{Name: listName, Args: []Type{elem}, Builtin: true}
		}
	case "any", "all":
		if len(args) == 1 {
			return TBool
		}
	case "forEach":
		if len(args) == 1 {
			return TUnit
		}
	case "fold":
		if len(args) == 2 {
			// fold returns the accumulator type — pull from the
			// init arg.
			if t := safeExprType(args[0].Value); t != ErrTypeVal {
				return t
			}
		}
	case "reduce":
		if len(args) == 1 {
			return &OptionalType{Inner: elem}
		}
	case "enumerate":
		if len(args) == 0 {
			pair := &TupleType{Elems: []Type{TInt, elem}}
			return &NamedType{Name: listName, Args: []Type{pair}, Builtin: true}
		}
	case "chunked", "windowed":
		// Both return `List<List<T>>`. windowed takes 2 args
		// (size, step); chunked takes 1 (size). The intermediate
		// `List<T>` keeps the source list's elem.
		inner := &NamedType{Name: listName, Args: []Type{elem}, Builtin: true}
		return &NamedType{Name: listName, Args: []Type{inner}, Builtin: true}
	case "partition":
		if len(args) == 1 {
			lst := &NamedType{Name: listName, Args: []Type{elem}, Builtin: true}
			return &TupleType{Elems: []Type{lst, lst}}
		}
	case "zip":
		if len(args) == 1 {
			// `zip(other: List<U>) -> List<(T, U)>` — derive U
			// from the other-arg's receiver-arg shape.
			otherT := safeExprType(args[0].Value)
			if otherT != ErrTypeVal {
				if nt, ok := otherT.(*NamedType); ok && nt != nil && (nt.Name == "List" || nt.Name == "Iter") && len(nt.Args) >= 1 {
					pair := &TupleType{Elems: []Type{elem, nt.Args[0]}}
					return &NamedType{Name: listName, Args: []Type{pair}, Builtin: true}
				}
			}
			pair := &TupleType{Elems: []Type{elem, elem}}
			return &NamedType{Name: listName, Args: []Type{pair}, Builtin: true}
		}
	case "flatMap":
		if len(args) == 1 {
			if r := flatMapElemFromArg(args[0].Value); r != nil {
				return &NamedType{Name: listName, Args: []Type{r}, Builtin: true}
			}
		}
	case "len":
		return TInt
	case "isEmpty":
		return TBool
	}
	return nil
}

// flatMapElemFromArg extracts the element type R of `List<R>` from a
// `flatMap` argument's closure or named-fn return.
//
// Accepts only `List<R>` returns. `List<T>.flatMap` is declared as
// `fn(T) -> List<R>`; an `Iter<R>` return would not type-check
// against that signature, and silently re-typing a poisoned `Iter`
// closure as if it were `List` would feed an invalid R into
// monomorph and mask the underlying checker mismatch.
func flatMapElemFromArg(e Expr) Type {
	if e == nil {
		return nil
	}
	t := closureReturnType(e)
	if t == nil {
		if et := e.Type(); et != nil {
			if ft, ok := et.(*FnType); ok && ft != nil && ft.Return != nil {
				t = ft.Return
			}
		}
	}
	// Closure body inference fallback: when the checker left the
	// closure's recorded return type poisoned (`<error>`), or carrying
	// a residual TyVar that the outer unification never resolved, walk
	// the AST body of a closure literal and pull a recoverable list
	// type from its tail expression.
	tNeedsFallback := t == nil || t == ErrTypeVal || hasPoisonedTypeArg(t) || containsTypeVar(t)
	if tNeedsFallback {
		if cl, ok := e.(*Closure); ok && cl != nil && cl.Body != nil && cl.Body.Result != nil {
			if bt := cl.Body.Result.Type(); bt != nil && bt != ErrTypeVal && !hasPoisonedTypeArg(bt) && !containsTypeVar(bt) {
				t = bt
			}
		}
	}
	if t == nil {
		return nil
	}
	if nt, ok := t.(*NamedType); ok && nt != nil && nt.Name == "List" && len(nt.Args) >= 1 {
		if r := nt.Args[0]; r != nil && r != ErrTypeVal && !hasPoisonedTypeArg(r) && !containsTypeVar(r) {
			return r
		}
	}
	return nil
}

func recoverOptionHigherOrderReturn(inner Type, method string, args []Arg) Type {
	if inner == nil {
		return nil
	}
	switch method {
	case "map":
		if len(args) == 1 {
			r := closureReturnType(args[0].Value)
			if r == nil {
				r = inner
			}
			return &OptionalType{Inner: r}
		}
	case "andThen":
		if len(args) == 1 {
			// closure returns `U?`; the method threads that
			// through. Pull the inner U from the closure's
			// Return if it's an Option.
			if r := closureReturnType(args[0].Value); r != nil {
				if ot, ok := r.(*OptionalType); ok && ot != nil {
					return ot
				}
				return &OptionalType{Inner: r}
			}
			return &OptionalType{Inner: inner}
		}
	case "filter":
		if len(args) == 1 {
			return &OptionalType{Inner: inner}
		}
	case "inspect":
		if len(args) == 1 {
			return &OptionalType{Inner: inner}
		}
	case "forEach":
		if len(args) == 1 {
			return TUnit
		}
	case "mapOr":
		if len(args) == 2 {
			if t := safeExprType(args[0].Value); t != ErrTypeVal {
				return t
			}
			if r := closureReturnType(args[1].Value); r != nil {
				return r
			}
		}
	case "mapOrElse":
		if len(args) == 2 {
			if r := closureReturnType(args[1].Value); r != nil {
				return r
			}
			if r := closureReturnType(args[0].Value); r != nil {
				return r
			}
		}
	case "unwrapOr", "unwrapOrElse":
		return inner
	case "orElse", "or":
		return &OptionalType{Inner: inner}
	case "isSomeAnd", "isNoneOr":
		return TBool
	}
	return nil
}

func recoverResultHigherOrderReturn(ok, errT Type, method string, args []Arg) Type {
	if ok == nil || errT == nil {
		return nil
	}
	switch method {
	case "map":
		if len(args) == 1 {
			r := closureReturnType(args[0].Value)
			if r == nil {
				r = ok
			}
			return &NamedType{Name: "Result", Args: []Type{r, errT}, Builtin: true}
		}
	case "andThen":
		if len(args) == 1 {
			if r := closureReturnType(args[0].Value); r != nil {
				return r
			}
			return &NamedType{Name: "Result", Args: []Type{ok, errT}, Builtin: true}
		}
	case "mapErr":
		if len(args) == 1 {
			r := closureReturnType(args[0].Value)
			if r == nil {
				r = errT
			}
			return &NamedType{Name: "Result", Args: []Type{ok, r}, Builtin: true}
		}
	case "unwrapOr", "unwrapOrElse":
		return ok
	}
	return nil
}

// refineExpectedFromOtherArgs patches `expected` in place using
// arg types the table function couldn't see when building the
// initial signature. Currently handles:
//
//   - `List<T>.fold<A>(init: A, |acc: A, n: T| ...) -> A` —
//     the closure's `acc` slot (Params[0]) and Return both come
//     from the init arg (`args[0].Value.Type()`). Without this
//     `xs.fold(0, |acc, n| acc + n)` lowers with `acc` as
//     ErrTypeVal and the body's `acc + n` collapses to `<error>`.
//
// All other shapes are pass-through.
func refineExpectedFromOtherArgs(expected *FnType, recvT Type, method string, args []Arg, closureIdx int) {
	if expected == nil {
		return
	}
	if nt, ok := recvT.(*NamedType); ok && nt != nil && (nt.Name == "List" || nt.Name == "Iter") {
		if (method == "fold" || method == "scan") && closureIdx == 1 && len(args) == 2 && len(expected.Params) >= 1 {
			// `fold<A>(init: A, fn(A, T) -> A) -> A` and
			// `scan<A>(init: A, fn(A, T) -> A) -> List<A>` share
			// the closure shape — fill `A` from the init arg's
			// concrete type so the closure's first param +
			// return resolve before backfill.
			if expected.Params[0] == nil {
				if initT := safeExprType(args[0].Value); initT != ErrTypeVal {
					expected.Params[0] = initT
				}
			}
			if expected.Return == nil || expected.Return == ErrTypeVal {
				if initT := safeExprType(args[0].Value); initT != ErrTypeVal {
					expected.Return = initT
				}
			}
		}
	}
}

func closureReturnType(e Expr) Type {
	if e == nil {
		return nil
	}
	cl, ok := e.(*Closure)
	if !ok || cl == nil {
		return nil
	}
	if cl.Return != nil && cl.Return != ErrTypeVal && cl.Return != TUnit {
		return cl.Return
	}
	if cl.Body != nil && cl.Body.Result != nil {
		if t := cl.Body.Result.Type(); t != nil && t != ErrTypeVal {
			return t
		}
	}
	return nil
}

// expectedClosureFnTypeForBuiltin returns the canonical closure
// signature for a known stdlib higher-order method call on a
// recognised builtin generic receiver. Arg index `argIdx` lets a
// method with multiple closure-typed params (e.g. `mapOrElse(d,
// f)`) pick the right slot; `argCount` distinguishes overload
// shapes when only argument count carries the information.
// Returns nil for unrecognised shapes.
func expectedClosureFnTypeForBuiltin(recvT Type, method string, argIdx, argCount int) *FnType {
	if recvT == nil {
		return nil
	}
	// Normalise `T?` to `Option<T>` for the lookup so the chained
	// `?.filter(...).map(...)` shape works the same as
	// `Some(x).filter(...)`.
	switch r := recvT.(type) {
	case *OptionalType:
		if r == nil || r.Inner == nil {
			return nil
		}
		return expectedClosureFnTypeForOption(r.Inner, method, argIdx, argCount)
	case *NamedType:
		if r == nil || !r.Builtin {
			return nil
		}
		switch r.Name {
		case "List", "Iter":
			if len(r.Args) < 1 {
				return nil
			}
			return expectedClosureFnTypeForList(r.Args[0], method, argIdx, argCount)
		case "Option", "Maybe":
			if len(r.Args) < 1 {
				return nil
			}
			return expectedClosureFnTypeForOption(r.Args[0], method, argIdx, argCount)
		case "Result":
			if len(r.Args) < 2 {
				return nil
			}
			return expectedClosureFnTypeForResult(r.Args[0], r.Args[1], method, argIdx, argCount)
		case "Map":
			if len(r.Args) < 2 {
				return nil
			}
			return expectedClosureFnTypeForMap(r.Args[0], r.Args[1], method, argIdx, argCount)
		case "Set":
			if len(r.Args) < 1 {
				return nil
			}
			return expectedClosureFnTypeForSet(r.Args[0], method, argIdx, argCount)
		}
	}
	return nil
}

func expectedClosureFnTypeForList(elem Type, method string, argIdx, argCount int) *FnType {
	if elem == nil {
		return nil
	}
	switch method {
	case "map":
		// `map<R>(fn(T) -> R) -> List<R>` — R isn't known here so
		// we leave Return nil; backfillClosure only fills the
		// param slots when Return is unconstrained.
		if argIdx == 0 && argCount == 1 {
			return &FnType{Params: []Type{elem}}
		}
	case "filter", "any", "all", "forEach", "find", "partition":
		// `filter(fn(T) -> Bool) -> List<T>`, similar shape for
		// the others. `find(fn(T) -> Bool) -> T?` and
		// `partition(fn(T) -> Bool) -> (List<T>, List<T>)` share
		// the predicate shape.
		if argIdx == 0 && argCount == 1 {
			ret := TBool
			if method == "forEach" {
				ret = TUnit
			}
			return &FnType{Params: []Type{elem}, Return: ret}
		}
	case "fold", "scan":
		// `fold<A>(init: A, f: fn(A, T) -> A) -> A` and
		// `scan<A>(init: A, f: fn(A, T) -> A) -> List<A>` share
		// the closure shape — only the return wrapping differs.
		// `A` is derivable from the init arg —
		// `expectedClosureFnTypeForBuiltinAtArg` patches it from
		// the surrounding args.
		if argIdx == 1 && argCount == 2 {
			return &FnType{Params: []Type{nil, elem}}
		}
	case "reduce":
		// `reduce(fn(T, T) -> T) -> T?`.
		if argIdx == 0 && argCount == 1 {
			return &FnType{Params: []Type{elem, elem}, Return: elem}
		}
	case "flatMap":
		// `flatMap<R>(fn(T) -> List<R>) -> List<R>` — R is not
		// known here; backfillClosure leaves Return nil and only
		// fills the param slot, matching how `map` is handled.
		if argIdx == 0 && argCount == 1 {
			return &FnType{Params: []Type{elem}}
		}
	case "groupBy":
		// `groupBy<K>(key: fn(T) -> K) -> Map<K, List<T>>` — K is
		// not known here; like `map`, leave Return nil.
		if argIdx == 0 && argCount == 1 {
			return &FnType{Params: []Type{elem}}
		}
	}
	return nil
}

func expectedClosureFnTypeForOption(inner Type, method string, argIdx, argCount int) *FnType {
	if inner == nil {
		return nil
	}
	switch method {
	case "map", "andThen":
		if argIdx == 0 && argCount == 1 {
			return &FnType{Params: []Type{inner}}
		}
	case "filter", "isSomeAnd", "isNoneOr":
		if argIdx == 0 && argCount == 1 {
			return &FnType{Params: []Type{inner}, Return: TBool}
		}
	case "inspect", "forEach":
		if argIdx == 0 && argCount == 1 {
			return &FnType{Params: []Type{inner}, Return: TUnit}
		}
	case "mapOr":
		// `mapOr<U>(fallback: U, f: fn(T) -> U) -> U` — closure
		// is second arg.
		if argIdx == 1 && argCount == 2 {
			return &FnType{Params: []Type{inner}}
		}
	case "mapOrElse":
		// `mapOrElse<U>(fallback: fn() -> U, f: fn(T) -> U) -> U`.
		switch argIdx {
		case 0:
			if argCount == 2 {
				return &FnType{Params: nil}
			}
		case 1:
			if argCount == 2 {
				return &FnType{Params: []Type{inner}}
			}
		}
	case "unwrapOrElse":
		// `unwrapOrElse(fallback: fn() -> T) -> T`.
		if argIdx == 0 && argCount == 1 {
			return &FnType{Params: nil, Return: inner}
		}
	case "orElse":
		// `orElse(fallback: fn() -> T?) -> T?`.
		if argIdx == 0 && argCount == 1 {
			return &FnType{Params: nil, Return: &OptionalType{Inner: inner}}
		}
	}
	return nil
}

func expectedClosureFnTypeForResult(ok, errT Type, method string, argIdx, argCount int) *FnType {
	if ok == nil || errT == nil {
		return nil
	}
	switch method {
	case "map", "andThen":
		if argIdx == 0 && argCount == 1 {
			return &FnType{Params: []Type{ok}}
		}
	case "mapErr":
		if argIdx == 0 && argCount == 1 {
			return &FnType{Params: []Type{errT}}
		}
	case "unwrapOr":
		// non-closure overload — no backfill needed.
	case "unwrapOrElse":
		if argIdx == 0 && argCount == 1 {
			return &FnType{Params: []Type{errT}, Return: ok}
		}
	}
	return nil
}

// expectedClosureFnTypeForMap derives the closure signature for
// the stdlib `Map<K, V>` higher-order methods that the IR-side
// inference path needs to backfill before MIR lifts the closure
// body into a separate function. Mirrors the source declarations
// in `internal/stdlib/modules/collections.osty`:
//
//   - `update(k: K, f: fn(V?) -> V)` — closure param is V?,
//     return is V. Canonical usage: `m.update(k, |n| (n ?? 0) + 1)`.
//   - `getOrInsertWith(k: K, make: fn() -> V) -> V` — closure
//     param-less, returns V.
//   - `mapValues<R>(f: fn(V) -> R) -> Map<K, R>` — R inferred
//     from the closure body's return shape.
//   - `forEach(f: fn(K, V) -> ())` — 2-param closure, Unit
//     return.
//   - `any(pred: fn(K, V) -> Bool) -> Bool`, `all(...) -> Bool`,
//     `count(pred) -> Int` — 2-param Bool-returning closure.
//   - `filter(pred: fn(K, V) -> Bool) -> Map<K, V>` — 2-param
//     Bool, same Map back.
//   - `retainIf(pred: fn(K, V) -> Bool)` — same shape, no
//     return.
//   - `mergeWith(other, combine: fn(V, V) -> V)` — closure
//     param is (V, V), return is V.
func expectedClosureFnTypeForMap(keyT, valT Type, method string, argIdx, argCount int) *FnType {
	if keyT == nil || valT == nil {
		return nil
	}
	switch method {
	case "update":
		if argIdx == 1 && argCount == 2 {
			return &FnType{Params: []Type{&OptionalType{Inner: valT}}, Return: valT}
		}
	case "getOrInsertWith":
		if argIdx == 1 && argCount == 2 {
			return &FnType{Params: nil, Return: valT}
		}
	case "mapValues":
		if argIdx == 0 && argCount == 1 {
			return &FnType{Params: []Type{valT}}
		}
	case "forEach":
		if argIdx == 0 && argCount == 1 {
			return &FnType{Params: []Type{keyT, valT}, Return: TUnit}
		}
	case "any", "all":
		if argIdx == 0 && argCount == 1 {
			return &FnType{Params: []Type{keyT, valT}, Return: TBool}
		}
	case "count":
		if argIdx == 0 && argCount == 1 {
			return &FnType{Params: []Type{keyT, valT}, Return: TBool}
		}
	case "filter", "retainIf":
		if argIdx == 0 && argCount == 1 {
			ret := TBool
			return &FnType{Params: []Type{keyT, valT}, Return: ret}
		}
	case "mergeWith":
		if argIdx == 1 && argCount == 2 {
			return &FnType{Params: []Type{valT, valT}, Return: valT}
		}
	}
	return nil
}

// expectedClosureFnTypeForSet mirrors `expectedClosureFnTypeForMap`
// for the stdlib `Set<T>` higher-order surface:
//
//   - `forEach(fn(T) -> ())` — predicate-shaped probe.
//   - `any/all(fn(T) -> Bool) -> Bool`.
//   - `filter(fn(T) -> Bool) -> Set<T>`.
//   - `retainIf(fn(T) -> Bool)`.
func expectedClosureFnTypeForSet(elem Type, method string, argIdx, argCount int) *FnType {
	if elem == nil {
		return nil
	}
	switch method {
	case "forEach":
		if argIdx == 0 && argCount == 1 {
			return &FnType{Params: []Type{elem}, Return: TUnit}
		}
	case "any", "all":
		if argIdx == 0 && argCount == 1 {
			return &FnType{Params: []Type{elem}, Return: TBool}
		}
	case "filter", "retainIf":
		if argIdx == 0 && argCount == 1 {
			return &FnType{Params: []Type{elem}, Return: TBool}
		}
	}
	return nil
}

// backfillClosure fills nil-typed param slots and an unset return
// slot from the expected fn type. Params already typed (explicit
// annotation) are left alone — the user might intentionally pick
// a wider type. Return is only filled when the closure's own
// Return is the default TUnit AND the body's result type doesn't
// already carry a concrete inferable type.
//
// After param-type recovery, the closure body's Ident references
// (`n`, `x`, …) still carry the ErrTypeVal they picked up during
// the initial lowering pass (the resolver could not look up the
// param type because it was nil). Re-walk the body and replace
// the typed-by-symbol Idents whose name matches a backfilled
// param, then re-propagate the resulting concrete types through
// the trivially-typed parent expressions (BinaryExpr, FieldExpr,
// MethodCall, CoalesceExpr). This catches the `n > 0` /
// `x * 2` / `box.n` shapes that drive the canonical
// stdlib-higher-order closure patterns without re-running the
// full checker.
func backfillClosure(cl *Closure, expected *FnType) {
	if cl == nil || expected == nil {
		return
	}
	updates := map[string]Type{}
	for i, p := range cl.Params {
		if p == nil || p.Type != nil {
			continue
		}
		if i >= len(expected.Params) {
			break
		}
		exp := expected.Params[i]
		if exp == nil || exp == ErrTypeVal {
			continue
		}
		p.Type = exp
		if p.Name != "" {
			updates[p.Name] = exp
		}
	}
	if len(updates) > 0 && cl.Body != nil {
		repropagateClosureBodyTypes(cl.Body, updates)
	}
	if expected.Return != nil && expected.Return != ErrTypeVal {
		// Only force the return slot when the closure didn't
		// resolve one for itself — `mapOr(0, |x| x)` should pick
		// up the U from the fallback arg's checker context, not
		// from the closure's body which we haven't seen yet.
		if cl.Return == nil || cl.Return == TUnit || cl.Return == ErrTypeVal {
			cl.Return = expected.Return
		}
	}
	// If the body now has a concrete result type, use it to
	// refine an otherwise-empty Return slot (e.g. `map(|x|
	// x.toString())` produces String regardless of expected.Return
	// being nil for the map-without-known-output case).
	if (cl.Return == nil || cl.Return == TUnit || cl.Return == ErrTypeVal) && cl.Body != nil && cl.Body.Result != nil {
		if rt := cl.Body.Result.Type(); rt != nil && rt != ErrTypeVal {
			cl.Return = rt
		}
	}
	if cl.T == nil || cl.T == ErrTypeVal {
		// Avoid storing a typed-nil `*FnType` in the Type
		// interface slot — `synthesiseClosureFnType` returns
		// `*FnType(nil)` when params/return aren't fully
		// resolved, which would still satisfy `cl.T != nil`
		// but panic on any subsequent `t.Return` access.
		if fnT := synthesiseClosureFnType(cl); fnT != nil {
			cl.T = fnT
		}
	}
}

// repropagateClosureBodyTypes walks `body` and updates Ident
// references that name a backfilled closure parameter. Scope-aware:
// a LetStmt / ForStmt / pattern binding that rebinds one of the
// param names shadows the update within its scope, so a body
// like `|x| { let x = 0; x + 1 }` only retypes the outer `x` use.
//
// After Idents are re-typed, common trivially-typed parents
// (BinaryExpr.T, CallExpr.T, MethodCall.T, CoalesceExpr.T) are
// refreshed in a second pass so the closure's body Result picks
// up the propagated type — without this the surrounding `n > 0`
// keeps its ErrTypeVal.
func repropagateClosureBodyTypes(body *Block, updates map[string]Type) {
	if body == nil || len(updates) == 0 {
		return
	}
	retypeIdentsScoped(body, updates)
	Walk(VisitorFunc(func(n Node) bool {
		switch e := n.(type) {
		case *BinaryExpr:
			if e.T == nil || e.T == ErrTypeVal {
				e.T = inferBinaryResultType(e)
			}
		case *CallExpr:
			if e.T == nil || e.T == ErrTypeVal {
				if fnT, ok := e.Callee.Type().(*FnType); ok && fnT != nil {
					e.T = fnT.Return
				}
			}
		case *MethodCall:
			if e.T == nil || e.T == ErrTypeVal {
				if t := recoverMethodCallType(e.Name, e.TypeArgs); t != ErrTypeVal {
					e.T = t
				}
			}
		case *CoalesceExpr:
			if e.T == nil || e.T == ErrTypeVal {
				if recovered := recoverCoalesceType(e.Left, e.Right); recovered != ErrTypeVal {
					e.T = recovered
				}
			}
		}
		return true
	}), body)
}

// retypeIdentsScoped recursively walks any IR subtree, updating
// Ident.T for references that name an entry in `live` and removing
// names from `live` for the duration of any shadowing
// LetStmt/ForStmt/pattern-bound scope. Only Idents whose existing
// T is nil or ErrTypeVal are touched — already-typed references
// (including any visible-but-shadowed binding the lowerer typed
// from the value side) are left alone.
func retypeIdentsScoped(n Node, live map[string]Type) {
	if n == nil || len(live) == 0 {
		return
	}
	switch x := n.(type) {
	case *Ident:
		if x == nil {
			return
		}
		if newT, hit := live[x.Name]; hit {
			if x.T == nil || x.T == ErrTypeVal {
				x.T = CloneType(newT)
			}
		}
	case *Block:
		if x == nil {
			return
		}
		// LetStmts inside the block introduce shadowing for the
		// rest of the block. Walk statements in order, popping
		// names off `live` when shadowed.
		shadowed := map[string]Type{}
		for _, s := range x.Stmts {
			if ls, ok := s.(*LetStmt); ok && ls != nil {
				// LetStmt.Value is in the outer scope — walk it
				// before the name is shadowed.
				if ls.Value != nil {
					retypeIdentsScoped(ls.Value, live)
				}
				// Walk the LetStmt's pattern bindings (irrefutable
				// patterns destructure into multiple names).
				if ls.Pattern != nil {
					retypeIdentsScoped(ls.Pattern, live)
				}
				for _, name := range bindingNames(ls) {
					if _, hit := live[name]; hit {
						shadowed[name] = live[name]
						delete(live, name)
					}
				}
				continue
			}
			retypeIdentsScoped(s, live)
		}
		if x.Result != nil {
			retypeIdentsScoped(x.Result, live)
		}
		// Restore shadowed names for sibling scopes.
		for k, v := range shadowed {
			live[k] = v
		}
	case *ForStmt:
		if x == nil {
			return
		}
		if x.Iter != nil {
			retypeIdentsScoped(x.Iter, live)
		}
		if x.Cond != nil {
			retypeIdentsScoped(x.Cond, live)
		}
		if x.Start != nil {
			retypeIdentsScoped(x.Start, live)
		}
		if x.End != nil {
			retypeIdentsScoped(x.End, live)
		}
		shadowedNames := patternBindingNames(x.Pattern)
		shadowed := withShadow(live, shadowedNames)
		if x.Body != nil {
			retypeIdentsScoped(x.Body, live)
		}
		restoreShadow(live, shadowed)
	case *Closure:
		if x == nil {
			return
		}
		// Nested closure introduces its own param scope; remove
		// any shadowed names while walking its body.
		var paramNames []string
		for _, p := range x.Params {
			if p != nil && p.Name != "" {
				paramNames = append(paramNames, p.Name)
			}
		}
		shadowed := withShadow(live, paramNames)
		if x.Body != nil {
			retypeIdentsScoped(x.Body, live)
		}
		restoreShadow(live, shadowed)
	case *MatchExpr:
		if x == nil {
			return
		}
		if x.Scrutinee != nil {
			retypeIdentsScoped(x.Scrutinee, live)
		}
		for _, arm := range x.Arms {
			if arm == nil {
				continue
			}
			shadowedNames := patternBindingNames(arm.Pattern)
			shadowed := withShadow(live, shadowedNames)
			if arm.Guard != nil {
				retypeIdentsScoped(arm.Guard, live)
			}
			if arm.Body != nil {
				retypeIdentsScoped(arm.Body, live)
			}
			restoreShadow(live, shadowed)
		}
	case *IfLetExpr:
		if x == nil {
			return
		}
		if x.Scrutinee != nil {
			retypeIdentsScoped(x.Scrutinee, live)
		}
		shadowedNames := patternBindingNames(x.Pattern)
		shadowed := withShadow(live, shadowedNames)
		if x.Then != nil {
			retypeIdentsScoped(x.Then, live)
		}
		restoreShadow(live, shadowed)
		if x.Else != nil {
			retypeIdentsScoped(x.Else, live)
		}
	default:
		// Fall back to a generic walk that visits every child
		// node — covers the common Expr / Stmt shapes (BinaryExpr,
		// UnaryExpr, CallExpr, FieldExpr, etc.) without enumerating
		// each case here. Re-uses ir.Walk's traversal, applying
		// the scope-aware logic recursively to children we
		// recognise.
		Walk(VisitorFunc(func(child Node) bool {
			switch child.(type) {
			case *Block, *ForStmt, *Closure, *MatchExpr, *IfLetExpr:
				retypeIdentsScoped(child, live)
				return false
			case *Ident:
				retypeIdentsScoped(child, live)
				return false
			}
			return true
		}), n)
	}
}

// bindingNames returns the names a LetStmt introduces — typically a
// single `Name`, but a destructuring `let (a, b) = ...` or a
// struct/variant pattern can yield several.
func bindingNames(ls *LetStmt) []string {
	if ls == nil {
		return nil
	}
	if ls.Name != "" {
		return []string{ls.Name}
	}
	return patternBindingNames(ls.Pattern)
}

// patternBindingNames returns every fresh name a pattern
// introduces. Wildcards and literals contribute nothing; binding
// patterns contribute both the outer name and any inner names.
func patternBindingNames(p Pattern) []string {
	if p == nil {
		return nil
	}
	var out []string
	var walk func(p Pattern)
	walk = func(p Pattern) {
		switch x := p.(type) {
		case *IdentPat:
			if x != nil && x.Name != "" {
				out = append(out, x.Name)
			}
		case *BindingPat:
			if x == nil {
				return
			}
			if x.Name != "" {
				out = append(out, x.Name)
			}
			walk(x.Pattern)
		case *TuplePat:
			for _, e := range x.Elems {
				walk(e)
			}
		case *StructPat:
			for _, f := range x.Fields {
				if f.Pattern != nil {
					walk(f.Pattern)
				} else if f.Name != "" {
					out = append(out, f.Name)
				}
			}
		case *VariantPat:
			for _, a := range x.Args {
				walk(a)
			}
		case *OrPat:
			for _, alt := range x.Alts {
				walk(alt)
			}
		}
	}
	walk(p)
	return out
}

// withShadow removes `names` from `live`, returning the prior
// values so a matching restoreShadow can reinstate them after the
// scoped subtree is walked. Names not in live are skipped.
func withShadow(live map[string]Type, names []string) map[string]Type {
	if len(names) == 0 {
		return nil
	}
	saved := map[string]Type{}
	for _, n := range names {
		if t, hit := live[n]; hit {
			saved[n] = t
			delete(live, n)
		}
	}
	return saved
}

func restoreShadow(live map[string]Type, saved map[string]Type) {
	for k, v := range saved {
		live[k] = v
	}
}

// inferBinaryResultType derives a result type for a BinaryExpr
// whose checker-recorded type is ErrTypeVal. Used as a fallback
// during closure-body re-typing where the operands have just been
// refreshed but the parent expression's T slot still carries the
// stale ErrType.
//
// Returns Bool for comparison / logical ops. For arithmetic and
// bitwise ops the operand types are unified via `numericResult`
// (Float promotion + Int default lane) — if unification fails
// (mixed non-numeric or incompatible types), ErrTypeVal is
// preserved rather than guessing.
func inferBinaryResultType(e *BinaryExpr) Type {
	if e == nil {
		return ErrTypeVal
	}
	switch e.Op {
	case BinEq, BinNeq, BinLt, BinLeq, BinGt, BinGeq, BinAnd, BinOr:
		return TBool
	case BinAdd, BinSub, BinMul, BinDiv, BinMod,
		BinBitAnd, BinBitOr, BinBitXor, BinShl, BinShr:
		lt := safeExprType(e.Left)
		rt := safeExprType(e.Right)
		if lt == ErrTypeVal || rt == ErrTypeVal {
			return ErrTypeVal
		}
		return numericResult(lt, rt)
	}
	return ErrTypeVal
}

func safeExprType(e Expr) Type {
	if e == nil {
		return ErrTypeVal
	}
	t := e.Type()
	if t == nil {
		return ErrTypeVal
	}
	return t
}

// recoverUserMethodReturnType walks the receiver's struct/enum AST
// declaration looking for a method whose name matches. Returns its
// lowered ReturnType, or nil when no such method exists or the
// receiver's type is not a user-defined nominal. Mirrors the lookup
// path in lowerMethodCall fallback for stdlib intrinsics, but for
// user-side declarations the IR has to consult the AST directly because
// the checker's per-node Types map is no longer populated (#1645) and
// the byID/byKey SemanticDB lookups for arbitrary CallExpr / MethodCall
// nodes are unreliable (selfhost arena ids != AST NodeIDs).
func (l *lowerer) recoverUserMethodReturnType(name string, recv Expr) Type {
	if l == nil || recv == nil || name == "" {
		return nil
	}
	rt := recv.Type()
	if rt == nil || rt == ErrTypeVal {
		return nil
	}
	named, ok := rt.(*NamedType)
	if !ok || named == nil || named.Builtin {
		return nil
	}
	if sd := l.structDeclByName(named.Name); sd != nil {
		for _, m := range sd.Methods {
			if m == nil || m.Name != name {
				continue
			}
			if m.ReturnType == nil {
				return TUnit
			}
			lowered := l.lowerType(m.ReturnType)
			if lowered != nil && lowered != ErrTypeVal {
				return lowered
			}
		}
	}
	if ed := l.enumDeclByName(named.Name); ed != nil {
		for _, m := range ed.Methods {
			if m == nil || m.Name != name {
				continue
			}
			if m.ReturnType == nil {
				return TUnit
			}
			lowered := l.lowerType(m.ReturnType)
			if lowered != nil && lowered != ErrTypeVal {
				return lowered
			}
		}
	}
	return nil
}

// enumDeclByName mirrors `structDeclByName` for `enum Foo { ... fn ... }`.
func (l *lowerer) enumDeclByName(name string) *ast.EnumDecl {
	if name == "" {
		return nil
	}
	if l.file != nil {
		for _, decl := range l.file.Decls {
			if ed, ok := decl.(*ast.EnumDecl); ok && ed != nil && ed.Name == name {
				return ed
			}
		}
	}
	if l.res != nil && l.res.FileScope != nil {
		if sym := l.res.FileScope.Lookup(name); sym != nil {
			if ed, ok := sym.Decl.(*ast.EnumDecl); ok {
				return ed
			}
		}
	}
	return nil
}

// recoverMethodReturnType derives the return type of a receiver-and-
// method pair for the subset of stdlib intrinsics where the return
// shape is fixed by the name alone. Used as a fallback when the
// checker doesn't populate `Types[e]` for the call. Mirrors the
// routing in internal/mir/lower.go:methodToIntrinsic.
func recoverMethodReturnType(name string, recv Expr) Type {
	if recv == nil {
		return nil
	}
	return recoverMethodReturnTypeFromType(name, recv.Type())
}

func recoverMethodReturnTypeFromType(name string, rt Type) Type {
	if rt == nil || rt == ErrTypeVal {
		return nil
	}
	// Common name-based shortcuts that don't depend on the receiver's
	// concrete generic args.
	switch name {
	case "len":
		if isBuiltinContainer(rt) || isPrim(rt, PrimString) || isPrim(rt, PrimBytes) {
			return TInt
		}
	case "isEmpty":
		if isBuiltinContainer(rt) || isPrim(rt, PrimString) || isPrim(rt, PrimBytes) {
			return TBool
		}
	case "contains", "hasPrefix", "hasSuffix", "startsWith", "endsWith":
		if isPrim(rt, PrimString) || isPrim(rt, PrimBytes) || isBuiltinContainer(rt) {
			return TBool
		}
	case "toUpper", "toLower", "trim", "trimSpace", "trimLeft", "trimRight":
		if isPrim(rt, PrimString) {
			return TString
		}
	case "substring", "slice", "replace", "repeat":
		if isPrim(rt, PrimString) {
			return TString
		}
	case "toBytes":
		if isPrim(rt, PrimString) {
			return TBytes
		}
	case "split":
		if isPrim(rt, PrimString) {
			return &NamedType{Name: "List", Builtin: true, Args: []Type{TString}}
		}
	case "join":
		// `parts.join(sep)` on List<String> returns String.
		if nt, ok := rt.(*NamedType); ok && nt.Builtin && nt.Name == "List" {
			return TString
		}
	case "toInt":
		// `String.toInt(self) -> Result<Int, Error>`. Char.toInt returns
		// a bare Int, but the prelude / primitive shape disambiguates by
		// receiver type — only the String surface routes through `?`
		// propagation, so we restrict the recovery to that.
		if isPrim(rt, PrimString) {
			return &NamedType{
				Name:    "Result",
				Builtin: true,
				Args: []Type{
					TInt,
					&NamedType{Name: "Error", Builtin: true},
				},
			}
		}
	case "toFloat":
		if isPrim(rt, PrimString) {
			return &NamedType{
				Name:    "Result",
				Builtin: true,
				Args: []Type{
					TFloat,
					&NamedType{Name: "Error", Builtin: true},
				},
			}
		}
	case "toString":
		// Primitive `.toString()` returns String for every numeric kind,
		// Bool, Char, and Byte. The MIR-direct backend's
		// `emitPrimitiveMethodCall` routes the lowered `Type__toString`
		// symbol to the matching `osty_rt_*_to_string` runtime helper,
		// but that lowering only fires once the call's destination
		// local has a concrete String type. Without this arm, an
		// injected stdlib body that calls e.g. `fill.toString()` (where
		// `fill: Char`) ended up with an ErrType local that the LLVM
		// emitter rejected with "unsupported local type <error>".
		if isPrim(rt, PrimChar) || isPrim(rt, PrimByte) || isPrim(rt, PrimInt) ||
			isPrim(rt, PrimFloat) || isPrim(rt, PrimBool) {
			return TString
		}
		// String.toString is identity.
		if isPrim(rt, PrimString) {
			return TString
		}
	}
	// String-specific element-type returns.
	if isPrim(rt, PrimString) {
		switch name {
		case "chars":
			return &NamedType{Name: "List", Builtin: true, Args: []Type{TChar}}
		case "bytes":
			return &NamedType{Name: "List", Builtin: true, Args: []Type{TByte}}
		case "indexOf", "lastIndexOf":
			return &OptionalType{Inner: TInt}
		case "lines", "fields":
			return &NamedType{Name: "List", Builtin: true, Args: []Type{TString}}
		case "padLeft", "padRight", "padStart", "padEnd",
			"concat", "reverse", "toUpperCase", "toLowerCase",
			"replaceAll", "replaceFirst":
			return TString
		case "count", "byteSize":
			return TInt
		case "first", "last":
			return &OptionalType{Inner: TChar}
		case "stripPrefix", "stripSuffix":
			return &OptionalType{Inner: TString}
		case "byteAt":
			return &OptionalType{Inner: TByte}
		case "charAt":
			return &OptionalType{Inner: TChar}
		case "splitFirst", "splitLast":
			return &OptionalType{Inner: &TupleType{Elems: []Type{TString, TString}}}
		}
	}
	// Range<Int> intrinsic returns. `Builtin` flag may or may not be
	// set depending on call site; accept either form.
	if nt, ok := rt.(*NamedType); ok && nt.Name == "Range" {
		switch name {
		case "toList":
			return &NamedType{Name: "List", Builtin: true, Args: []Type{TInt}}
		case "len":
			return TInt
		case "isEmpty", "contains":
			return TBool
		}
	}
	// Char / Byte primitive method returns.
	if isPrim(rt, PrimChar) || isPrim(rt, PrimByte) {
		switch name {
		case "toInt":
			return TInt
		}
	}
	// Char-specific predicate methods.
	if isPrim(rt, PrimChar) {
		switch name {
		case "isDigit", "isAlpha", "isWhitespace", "isUpper", "isLower",
			"isAlnum", "isAscii":
			return TBool
		}
	}
	// Int primitive method returns.
	if isPrim(rt, PrimInt) {
		switch name {
		case "abs", "min", "max", "neg", "pow", "clamp", "gcd", "lcm", "sign":
			return TInt
		case "countOnes", "countZeros", "leadingZeros", "trailingZeros":
			return TInt
		case "toFloat":
			return TFloat
		case "toHex", "toBinary", "toOctal":
			return TString
		case "isPositive", "isNegative", "isZero", "isEven", "isOdd":
			return TBool
		}
	}
	// Float primitive method returns.
	if isPrim(rt, PrimFloat) {
		switch name {
		case "abs", "sqrt", "floor", "ceil", "round", "min", "max", "pow", "neg",
			"log", "log2", "log10", "exp", "sin", "cos", "tan",
			"asin", "acos", "atan", "atan2", "sinh", "cosh", "tanh",
			"cbrt", "ln":
			return TFloat
		case "toInt":
			return TInt
		case "isNan", "isFinite", "isInfinite", "isPositive", "isNegative", "isZero":
			return TBool
		}
	}
	// Bool predicates: redundant but documents.
	if isPrim(rt, PrimBool) {
		switch name {
		case "not":
			return TBool
		}
	}
	// Bytes-specific intrinsic returns.
	if isPrim(rt, PrimBytes) {
		switch name {
		case "indexOf", "lastIndexOf":
			return &OptionalType{Inner: TInt}
		case "len":
			return TInt
		case "concat", "slice", "repeat":
			return TBytes
		case "toHex":
			return TString
		case "toString":
			return &NamedType{Name: "Result", Builtin: true, Args: []Type{
				TString, &NamedType{Name: "Error", Builtin: true},
			}}
		}
	}
	// Element-type returns: List<T>.first / .last / .get → T?, .push → Unit.
	if nt, ok := rt.(*NamedType); ok && nt.Builtin && nt.Name == "List" && len(nt.Args) == 1 {
		elem := nt.Args[0]
		switch name {
		case "first", "last", "min", "max", "pop":
			return &OptionalType{Inner: elem}
		case "push", "add", "clear", "insert", "removeAt", "extend":
			return TUnit
		case "remove":
			return elem
		case "indexOf", "lastIndexOf":
			return &OptionalType{Inner: TInt}
		case "sorted", "reverse", "reversed", "filter",
			"slice", "take", "drop", "concat", "append":
			return nt
		case "contains", "any", "all", "isEmpty":
			return TBool
		case "count":
			return TInt
		case "sum", "product":
			return elem
		case "entries":
			return &NamedType{Name: "List", Builtin: true, Args: []Type{
				&TupleType{Elems: []Type{TInt, elem}},
			}}
		case "toSet":
			return &NamedType{Name: "Set", Builtin: true, Args: []Type{elem}}
		case "iter":
			return &NamedType{Name: "Iterator", Builtin: true, Args: []Type{elem}}
		case "reduce":
			return &OptionalType{Inner: elem}
		case "distinct":
			return nt
		case "partition":
			return &TupleType{Elems: []Type{nt, nt}}
		}
	}
	// List<List<T>>.flatten() → List<T>.
	if nt, ok := rt.(*NamedType); ok && nt.Builtin && nt.Name == "List" && len(nt.Args) == 1 {
		if inner, ok := nt.Args[0].(*NamedType); ok && inner.Builtin && inner.Name == "List" && len(inner.Args) == 1 && name == "flatten" {
			return &NamedType{Name: "List", Builtin: true, Args: []Type{inner.Args[0]}}
		}
	}
	// Iterator<T> methods. Iterator is a stdlib protocol type whose
	// `Builtin` flag may or may not be set depending on how it was
	// declared at the call site — accept either form.
	if nt, ok := rt.(*NamedType); ok && nt.Name == "Iterator" && len(nt.Args) == 1 {
		elem := nt.Args[0]
		switch name {
		case "collect":
			return &NamedType{Name: "List", Builtin: true, Args: []Type{elem}}
		case "next":
			return &OptionalType{Inner: elem}
		case "hasNext":
			return TBool
		case "count":
			return TInt
		}
	}
	// Channel<T> methods.
	if nt, ok := rt.(*NamedType); ok && nt.Name == "Channel" && len(nt.Args) == 1 {
		elem := nt.Args[0]
		switch name {
		case "send", "close":
			return TUnit
		case "recv":
			return &OptionalType{Inner: elem}
		case "isClosed":
			return TBool
		}
	}
	// Duration methods.
	if nt, ok := rt.(*NamedType); ok && nt.Name == "Duration" {
		switch name {
		case "toMillis", "toMicros", "toNanos", "toSeconds", "toMinutes", "toHours":
			return TInt
		}
	}
	// Map<K, V> method returns.
	if nt, ok := rt.(*NamedType); ok && nt.Builtin && nt.Name == "Map" && len(nt.Args) == 2 {
		k, v := nt.Args[0], nt.Args[1]
		switch name {
		case "get", "remove":
			return &OptionalType{Inner: v}
		case "getOr":
			return v
		case "keys":
			return &NamedType{Name: "List", Builtin: true, Args: []Type{k}}
		case "values":
			return &NamedType{Name: "List", Builtin: true, Args: []Type{v}}
		case "entries":
			return &NamedType{Name: "List", Builtin: true, Args: []Type{
				&TupleType{Elems: []Type{k, v}},
			}}
		case "containsKey":
			return TBool
		case "insert":
			return TUnit
		}
	}
	// Set<T> method returns: .toList() → List<T>, .add/.remove → Unit,
	// .contains → Bool. Mirrors the List/Map intrinsic tables.
	if nt, ok := rt.(*NamedType); ok && nt.Builtin && nt.Name == "Set" && len(nt.Args) == 1 {
		switch name {
		case "toList":
			return &NamedType{Name: "List", Builtin: true, Args: []Type{nt.Args[0]}}
		case "add", "remove", "clear":
			return TUnit
		case "contains", "isEmpty":
			return TBool
		case "len":
			return TInt
		}
	}
	// Map<K,V> generic isEmpty/len.
	if nt, ok := rt.(*NamedType); ok && nt.Builtin && nt.Name == "Map" && len(nt.Args) == 2 {
		switch name {
		case "isEmpty":
			return TBool
		case "len":
			return TInt
		}
	}
	// Option<T> methods. `OptionalType{Inner}` carries T directly.
	if ot, ok := rt.(*OptionalType); ok && ot != nil {
		switch name {
		case "isSome", "isNone":
			return TBool
		case "unwrapOr", "unwrap", "expect", "unwrapOrElse":
			return ot.Inner
		case "orElse", "filter", "or", "and":
			return ot
		}
	}
	// Result<T, E> methods.
	if nt, ok := rt.(*NamedType); ok && nt.Builtin && nt.Name == "Result" && len(nt.Args) == 2 {
		t, e := nt.Args[0], nt.Args[1]
		switch name {
		case "isOk", "isErr":
			return TBool
		case "unwrapOr", "unwrap", "expect", "unwrapOrElse":
			return t
		case "unwrapErr":
			return e
		case "ok":
			return &OptionalType{Inner: t}
		case "err":
			return &OptionalType{Inner: e}
		}
	}
	return nil
}

// isBuiltinContainer reports whether t is one of the builtin
// homogeneous collections whose len/isEmpty/contains return types
// can be derived from the method name alone.
func isBuiltinContainer(t Type) bool {
	nt, ok := t.(*NamedType)
	if !ok || !nt.Builtin {
		return false
	}
	switch nt.Name {
	case "List", "Map", "Set":
		return true
	}
	return false
}

// lowerVariantCall builds a VariantLit from a call whose callee is a
// variant symbol (`Some(42)`) or an enum-qualified variant
// (`Color.Red(255)`).
func (l *lowerer) lowerVariantCall(e *ast.CallExpr, enum, variant string) Expr {
	t := l.exprType(e)
	if !usableRecoveredType(t) {
		if rec := l.bindingTypeFromAST(e); usableRecoveredType(rec) {
			t = rec
		}
	}
	out := &VariantLit{
		Enum:    enum,
		Variant: variant,
		T:       t,
		SpanV:   nodeSpan(e),
	}
	for _, a := range e.Args {
		out.Args = append(out.Args, l.lowerArg(a))
	}
	return out
}

// printlnLikeKind reports whether the IntrinsicKind is one of the
// print-family intrinsics that consume a single value through
// emitPrintlnLike (which only handles primitives natively).
func printlnLikeKind(k IntrinsicKind) bool {
	switch k {
	case IntrinsicPrint, IntrinsicPrintln, IntrinsicEprint, IntrinsicEprintln:
		return true
	}
	return false
}

// shouldAutoToString reports whether a print-arg expression's type
// is non-primitive enough that the LLVM backend's emitPrintlnLike
// would wall on it without a `.toString()` wrapper. Excludes
// nil/poisoned types (the existing recovery path handles those)
// and excludes types that already produce a String at the call
// boundary (avoids a redundant identity wrap).
func shouldAutoToString(e Expr) bool {
	if e == nil {
		return false
	}
	t := e.Type()
	if t == nil || t == ErrTypeVal {
		return false
	}
	if hasPoisonedTypeArg(t) {
		return false
	}
	if pt, ok := t.(*PrimType); ok {
		switch pt.Kind {
		case PrimString:
			return false
		case PrimInvalid:
			return false
		}
		// Other primitives (Int, Bool, Float, Char, Byte, ...) are
		// emitPrintlnLike-native: leave them alone.
		return false
	}
	// Don't double-wrap an already-toString-shaped MethodCall.
	if mc, ok := e.(*MethodCall); ok && mc.Name == "toString" {
		return false
	}
	// Built-in containers (List<T>, Map<K, V>, Set<T>, Bytes,
	// Channel<T>, Handle<T>) skip auto-wrap; they have their own
	// runtime println paths or aren't meant to be printed directly.
	if nt, ok := t.(*NamedType); ok && nt.Builtin {
		switch nt.Name {
		case "List", "Map", "Set", "Bytes", "Channel", "Handle":
			return false
		}
	}
	// Everything else — user struct / enum, stdlib opaque types —
	// route through `.toString()`.
	return true
}

func intrinsicByName(name string) (IntrinsicKind, bool) {
	switch name {
	case "print":
		return IntrinsicPrint, true
	case "println":
		return IntrinsicPrintln, true
	case "eprint":
		return IntrinsicEprint, true
	case "eprintln":
		return IntrinsicEprintln, true
	}
	return 0, false
}

func isPreludeVariantName(name string) bool {
	switch name {
	case "Some", "None", "Ok", "Err":
		return true
	}
	return false
}

func (l *lowerer) lowerList(e *ast.ListExpr) Expr {
	out := &ListLit{SpanV: nodeSpan(e)}
	for _, el := range e.Elems {
		out.Elems = append(out.Elems, l.lowerExpr(el))
	}
	// Derive the element type from the checker's inferred list type.
	if t := l.exprType(e); t != nil {
		if nt, ok := t.(*NamedType); ok && nt.Name == "List" && len(nt.Args) == 1 {
			out.Elem = nt.Args[0]
		}
	}
	if out.Elem == nil && len(out.Elems) > 0 {
		out.Elem = out.Elems[0].Type()
	}
	// Final fallback: when both the checker's list type and the
	// first lowered element's type are poisoned (`<error>`),
	// inspect the first AST element's syntactic shape to recover a
	// concrete element type. The `[Point {...}]` shorthand is the
	// common case — the literal's head ident names the struct
	// without going through the per-node Types map.
	if (out.Elem == nil || out.Elem == ErrTypeVal) && len(e.Elems) > 0 {
		if t := l.bindingTypeFromAST(e.Elems[0]); t != nil {
			out.Elem = t
		}
	}
	if out.Elem == nil {
		out.Elem = ErrTypeVal
	}
	return out
}

func (l *lowerer) lowerIfExpr(e *ast.IfExpr) Expr {
	t := l.exprType(e)
	thenBlk := l.lowerBlock(e.Then)
	elseBlk := l.lowerElse(e.Else)
	if t == ErrTypeVal {
		t = recoverBlockType(thenBlk, elseBlk)
	}
	if !usableRecoveredType(t) {
		if rec := l.bindingTypeFromAST(e); usableRecoveredType(rec) {
			t = rec
		}
	}
	if e.IsIfLet {
		scrut := l.lowerExpr(e.Cond)
		pat := l.lowerPattern(e.Pattern)
		// Pattern-binding type recovery: when a pattern like
		// `Some(n)` introduces `n: T` from a scrutinee `opt:
		// Option<T>`, the IR-level `lowerExpr` of the Then
		// block may have run before the resolver propagated
		// `n`'s type to the body's Idents. Walk Then for
		// Idents matching the pattern's binding names and
		// refresh their Type — this also re-types any
		// captures inside trailing closures via
		// `repropagateClosureBodyTypes`.
		recoverIfLetPatternBindingTypes(scrut, pat, thenBlk)
		return &IfLetExpr{
			Pattern:   pat,
			Scrutinee: scrut,
			Then:      thenBlk,
			Else:      elseBlk,
			T:         t,
			SpanV:     nodeSpan(e),
		}
	}
	return &IfExpr{Cond: l.lowerExpr(e.Cond), Then: thenBlk, Else: elseBlk, T: t, SpanV: nodeSpan(e)}
}

// recoverIfLetPatternBindingTypes walks `body` and re-types
// Idents whose Name matches a binding the pattern introduces.
// The binding type comes from the scrutinee's structural shape:
// `Some(n)` on `Option<T>` yields `n: T`; `Ok(v)` on
// `Result<T,E>` yields `v: T`; tuple / struct destructures
// recurse into the matching scrutinee field types.
//
// Without this recovery, captures inside trailing closures
// (e.g. `if let Some(n) = opt { |x| x + n }`) snapshot `n`'s
// type at body-lowering time when it's still ErrTypeVal, and
// the captured local in the lifted MIR fn lowers as
// `<error>`-typed. The MIR-side `builtinVariantPayloadType`
// (PR #1820) covers match arms and direct payload reads but
// runs too late to update closure captures.
func recoverIfLetPatternBindingTypes(scrut Expr, pat Pattern, body *Block) {
	if scrut == nil || pat == nil || body == nil {
		return
	}
	bindings := map[string]Type{}
	collectPatternBindingTypes(pat, scrut.Type(), bindings)
	if len(bindings) == 0 {
		return
	}
	retypeIdentsScoped(body, bindings)
	// Closures inside the body need their Captures' T slot
	// patched too — `repropagateClosureBodyTypes` updates
	// Idents (including the captured ones inside lifted
	// closure bodies) but the Closure.Captures snapshot still
	// references the old types. Walk and refresh.
	Walk(VisitorFunc(func(n Node) bool {
		cl, ok := n.(*Closure)
		if !ok || cl == nil {
			return true
		}
		for _, c := range cl.Captures {
			if c == nil {
				continue
			}
			if c.T != nil && c.T != ErrTypeVal {
				continue
			}
			if t, hit := bindings[c.Name]; hit && t != nil && t != ErrTypeVal {
				c.T = CloneType(t)
			}
		}
		return true
	}), body)
}

// collectPatternBindingTypes walks a pattern and a scrutinee
// type together, recording each IdentPat's introduced name with
// the structural sub-type the pattern position implies. Mirrors
// `builtinVariantPayloadType` (PR #1820) for prelude Option /
// Result variants; for struct / tuple patterns the matching
// field / element type is pulled directly off the scrutinee.
// Unrecognised shapes fall through silently.
func collectPatternBindingTypes(p Pattern, scrutT Type, out map[string]Type) {
	if p == nil || scrutT == nil || scrutT == ErrTypeVal {
		return
	}
	switch x := p.(type) {
	case *IdentPat:
		if x != nil && x.Name != "" {
			out[x.Name] = scrutT
		}
	case *BindingPat:
		if x == nil {
			return
		}
		if x.Name != "" {
			out[x.Name] = scrutT
		}
		collectPatternBindingTypes(x.Pattern, scrutT, out)
	case *VariantPat:
		if x == nil {
			return
		}
		for i, arg := range x.Args {
			pt := builtinVariantPayloadType(scrutT, x.Variant, i)
			if pt == nil || pt == ErrTypeVal {
				continue
			}
			collectPatternBindingTypes(arg, pt, out)
		}
	case *TuplePat:
		if x == nil {
			return
		}
		tt, ok := scrutT.(*TupleType)
		if !ok || tt == nil {
			return
		}
		for i, elem := range x.Elems {
			if i >= len(tt.Elems) {
				break
			}
			collectPatternBindingTypes(elem, tt.Elems[i], out)
		}
	case *StructPat:
		if x == nil {
			return
		}
		nt, ok := scrutT.(*NamedType)
		if !ok || nt == nil {
			return
		}
		for _, f := range x.Fields {
			if f.Pattern == nil {
				continue
			}
			// Resolve the field's type from the struct decl
			// when accessible. Conservative: skip if we can't
			// resolve — the binding stays untyped (no worse
			// than before).
		}
		_ = nt
	}
}

// builtinVariantPayloadType is the IR-side mirror of the MIR
// helper added in PR #1820. Returns the i-th payload type for
// a prelude Option / Result variant when the scrutinee carries
// no user enum decl (the builtin case). Returns nil for
// unrecognised shapes so callers leave the slot un-typed.
func builtinVariantPayloadType(scrutT Type, variantName string, idx int) Type {
	if scrutT == nil {
		return nil
	}
	if ot, ok := scrutT.(*OptionalType); ok && ot != nil {
		if variantName == "Some" && idx == 0 {
			return ot.Inner
		}
		return nil
	}
	nt, ok := scrutT.(*NamedType)
	if !ok || nt == nil || !nt.Builtin {
		return nil
	}
	switch nt.Name {
	case "Option", "Maybe":
		if variantName == "Some" && idx == 0 && len(nt.Args) >= 1 {
			return nt.Args[0]
		}
	case "Result":
		switch variantName {
		case "Ok":
			if idx == 0 && len(nt.Args) >= 1 {
				return nt.Args[0]
			}
		case "Err":
			if idx == 0 && len(nt.Args) >= 2 {
				return nt.Args[1]
			}
		}
	}
	return nil
}

// recoverBlockType picks a non-Err type from either branch of an if/else.
// When only one branch carries a good type and the other is ErrType —
// typical when the checker's elab reports one branch at default rules
// but drops the enclosing if — prefer the good one.
func recoverBlockType(then, els *Block) Type {
	tt := blockResultType(then)
	et := blockResultType(els)
	if tt != nil && tt != ErrTypeVal {
		return tt
	}
	if et != nil && et != ErrTypeVal {
		return et
	}
	return ErrTypeVal
}

func blockResultType(b *Block) Type {
	if b == nil || b.Result == nil {
		return nil
	}
	return b.Result.Type()
}

// lowerElse normalises an else arm (which is an ast.Expr per the
// parser) into a *Block, or nil for no-else.
func (l *lowerer) lowerElse(alt ast.Expr) *Block {
	switch alt := alt.(type) {
	case nil:
		return nil
	case *ast.Block:
		return l.lowerBlock(alt)
	case *ast.IfExpr:
		inner := l.lowerIfExpr(alt)
		return &Block{Result: inner, SpanV: inner.At()}
	default:
		lowered := l.lowerExpr(alt)
		return &Block{Result: lowered, SpanV: lowered.At()}
	}
}

// ==== Span helpers ====

func posFromToken(p token.Pos) Pos {
	return Pos{Offset: p.Offset, Line: p.Line, Column: p.Column}
}

func nodeSpan(n ast.Node) Span {
	return Span{Start: posFromToken(n.Pos()), End: posFromToken(n.End())}
}

// ==== Additional declarations ====

func (l *lowerer) lowerUseDecl(u *ast.UseDecl) Decl {
	out := &UseDecl{
		Path:         append([]string(nil), u.Path...),
		RawPath:      u.RawPath,
		Alias:        u.Alias,
		IsGoFFI:      u.IsGoFFI,
		IsRuntimeFFI: u.IsRuntimeFFI,
		GoPath:       u.GoPath,
		RuntimePath:  u.RuntimePath,
		SpanV:        nodeSpan(u),
	}
	if out.Alias == "" && len(out.Path) > 0 {
		out.Alias = out.Path[len(out.Path)-1]
	}
	for _, d := range u.GoBody {
		if lowered := l.lowerDecl(d); lowered != nil {
			out.GoBody = append(out.GoBody, lowered)
		}
	}
	// Populate UseDecl.Imports from the consumer's import surfaces so
	// MIR's `useDeclFnType` can recover cross-pkg return types when
	// the call's IR-level Type is poisoned. Aliased to use-decl's
	// resolved alias; FFI uses (GoBody / IsGoFFI / IsRuntimeFFI) take
	// their own path and don't need this seed.
	if !out.IsGoFFI && !out.IsRuntimeFFI && out.Alias != "" && l.chk != nil {
		for i := range l.chk.ImportSurfaces {
			surface := &l.chk.ImportSurfaces[i]
			if surface.Alias != out.Alias {
				continue
			}
			for _, fn := range surface.Functions {
				if fnDecl := l.lowerImportedFn(&fn); fnDecl != nil {
					out.Imports = append(out.Imports, fnDecl)
				}
			}
		}
	}
	return out
}

// lowerImportedFn converts one cross-pkg import-surface function into
// a minimal `*ir.FnDecl` carrying just the name and signature shape
// (params + return type) so MIR's `useDeclFnType` can read it back.
//
// We do NOT lower the body — the consumer doesn't have one and the
// dep's body lives in its own IR module. Param types come from the
// surface's `ParamTypeReprs` (the structural form). The legacy
// `ParamTypes []string` fallback is intentionally NOT parsed — those
// strings are pre-monomorph, generics-bearing renderings (e.g.
// "List<T>", "Map<K, V>") that would need the consumer's type-arg
// substitution context to round-trip into IR shapes. Callers feeding
// only legacy strings will see ErrTypeVal slots; in practice all
// production paths through `arenaBuildImportedFn` populate both
// fields, so this is a documentation-only limitation.
//
// Skips functions registered with a non-empty Owner (those are
// method-form alias-prefixed registrations the surface arena walker
// emits alongside the free-fn form per
// `import_surface_arena.go:225-229`). Only the free-fn form gets a
// lowered `FnDecl` here so cross-pkg method-call dispatch keeps
// going through the receiver-type method lookup in MIR.
func (l *lowerer) lowerImportedFn(fn *api.PackageCheckFn) *FnDecl {
	if fn == nil || fn.Name == "" || fn.Owner != "" {
		return nil
	}
	params := make([]*Param, 0, len(fn.ParamNames))
	for i, name := range fn.ParamNames {
		paramTy := importedParamType(fn.ParamTypeReprs, i)
		// `?`-prefixed names mark trailing defaults (see
		// `selfhostImportFnParamNames` + check.osty::paramDefaultCount).
		// MIR's signature recovery cares about the bare name, so strip
		// the marker before the FnDecl materializes.
		bareName := name
		if len(bareName) > 0 && bareName[0] == '?' {
			bareName = bareName[1:]
		}
		params = append(params, &Param{Name: bareName, Type: paramTy})
	}
	retTy := importedReturnType(fn.ReturnTypeRepr, fn.ReturnType)
	return &FnDecl{
		Name:   fn.Name,
		Params: params,
		Return: retTy,
	}
}

// importedParamType returns the param type at `index` in the
// structural `ParamTypeReprs` slice, or ErrTypeVal when the index is
// out of range or the repr at that position is malformed. The legacy
// `ParamTypes []string` slice is intentionally not parsed — see
// `lowerImportedFn` for the rationale.
func importedParamType(reprs []api.TypeRepr, index int) Type {
	if index < 0 || index >= len(reprs) {
		return ErrTypeVal
	}
	repr := reprs[index]
	if t := lowerImportedTypeRepr(&repr); t != nil {
		return t
	}
	return ErrTypeVal
}

// importedReturnType picks the function's return type — preferring
// the structural ReturnTypeRepr, falling back to the legacy
// ReturnType string for the well-defined sentinels ("", "()") that
// `selfhostInstallImportSurfaces` already treats as Unit. Other
// legacy strings reach IR as ErrTypeVal for the same reason
// `importedParamType` returns it.
func importedReturnType(repr *api.TypeRepr, legacy string) Type {
	if repr != nil {
		if t := lowerImportedTypeRepr(repr); t != nil {
			return t
		}
	}
	if legacy == "" || legacy == "()" {
		return TUnit
	}
	return ErrTypeVal
}

// lowerImportedTypeRepr converts an `api.TypeRepr` (the structured
// type description shared with the selfhost JSON wire format) into an
// `ir.Type`. Best-effort: unknown kinds fall back to ErrTypeVal so
// the caller can decide whether to drop the FnDecl or carry a
// partial signature.
func lowerImportedTypeRepr(repr *api.TypeRepr) Type {
	if repr == nil {
		return nil
	}
	switch repr.Kind {
	case "primitive":
		switch repr.Name {
		case "Int":
			return TInt
		case "Int8":
			return TInt8
		case "Int16":
			return TInt16
		case "Int32":
			return TInt32
		case "Int64":
			return TInt64
		case "Float32":
			return TFloat32
		case "Float64":
			return TFloat64
		case "Bool":
			return TBool
		case "String":
			return TString
		case "Char":
			return TChar
		case "Byte":
			return TByte
		case "Bytes":
			return TBytes
		case "()":
			return TUnit
		}
		return ErrTypeVal
	case "named":
		// Builtin generic shapes get the Builtin flag for downstream
		// recovery (Option/Result/List/Map/Set). Unknown nameds are
		// split into Package + Name via `splitQualifiedTypeName` so
		// the IR invariant (`NamedType.String()` and lookups elsewhere
		// rely on the two fields being separated) holds for qualified
		// import-surface names like "dep.Foo".
		pkg, base := splitQualifiedTypeName(repr.Name)
		nt := &NamedType{Package: pkg, Name: base}
		if len(repr.Args) > 0 {
			for i := range repr.Args {
				nt.Args = append(nt.Args, lowerImportedTypeRepr(&repr.Args[i]))
			}
		}
		// Builtin tag keys on the unqualified base name — qualified
		// shapes like "dep.List" are user types, not the prelude
		// `List`.
		if pkg == "" {
			switch base {
			case "Option", "Maybe", "Result", "List", "Map", "Set":
				nt.Builtin = true
			}
		}
		return nt
	case "optional":
		// `api.TypeRepr` carries the optional's inner type in
		// `Return`, not `Args` (see `internal/selfhost/api/types.go:49`
		// + `TypeRepr.String()` which dereferences `tr.Return` for the
		// optional rendering). A missing Return defaults the inner to
		// ErrTypeVal so the wrapper still surfaces as Optional.
		if repr.Return != nil {
			return &OptionalType{Inner: lowerImportedTypeRepr(repr.Return)}
		}
		return &OptionalType{Inner: ErrTypeVal}
	case "unit":
		return TUnit
	case "tuple":
		if len(repr.Args) == 0 {
			return TUnit
		}
		elems := make([]Type, 0, len(repr.Args))
		for i := range repr.Args {
			elems = append(elems, lowerImportedTypeRepr(&repr.Args[i]))
		}
		return &TupleType{Elems: elems}
	case "fn":
		params := make([]Type, 0, len(repr.Args))
		for i := range repr.Args {
			params = append(params, lowerImportedTypeRepr(&repr.Args[i]))
		}
		var ret Type = TUnit
		if repr.Return != nil {
			ret = lowerImportedTypeRepr(repr.Return)
		}
		paramNames := append([]string(nil), repr.ParamNames...)
		return &FnType{Params: params, ParamNames: paramNames, Return: ret}
	}
	return ErrTypeVal
}

func (l *lowerer) lowerInterfaceDecl(id *ast.InterfaceDecl) Decl {
	out := &InterfaceDecl{
		Name:     id.Name,
		Exported: id.Pub,
		SpanV:    nodeSpan(id),
	}
	for _, gp := range id.Generics {
		out.Generics = append(out.Generics, l.lowerTypeParam(gp, id.Name))
	}
	for _, ext := range id.Extends {
		out.Extends = append(out.Extends, l.lowerType(ext))
	}
	for _, m := range id.Methods {
		out.Methods = append(out.Methods, l.lowerFnDecl(m))
	}
	return out
}

func (l *lowerer) lowerTypeAliasDecl(td *ast.TypeAliasDecl) Decl {
	out := &TypeAliasDecl{
		Name:     td.Name,
		Target:   l.lowerType(td.Target),
		Exported: td.Pub,
		SpanV:    nodeSpan(td),
	}
	for _, gp := range td.Generics {
		out.Generics = append(out.Generics, l.lowerTypeParam(gp, td.Name))
	}
	return out
}

// ==== Additional statements ====

func (l *lowerer) lowerDeferStmt(s *ast.DeferStmt) Stmt {
	// DeferStmt's X is expression-typed but almost always a Block;
	// normalise to always a *Block in the IR so backends don't have to
	// peek at the inner expression.
	out := &DeferStmt{SpanV: nodeSpan(s)}
	if blk, ok := s.X.(*ast.Block); ok {
		out.Body = l.lowerBlock(blk)
		return out
	}
	lowered := l.lowerExpr(s.X)
	out.Body = &Block{
		Stmts: []Stmt{&ExprStmt{X: lowered, SpanV: lowered.At()}},
		SpanV: lowered.At(),
	}
	return out
}

// ==== Additional expressions ====

func (l *lowerer) lowerFieldExpr(e *ast.FieldExpr) Expr {
	// A numeric name (`t.0`) is tuple-indexed access. The parser spells
	// it with a FieldExpr; lift it to TupleAccess to keep backends
	// simple.
	if idx, ok := tupleIndex(e.Name); ok {
		x := l.lowerExpr(e.X)
		t := l.exprType(e)
		// AST recovery: when the checker didn't populate the tuple-
		// access type (post-#1645 `chk.Types[e]` is empty and the
		// SemanticDB byID/byKey lookups miss for FieldExpr-on-tuple),
		// pull the element type off the receiver's TupleType. Without
		// this `fn f(t: (Int, Int)) -> Int { t.0 + t.1 }` lowers with
		// every TupleAccess at T=<error>, poisoning the BinaryExpr.
		if t == nil || t == ErrTypeVal || hasPoisonedTypeArg(t) {
			if tt, ok := x.Type().(*TupleType); ok && tt != nil && idx >= 0 && idx < len(tt.Elems) {
				if elem := tt.Elems[idx]; elem != nil && elem != ErrTypeVal {
					t = elem
				}
			}
		}
		return &TupleAccess{
			X:     x,
			Index: idx,
			T:     t,
			SpanV: nodeSpan(e),
		}
	}
	// Paren-less nullary-method fallback. Spec §10.20 Duration
	// constructors (`5.s`, `100.ms`, etc.) parse as field-access but
	// desugar to nullary method calls. The selfhost checker accepts
	// them via elabNullaryMethodFallback; this branch routes the same
	// shape into a MethodCall HIR node so the LLVM backend reuses the
	// existing primitive-method intercept (`emitPrimitiveMethodCall`)
	// instead of a degenerate field access on an i64 receiver.
	if isParenlessDurationMethod(e.Name) {
		return &MethodCall{
			Receiver: l.lowerExpr(e.X),
			Name:     e.Name,
			T:        l.exprType(e),
			SpanV:    nodeSpan(e),
		}
	}
	x := l.lowerExpr(e.X)
	t := l.exprType(e)
	if t == ErrTypeVal || t == nil {
		// Recover from the struct declaration when the checker didn't
		// record a type for this field access. Without this, a single
		// checker-skipped FieldExpr propagates ErrType to every
		// `.locals.len()` or `.name + something` chain downstream.
		if recovered := l.recoverFieldType(x.Type(), e.Name); recovered != nil {
			t = recovered
		}
		if !usableRecoveredType(t) {
			if recovered := l.bindingTypeFromAST(e); usableRecoveredType(recovered) {
				t = recovered
			}
		}
		// AST-side receiver recovery: when the IR receiver's T is
		// `<error>` (chained method receivers, `self`, idents whose
		// inferred type didn't make it through), resolve the receiver
		// statically and retry the struct-decl field lookup. Without
		// this, every `recv.field` chain where the receiver is itself
		// a non-trivial expression keeps T=<error> and cascades.
		if !usableRecoveredType(t) {
			if astRecvT := l.resolveExprStaticType(e.X); astRecvT != nil && astRecvT != ErrTypeVal {
				if recovered := l.recoverFieldType(astRecvT, e.Name); recovered != nil {
					t = recovered
				}
			}
		}
	}
	return &FieldExpr{
		X:        x,
		Name:     e.Name,
		Optional: e.IsOptional,
		T:        t,
		SpanV:    nodeSpan(e),
	}
}

// isParenlessDurationMethod reports whether `name` is one of the
// paren-less Duration constructors from §10.20. The checker side
// already accepts the FieldExpr shape as a method call via
// `elabNullaryMethodFallback`; this list lets the lowerer mirror the
// same dispatch on the Go-host side without re-querying the primitive
// method table for every field access. Limiting it to the
// duration-constructor names keeps every other field/property access
// on its existing path so this fallback can stay narrow until the
// checker exposes a structured "this FieldExpr is actually a method
// call" signal.
func isParenlessDurationMethod(name string) bool {
	switch name {
	case "ns", "us", "ms", "s", "minutes", "h", "days", "weeks":
		return true
	}
	return false
}

// recoverFieldType resolves a field access `receiverType.fieldName`
// back to the declared field type by consulting the resolver's type
// decl for receiverType. Handles partial struct declarations (multiple
// `pub struct X { ... }` blocks across the same package) by walking
// `l.file.Decls` for any StructDecl with the same head name when the
// resolver-anchored decl doesn't carry the requested field.
func (l *lowerer) recoverFieldType(receiverType Type, fieldName string) Type {
	if receiverType == nil || receiverType == ErrTypeVal {
		return nil
	}
	// Optional chaining: `b?.n` where `b: Box?` lowers as a single
	// FieldExpr{Optional:true} whose receiver type is `Box?`. The
	// checker often doesn't record a type for it, and the canonical
	// struct-field lookup expects a NamedType. Unwrap the option,
	// look up the field on the inner nominal, and propagate the
	// None-short-circuit semantics: the result is Option<fieldType>
	// unless the field is *already* optional (`Outer.inner: Inner?`),
	// in which case `?.` flattens — chained `outer?.inner?.value`
	// must produce `Int?`, not `Int??`. Without the flatten, MIR
	// builds an extra Option wrapper whose None branch falls back
	// to `none <error>` because the synthesised type can't be
	// inferred two levels down.
	if ot, ok := receiverType.(*OptionalType); ok && ot != nil {
		if innerNT, ok := ot.Inner.(*NamedType); ok && innerNT != nil && !innerNT.Builtin {
			if t := l.lookupStructFieldType(innerNT.Name, fieldName); t != nil {
				if _, alreadyOpt := t.(*OptionalType); alreadyOpt {
					return t
				}
				return &OptionalType{Inner: t}
			}
		}
		return nil
	}
	nt, ok := receiverType.(*NamedType)
	if !ok || nt.Builtin {
		return nil
	}
	return l.lookupStructFieldType(nt.Name, fieldName)
}

// lookupStructFieldType returns the declared type of a field on a
// (possibly partial) struct. Memoised per (typeName, fieldName) so the
// merged-toolchain hot path doesn't re-walk file decls per access.
func (l *lowerer) lookupStructFieldType(typeName, fieldName string) Type {
	if l.fieldTypeCache != nil {
		if v, ok := l.fieldTypeCache[fieldKey{typeName, fieldName}]; ok {
			return v
		}
	}
	t := l.lookupStructFieldTypeUncached(typeName, fieldName)
	if l.fieldTypeCache == nil {
		l.fieldTypeCache = map[fieldKey]Type{}
	}
	l.fieldTypeCache[fieldKey{typeName, fieldName}] = t
	return t
}

func (l *lowerer) lookupStructFieldTypeUncached(typeName, fieldName string) Type {
	if l.res != nil {
		if sym := l.res.FileScope.Lookup(typeName); sym != nil && sym.Decl != nil {
			if sd, ok := sym.Decl.(*ast.StructDecl); ok && sd != nil {
				if t := structFieldType(sd, fieldName); t != nil {
					return l.lowerType(t)
				}
			}
		}
	}
	if l.file == nil {
		return nil
	}
	for _, d := range l.file.Decls {
		sd, ok := d.(*ast.StructDecl)
		if !ok || sd == nil || sd.Name != typeName {
			continue
		}
		if t := structFieldType(sd, fieldName); t != nil {
			return l.lowerType(t)
		}
	}
	return nil
}

func structFieldType(sd *ast.StructDecl, fieldName string) ast.Type {
	for _, f := range sd.Fields {
		if f == nil || f.Name != fieldName {
			continue
		}
		return f.Type
	}
	return nil
}

type fieldKey struct {
	typeName  string
	fieldName string
}

// tupleIndex parses a field name like "0" or "12" as a tuple index.
// Returns (idx, true) when the whole string is a non-negative decimal.
func tupleIndex(name string) (int, bool) {
	if name == "" {
		return 0, false
	}
	n := 0
	for _, r := range name {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}

func (l *lowerer) lowerStructLit(s *ast.StructLit) Expr {
	name := ""
	var headIdent *ast.Ident
	switch h := s.Type.(type) {
	case *ast.Ident:
		name = h.Name
		headIdent = h
	case *ast.FieldExpr:
		// `pkg.Type { ... }` — keep the trailing name; the IR doesn't
		// model packages yet.
		name = h.Name
	}
	out := &StructLit{
		TypeName: name,
		T:        l.exprType(s),
		SpanV:    nodeSpan(s),
	}
	if !usableRecoveredType(out.T) {
		if t := l.bindingTypeFromAST(s); usableRecoveredType(t) {
			out.T = t
		}
	}
	explicit := map[string]bool{}
	for _, f := range s.Fields {
		field := StructLitField{
			Name:  f.Name,
			SpanV: Span{Start: posFromToken(f.Pos()), End: posFromToken(f.End())},
		}
		if f.Value != nil {
			field.Value = l.lowerExpr(f.Value)
		}
		out.Fields = append(out.Fields, field)
		explicit[f.Name] = true
	}
	if s.Spread != nil {
		out.Spread = l.lowerExpr(s.Spread)
	}
	// Inject defaults for unspecified fields when no spread is present.
	// With a spread, missing fields come from the spread base — defaults
	// are only relevant on the bare `T { explicit, ... }` shape. Defaults
	// are pure literals per spec (§3.4: literal-only default values), so
	// `lowerExpr` produces a self-contained IR Expr per construction site.
	if s.Spread == nil && headIdent != nil {
		if sd := l.structDeclByIdent(headIdent); sd != nil {
			for _, df := range sd.Fields {
				if df == nil || df.Default == nil || df.Name == "" {
					continue
				}
				if explicit[df.Name] {
					continue
				}
				out.Fields = append(out.Fields, StructLitField{
					Name:  df.Name,
					Value: l.lowerExpr(df.Default),
					SpanV: nodeSpan(s),
				})
			}
		}
	}
	return out
}

// isMapLitPlaceholderType reports whether a MapLit KeyT/ValT slot
// is unresolved — covers nil, ErrType, and PrimType{PrimInvalid}.
// The checker's "unknown type" fallback can land in any of those
// shapes depending on which inference path bailed; the entry-driven
// recovery treats them uniformly.
func isMapLitPlaceholderType(t Type) bool {
	if t == nil {
		return true
	}
	if _, ok := t.(*ErrType); ok {
		return true
	}
	if pt, ok := t.(*PrimType); ok && pt.Kind == PrimInvalid {
		return true
	}
	return false
}

func (l *lowerer) lowerMapLit(m *ast.MapExpr) Expr {
	out := &MapLit{SpanV: nodeSpan(m)}
	for _, en := range m.Entries {
		out.Entries = append(out.Entries, MapEntry{
			Key:   l.lowerExpr(en.Key),
			Value: l.lowerExpr(en.Value),
			SpanV: Span{Start: posFromToken(en.Pos()), End: posFromToken(en.End())},
		})
	}
	if t := l.exprType(m); t != nil {
		if nt, ok := t.(*NamedType); ok && nt.Name == "Map" && len(nt.Args) == 2 {
			out.KeyT = nt.Args[0]
			out.ValT = nt.Args[1]
		}
	}
	// Entry-driven recovery: when the checker leaves the map literal
	// without a pinned K/V (`let m = {"a": 1}` reaches ir.Lower with
	// `Types[m]` either empty or carrying a `Map<?, ?>` shape whose
	// type-args are PrimInvalid placeholders), pull the K/V off the
	// first entry's lowered Key/Value types. Without this, the MapLit
	// hits MIR with KeyT/ValT == placeholder and the downstream
	// `for-in over Map with unresolved key/value type` walls fire
	// — even though every entry has a concrete type.
	if isMapLitPlaceholderType(out.KeyT) && len(out.Entries) > 0 {
		if t := out.Entries[0].Key.Type(); !isMapLitPlaceholderType(t) {
			out.KeyT = t
		}
	}
	if isMapLitPlaceholderType(out.ValT) && len(out.Entries) > 0 {
		if t := out.Entries[0].Value.Type(); !isMapLitPlaceholderType(t) {
			out.ValT = t
		}
	}
	if out.KeyT == nil {
		out.KeyT = ErrTypeVal
	}
	if out.ValT == nil {
		out.ValT = ErrTypeVal
	}
	return out
}

func (l *lowerer) lowerClosure(c *ast.ClosureExpr) Expr {
	out := &Closure{
		Return: l.lowerType(c.ReturnType),
		T:      l.exprType(c),
		SpanV:  nodeSpan(c),
	}
	if out.Return == nil {
		out.Return = TUnit
	}
	for _, p := range c.Params {
		out.Params = append(out.Params, l.lowerParam(p))
	}
	// Inline closures (`|acc, n| ...`) leave the per-param AST Type
	// nil because the user didn't annotate them — the checker's
	// inference picks them up via the call-site context (e.g.
	// `xs.fold(0, |acc, n| ...)` resolves to fn(Int, Int) -> Int).
	// `lowerParam` faithfully forwards the nil, but downstream IR
	// validation rejects nil param types ("Closure: param[i] nil
	// Type"). The checker's inferred FnType is in out.T already, so
	// backfill missing param types from there.
	//
	// Closure return is filled the same way when no explicit
	// annotation is present and the inferred FnType has a Return.
	if fnT, ok := out.T.(*FnType); ok && fnT != nil {
		for i, p := range out.Params {
			if p == nil || p.Type != nil || i >= len(fnT.Params) {
				continue
			}
			p.Type = fnT.Params[i]
		}
		if c.ReturnType == nil && fnT.Return != nil && out.Return == TUnit {
			out.Return = fnT.Return
		}
	}
	// Body is always an expression. Wrap non-block bodies in a synthetic
	// block with the expression as the Result.
	if blk, ok := c.Body.(*ast.Block); ok {
		out.Body = l.lowerBlock(blk)
	} else {
		lowered := l.lowerExpr(c.Body)
		out.Body = &Block{Result: lowered, SpanV: lowered.At()}
	}
	// Recover return type from the lowered body when no explicit
	// annotation was given AND the checker also didn't supply a
	// FnType (out.T stayed ErrType / nil). Without this fallback,
	// `let inc = |x: Int| x + 1` ends up with Return=TUnit even
	// though the body trivially returns Int — downstream lowering
	// then reads the binding as `<error>` because the let's
	// inferred type is built from the closure's `Return` slot.
	if c.ReturnType == nil && out.Return == TUnit {
		if bodyT := closureBodyResultType(out.Body); bodyT != nil && bodyT != ErrTypeVal {
			out.Return = bodyT
		}
	}
	// Synthesise a FnType for the closure value when the checker
	// left out.T as ErrType / nil. Param + return slots are now
	// concretely typed (annotations + body recovery), so the
	// surface fn(...) -> R is recoverable and downstream LetStmt
	// binding type recovery picks it up.
	if out.T == nil || out.T == ErrTypeVal {
		if fnT := synthesiseClosureFnType(out); fnT != nil {
			out.T = fnT
		}
	}
	// Compute free-variable captures.
	out.Captures = ComputeCaptures(out.Body, out.Params)
	return out
}

// closureBodyResultType reads the trailing-expression type of a
// closure body block. Returns nil for void closures (no Result) or
// for bodies whose Result has no usable type.
func closureBodyResultType(body *Block) Type {
	if body == nil || body.Result == nil {
		return nil
	}
	return body.Result.Type()
}

// synthesiseClosureFnType builds a `fn(P0, P1, …) -> R` for a
// closure whose param/return slots are already resolved. Returns
// nil when any slot is still unset — leaving out.T as ErrType so
// downstream validators can flag the missing piece instead of
// pretending the closure is well-typed.
func synthesiseClosureFnType(cl *Closure) *FnType {
	if cl == nil || cl.Return == nil || cl.Return == ErrTypeVal {
		return nil
	}
	params := make([]Type, 0, len(cl.Params))
	for _, p := range cl.Params {
		if p == nil || p.Type == nil || p.Type == ErrTypeVal {
			return nil
		}
		params = append(params, p.Type)
	}
	return &FnType{Params: params, Return: cl.Return}
}

func (l *lowerer) lowerTurbofish(tf *ast.TurbofishExpr) Expr {
	// A bare turbofish without a call (`f::<Int>`) — retain the type
	// args on the underlying ident so backends that monomorphise off
	// function references can observe them.
	base := l.lowerExpr(tf.Base)
	typeArgs := make([]Type, 0, len(tf.Args))
	for _, a := range tf.Args {
		typeArgs = append(typeArgs, l.lowerType(a))
	}
	if id, ok := base.(*Ident); ok {
		id.TypeArgs = typeArgs
		return id
	}
	l.note("bare turbofish at %v attached to non-ident base; type args dropped", tf.Pos())
	return base
}

func (l *lowerer) lowerLoopExpr(e *ast.LoopExpr) Expr {
	t := l.exprType(e)
	if t == nil || t == ErrTypeVal {
		t = TUnit
	}
	return &LoopExpr{
		Label: e.Label,
		Body:  l.lowerBlock(e.Body),
		T:     t,
		SpanV: nodeSpan(e),
	}
}

func (l *lowerer) lowerMatchStmt(m *ast.MatchExpr) Stmt {
	out := &MatchStmt{
		Scrutinee: l.lowerExpr(m.Scrutinee),
		Arms:      l.lowerMatchArms(m.Arms),
		SpanV:     nodeSpan(m),
	}
	out.Tree = CompileDecisionTree(out.Scrutinee.Type(), out.Arms)
	return out
}

func (l *lowerer) lowerMatchExpr(m *ast.MatchExpr) Expr {
	out := &MatchExpr{
		Scrutinee: l.lowerExpr(m.Scrutinee),
		T:         l.exprType(m),
		SpanV:     nodeSpan(m),
	}
	// Populate pattern-binding types BEFORE lowering arms so the
	// arm bodies see proper Idents typed by their bindings instead
	// of `ErrType`. The embedded selfhost checker doesn't populate
	// `SymTypes` for match-arm pattern bindings on cross-pkg
	// variants (e.g. `Err(e)` against `Result<T, Error>` where
	// Error is from std.error) — without this priming, the IdentPat
	// for `e` has no recorded type, `lowerIdent` falls through to
	// `ErrTypeVal`, and downstream method-call dispatch
	// (`e.message()`) loses the receiver-type info needed for
	// stdlib body injection + interface method lookup.
	scrutT := out.Scrutinee.Type()
	if scrutT == nil || scrutT == ErrTypeVal {
		// Recovery: when the lowered scrutinee's type is poisoned
		// (cross-pkg generic args, native-checker miss on a call
		// site like `make()` returning `Result<Int, Error>` where
		// Error is from std.error), re-derive from the AST. Mirrors
		// the `bindingTypeFromAST` pathway used for `let` patterns.
		if recovered := l.bindingTypeFromAST(m.Scrutinee); recovered != nil && recovered != ErrTypeVal {
			scrutT = recovered
		}
	}
	if scrutT != nil && scrutT != ErrTypeVal {
		for _, arm := range m.Arms {
			if arm == nil {
				continue
			}
			l.populatePatternBindingTypes(arm.Pattern, scrutT)
		}
	}
	out.Arms = l.lowerMatchArms(m.Arms)
	// Recover the match type from its arm bodies when the checker
	// left it as <error>. The checker's type inference for match
	// expressions across large arm sets sometimes loses the common
	// arm type (observed on toolchain/core.osty `corePrintNodeBody`
	// and similar dispatch tables), which then poisons every
	// downstream operation consuming the match result. Unifying from
	// arm bodies keeps the MIR fast path live as long as every arm
	// resolved to the same concrete type.
	if out.T == nil || out.T == ErrTypeVal {
		if recovered := recoverMatchType(out.Arms); recovered != nil && recovered != ErrTypeVal {
			out.T = recovered
		} else {
			// Fallback: each arm's body Result type was poisoned (ErrType)
			// but the body is a single ident bound by a known sum-type
			// variant pattern. Derive the binding's type from the
			// scrutinee's NamedType args and re-attempt unification.
			// Covers the common `match r { Ok(s) -> s, Err(_) -> {…} }`
			// shape where `s`'s type the checker didn't record.
			if scrutT := out.Scrutinee.Type(); scrutT != nil && scrutT != ErrTypeVal {
				var candidate Type
				disagree := false
				for _, arm := range out.Arms {
					t := recoverVariantBindingArmType(scrutT, arm)
					if t == nil || t == ErrTypeVal {
						continue
					}
					if candidate == nil {
						candidate = t
						continue
					}
					if !typesEquivalent(candidate, t) {
						disagree = true
						break
					}
				}
				if !disagree && candidate != nil {
					out.T = candidate
				}
			}
		}
	}
	// Compile a decision tree when the arm shapes are specialisable.
	out.Tree = CompileDecisionTree(out.Scrutinee.Type(), out.Arms)
	return out
}

// recoverVariantBindingArmType derives the result type of a match arm
// whose body is `body == arm.Body.Result.(*Ident)` referring to a
// name introduced by `arm.Pattern.(*VariantPat)`, by looking up the
// payload slot in the scrutinee's NamedType args. Returns nil for
// shapes outside the supported envelope or when the scrutinee
// isn't a recognised sum type.
//
// Used as a secondary fallback by `lowerMatchExpr` when
// `recoverMatchType` can't unify because every arm's body type was
// poisoned at the checker stage (observed on `runCompile` and
// `runLirProtoLower` in `toolchain/main.osty` where
// `match fs.readToString(path) { Ok(s) -> s, Err(_) -> {…} }`
// dropped `s`'s String type through the SemanticDB→IR seam — same
// family as the trailing-stdlib-call recovery in
// `internal/ir/lower.go:bindingTypeFromAST`).
//
// Currently supports the prelude sum types `Result<T, E>` and
// `Option<T>`; non-builtin enums need their variant→payload index
// pulled from `resolve.Symbol.Decl` and stay out of scope here
// because the binding-→type pipeline for user enums runs through
// a different recovery path (`lowerLetStmt:nativeBindingType`).
// populatePatternBindingTypes seeds `bindingPatTypes` for every
// IdentPat reachable from `pat`, given the known scrutinee type
// `scrutT`. Mirrors the logic of `recoverVariantBindingArmType` but
// (1) at the *binding* level rather than the arm-body level, (2)
// for AST-side IdentPats (which are what `lowerIdent`'s fallback
// consults via `sym.Decl.(*ast.IdentPat)`), and (3) handles nested
// patterns so `Ok((a, b))` → `a`/`b` both get their tuple-element
// types. Conservative: when the scrutinee shape doesn't match the
// pattern shape (or the type info is missing) we silently leave
// the binding uncached and let the existing fallback paths kick
// in.
func (l *lowerer) populatePatternBindingTypes(pat ast.Pattern, scrutT Type) {
	if pat == nil || scrutT == nil || scrutT == ErrTypeVal {
		return
	}
	switch p := pat.(type) {
	case *ast.IdentPat:
		if p == nil {
			return
		}
		if l.bindingPatTypes == nil {
			l.bindingPatTypes = map[*ast.IdentPat]Type{}
		}
		if _, ok := l.bindingPatTypes[p]; !ok {
			l.bindingPatTypes[p] = scrutT
		}
		if l.bindingPatTypesByOffset == nil {
			l.bindingPatTypesByOffset = map[int]Type{}
		}
		off := p.Pos().Offset
		if _, ok := l.bindingPatTypesByOffset[off]; !ok {
			l.bindingPatTypesByOffset[off] = scrutT
		}
	case *ast.VariantPat:
		if p == nil {
			return
		}
		named, ok := scrutT.(*NamedType)
		if !ok || named == nil {
			return
		}
		// Result<T, E> / Option<T> / Maybe<T> ABI shapes.
		if len(p.Path) == 0 {
			return
		}
		variant := p.Path[len(p.Path)-1]
		switch named.Name {
		case "Result":
			if len(named.Args) < 2 {
				return
			}
			switch variant {
			case "Ok":
				if len(p.Args) > 0 {
					l.populatePatternBindingTypes(p.Args[0], named.Args[0])
				}
			case "Err":
				if len(p.Args) > 0 {
					l.populatePatternBindingTypes(p.Args[0], named.Args[1])
				}
			}
		case "Option", "Maybe":
			if len(named.Args) < 1 {
				return
			}
			if variant == "Some" && len(p.Args) > 0 {
				l.populatePatternBindingTypes(p.Args[0], named.Args[0])
			}
		}
	case *ast.TuplePat:
		if p == nil {
			return
		}
		if tt, ok := scrutT.(*TupleType); ok && len(tt.Elems) == len(p.Elems) {
			for i, elem := range p.Elems {
				l.populatePatternBindingTypes(elem, tt.Elems[i])
			}
		}
	}
}

func recoverVariantBindingArmType(scrutT Type, arm *MatchArm) Type {
	if arm == nil || arm.Body == nil || arm.Body.Result == nil {
		return nil
	}
	bodyIdent, ok := arm.Body.Result.(*Ident)
	if !ok {
		return nil
	}
	if len(arm.Body.Stmts) != 0 {
		// Only single-expression arm bodies — multi-statement blocks
		// belong to the existing recoverMatchType path (which already
		// understands trailing-expression yield).
		return nil
	}
	vpat, ok := arm.Pattern.(*VariantPat)
	if !ok {
		return nil
	}
	bindingIdx := -1
	for i, a := range vpat.Args {
		ip, ok := a.(*IdentPat)
		if !ok {
			continue
		}
		if ip.Name == bodyIdent.Name {
			bindingIdx = i
			break
		}
	}
	if bindingIdx < 0 {
		return nil
	}
	named, ok := scrutT.(*NamedType)
	if !ok {
		return nil
	}
	switch named.Name {
	case "Result":
		if len(named.Args) < 2 {
			return nil
		}
		switch vpat.Variant {
		case "Ok":
			if bindingIdx == 0 {
				return named.Args[0]
			}
		case "Err":
			if bindingIdx == 0 {
				return named.Args[1]
			}
		}
	case "Option":
		if len(named.Args) < 1 {
			return nil
		}
		if vpat.Variant == "Some" && bindingIdx == 0 {
			return named.Args[0]
		}
	}
	return nil
}

// recoverMatchType returns the common body type across a set of
// match arms, or ErrTypeVal when arms disagree / are not available.
// Used as a fallback when the checker didn't record a type for the
// enclosing match expression. A Block's yielded type is its Result
// expression's type (or TUnit when Result is nil).
func recoverMatchType(arms []*MatchArm) Type {
	var candidate Type
	for _, arm := range arms {
		if arm == nil || arm.Body == nil {
			continue
		}
		t := blockResultType(arm.Body)
		if t == nil || t == ErrTypeVal {
			continue
		}
		if candidate == nil {
			candidate = t
			continue
		}
		if !typesEquivalent(candidate, t) {
			return ErrTypeVal
		}
	}
	if candidate == nil {
		return ErrTypeVal
	}
	return candidate
}

// typesEquivalent is a narrow equality suitable for match-arm
// unification. It's intentionally strict: primitive kinds must match
// exactly, named types compare by (package, name, builtin) tuple.
// Structural types (tuples, optionals, fn types) recurse. Anything
// involving ErrType short-circuits to false so recovery doesn't
// silently accept a poisoned arm.
func typesEquivalent(a, b Type) bool {
	if a == nil || b == nil {
		return false
	}
	if a == ErrTypeVal || b == ErrTypeVal {
		return false
	}
	switch ax := a.(type) {
	case *PrimType:
		bx, ok := b.(*PrimType)
		return ok && ax.Kind == bx.Kind
	case *NamedType:
		bx, ok := b.(*NamedType)
		if !ok || ax.Name != bx.Name || ax.Package != bx.Package || ax.Builtin != bx.Builtin {
			return false
		}
		if len(ax.Args) != len(bx.Args) {
			return false
		}
		for i := range ax.Args {
			if !typesEquivalent(ax.Args[i], bx.Args[i]) {
				return false
			}
		}
		return true
	case *OptionalType:
		bx, ok := b.(*OptionalType)
		return ok && typesEquivalent(ax.Inner, bx.Inner)
	case *TupleType:
		bx, ok := b.(*TupleType)
		if !ok || len(ax.Elems) != len(bx.Elems) {
			return false
		}
		for i := range ax.Elems {
			if !typesEquivalent(ax.Elems[i], bx.Elems[i]) {
				return false
			}
		}
		return true
	}
	return false
}

func (l *lowerer) lowerMatchArms(arms []*ast.MatchArm) []*MatchArm {
	out := make([]*MatchArm, 0, len(arms))
	for _, arm := range arms {
		a := &MatchArm{
			Pattern: l.lowerPattern(arm.Pattern),
			SpanV:   Span{Start: posFromToken(arm.Pos()), End: posFromToken(arm.End())},
		}
		if arm.Guard != nil {
			a.Guard = l.lowerExpr(arm.Guard)
		}
		a.Body = l.lowerArmBody(arm.Body)
		out = append(out, a)
	}
	return out
}

// lowerArmBody normalises a match-arm body (expression or block) into a
// *Block so consumers see a uniform shape.
func (l *lowerer) lowerArmBody(e ast.Expr) *Block {
	if blk, ok := e.(*ast.Block); ok {
		return l.lowerBlock(blk)
	}
	lowered := l.lowerExpr(e)
	return &Block{Result: lowered, SpanV: lowered.At()}
}

// ==== Patterns ====

func (l *lowerer) lowerPattern(p ast.Pattern) Pattern {
	if p == nil {
		return nil
	}
	switch p := p.(type) {
	case *ast.WildcardPat:
		return &WildPat{SpanV: nodeSpan(p)}
	case *ast.IdentPat:
		return &IdentPat{Name: p.Name, SpanV: nodeSpan(p)}
	case *ast.LiteralPat:
		var val Expr
		if p.Literal != nil {
			val = l.lowerExpr(p.Literal)
		}
		return &LitPat{Value: val, SpanV: nodeSpan(p)}
	case *ast.TuplePat:
		out := &TuplePat{SpanV: nodeSpan(p)}
		for _, e := range p.Elems {
			out.Elems = append(out.Elems, l.lowerPattern(e))
		}
		return out
	case *ast.StructPat:
		out := &StructPat{Rest: p.Rest, SpanV: nodeSpan(p)}
		if len(p.Type) > 0 {
			out.TypeName = p.Type[len(p.Type)-1]
		}
		for _, f := range p.Fields {
			field := StructPatField{
				Name: f.Name,
				SpanV: Span{
					Start: posFromToken(f.Pos()),
					End:   posFromToken(f.End()),
				},
			}
			if f.Pattern != nil {
				field.Pattern = l.lowerPattern(f.Pattern)
			}
			out.Fields = append(out.Fields, field)
		}
		return out
	case *ast.VariantPat:
		out := &VariantPat{SpanV: nodeSpan(p)}
		if n := len(p.Path); n >= 1 {
			out.Variant = p.Path[n-1]
			if n >= 2 {
				out.Enum = p.Path[n-2]
			}
		}
		for _, a := range p.Args {
			out.Args = append(out.Args, l.lowerPattern(a))
		}
		return out
	case *ast.RangePat:
		out := &RangePat{Inclusive: p.Inclusive, SpanV: nodeSpan(p)}
		if p.Start != nil {
			out.Low = l.lowerExpr(p.Start)
		}
		if p.Stop != nil {
			out.High = l.lowerExpr(p.Stop)
		}
		return out
	case *ast.OrPat:
		out := &OrPat{SpanV: nodeSpan(p)}
		for _, a := range p.Alts {
			out.Alts = append(out.Alts, l.lowerPattern(a))
		}
		return out
	case *ast.BindingPat:
		return &BindingPat{
			Name:    p.Name,
			Pattern: l.lowerPattern(p.Pattern),
			SpanV:   nodeSpan(p),
		}
	}
	l.note("unsupported pattern %T at %v", p, p.Pos())
	return &ErrorPat{Note: fmt.Sprintf("%T", p), SpanV: nodeSpan(p)}
}
