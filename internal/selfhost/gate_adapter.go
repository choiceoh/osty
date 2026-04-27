package selfhost

import (
	"strings"

	"github.com/osty/osty/internal/diag"
)

// Until internal/selfhost/generated.go regenerates cleanly with
// toolchain/check_gates.osty folded in, synthesize the subset of gate
// diagnostics that native package/file callers already rely on. The merge
// helper dedupes against newer generated bundles, so this stays safe once the
// selfhost output catches up.

// IntrinsicBodyDiagsForSource runs the arena-direct `#[intrinsic]`
// body-shape gate (LANG_SPEC §19.6 / E0773) over src and returns the
// produced diagnostics already stamped with path. Used by the Go
// check.File / check.Package drivers as the single source of truth for
// E0773 — the legacy Go-side `runIntrinsicBodyChecks` walker was
// retired once this public entry landed.
//
// Returns nil when parse fails fatally (the parser's own error
// diagnostics surface separately on the main check path). Runs
// astbridge-free: the FrontendRun created here never calls File(), so
// the AstbridgeLowerCount counter stays at 0.
func IntrinsicBodyDiagsForSource(src []byte, path string) []*diag.Diagnostic {
	if len(src) == 0 {
		return nil
	}
	run := Run(src)
	if run == nil || run.parser == nil || run.parser.arena == nil {
		return nil
	}
	if selfhostRunHasErrorDiagnostics(run) {
		return nil
	}
	records := selfhostIntrinsicBodyDiagnosticsFromArena(run.parser.arena, run.rt, run.stream, 0, path)
	return CheckDiagnosticsAsDiag(src, records)
}

func selfhostAppendIntrinsicBodyGateForSource(result *CheckResult, src []byte) {
	if result == nil || len(src) == 0 {
		return
	}
	run := Run(src)
	if selfhostRunHasErrorDiagnostics(run) || run.parser == nil || run.parser.arena == nil {
		return
	}
	records := selfhostIntrinsicBodyDiagnosticsFromArena(run.parser.arena, run.rt, run.stream, 0, "")
	selfhostMergeDiagnosticRecords(result, records...)
}

func selfhostAppendPureGateForSource(result *CheckResult, src []byte) {
	if result == nil || len(src) == 0 {
		return
	}
	run := Run(src)
	if selfhostRunHasErrorDiagnostics(run) || run.parser == nil || run.parser.arena == nil {
		return
	}
	records := selfhostPureDiagnosticsFromArena(run.parser.arena, run.rt, run.stream, 0, "")
	selfhostMergeDiagnosticRecords(result, records...)
}

// selfhostAppendIntrinsicBodyGateForRun is the FrontendRun-aware
// sibling of selfhostAppendIntrinsicBodyGateForSource. It walks the
// run's AstArena directly via selfhostIntrinsicBodyDiagnosticsFromArena
// instead of forcing an astbridge *ast.File lowering, so
// CheckStructuredFromRun produces zero astbridge bumps on the happy
// path.
func selfhostAppendIntrinsicBodyGateForRun(result *CheckResult, run *FrontendRun) {
	if result == nil || run == nil {
		return
	}
	if selfhostRunHasErrorDiagnostics(run) || run.parser == nil || run.parser.arena == nil {
		return
	}
	records := selfhostIntrinsicBodyDiagnosticsFromArena(run.parser.arena, run.rt, run.stream, 0, "")
	selfhostMergeDiagnosticRecords(result, records...)
}

func selfhostAppendPureGateForRun(result *CheckResult, run *FrontendRun) {
	if result == nil || run == nil {
		return
	}
	if selfhostRunHasErrorDiagnostics(run) || run.parser == nil || run.parser.arena == nil {
		return
	}
	records := selfhostPureDiagnosticsFromArena(run.parser.arena, run.rt, run.stream, 0, "")
	selfhostMergeDiagnosticRecords(result, records...)
}

