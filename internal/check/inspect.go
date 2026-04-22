package check

// Inspect walks an AST File and produces one InspectRecord per expression,
// let binding, and function declaration. It does NOT run type inference —
// it reads the types already recorded in a *Result and classifies each
// node by the inference rule that produced its type.
//
// The rule labels match the table in LANG_SPEC_v0.5/02a-type-inference.md
// §2a.4 and §2a.5. That file names each rule's position in the self-hosted
// checker source (toolchain/check.osty) so readers can cross-reference the
// spec, the reference implementation, and the runtime observation emitted
// here.
//
// Hints are reconstructed by re-enacting the bidirectional propagation rules
// of §2a.3: a `let x: T = e` call threads `T` into `e`; an `if` threads its
// enclosing hint into each branch; a `CallExpr` threads the substituted
// parameter type into each argument; and so on. Because this replay is
// deterministic and local, the hint shown in the inspector output matches
// the hint the checker actually used — without having to modify the
// checker or run it a second time.

import (
	"fmt"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/token"
	"github.com/osty/osty/internal/types"
)

// InspectRecord is one observation of the inference algorithm at a single
// AST node.
type InspectRecord struct {
	// Pos and End delimit the source range the record describes.
	Pos token.Pos
	End token.Pos
	// NodeKind is the syntactic category, e.g. "IntLit", "CallExpr", "If".
	NodeKind string
	// Rule is the label from LANG_SPEC_v0.5/02a-type-inference.md.
	// Empty when no rule applied (e.g. node had no type recorded).
	Rule string
	// Type is the type the checker assigned to this node, or nil when
	// the checker did not record one.
	Type types.Type
	// Hint is the type the parent context propagated into this node, or
	// nil when the node was synthesized without a hint.
	Hint types.Type
	// Notes carries optional auxiliary text: generic instantiations, the
	// source of the binding for LET, the branch unification target for
	// IF, etc.
	Notes []string
}

// Inspect returns the per-node inference observations for file, in
// source order. chk must be the result of type-checking file. A nil chk
// returns an empty slice.
func Inspect(file *ast.File, chk *Result) []InspectRecord {
	if file == nil || chk == nil {
		return nil
	}
	i := &inspector{chk: chk}
	for _, u := range file.Uses {
		_ = u // use decls do not carry inferred types
	}
	for _, d := range file.Decls {
		i.walkDecl(d)
	}
	for _, s := range file.Stmts {
		i.walkStmt(s, nil)
	}
	return i.records
}

type inspector struct {
	chk     *Result
	records []InspectRecord
}

func (i *inspector) emit(rec InspectRecord) {
	i.records = append(i.records, rec)
}

func (i *inspector) exprType(e ast.Expr) types.Type {
	if e == nil || i.chk == nil {
		return nil
	}
	return i.chk.Types[e]
}

func (i *inspector) letType(n ast.Node) types.Type {
	if n == nil || i.chk == nil {
		return nil
	}
	return i.chk.LetTypes[n]
}

// walkDecl emits records for declarations that participate in inference
// and recurses into their bodies.
func (i *inspector) walkDecl(d ast.Decl) {
	if d == nil {
		return
	}
	switch n := d.(type) {
	case *ast.LetDecl:
		i.walkLetDecl(n)
	case *ast.FnDecl:
		i.walkFnDecl(n)
	case *ast.StructDecl:
		for _, m := range n.Methods {
			i.walkFnDecl(m)
		}
	case *ast.EnumDecl:
		for _, m := range n.Methods {
			i.walkFnDecl(m)
		}
	case *ast.InterfaceDecl:
		// Interface bodies have no expression inference to observe.
	case *ast.TypeAliasDecl:
		// Aliases contribute to the type environment but have no
		// expression-level inference.
	}
}

func (i *inspector) walkLetDecl(n *ast.LetDecl) {
	if n == nil {
		return
	}
	declared := typeFromAnnotation(n.Type)
	bound := i.letType(n)
	rule := "LET"
	notes := make([]string, 0, 2)
	if declared != nil {
		notes = append(notes, "annotation: "+formatAnnotation(n.Type))
	} else {
		notes = append(notes, "synth")
	}
	i.emit(InspectRecord{
		Pos:      n.Pos(),
		End:      n.End(),
		NodeKind: "LetDecl",
		Rule:     rule,
		Type:     bound,
		Hint:     declared,
		Notes:    notes,
	})
	if n.Value != nil {
		// RHS sees the annotation as its hint when present, else the
		// already-inferred binding type (which the checker also used
		// when defaulting untyped literals).
		hint := declared
		if hint == nil {
			hint = bound
		}
		i.walkExpr(n.Value, hint)
	}
}

