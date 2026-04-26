package airepair

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/canonical"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/lexer"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/repair"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
	"github.com/osty/osty/internal/token"
	"github.com/osty/osty/internal/types"
)

const semanticIndentStep = "    "

type semanticEdit struct {
	start  int
	end    int
	text   string
	change repair.Change
}

func diagnosticSemanticSource(src []byte, diags []*diag.Diagnostic) repair.Result {
	if !wantsSemanticRepair(src, diags) {
		return repair.Result{Source: src}
	}

	current := append([]byte(nil), src...)
	var changes []repair.Change

	if out, rewritten, ok := rewriteEnumerateLoopsForChecker(current); ok {
		current = out
		changes = append(changes, rewritten...)
	}
	if out, rewritten, ok := rewriteSemanticAppendLines(current); ok {
		current = out
		changes = append(changes, rewritten...)
	}
	if out, rewritten, ok := rewriteLengthProperties(current); ok {
		current = out
		changes = append(changes, rewritten...)
	}
	if out, rewritten, ok := rewriteBuiltinLenCalls(current); ok {
		current = out
		changes = append(changes, rewritten...)
	}

	if len(changes) == 0 {
		return repair.Result{Source: src}
	}
	return repair.Result{
		Source:  current,
		Changes: changes,
	}
}

func wantsSemanticRepair(src []byte, diags []*diag.Diagnostic) bool {
	if !bytes.Contains(src, []byte(".enumerate()")) &&
		!bytes.Contains(src, []byte("append(")) &&
		!bytes.Contains(src, []byte("len(")) &&
		!bytes.Contains(src, []byte(".length")) {
		return false
	}
	for _, d := range diags {
		if d != nil && d.Severity == diag.Error {
			return true
		}
	}
	return false
}

func rewriteEnumerateLoopsForChecker(src []byte) ([]byte, []repair.Change, bool) {
	lines := splitSourceLines(src)
	if len(lines) == 0 {
		return src, nil, false
	}

	var (
		out     strings.Builder
		changes []repair.Change
		changed bool
		counter int
	)

	for _, line := range lines {
		if line.trimmed == "" || isIgnorablePythonLine(line.trimmed) {
			out.WriteString(line.raw)
			continue
		}

		rewritten, changeSet, ok := rewriteEnumerateLoopHeader(line, &counter)
		if !ok {
			out.WriteString(line.raw)
			continue
		}

		out.WriteString(rewritten)
		if line.hasNewline && !strings.HasSuffix(rewritten, "\n") {
			out.WriteByte('\n')
		}
		changes = append(changes, changeSet...)
		changed = true
	}

	if !changed {
		return src, nil, false
	}
	return []byte(out.String()), changes, true
}