// selfhostIntrinsicBodyDiagnosticsFromArena is the arena-native
// sibling of selfhostIntrinsicBodyDiagnostics. It iterates the
// self-host parser's AstArena, flagging every `#[intrinsic]` function
// whose body is non-empty (LANG_SPEC §19.6). Body offsets are mapped
// from token indices back to byte offsets via checkNodeOffsets using
// the caller-supplied runeTable + FrontLexStream, matching exactly
// what the *ast.File walker produces.
func selfhostIntrinsicBodyDiagnosticsFromArena(arena *AstArena, rt runeTable, stream *FrontLexStream, base int, path string) []CheckDiagnosticRecord {
	if arena == nil {
		return nil
	}
	var out []CheckDiagnosticRecord
	for _, declIdx := range arena.decls {
		selfhostGateWalkArenaDecl(arena, declIdx, rt, stream, base, path, &out)
	}
	return out
}

func selfhostGateWalkArenaDecl(arena *AstArena, idx int, rt runeTable, stream *FrontLexStream, base int, path string, out *[]CheckDiagnosticRecord) {
	if out == nil || idx < 0 || idx >= len(arena.nodes) {
		return
	}
	n := arena.nodes[idx]
	if n == nil {
		return
	}
	switch n.kind.(type) {
	case *AstNodeKind_AstNFnDecl:
		if rec := selfhostGateArenaFnRecord(arena, n, rt, stream, base, path); rec != nil {
			*out = append(*out, *rec)
		}
	case *AstNodeKind_AstNStructDecl, *AstNodeKind_AstNEnumDecl:
		for _, childIdx := range n.children {
			if childIdx < 0 || childIdx >= len(arena.nodes) {
				continue
			}
			child := arena.nodes[childIdx]
			if child == nil {
				continue
			}
			if _, ok := child.kind.(*AstNodeKind_AstNFnDecl); !ok {
				continue
			}
			if rec := selfhostGateArenaFnRecord(arena, child, rt, stream, base, path); rec != nil {
				*out = append(*out, *rec)
			}
		}
	case *AstNodeKind_AstNUseDecl:
		for _, childIdx := range n.children {
			selfhostGateWalkArenaDecl(arena, childIdx, rt, stream, base, path, out)
		}
	}
}

func selfhostGateArenaFnRecord(arena *AstArena, fn *AstNode, rt runeTable, stream *FrontLexStream, base int, path string) *CheckDiagnosticRecord {
	if fn == nil {
		return nil
	}
	if !selfhostArenaHasAnnotation(arena, fn.extra, "intrinsic") {
		return nil
	}
	if fn.right < 0 || fn.right >= len(arena.nodes) {
		return nil
	}
	body := arena.nodes[fn.right]
	if body == nil {
		return nil
	}
	if _, ok := body.kind.(*AstNodeKind_AstNBlock); !ok {
		return nil
	}
	if len(body.children) == 0 {
		return nil
	}
	bodyStart, bodyEnd := checkNodeOffsets(rt, stream, body.start, body.end)
	start := base + bodyStart
	end := base + bodyEnd
	if end < start {
		end = start
	}
	return &CheckDiagnosticRecord{
		Code:     diag.CodeIntrinsicNonEmptyBody,
		Severity: "error",
		Message:  "`#[intrinsic]` function `" + fn.text + "` must have an empty body",
		Start:    start,
		End:      end,
		File:     path,
		Notes: []string{
			"LANG_SPEC §19.6: intrinsic implementations are supplied by the lowering layer; the source body is ignored",
			"hint: keep the signature and drop the body, or use `{}`",
		},
	}
}

// selfhostArenaHasAnnotation resolves the packed annotation extra
// slot produced by opPackAnnotations: -1 for none, a single
// annotation node index when count==1, or a synthetic "__group"
// Annotation node whose children list holds the individual
// annotation indices when count>1.
func selfhostArenaHasAnnotation(arena *AstArena, extra int, name string) bool {
	if extra < 0 || extra >= len(arena.nodes) {
		return false
	}
	n := arena.nodes[extra]
	if n == nil {
		return false
	}
	if _, ok := n.kind.(*AstNodeKind_AstNAnnotation); !ok {
		return false
	}
	if n.text == "__group" {
		for _, childIdx := range n.children {
			if childIdx < 0 || childIdx >= len(arena.nodes) {
				continue
			}
			child := arena.nodes[childIdx]
			if child == nil {
				continue
			}
			if _, ok := child.kind.(*AstNodeKind_AstNAnnotation); !ok {
				continue
			}
			if child.text == name {
				return true
			}
		}
		return false
	}
	return n.text == name
}