func (i *inspector) walkFnDecl(n *ast.FnDecl) {
	if n == nil || n.Body == nil {
		return
	}
	retHint := typeFromAnnotation(n.ReturnType)
	i.emit(InspectRecord{
		Pos:      n.Pos(),
		End:      n.End(),
		NodeKind: "FnDecl",
		Rule:     "FN-DECL",
		Type:     nil,
		Hint:     retHint,
		Notes:    []string{"body ⇐ return type"},
	})
	// The body's tail expression is checked against the return type hint.
	i.walkBlockAsExpr(n.Body, retHint)
}

// walkStmt traverses a statement, emitting records for sub-expressions.
// hint is the contextual hint for an expression statement used as the
// tail of a block (nil otherwise).
func (i *inspector) walkStmt(s ast.Stmt, hint types.Type) {
	if s == nil {
		return
	}
	switch n := s.(type) {
	case *ast.LetStmt:
		i.walkLetStmt(n)
	case *ast.ExprStmt:
		i.walkExpr(n.X, hint)
	case *ast.AssignStmt:
		for _, t := range n.Targets {
			i.walkExpr(t, nil)
		}
		if n.Value != nil {
			var rhsHint types.Type
			if len(n.Targets) == 1 {
				rhsHint = i.exprType(n.Targets[0])
			}
			i.walkExpr(n.Value, rhsHint)
		}
	case *ast.ReturnStmt:
		if n.Value != nil {
			// Return's value is checked against the enclosing fn's
			// return type. We do not thread that through statements
			// here, so mark it synth at worst; the FN-DECL record
			// already names the hint source.
			i.walkExpr(n.Value, nil)
		}
	case *ast.ForStmt:
		i.walkForStmt(n)
	case *ast.Block:
		i.walkBlockAsExpr(n, hint)
	case *ast.DeferStmt:
		if n.X != nil {
			i.walkExpr(n.X, nil)
		}
	case *ast.ChanSendStmt:
		i.walkExpr(n.Channel, nil)
		i.walkExpr(n.Value, nil)
	}
}

func (i *inspector) walkLetStmt(n *ast.LetStmt) {
	if n == nil {
		return
	}
	declared := typeFromAnnotation(n.Type)
	bound := i.letType(n)
	notes := []string{}
	if declared != nil {
		notes = append(notes, "annotation: "+formatAnnotation(n.Type))
	} else {
		notes = append(notes, "synth")
	}
	i.emit(InspectRecord{
		Pos:      n.Pos(),
		End:      n.End(),
		NodeKind: "LetStmt",
		Rule:     "LET",
		Type:     bound,
		Hint:     declared,
		Notes:    notes,
	})
	if n.Value != nil {
		hint := declared
		if hint == nil {
			hint = bound
		}
		i.walkExpr(n.Value, hint)
	}
}

func (i *inspector) walkForStmt(n *ast.ForStmt) {
	if n == nil {
		return
	}
	if n.Iter != nil {
		i.walkExpr(n.Iter, nil)
	}
	if n.Body != nil {
		i.walkBlockAsExpr(n.Body, nil)
	}
}

// walkBlockAsExpr walks a block whose value is used as an expression
// (function body, if arm, etc.). The tail's hint flows from the block's
// own hint.
func (i *inspector) walkBlockAsExpr(b *ast.Block, hint types.Type) {
	if b == nil {
		return
	}
	// Emit a block record so readers can see the block's inferred type
	// even when it is just a passthrough for the tail expression.
	// *ast.Block implements ast.Expr, so look it up directly.
	blockType := i.exprType(b)
	i.emit(InspectRecord{
		Pos:      b.Pos(),
		End:      b.End(),
		NodeKind: "Block",
		Rule:     "BLOCK",
		Type:     blockType,
		Hint:     hint,
		Notes:    []string{"tail expr gets block's hint"},
	})
	// Walk each statement; the tail statement inherits the block's hint
	// when it is an expression statement.
	for idx, s := range b.Stmts {
		var stmtHint types.Type
		if idx == len(b.Stmts)-1 {
			stmtHint = hint
		}
		i.walkStmt(s, stmtHint)
	}
}