func rewriteEnumerateLoopHeader(line sourceLine, counter *int) (string, []repair.Change, bool) {
	trimmed := line.trimmed
	if !strings.HasPrefix(trimmed, "for ") || !strings.HasSuffix(trimmed, "{") {
		return "", nil, false
	}
	body := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "for "), "{"))
	inIdx := strings.Index(body, " in ")
	if inIdx <= 0 {
		return "", nil, false
	}

	lhs := strings.TrimSpace(body[:inIdx])
	rhs := strings.TrimSpace(body[inIdx+4:])
	if !strings.HasSuffix(rhs, ".enumerate()") {
		return "", nil, false
	}
	iterExpr := strings.TrimSpace(strings.TrimSuffix(rhs, ".enumerate()"))
	if iterExpr == "" {
		return "", nil, false
	}

	if strings.HasPrefix(lhs, "(") && strings.HasSuffix(lhs, ")") {
		lhs = strings.TrimSpace(lhs[1 : len(lhs)-1])
	}
	parts := splitTopLevelComma(lhs)
	if len(parts) != 2 {
		return "", nil, false
	}

	indexBinding := strings.TrimSpace(parts[0])
	valueBinding := strings.TrimSpace(parts[1])
	if valueBinding == "" {
		return "", nil, false
	}

	indexVar := indexBinding
	switch {
	case indexBinding == "_":
		indexVar = fmt.Sprintf("_osty_index%d", *counter)
	case isSimpleIdentifierBinding(indexBinding):
		// use the original binding directly in the rewritten loop header.
	default:
		return "", nil, false
	}

	next := *counter
	*counter = next + 1
	iterTemp := fmt.Sprintf("_osty_enumerate%d", next)

	bodyIndent := line.indent + semanticIndentStep
	var out strings.Builder
	out.WriteString(line.indent)
	out.WriteString("let ")
	out.WriteString(iterTemp)
	out.WriteString(" = ")
	out.WriteString(iterExpr)
	out.WriteByte('\n')
	out.WriteString(line.indent)
	out.WriteString("for ")
	out.WriteString(indexVar)
	out.WriteString(" in 0..")
	out.WriteString(iterTemp)
	out.WriteString(".len() {\n")
	out.WriteString(bodyIndent)
	out.WriteString("let ")
	out.WriteString(valueBinding)
	out.WriteString(" = ")
	out.WriteString(iterTemp)
	out.WriteByte('[')
	out.WriteString(indexVar)
	out.WriteByte(']')

	return out.String(), []repair.Change{
		{
			Kind:    "enumerate_index_loop",
			Message: "replace `.enumerate()` tuple loop with an indexed Osty loop the native checker can validate",
			Pos: token.Pos{
				Offset: line.start + len(line.indent),
				Line:   line.lineNo,
				Column: len([]rune(line.indent)) + 1,
			},
		},
	}, true
}

func rewriteSemanticAppendLines(src []byte) ([]byte, []repair.Change, bool) {
	lines := splitSourceLines(src)
	if len(lines) == 0 {
		return src, nil, false
	}

	var (
		out     strings.Builder
		changes []repair.Change
		changed bool
	)

	for _, line := range lines {
		if line.trimmed == "" || isIgnorablePythonLine(line.trimmed) {
			out.WriteString(line.raw)
			continue
		}

		rewritten, changeSet, ok := rewriteAppendLine(line)
		if !ok {
			out.WriteString(line.raw)
			continue
		}

		out.WriteString(rewritten)
		if line.hasNewline && !strings.HasSuffix(rewritten, "\n") {
			out.WriteByte('\n')
		}
		changes = append(changes, changeSet...)
		changed = true
	}

	if !changed {
		return src, nil, false
	}
	return []byte(out.String()), changes, true
}

func rewriteAppendLine(line sourceLine) (string, []repair.Change, bool) {
	trimmed := line.trimmed
	if !strings.Contains(trimmed, "append(") {
		return "", nil, false
	}

	if rewritten, ok := rewriteLetAppendLine(line); ok {
		return rewritten, []repair.Change{{
			Kind:    "builtin_append_call",
			Message: "replace foreign `append(...)` helper with Osty list mutation",
			Pos: token.Pos{
				Offset: line.start + len(line.indent),
				Line:   line.lineNo,
				Column: len([]rune(line.indent)) + 1,
			},
		}}, true
	}
	if rewritten, ok := rewriteSelfAssignAppendLine(line); ok {
		return rewritten, []repair.Change{{
			Kind:    "builtin_append_call",
			Message: "replace foreign `append(...)` helper with Osty list mutation",
			Pos: token.Pos{
				Offset: line.start + len(line.indent),
				Line:   line.lineNo,
				Column: len([]rune(line.indent)) + 1,
			},
		}}, true
	}
	if rewritten, ok := rewriteStandaloneAppendLine(line); ok {
		return rewritten, []repair.Change{{
			Kind:    "builtin_append_call",
			Message: "replace foreign `append(...)` helper with Osty list mutation",
			Pos: token.Pos{
				Offset: line.start + len(line.indent),
				Line:   line.lineNo,
				Column: len([]rune(line.indent)) + 1,
			},
		}}, true
	}
	return "", nil, false
}