func selfhostPureDiagnosticsFromArena(arena *AstArena, rt runeTable, stream *FrontLexStream, base int, path string) []CheckDiagnosticRecord {
	if arena == nil {
		return nil
	}
	pureNames := selfhostCollectAnnotatedFnNames(arena, "pure")
	if len(pureNames) == 0 {
		return nil
	}
	var out []CheckDiagnosticRecord
	for _, declIdx := range arena.decls {
		selfhostPureWalkDecl(arena, declIdx, rt, stream, base, path, pureNames, &out)
	}
	return out
}

func selfhostCollectAnnotatedFnNames(arena *AstArena, annotation string) map[string]struct{} {
	out := map[string]struct{}{}
	if arena == nil {
		return out
	}
	for _, declIdx := range arena.decls {
		if declIdx < 0 || declIdx >= len(arena.nodes) {
			continue
		}
		n := arena.nodes[declIdx]
		if n == nil {
			continue
		}
		switch n.kind.(type) {
		case *AstNodeKind_AstNFnDecl:
			if n.right >= 0 && selfhostArenaHasAnnotation(arena, n.extra, annotation) {
				out[n.text] = struct{}{}
			}
		case *AstNodeKind_AstNStructDecl, *AstNodeKind_AstNEnumDecl:
			for _, childIdx := range n.children {
				if childIdx < 0 || childIdx >= len(arena.nodes) {
					continue
				}
				child := arena.nodes[childIdx]
				if child == nil {
					continue
				}
				if _, ok := child.kind.(*AstNodeKind_AstNFnDecl); ok && child.right >= 0 && selfhostArenaHasAnnotation(arena, child.extra, annotation) {
					out[child.text] = struct{}{}
				}
			}
		}
	}
	return out
}

func selfhostPureWalkDecl(arena *AstArena, idx int, rt runeTable, stream *FrontLexStream, base int, path string, pureNames map[string]struct{}, out *[]CheckDiagnosticRecord) {
	if out == nil || idx < 0 || idx >= len(arena.nodes) {
		return
	}
	n := arena.nodes[idx]
	if n == nil {
		return
	}
	switch n.kind.(type) {
	case *AstNodeKind_AstNFnDecl:
		selfhostPureCheckFn(arena, n, rt, stream, base, path, pureNames, out)
	case *AstNodeKind_AstNStructDecl, *AstNodeKind_AstNEnumDecl:
		for _, childIdx := range n.children {
			if childIdx < 0 || childIdx >= len(arena.nodes) {
				continue
			}
			child := arena.nodes[childIdx]
			if child == nil {
				continue
			}
			if _, ok := child.kind.(*AstNodeKind_AstNFnDecl); ok {
				selfhostPureCheckFn(arena, child, rt, stream, base, path, pureNames, out)
			}
		}
	}
}

func selfhostPureCheckFn(arena *AstArena, fn *AstNode, rt runeTable, stream *FrontLexStream, base int, path string, pureNames map[string]struct{}, out *[]CheckDiagnosticRecord) {
	if fn == nil || fn.right < 0 || !selfhostArenaHasAnnotation(arena, fn.extra, "pure") {
		return
	}
	locals := map[string]struct{}{}
	for _, paramIdx := range fn.children {
		if paramIdx < 0 || paramIdx >= len(arena.nodes) {
			continue
		}
		param := arena.nodes[paramIdx]
		if param == nil {
			continue
		}
		if _, ok := param.kind.(*AstNodeKind_AstNParam); ok && param.text != "" && param.text != "self" {
			locals[param.text] = struct{}{}
		}
	}
	selfhostPureWalkExpr(arena, fn.right, fn.text, pureNames, locals, rt, stream, base, path, out)
}