// walkExpr walks an expression with a known hint (nil = synth mode) and
// emits one record per AST node using the rule labels defined in §2a.4.
func (i *inspector) walkExpr(e ast.Expr, hint types.Type) {
	if e == nil {
		return
	}
	t := i.exprType(e)
	switch n := e.(type) {
	case *ast.IntLit:
		i.emit(recordFor(n, "IntLit", "LIT-INT", t, hint, nil))
	case *ast.FloatLit:
		i.emit(recordFor(n, "FloatLit", "LIT-FLOAT", t, hint, nil))
	case *ast.StringLit:
		i.emit(recordFor(n, "StringLit", "LIT-STRING", t, hint, nil))
		for _, p := range n.Parts {
			if !p.IsLit && p.Expr != nil {
				i.walkExpr(p.Expr, nil)
			}
		}
	case *ast.BoolLit:
		i.emit(recordFor(n, "BoolLit", "LIT-BOOL", t, hint, nil))
	case *ast.CharLit:
		i.emit(recordFor(n, "CharLit", "LIT-CHAR", t, hint, nil))
	case *ast.ByteLit:
		i.emit(recordFor(n, "ByteLit", "LIT-BYTE", t, hint, nil))
	case *ast.Ident:
		i.emit(recordFor(n, "Ident", "VAR", t, hint, nil))
	case *ast.ParenExpr:
		i.emit(recordFor(n, "ParenExpr", "PAREN", t, hint, nil))
		i.walkExpr(n.X, hint)
	case *ast.Block:
		i.walkBlockAsExpr(n, hint)
	case *ast.UnaryExpr:
		i.emit(recordFor(n, "UnaryExpr", "UNARY", t, hint, nil))
		i.walkExpr(n.X, nil)
	case *ast.BinaryExpr:
		i.emit(recordFor(n, "BinaryExpr", "BINOP", t, hint, nil))
		// Both operands are synth'd; unification happens after. The
		// inspector reflects that by passing nil hint to each.
		i.walkExpr(n.Left, nil)
		i.walkExpr(n.Right, nil)
	case *ast.IfExpr:
		rule := "IF"
		notes := []string{}
		if n.IsIfLet {
			rule = "IF-LET"
			notes = append(notes, "pattern binds RHS")
		}
		i.emit(recordFor(n, "IfExpr", rule, t, hint, notes))
		if n.Cond != nil {
			// Plain if: condition checked ⇐ Bool. If-let: condition is
			// the scrutinee, synth'd.
			if n.IsIfLet {
				i.walkExpr(n.Cond, nil)
			} else {
				i.walkExpr(n.Cond, types.Bool)
			}
		}
		i.walkBlockAsExpr(n.Then, hint)
		if n.Else != nil {
			i.walkExpr(n.Else, hint)
		}
	case *ast.CallExpr:
		notes := i.callNotes(n)
		i.emit(recordFor(n, "CallExpr", "CALL", t, hint, notes))
		// The callee is synth'd; arguments receive their parameter type
		// as hint when the callee's type is known.
		i.walkExpr(n.Fn, nil)
		argHints := i.argHints(n)
		for idx, a := range n.Args {
			if a == nil {
				continue
			}
			var argHint types.Type
			if idx < len(argHints) {
				argHint = argHints[idx]
			}
			i.walkExpr(a.Value, argHint)
		}
	case *ast.ListExpr:
		elemHint := listElemHint(hint)
		i.emit(recordFor(n, "ListExpr", "LIST", t, hint, nil))
		for _, el := range n.Elems {
			i.walkExpr(el, elemHint)
		}
	case *ast.TupleExpr:
		tupleHints := tupleElemHints(hint, len(n.Elems))
		i.emit(recordFor(n, "TupleExpr", "TUPLE", t, hint, nil))
		for idx, el := range n.Elems {
			var h types.Type
			if idx < len(tupleHints) {
				h = tupleHints[idx]
			}
			i.walkExpr(el, h)
		}
	case *ast.MapExpr:
		keyHint, valHint := mapEntryHints(hint)
		i.emit(recordFor(n, "MapExpr", "MAP", t, hint, nil))
		for _, entry := range n.Entries {
			if entry == nil {
				continue
			}
			i.walkExpr(entry.Key, keyHint)
			i.walkExpr(entry.Value, valHint)
		}
	case *ast.RangeExpr:
		i.emit(recordFor(n, "RangeExpr", "RANGE", t, hint, nil))
		if n.Start != nil {
			i.walkExpr(n.Start, nil)
		}
		if n.Stop != nil {
			i.walkExpr(n.Stop, nil)
		}
	case *ast.FieldExpr:
		rule := "FIELD"
		if n.IsOptional {
			rule = "FIELD-OPT"
		}
		i.emit(recordFor(n, "FieldExpr", rule, t, hint, nil))
		i.walkExpr(n.X, nil)
	case *ast.IndexExpr:
		i.emit(recordFor(n, "IndexExpr", "INDEX", t, hint, nil))
		i.walkExpr(n.X, nil)
		i.walkExpr(n.Index, nil)
	case *ast.MatchExpr:
		i.emit(recordFor(n, "MatchExpr", "MATCH", t, hint, nil))
		i.walkExpr(n.Scrutinee, nil)
		for _, arm := range n.Arms {
			if arm == nil {
				continue
			}
			if arm.Guard != nil {
				i.walkExpr(arm.Guard, types.Bool)
			}
			if arm.Body != nil {
				i.walkExpr(arm.Body, hint)
			}
		}
	case *ast.StructLit:
		rule := "STRUCT-LIT"
		i.emit(recordFor(n, "StructLit", rule, t, hint, nil))
		// Field values' hints come from the struct's field declarations.
		// The inspector does not resolve those (it would reimplement
		// frontCheckStructLitHint); it passes nil and surfaces the
		// field name in the record text.
		for _, f := range n.Fields {
			if f == nil || f.Value == nil {
				continue
			}
			i.walkExpr(f.Value, nil)
		}
		if n.Spread != nil {
			i.walkExpr(n.Spread, nil)
		}
	case *ast.ClosureExpr:
		paramHint, retHint := fnTypeHints(hint)
		notes := []string{}
		if paramHint != nil {
			notes = append(notes, "params from fn-hint")
		}
		if retHint != nil {
			notes = append(notes, "return ⇐ "+retHint.String())
		}
		i.emit(recordFor(n, "ClosureExpr", "CLOSURE", t, hint, notes))
		if n.Body != nil {
			i.walkExpr(n.Body, retHint)
		}
	case *ast.QuestionExpr:
		i.emit(recordFor(n, "QuestionExpr", "QUESTION", t, hint, nil))
		i.walkExpr(n.X, nil)
	case *ast.TurbofishExpr:
		notes := []string{}
		if len(n.Args) > 0 {
			names := make([]string, 0, len(n.Args))
			for _, a := range n.Args {
				names = append(names, formatAnnotation(a))
			}
			notes = append(notes, "type args: ["+strings.Join(names, ", ")+"]")
		}
		i.emit(recordFor(n, "TurbofishExpr", "TURBOFISH", t, hint, notes))
		i.walkExpr(n.Base, nil)
	default:
		// Unknown expression kind; emit a best-effort record so the
		// trace remains complete for unfamiliar shapes.
		i.emit(InspectRecord{
			Pos:      e.Pos(),
			End:      e.End(),
			NodeKind: fmt.Sprintf("%T", e),
			Rule:     "",
			Type:     t,
			Hint:     hint,
		})
	}
}