func rewriteLetAppendLine(line sourceLine) (string, bool) {
	trimmed := line.trimmed
	if !strings.HasPrefix(trimmed, "let ") {
		return "", false
	}

	rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "let "))
	if strings.HasPrefix(rest, "mut ") {
		rest = strings.TrimSpace(strings.TrimPrefix(rest, "mut "))
	}
	eqIdx := strings.Index(rest, " = ")
	if eqIdx <= 0 {
		return "", false
	}

	name := strings.TrimSpace(rest[:eqIdx])
	rhs := strings.TrimSpace(rest[eqIdx+3:])
	if !isSimpleIdentifierBinding(name) {
		return "", false
	}

	base, item, ok := parseAppendCall(rhs)
	if !ok || strings.TrimSpace(base) == name {
		return "", false
	}

	return line.indent + "let mut " + name + " = " + base + "\n" +
		line.indent + name + ".push(" + item + ")", true
}

func rewriteSelfAssignAppendLine(line sourceLine) (string, bool) {
	trimmed := line.trimmed
	eqIdx := strings.Index(trimmed, " = ")
	if eqIdx <= 0 {
		return "", false
	}
	lhs := strings.TrimSpace(trimmed[:eqIdx])
	rhs := strings.TrimSpace(trimmed[eqIdx+3:])
	if !isSimpleIdentifierBinding(lhs) {
		return "", false
	}

	base, item, ok := parseAppendCall(rhs)
	if !ok || strings.TrimSpace(base) != lhs {
		return "", false
	}

	return line.indent + lhs + ".push(" + item + ")", true
}

func rewriteStandaloneAppendLine(line sourceLine) (string, bool) {
	base, item, ok := parseAppendCall(line.trimmed)
	if !ok || !isSimpleIdentifierBinding(strings.TrimSpace(base)) {
		return "", false
	}
	return line.indent + strings.TrimSpace(base) + ".push(" + item + ")", true
}

func parseAppendCall(expr string) (string, string, bool) {
	if !strings.HasPrefix(expr, "append(") || !strings.HasSuffix(expr, ")") {
		return "", "", false
	}
	args := splitTopLevelComma(strings.TrimSpace(expr[len("append(") : len(expr)-1]))
	if len(args) != 2 {
		return "", "", false
	}
	base := strings.TrimSpace(args[0])
	item := strings.TrimSpace(args[1])
	if base == "" || item == "" {
		return "", "", false
	}
	return base, item, true
}

func rewriteLengthProperties(src []byte) ([]byte, []repair.Change, bool) {
	toks := lexer.New(src).Lex()
	protected := validLengthFieldOffsets(src)
	var edits []semanticEdit

	for i := 0; i+1 < len(toks); i++ {
		if toks[i].Kind != token.DOT {
			continue
		}
		name := toks[i+1]
		if name.Kind != token.IDENT || name.Value != "length" {
			continue
		}
		if i+2 < len(toks) && toks[i+2].Kind == token.LPAREN {
			continue
		}
		if protected[name.Pos.Offset] {
			continue
		}
		edits = append(edits, semanticEdit{
			start: name.Pos.Offset,
			end:   name.End.Offset,
			text:  "len()",
			change: repair.Change{
				Kind:    "length_property",
				Message: "replace foreign `.length` property with Osty `.len()` method",
				Pos:     name.Pos,
			},
		})
	}

	return applySemanticEdits(src, edits)
}