func selfhostPureWalkExpr(arena *AstArena, idx int, fnName string, pureNames map[string]struct{}, locals map[string]struct{}, rt runeTable, stream *FrontLexStream, base int, path string, out *[]CheckDiagnosticRecord) {
	if idx < 0 || idx >= len(arena.nodes) {
		return
	}
	n := arena.nodes[idx]
	if n == nil {
		return
	}
	switch n.kind.(type) {
	case *AstNodeKind_AstNList:
		selfhostPureAppendRecord(out, rt, stream, base, path, fnName, "allocate a list literal", "remove `#[pure]`, pass in precomputed data, or rewrite without managed allocation", n)
		for _, childIdx := range n.children {
			selfhostPureWalkExpr(arena, childIdx, fnName, pureNames, locals, rt, stream, base, path, out)
		}
	case *AstNodeKind_AstNMap:
		selfhostPureAppendRecord(out, rt, stream, base, path, fnName, "allocate a map literal", "remove `#[pure]`, pass in precomputed data, or rewrite without managed allocation", n)
		for _, childIdx := range n.children {
			selfhostPureWalkExpr(arena, childIdx, fnName, pureNames, locals, rt, stream, base, path, out)
		}
		for _, childIdx := range n.children2 {
			selfhostPureWalkExpr(arena, childIdx, fnName, pureNames, locals, rt, stream, base, path, out)
		}
	case *AstNodeKind_AstNStructLit:
		selfhostPureAppendRecord(out, rt, stream, base, path, fnName, "allocate a struct literal", "return scalar data or drop `#[pure]` until the value construction can be proven allocation-free", n)
		for _, fieldIdx := range n.children {
			if fieldIdx < 0 || fieldIdx >= len(arena.nodes) || arena.nodes[fieldIdx] == nil {
				continue
			}
			selfhostPureWalkExpr(arena, arena.nodes[fieldIdx].left, fnName, pureNames, locals, rt, stream, base, path, out)
		}
		selfhostPureWalkExpr(arena, n.right, fnName, pureNames, locals, rt, stream, base, path, out)
	case *AstNodeKind_AstNClosure:
		selfhostPureAppendRecord(out, rt, stream, base, path, fnName, "allocate a closure", "closures capture an environment; use a direct `#[pure]` helper function instead", n)
	case *AstNodeKind_AstNStringLit:
		if isAllocatingStringText(n.text) {
			selfhostPureAppendRecord(out, rt, stream, base, path, fnName, classifyAllocatingString(n.text), "only plain `\"...\"` literals are accepted in `#[pure]` bodies", n)
		}
	case *AstNodeKind_AstNCall:
		if !selfhostPureCalleeAllowed(arena, n.left, pureNames) {
			what := "call a function that is not `#[pure]`"
			if selfhostPureCalleeLooksLikeIO(arena, n.left) {
				what = "perform I/O"
			}
			selfhostPureAppendRecord(out, rt, stream, base, path, fnName, what, "mark the callee `#[pure]` only if it is side-effect free, or remove `#[pure]` from this function", n)
		}
		selfhostPureWalkExpr(arena, n.left, fnName, pureNames, locals, rt, stream, base, path, out)
		for _, argIdx := range n.children {
			selfhostPureWalkExpr(arena, argIdx, fnName, pureNames, locals, rt, stream, base, path, out)
		}
	case *AstNodeKind_AstNAssign:
		if !selfhostPureAssignTargetLocal(arena, n.left, locals) {
			selfhostPureAppendRecord(out, rt, stream, base, path, fnName, "write non-local state", "only assignment to local bindings declared inside the `#[pure]` function is allowed", n)
		}
		selfhostPureWalkExpr(arena, n.left, fnName, pureNames, locals, rt, stream, base, path, out)
		selfhostPureWalkExpr(arena, n.right, fnName, pureNames, locals, rt, stream, base, path, out)
	case *AstNodeKind_AstNChanSend:
		selfhostPureAppendRecord(out, rt, stream, base, path, fnName, "send on a channel", "channel sends are observable side effects; remove `#[pure]`", n)
		selfhostPureWalkExpr(arena, n.left, fnName, pureNames, locals, rt, stream, base, path, out)
		selfhostPureWalkExpr(arena, n.right, fnName, pureNames, locals, rt, stream, base, path, out)
	case *AstNodeKind_AstNDefer:
		selfhostPureAppendRecord(out, rt, stream, base, path, fnName, "register a deferred effect", "defer runs code after the function returns and is not allowed in `#[pure]` bodies", n)
		selfhostPureWalkExpr(arena, n.left, fnName, pureNames, locals, rt, stream, base, path, out)
	case *AstNodeKind_AstNLet:
		selfhostPureWalkExpr(arena, n.right, fnName, pureNames, locals, rt, stream, base, path, out)
	case *AstNodeKind_AstNBlock:
		scoped := selfhostCopyStringSet(locals)
		for _, stmtIdx := range n.children {
			selfhostPureWalkExpr(arena, stmtIdx, fnName, pureNames, scoped, rt, stream, base, path, out)
			if stmtIdx >= 0 && stmtIdx < len(arena.nodes) && arena.nodes[stmtIdx] != nil {
				if _, ok := arena.nodes[stmtIdx].kind.(*AstNodeKind_AstNLet); ok {
					selfhostPureCollectPatternBindings(arena, arena.nodes[stmtIdx].left, scoped)
				}
			}
		}
	case *AstNodeKind_AstNExprStmt, *AstNodeKind_AstNReturn, *AstNodeKind_AstNQuestion, *AstNodeKind_AstNField, *AstNodeKind_AstNParen, *AstNodeKind_AstNTurbofish:
		selfhostPureWalkExpr(arena, n.left, fnName, pureNames, locals, rt, stream, base, path, out)
	case *AstNodeKind_AstNFor:
		loopLocals := selfhostCopyStringSet(locals)
		selfhostPureCollectPatternBindings(arena, n.left, loopLocals)
		selfhostPureWalkExpr(arena, n.left, fnName, pureNames, locals, rt, stream, base, path, out)
		if len(n.children) >= 2 {
			selfhostPureWalkExpr(arena, n.children[1], fnName, pureNames, locals, rt, stream, base, path, out)
		}
		selfhostPureWalkExpr(arena, n.right, fnName, pureNames, loopLocals, rt, stream, base, path, out)
	case *AstNodeKind_AstNIf:
		selfhostPureWalkExpr(arena, n.left, fnName, pureNames, locals, rt, stream, base, path, out)
		selfhostPureWalkExpr(arena, n.right, fnName, pureNames, locals, rt, stream, base, path, out)
		if len(n.children) > 0 {
			selfhostPureWalkExpr(arena, n.children[0], fnName, pureNames, locals, rt, stream, base, path, out)
		}
	case *AstNodeKind_AstNMatch:
		selfhostPureWalkExpr(arena, n.left, fnName, pureNames, locals, rt, stream, base, path, out)
		for _, armIdx := range n.children {
			if armIdx < 0 || armIdx >= len(arena.nodes) || arena.nodes[armIdx] == nil {
				continue
			}
			arm := arena.nodes[armIdx]
			armLocals := selfhostCopyStringSet(locals)
			selfhostPureCollectPatternBindings(arena, arm.left, armLocals)
			if len(arm.children) > 0 {
				selfhostPureWalkExpr(arena, arm.children[0], fnName, pureNames, armLocals, rt, stream, base, path, out)
			}
			selfhostPureWalkExpr(arena, arm.right, fnName, pureNames, armLocals, rt, stream, base, path, out)
		}
	case *AstNodeKind_AstNUnary:
		selfhostPureWalkExpr(arena, n.left, fnName, pureNames, locals, rt, stream, base, path, out)
	case *AstNodeKind_AstNBinary, *AstNodeKind_AstNIndex, *AstNodeKind_AstNRange:
		selfhostPureWalkExpr(arena, n.left, fnName, pureNames, locals, rt, stream, base, path, out)
		selfhostPureWalkExpr(arena, n.right, fnName, pureNames, locals, rt, stream, base, path, out)
	case *AstNodeKind_AstNTuple:
		for _, childIdx := range n.children {
			selfhostPureWalkExpr(arena, childIdx, fnName, pureNames, locals, rt, stream, base, path, out)
		}
	}
}