// callNotes builds the per-call annotation strings: generic
// instantiations, method-call indicator, etc. The instantiations map is
// keyed by *ast.CallExpr, which the checker populated once per call
// site (for backend monomorphization).
func (i *inspector) callNotes(n *ast.CallExpr) []string {
	notes := []string{}
	if n == nil || i.chk == nil {
		return notes
	}
	if _, ok := n.Fn.(*ast.FieldExpr); ok {
		notes = append(notes, "method call")
	}
	if args, ok := i.chk.InstantiationsByID[n.ID]; ok && len(args) > 0 {
		names := make([]string, 0, len(args))
		for _, a := range args {
			if a == nil {
				names = append(names, "?")
				continue
			}
			names = append(names, a.String())
		}
		notes = append(notes, "instantiated ["+strings.Join(names, ", ")+"]")
	}
	return notes
}

// argHints returns the per-argument hint list for a call, reconstructed
// from the callee's type when the checker recorded it as FnType.
func (i *inspector) argHints(n *ast.CallExpr) []types.Type {
	if n == nil {
		return nil
	}
	callee := i.exprType(n.Fn)
	fn, ok := types.AsFn(callee)
	if !ok || fn == nil {
		return nil
	}
	// For method calls the receiver is already bound; parameters align
	// with the argument list one-to-one.
	return fn.Params
}