// validLengthFieldOffsets returns the source offsets of `.length` identifiers
// whose receiver type already has a `length` field — rewriting these to
// `.len()` would replace a valid field read with a missing-method call.
func validLengthFieldOffsets(src []byte) map[int]bool {
	parsed := parser.ParseDetailed(src)
	if parsed.File == nil {
		return nil
	}
	res := resolve.ResolveFileDefault(parsed.File, stdlib.LoadCached())
	chk := check.SelfhostFile(parsed.File, res, checkOptsForSource(canonical.Source(src, parsed.File)))
	if chk == nil {
		return nil
	}
	skip := make(map[int]bool)
	walkFieldExprs(parsed.File, func(fe *ast.FieldExpr) {
		if fe.Name != "length" {
			return
		}
		t := semanticExprType(fe.X, res, chk)
		// Rewrite is only safe when the receiver is a known builtin
		// container whose `.length` is genuine JS habit (String/Bytes/List/Map/Set).
		// Unknown receiver types may resolve to a user struct in another
		// file (airepair runs per-file, so cross-file imports are invisible),
		// and user structs may legitimately declare a `length: Int` field —
		// rewriting those to `.len()` replaces a valid field read with a
		// missing-method call. Be conservative: protect anything that is
		// not a known builtin.
		if !isRewritableLengthReceiver(t) {
			skip[fe.EndV.Offset-len(fe.Name)] = true
		}
	})
	return skip
}

func semanticExprType(e ast.Expr, res *resolve.Result, chk *check.Result) types.Type {
	if e == nil || chk == nil {
		return nil
	}
	if t := chk.Types[e]; t != nil {
		return t
	}
	id, ok := e.(*ast.Ident)
	if !ok || res == nil {
		return nil
	}
	sym := res.RefsByID[id.ID]
	if sym == nil {
		return nil
	}
	if t := chk.SymTypes[sym]; t != nil {
		return t
	}
	for node, t := range chk.LetTypes {
		switch n := node.(type) {
		case *ast.LetDecl:
			if sym.Decl == n {
				return t
			}
		case *ast.LetStmt:
			if semanticPatternBindsNode(n.Pattern, sym.Decl) {
				return t
			}
		}
	}
	return nil
}

func isRewritableLengthReceiver(t types.Type) bool {
	switch v := t.(type) {
	case *types.Primitive:
		return v.Kind == types.PString || v.Kind == types.PBytes
	case *types.Named:
		return v != nil && v.IsBuiltinNamed()
	}
	return false
}

func semanticPatternBindsNode(p ast.Pattern, target ast.Node) bool {
	if p == nil || target == nil {
		return false
	}
	if p == target {
		return true
	}
	switch p := p.(type) {
	case *ast.TuplePat:
		for _, elem := range p.Elems {
			if semanticPatternBindsNode(elem, target) {
				return true
			}
		}
	case *ast.StructPat:
		for _, f := range p.Fields {
			if semanticPatternBindsNode(f.Pattern, target) {
				return true
			}
		}
	case *ast.VariantPat:
		for _, arg := range p.Args {
			if semanticPatternBindsNode(arg, target) {
				return true
			}
		}
	case *ast.OrPat:
		for _, alt := range p.Alts {
			if semanticPatternBindsNode(alt, target) {
				return true
			}
		}
	case *ast.BindingPat:
		return semanticPatternBindsNode(p.Pattern, target)
	}
	return false
}