func selfhostPureAppendRecord(out *[]CheckDiagnosticRecord, rt runeTable, stream *FrontLexStream, base int, path, fnName, what, hint string, n *AstNode) {
	if out == nil || n == nil {
		return
	}
	startOffset, endOffset := checkNodeOffsets(rt, stream, n.start, n.end)
	start := base + startOffset
	end := base + endOffset
	if end < start {
		end = start
	}
	*out = append(*out, CheckDiagnosticRecord{
		Code:     diag.CodePureViolation,
		Severity: "error",
		Message:  "`#[pure]` function `" + fnName + "` cannot " + what,
		Start:    start,
		End:      end,
		File:     path,
		Notes: []string{
			"LANG_SPEC v0.6 A13: `#[pure]` lowers to LLVM `readnone`, so the body must not write non-local state, perform I/O, allocate, or call impure functions",
			"hint: " + hint,
		},
	})
}

func selfhostPureAssignTargetLocal(arena *AstArena, targetIdx int, locals map[string]struct{}) bool {
	if arena == nil || targetIdx < 0 || targetIdx >= len(arena.nodes) || arena.nodes[targetIdx] == nil {
		return false
	}
	target := arena.nodes[targetIdx]
	if _, ok := target.kind.(*AstNodeKind_AstNIdent); !ok {
		return false
	}
	_, ok := locals[target.text]
	return ok
}