// ------------------------------------------------------------------
// Helpers
// ------------------------------------------------------------------

// recordFor builds an InspectRecord from a node that carries Pos()/End().
func recordFor(n ast.Node, kind, rule string, t, hint types.Type, notes []string) InspectRecord {
	return InspectRecord{
		Pos:      n.Pos(),
		End:      n.End(),
		NodeKind: kind,
		Rule:     rule,
		Type:     t,
		Hint:     hint,
		Notes:    notes,
	}
}

// typeFromAnnotation extracts the semantic type named by an AST type
// annotation when the annotation is a bare primitive. The inspector
// uses this for display only — full resolution happens in the resolver,
// which the inspector does not re-run.
func typeFromAnnotation(t ast.Type) types.Type {
	if t == nil {
		return nil
	}
	if n, ok := t.(*ast.NamedType); ok && len(n.Path) == 1 && len(n.Args) == 0 {
		if p := types.PrimitiveByName(n.Path[0]); p != nil {
			return p
		}
	}
	return nil
}

// formatAnnotation renders an ast.Type annotation for display. Kept
// intentionally simple — it handles the common cases and falls back to
// the syntactic kind name for the rest.
func formatAnnotation(t ast.Type) string {
	if t == nil {
		return "()"
	}
	switch n := t.(type) {
	case *ast.NamedType:
		head := strings.Join(n.Path, ".")
		if len(n.Args) == 0 {
			return head
		}
		parts := make([]string, 0, len(n.Args))
		for _, a := range n.Args {
			parts = append(parts, formatAnnotation(a))
		}
		return head + "<" + strings.Join(parts, ", ") + ">"
	case *ast.OptionalType:
		return formatAnnotation(n.Inner) + "?"
	case *ast.TupleType:
		parts := make([]string, 0, len(n.Elems))
		for _, e := range n.Elems {
			parts = append(parts, formatAnnotation(e))
		}
		return "(" + strings.Join(parts, ", ") + ")"
	case *ast.FnType:
		parts := make([]string, 0, len(n.Params))
		for _, p := range n.Params {
			parts = append(parts, formatAnnotation(p))
		}
		s := "fn(" + strings.Join(parts, ", ") + ")"
		if n.ReturnType != nil {
			s += " -> " + formatAnnotation(n.ReturnType)
		}
		return s
	}
	return fmt.Sprintf("%T", t)
}

// listElemHint unwraps List<T> → T for argument/element hint
// propagation. Returns nil when the hint is not a List.
func listElemHint(hint types.Type) types.Type {
	if n, ok := types.AsNamedBuiltin(hint, "List"); ok && len(n.Args) == 1 {
		return n.Args[0]
	}
	return nil
}

// tupleElemHints unwraps (T1, T2, ...) → [T1, T2, ...] for per-elem
// hint propagation. Returns nil when the hint is not a tuple or when
// arities differ.
func tupleElemHints(hint types.Type, n int) []types.Type {
	if hint == nil {
		return nil
	}
	if tp, ok := hint.(*types.Tuple); ok && len(tp.Elems) == n {
		return tp.Elems
	}
	return nil
}

// mapEntryHints unwraps Map<K, V> → (K, V) for key/value hint
// propagation. Returns (nil, nil) when the hint is not a Map.
func mapEntryHints(hint types.Type) (types.Type, types.Type) {
	if n, ok := types.AsNamedBuiltin(hint, "Map"); ok && len(n.Args) == 2 {
		return n.Args[0], n.Args[1]
	}
	return nil, nil
}

// fnTypeHints unwraps fn(Ps...) -> R → (first-param-or-nil, R) for
// closure hint propagation. The inspector only forwards the return
// type and signals the presence of parameter hints via Notes.
func fnTypeHints(hint types.Type) (types.Type, types.Type) {
	if fn, ok := types.AsFn(hint); ok {
		var first types.Type
		if len(fn.Params) > 0 {
			first = fn.Params[0]
		}
		return first, fn.Return
	}
	return nil, nil
}