// walkFieldExprs visits every FieldExpr in file, invoking fn.
func walkFieldExprs(file *ast.File, fn func(*ast.FieldExpr)) {
	var visit func(n ast.Node)
	visit = func(n ast.Node) {
		if n == nil {
			return
		}
		switch v := n.(type) {
		case *ast.File:
			for _, d := range v.Decls {
				visit(d)
			}
			for _, s := range v.Stmts {
				visit(s)
			}
		case *ast.FnDecl:
			for _, p := range v.Params {
				if p != nil && p.Default != nil {
					visit(p.Default)
				}
			}
			if v.Body != nil {
				visit(v.Body)
			}
		case *ast.StructDecl:
			for _, m := range v.Methods {
				visit(m)
			}
		case *ast.EnumDecl:
			for _, m := range v.Methods {
				visit(m)
			}
		case *ast.InterfaceDecl:
			for _, m := range v.Methods {
				visit(m)
			}
		case *ast.LetDecl:
			visit(v.Value)
		case *ast.Block:
			for _, s := range v.Stmts {
				visit(s)
			}
		case *ast.LetStmt:
			visit(v.Value)
		case *ast.AssignStmt:
			for _, t := range v.Targets {
				visit(t)
			}
			visit(v.Value)
		case *ast.ExprStmt:
			visit(v.X)
		case *ast.ReturnStmt:
			visit(v.Value)
		case *ast.ChanSendStmt:
			visit(v.Channel)
			visit(v.Value)
		case *ast.DeferStmt:
			visit(v.X)
		case *ast.ForStmt:
			visit(v.Iter)
			if v.Body != nil {
				visit(v.Body)
			}
		case *ast.FieldExpr:
			fn(v)
			visit(v.X)
		case *ast.CallExpr:
			visit(v.Fn)
			for _, a := range v.Args {
				if a != nil {
					visit(a.Value)
				}
			}
		case *ast.IndexExpr:
			visit(v.X)
			visit(v.Index)
		case *ast.BinaryExpr:
			visit(v.Left)
			visit(v.Right)
		case *ast.UnaryExpr:
			visit(v.X)
		case *ast.ParenExpr:
			visit(v.X)
		case *ast.QuestionExpr:
			visit(v.X)
		case *ast.TurbofishExpr:
			visit(v.Base)
		case *ast.RangeExpr:
			visit(v.Start)
			visit(v.Stop)
		case *ast.IfExpr:
			visit(v.Cond)
			if v.Then != nil {
				visit(v.Then)
			}
			visit(v.Else)
		case *ast.MatchExpr:
			visit(v.Scrutinee)
			for _, arm := range v.Arms {
				if arm == nil {
					continue
				}
				visit(arm.Guard)
				visit(arm.Body)
			}
		case *ast.StructLit:
			visit(v.Type)
			for _, f := range v.Fields {
				if f != nil {
					visit(f.Value)
				}
			}
			visit(v.Spread)
		case *ast.ListExpr:
			for _, e := range v.Elems {
				visit(e)
			}
		case *ast.TupleExpr:
			for _, e := range v.Elems {
				visit(e)
			}
		case *ast.MapExpr:
			for _, e := range v.Entries {
				if e == nil {
					continue
				}
				visit(e.Key)
				visit(e.Value)
			}
		case *ast.ClosureExpr:
			for _, p := range v.Params {
				if p != nil && p.Default != nil {
					visit(p.Default)
				}
			}
			visit(v.Body)
		case *ast.StringLit:
			for _, p := range v.Parts {
				if !p.IsLit {
					visit(p.Expr)
				}
			}
		}
	}
	visit(file)
}

func structHasField(t types.Type, name string) bool {
	named, ok := t.(*types.Named)
	if !ok || named == nil || named.Sym == nil {
		return false
	}
	decl, ok := named.Sym.Decl.(*ast.StructDecl)
	if !ok {
		return false
	}
	for _, f := range decl.Fields {
		if f != nil && f.Name == name {
			return true
		}
	}
	return false
}