func selfhostPureCalleeAllowed(arena *AstArena, calleeIdx int, pureNames map[string]struct{}) bool {
	if arena == nil || calleeIdx < 0 || calleeIdx >= len(arena.nodes) || arena.nodes[calleeIdx] == nil {
		return false
	}
	callee := arena.nodes[calleeIdx]
	switch callee.kind.(type) {
	case *AstNodeKind_AstNIdent, *AstNodeKind_AstNField:
		_, ok := pureNames[callee.text]
		return ok
	case *AstNodeKind_AstNTurbofish:
		return selfhostPureCalleeAllowed(arena, callee.left, pureNames)
	default:
		return false
	}
}

func selfhostPureCalleeLooksLikeIO(arena *AstArena, calleeIdx int) bool {
	switch selfhostPureCalleeLastName(arena, calleeIdx) {
	case "print", "println", "eprint", "eprintln", "read", "readAll", "readToString", "write", "writeAll", "open", "create":
		return true
	default:
		return false
	}
}

func selfhostPureCalleeLastName(arena *AstArena, calleeIdx int) string {
	if arena == nil || calleeIdx < 0 || calleeIdx >= len(arena.nodes) || arena.nodes[calleeIdx] == nil {
		return ""
	}
	callee := arena.nodes[calleeIdx]
	switch callee.kind.(type) {
	case *AstNodeKind_AstNIdent, *AstNodeKind_AstNField:
		return callee.text
	case *AstNodeKind_AstNTurbofish:
		return selfhostPureCalleeLastName(arena, callee.left)
	default:
		return ""
	}
}

func selfhostCopyStringSet(in map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func selfhostPureCollectPatternBindings(arena *AstArena, patIdx int, out map[string]struct{}) {
	if arena == nil || patIdx < 0 || patIdx >= len(arena.nodes) || arena.nodes[patIdx] == nil {
		return
	}
	pat := arena.nodes[patIdx]
	if _, ok := pat.kind.(*AstNodeKind_AstNPattern); !ok {
		return
	}
	if pat.extra == astPatternIdentKind() || pat.extra == astPatternBindingKind() {
		if pat.text != "" && pat.text != "_" {
			out[pat.text] = struct{}{}
		}
	}
	selfhostPureCollectPatternBindings(arena, pat.left, out)
	selfhostPureCollectPatternBindings(arena, pat.right, out)
	for _, childIdx := range pat.children {
		selfhostPureCollectPatternBindings(arena, childIdx, out)
	}
}

func selfhostAppendIntrinsicBodyGateForPackage(result *CheckResult, input PackageCheckInput) {
	if result == nil || len(input.Files) == 0 {
		return
	}
	var records []CheckDiagnosticRecord
	for _, file := range input.Files {
		if len(file.Source) == 0 {
			continue
		}
		run := Run(file.Source)
		if selfhostRunHasErrorDiagnostics(run) || run.parser == nil || run.parser.arena == nil {
			continue
		}
		records = append(records, selfhostIntrinsicBodyDiagnosticsFromArena(run.parser.arena, run.rt, run.stream, file.Base, file.Path)...)
	}
	selfhostMergeDiagnosticRecords(result, records...)
}

func selfhostAppendPureGateForPackage(result *CheckResult, input PackageCheckInput) {
	if result == nil || len(input.Files) == 0 {
		return
	}
	var records []CheckDiagnosticRecord
	for _, file := range input.Files {
		if len(file.Source) == 0 {
			continue
		}
		run := Run(file.Source)
		if selfhostRunHasErrorDiagnostics(run) || run.parser == nil || run.parser.arena == nil {
			continue
		}
		records = append(records, selfhostPureDiagnosticsFromArena(run.parser.arena, run.rt, run.stream, file.Base, file.Path)...)
	}
	selfhostMergeDiagnosticRecords(result, records...)
}

func selfhostRunHasErrorDiagnostics(run *FrontendRun) bool {
	if run == nil {
		return false
	}
	if run.stream != nil && len(run.stream.diagnostics) > 0 {
		return true
	}
	return run.parser != nil && run.parser.arena != nil && len(run.parser.arena.errors) > 0
}

func selfhostHasErrorDiagnostics(diags []*diag.Diagnostic) bool {
	for _, d := range diags {
		if d != nil && d.Severity == diag.Error {
			return true
		}
	}
	return false
}

func selfhostMergeDiagnosticRecords(result *CheckResult, records ...CheckDiagnosticRecord) {
	if result == nil || len(records) == 0 {
		return
	}
	for _, record := range records {
		if idx := selfhostFindDiagnosticRecord(result.Diagnostics, record); idx >= 0 {
			existing := &result.Diagnostics[idx]
			if existing.File == "" {
				existing.File = record.File
			}
			existing.Notes = selfhostMergeNotes(existing.Notes, record.Notes)
			continue
		}
		result.Diagnostics = append(result.Diagnostics, record)
		selfhostTallyDiagnosticRecord(&result.Summary, record)
	}
}

func selfhostFindDiagnosticRecord(records []CheckDiagnosticRecord, want CheckDiagnosticRecord) int {
	for i, record := range records {
		if record.Code != want.Code || record.Severity != want.Severity || record.Message != want.Message {
			continue
		}
		if record.Start != want.Start || record.End != want.End {
			continue
		}
		return i
	}
	return -1
}

func selfhostMergeNotes(existing []string, extra []string) []string {
	if len(extra) == 0 {
		return existing
	}
	seen := make(map[string]struct{}, len(existing))
	for _, note := range existing {
		seen[note] = struct{}{}
	}
	for _, note := range extra {
		if _, ok := seen[note]; ok {
			continue
		}
		existing = append(existing, note)
		seen[note] = struct{}{}
	}
	return existing
}

func selfhostTallyDiagnosticRecord(summary *CheckSummary, record CheckDiagnosticRecord) {
	if summary == nil || !strings.EqualFold(record.Severity, "error") {
		return
	}
	summary.Errors++
	ctx := strings.TrimSpace(record.Code)
	if ctx == "" {
		ctx = "error"
	}
	if summary.ErrorsByContext == nil {
		summary.ErrorsByContext = map[string]int{}
	}
	summary.ErrorsByContext[ctx]++
	detail := strings.TrimSpace(record.Message)
	if detail == "" {
		return
	}
	if summary.ErrorDetails == nil {
		summary.ErrorDetails = map[string]map[string]int{}
	}
	if summary.ErrorDetails[ctx] == nil {
		summary.ErrorDetails[ctx] = map[string]int{}
	}
	summary.ErrorDetails[ctx][detail]++
}