func rewriteBuiltinLenCalls(src []byte) ([]byte, []repair.Change, bool) {
	toks := lexer.New(src).Lex()
	var edits []semanticEdit

	for i := 0; i+1 < len(toks); i++ {
		name := toks[i]
		if name.Kind != token.IDENT || name.Value != "len" {
			continue
		}
		if prev := semanticPrevToken(toks, i); prev >= 0 {
			switch toks[prev].Kind {
			case token.DOT, token.QDOT, token.COLONCOLON:
				continue
			}
		}
		open := semanticNextToken(toks, i)
		if open < 0 || toks[open].Kind != token.LPAREN {
			continue
		}
		close := semanticMatchParen(toks, open)
		if close < 0 {
			continue
		}
		if semanticHasTopLevelComma(toks, open, close) {
			continue
		}

		argSrc := strings.TrimSpace(string(src[toks[open].End.Offset:toks[close].Pos.Offset]))
		if argSrc == "" {
			continue
		}

		edits = append(edits, semanticEdit{
			start: name.Pos.Offset,
			end:   toks[close].End.Offset,
			text:  semanticMethodCall(argSrc, "len"),
			change: repair.Change{
				Kind:    "builtin_len_call",
				Message: "replace foreign `len(...)` helper call with Osty `.len()` method",
				Pos:     name.Pos,
			},
		})
		i = close
	}

	return applySemanticEdits(src, edits)
}

func semanticMethodCall(expr, method string) string {
	target := strings.TrimSpace(expr)
	if semanticNeedsParens(target) {
		target = "(" + target + ")"
	}
	return target + "." + method + "()"
}

func semanticNeedsParens(expr string) bool {
	if expr == "" {
		return false
	}
	for _, r := range expr {
		switch r {
		case ' ', '\t', '\n', '+', '-', '*', '/', '%', '<', '>', '=', '&', '|', '^', '?', ':':
			return true
		}
	}
	return false
}

func semanticPrevToken(toks []token.Token, i int) int {
	for j := i - 1; j >= 0; j-- {
		if toks[j].Kind == token.NEWLINE {
			continue
		}
		return j
	}
	return -1
}

func semanticNextToken(toks []token.Token, i int) int {
	for j := i + 1; j < len(toks); j++ {
		if toks[j].Kind == token.NEWLINE {
			continue
		}
		return j
	}
	return -1
}

func semanticMatchParen(toks []token.Token, open int) int {
	depth := 0
	for i := open; i < len(toks); i++ {
		switch toks[i].Kind {
		case token.LPAREN:
			depth++
		case token.RPAREN:
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func semanticHasTopLevelComma(toks []token.Token, open, close int) bool {
	depthParen := 0
	depthBracket := 0
	depthBrace := 0
	for i := open + 1; i < close; i++ {
		switch toks[i].Kind {
		case token.LPAREN:
			depthParen++
		case token.RPAREN:
			if depthParen > 0 {
				depthParen--
			}
		case token.LBRACKET:
			depthBracket++
		case token.RBRACKET:
			if depthBracket > 0 {
				depthBracket--
			}
		case token.LBRACE:
			depthBrace++
		case token.RBRACE:
			if depthBrace > 0 {
				depthBrace--
			}
		case token.COMMA:
			if depthParen == 0 && depthBracket == 0 && depthBrace == 0 {
				return true
			}
		}
	}
	return false
}

func applySemanticEdits(src []byte, edits []semanticEdit) ([]byte, []repair.Change, bool) {
	if len(edits) == 0 {
		return src, nil, false
	}
	sort.SliceStable(edits, func(i, j int) bool {
		if edits[i].start != edits[j].start {
			return edits[i].start > edits[j].start
		}
		return edits[i].end > edits[j].end
	})

	out := append([]byte(nil), src...)
	lastStart := len(src) + 1
	var changes []repair.Change
	for _, edit := range edits {
		if edit.start < 0 || edit.end < edit.start || edit.end > len(src) || edit.end > lastStart {
			continue
		}
		out = append(append([]byte(nil), out[:edit.start]...), append([]byte(edit.text), out[edit.end:]...)...)
		lastStart = edit.start
		changes = append(changes, edit.change)
	}
	if len(changes) == 0 {
		return src, nil, false
	}
	sort.SliceStable(changes, func(i, j int) bool {
		if changes[i].Pos.Line != changes[j].Pos.Line {
			return changes[i].Pos.Line < changes[j].Pos.Line
		}
		return changes[i].Pos.Column < changes[j].Pos.Column
	})
	return out, changes, true
}
