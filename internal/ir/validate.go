package ir

import (
	"errors"
	"fmt"
)

func errDecision(s string) error            { return errors.New(s) }
func errDecisionf(f string, a ...any) error { return fmt.Errorf(f, a...) }

// Validate performs a light structural sanity check over an IR Module.
// It does NOT re-run type checking — that's the front-end's job — but
// it does catch internal invariants that a buggy lowerer could
// violate, such as:
//
//   - Expr with nil Type()
//   - Empty Module with a nil Package
//   - FnDecl with a nil Body but not marked as an interface method
//   - MatchArm with nil Pattern or nil Body
//   - LetStmt with both Name == "" and Pattern == nil
//
// The returned slice is empty when the IR is well-formed. Backends
// may call Validate during development (or behind a flag) as cheap
// insurance against regressions in the lowerer.
func Validate(m *Module) []error {
	v := &validator{}
	v.validateModule(m)
	return v.errs
}

type validator struct {
	errs []error
	// methodCtx is true while validating a decl that's a method on a
	// struct/enum/interface (no Body required for interface stubs).
	inInterface bool
}

func (v *validator) addf(format string, args ...any) {
	v.errs = append(v.errs, fmt.Errorf(format, args...))
}

func (v *validator) validateModule(m *Module) {
	if m == nil {
		v.addf("nil module")
		return
	}
	if m.Package == "" {
		v.addf("module: empty Package name")
	}
	for i, d := range m.Decls {
		if d == nil {
			v.addf("decl[%d]: nil", i)
			continue
		}
		v.validateDecl(d)
	}
	for i, s := range m.Script {
		if s == nil {
			v.addf("script[%d]: nil", i)
			continue
		}
		v.validateStmt(s)
	}
}

func (v *validator) validateDecl(d Decl) {
	switch d := d.(type) {
	case *FnDecl:
		v.validateFnDecl(d, false)
	case *StructDecl:
		v.validateTypeParams("struct "+d.Name, d.Generics)
		for _, f := range d.Fields {
			if f.Type == nil {
				v.addf("struct %s: field %s has nil Type", d.Name, f.Name)
			} else {
				v.validateType(f.Type, fmt.Sprintf("struct %s field %s", d.Name, f.Name))
			}
			if f.Default != nil {
				v.validateExpr(f.Default)
			}
		}
		for _, m := range d.Methods {
			v.validateFnDecl(m, false)
		}
	case *EnumDecl:
		seen := map[string]bool{}
		for _, vr := range d.Variants {
			if vr.Name == "" {
				v.addf("enum %s: variant with empty name", d.Name)
			}
			if seen[vr.Name] {
				v.addf("enum %s: duplicate variant %s", d.Name, vr.Name)
			}
			seen[vr.Name] = true
			for i, p := range vr.Payload {
				if p == nil {
					v.addf("enum %s: variant %s payload[%d] nil Type", d.Name, vr.Name, i)
					continue
				}
				v.validateType(p, fmt.Sprintf("enum %s variant %s payload[%d]", d.Name, vr.Name, i))
			}
		}
		for _, m := range d.Methods {
			v.validateFnDecl(m, false)
		}
	case *InterfaceDecl:
		v.validateTypeParams("interface "+d.Name, d.Generics)
		for i, ext := range d.Extends {
			if ext == nil {
				v.addf("interface %s: extends[%d] nil Type", d.Name, i)
				continue
			}
			v.validateType(ext, fmt.Sprintf("interface %s extends[%d]", d.Name, i))
		}
		old := v.inInterface
		v.inInterface = true
		for _, m := range d.Methods {
			v.validateFnDecl(m, true)
		}
		v.inInterface = old
	case *TypeAliasDecl:
		if d.Target == nil {
			v.addf("alias %s: nil Target", d.Name)
		} else {
			v.validateTypeParams("alias "+d.Name, d.Generics)
			v.validateType(d.Target, "alias "+d.Name)
		}
	case *LetDecl:
		if d.Name == "" {
			v.addf("top-level let: empty Name")
		}
		if d.Type != nil {
			v.validateType(d.Type, "top-level let "+d.Name)
		}
		if d.Value == nil {
			v.addf("top-level let %s: nil Value", d.Name)
		} else {
			v.validateExpr(d.Value)
		}
	case *UseDecl:
		if d.Alias == "" && !d.IsFFI() {
			v.addf("use: empty Alias")
		}
	}
}

func (v *validator) validateFnDecl(fn *FnDecl, allowNoBody bool) {
	if fn.Name == "" {
		v.addf("fn: empty Name")
	}
	if fn.Return == nil {
		v.addf("fn %s: nil Return", fn.Name)
	} else {
		v.validateType(fn.Return, "fn "+fn.Name+" return")
	}
	v.validateTypeParams("fn "+fn.Name, fn.Generics)
	for i, p := range fn.Params {
		if p == nil {
			v.addf("fn %s: param[%d] nil", fn.Name, i)
			continue
		}
		if p.Type == nil {
			v.addf("fn %s: param %q nil Type", fn.Name, p.Name)
		} else {
			v.validateType(p.Type, fmt.Sprintf("fn %s param[%d]", fn.Name, i))
		}
		if p.Name == "" && p.Pattern == nil {
			v.addf("fn %s: param[%d] has neither Name nor Pattern", fn.Name, i)
		}
		if p.Name != "" && p.Pattern != nil {
			v.addf("fn %s: param %q has both Name and Pattern", fn.Name, p.Name)
		}
	}
	if fn.Body == nil {
		if !allowNoBody && !v.inInterface {
			v.addf("fn %s: nil Body", fn.Name)
		}
		return
	}
	v.validateBlock(fn.Body)
}

func (v *validator) validateBlock(b *Block) {
	if b == nil {
		v.addf("nil block")
		return
	}
	for i, s := range b.Stmts {
		if s == nil {
			v.addf("block stmt[%d]: nil", i)
			continue
		}
		v.validateStmt(s)
	}
	if b.Result != nil {
		v.validateExpr(b.Result)
	}
}

func (v *validator) validateStmt(s Stmt) {
	switch s := s.(type) {
	case *Block:
		v.validateBlock(s)
	case *LetStmt:
		if s.Name == "" && s.Pattern == nil {
			v.addf("let: both Name and Pattern empty")
		}
		if s.Type != nil {
			v.validateType(s.Type, "let stmt")
		}
		if s.Value != nil {
			v.validateExpr(s.Value)
		}
	case *ExprStmt:
		if s.X == nil {
			v.addf("ExprStmt: nil X")
		} else {
			v.validateExpr(s.X)
		}
	case *AssignStmt:
		if len(s.Targets) == 0 {
			v.addf("AssignStmt: no targets")
		}
		for _, t := range s.Targets {
			v.validateExpr(t)
		}
		v.validateExpr(s.Value)
	case *ReturnStmt:
		if s.Value != nil {
			v.validateExpr(s.Value)
		}
	case *BreakStmt, *ContinueStmt:
		// leaves
	case *IfStmt:
		if s.Cond == nil {
			v.addf("IfStmt: nil Cond")
		} else {
			v.validateExpr(s.Cond)
		}
		if s.Then == nil {
			v.addf("IfStmt: nil Then")
		} else {
			v.validateBlock(s.Then)
		}
		if s.Else != nil {
			v.validateBlock(s.Else)
		}
	case *ForStmt:
		if s.Body == nil {
			v.addf("ForStmt: nil Body")
		} else {
			v.validateBlock(s.Body)
		}
		if s.Var != "" && s.Pattern != nil {
			v.addf("ForStmt: has both Var and Pattern")
		}
		switch s.Kind {
		case ForWhile:
			if s.Cond == nil {
				v.addf("ForStmt While: nil Cond")
			} else {
				v.validateExpr(s.Cond)
			}
		case ForIn:
			if s.Iter == nil {
				v.addf("ForStmt In: nil Iter")
			} else {
				v.validateExpr(s.Iter)
			}
			if s.Var == "" && s.Pattern == nil {
				v.addf("ForStmt In: no loop binding")
			}
		case ForRange:
			if s.Start == nil {
				v.addf("ForStmt Range: nil Start")
			} else {
				v.validateExpr(s.Start)
			}
			if s.End == nil {
				v.addf("ForStmt Range: nil End")
			} else {
				v.validateExpr(s.End)
			}
		}
	case *DeferStmt:
		if s.Body == nil {
			v.addf("DeferStmt: nil Body")
		} else {
			v.validateBlock(s.Body)
		}
	case *ChanSendStmt:
		v.validateExpr(s.Channel)
		v.validateExpr(s.Value)
	case *MatchStmt:
		v.validateExpr(s.Scrutinee)
		for i, a := range s.Arms {
			if a == nil {
				v.addf("MatchStmt arm[%d]: nil", i)
				continue
			}
			v.validateMatchArm(a)
		}
		if s.Tree != nil {
			for _, err := range ValidateDecisionTree(s.Tree, len(s.Arms)) {
				v.addf("MatchStmt tree: %v", err)
			}
		}
	case *ErrorStmt:
		// allowed by design
	}
}

func (v *validator) validateExpr(e Expr) {
	if e == nil {
		v.addf("nil expression")
		return
	}
	if e.Type() == nil {
		v.addf("%T: nil Type()", e)
	}
	v.validateType(e.Type(), fmt.Sprintf("%T", e))
	switch e := e.(type) {
	case *UnaryExpr:
		v.validateExpr(e.X)
	case *BinaryExpr:
		v.validateExpr(e.Left)
		v.validateExpr(e.Right)
	case *Ident:
		for i, ta := range e.TypeArgs {
			if ta == nil {
				v.addf("Ident %s: TypeArgs[%d] nil", e.Name, i)
				continue
			}
			v.validateType(ta, fmt.Sprintf("Ident %s TypeArgs[%d]", e.Name, i))
		}
	case *CallExpr:
		v.validateExpr(e.Callee)
		for i, ta := range e.TypeArgs {
			if ta == nil {
				v.addf("CallExpr: TypeArgs[%d] nil", i)
				continue
			}
			v.validateType(ta, fmt.Sprintf("CallExpr TypeArgs[%d]", i))
		}
		for _, a := range e.Args {
			v.validateArg(a)
		}
	case *IntrinsicCall:
		for _, a := range e.Args {
			v.validateArg(a)
		}
	case *MethodCall:
		v.validateExpr(e.Receiver)
		if e.Name == "" {
			v.addf("MethodCall: empty Name")
		}
		for i, ta := range e.TypeArgs {
			if ta == nil {
				v.addf("MethodCall %s: TypeArgs[%d] nil", e.Name, i)
				continue
			}
			v.validateType(ta, fmt.Sprintf("MethodCall %s TypeArgs[%d]", e.Name, i))
		}
		for _, a := range e.Args {
			v.validateArg(a)
		}
	case *ListLit:
		v.validateType(e.Elem, "ListLit Elem")
		for _, el := range e.Elems {
			v.validateExpr(el)
		}
	case *MapLit:
		v.validateType(e.KeyT, "MapLit KeyT")
		v.validateType(e.ValT, "MapLit ValT")
		for _, en := range e.Entries {
			v.validateExpr(en.Key)
			v.validateExpr(en.Value)
		}
	case *TupleLit:
		for _, el := range e.Elems {
			v.validateExpr(el)
		}
	case *StructLit:
		if e.TypeName == "" {
			v.addf("StructLit: empty TypeName")
		}
		for _, f := range e.Fields {
			if f.Value != nil {
				v.validateExpr(f.Value)
			}
		}
		if e.Spread != nil {
			v.validateExpr(e.Spread)
		}
	case *RangeLit:
		if e.Start != nil {
			v.validateExpr(e.Start)
		}
		if e.End != nil {
			v.validateExpr(e.End)
		}
	case *QuestionExpr:
		v.validateExpr(e.X)
	case *CoalesceExpr:
		v.validateExpr(e.Left)
		v.validateExpr(e.Right)
	case *FieldExpr:
		v.validateExpr(e.X)
	case *TupleAccess:
		v.validateExpr(e.X)
	case *IndexExpr:
		v.validateExpr(e.X)
		v.validateExpr(e.Index)
	case *Closure:
		v.validateType(e.Return, "Closure Return")
		v.validateBlock(e.Body)
		for i, c := range e.Captures {
			if c == nil {
				v.addf("Closure: capture[%d] nil", i)
				continue
			}
			if c.Name == "" {
				v.addf("Closure: capture[%d] empty Name", i)
			}
			v.validateType(c.T, fmt.Sprintf("Closure capture[%d]", i))
		}
		for i, p := range e.Params {
			if p == nil {
				v.addf("Closure: param[%d] nil", i)
				continue
			}
			if p.Type == nil {
				v.addf("Closure: param[%d] nil Type", i)
				continue
			}
			v.validateType(p.Type, fmt.Sprintf("Closure param[%d]", i))
		}
	case *VariantLit:
		if e.Variant == "" {
			v.addf("VariantLit: empty Variant")
		}
		for _, a := range e.Args {
			v.validateArg(a)
		}
	case *BlockExpr:
		v.validateBlock(e.Block)
	case *IfExpr:
		v.validateExpr(e.Cond)
		v.validateBlock(e.Then)
		if e.Else != nil {
			v.validateBlock(e.Else)
		}
	case *IfLetExpr:
		if e.Pattern == nil {
			v.addf("IfLetExpr: nil Pattern")
		}
		v.validateExpr(e.Scrutinee)
		v.validateBlock(e.Then)
		if e.Else != nil {
			v.validateBlock(e.Else)
		}
	case *MatchExpr:
		v.validateExpr(e.Scrutinee)
		for i, a := range e.Arms {
			if a == nil {
				v.addf("MatchExpr arm[%d]: nil", i)
				continue
			}
			v.validateMatchArm(a)
		}
		if e.Tree != nil {
			for _, err := range ValidateDecisionTree(e.Tree, len(e.Arms)) {
				v.addf("MatchExpr tree: %v", err)
			}
		}
	case *StringLit:
		for _, p := range e.Parts {
			if !p.IsLit && p.Expr != nil {
				v.validateExpr(p.Expr)
			}
		}
	}
}

func (v *validator) validateTypeParams(owner string, params []*TypeParam) {
	if len(params) == 0 {
		return
	}
	seen := map[string]bool{}
	for i, p := range params {
		if p == nil {
			v.addf("%s: generic[%d] nil", owner, i)
			continue
		}
		if p.Name == "" {
			v.addf("%s: generic[%d] empty Name", owner, i)
		} else if seen[p.Name] {
			v.addf("%s: duplicate generic %s", owner, p.Name)
		}
		seen[p.Name] = true
		for j, b := range p.Bounds {
			if b == nil {
				v.addf("%s: generic %s bound[%d] nil", owner, p.Name, j)
				continue
			}
			v.validateType(b, fmt.Sprintf("%s generic %s bound[%d]", owner, p.Name, j))
		}
	}
}

func (v *validator) validateType(t Type, where string) {
	if t == nil {
		v.addf("%s: nil Type", where)
		return
	}
	switch t := t.(type) {
	case *PrimType:
		if t.Kind == PrimInvalid {
			v.addf("%s: invalid primitive type", where)
		}
	case *NamedType:
		if t.Name == "" {
			v.addf("%s: NamedType empty Name", where)
		}
		for i, a := range t.Args {
			if a == nil {
				v.addf("%s: NamedType arg[%d] nil", where, i)
				continue
			}
			v.validateType(a, fmt.Sprintf("%s arg[%d]", where, i))
		}
	case *OptionalType:
		if t.Inner == nil {
			v.addf("%s: OptionalType nil Inner", where)
			return
		}
		v.validateType(t.Inner, where+" inner")
	case *TupleType:
		for i, e := range t.Elems {
			if e == nil {
				v.addf("%s: TupleType elem[%d] nil", where, i)
				continue
			}
			v.validateType(e, fmt.Sprintf("%s elem[%d]", where, i))
		}
	case *FnType:
		for i, p := range t.Params {
			if p == nil {
				v.addf("%s: FnType param[%d] nil", where, i)
				continue
			}
			v.validateType(p, fmt.Sprintf("%s param[%d]", where, i))
		}
		if t.Return != nil {
			v.validateType(t.Return, where+" return")
		}
	case *TypeVar:
		if t.Name == "" {
			v.addf("%s: TypeVar empty Name", where)
		}
	case *ErrType:
		// allowed
	default:
		v.addf("%s: unknown Type %T", where, t)
	}
}

func (v *validator) validateMatchArm(a *MatchArm) {
	if a.Pattern == nil {
		v.addf("MatchArm: nil Pattern")
	}
	if a.Guard != nil {
		v.validateExpr(a.Guard)
	}
	if a.Body == nil {
		v.addf("MatchArm: nil Body")
	} else {
		v.validateBlock(a.Body)
	}
}

// validateArg validates one call argument.
func (v *validator) validateArg(a Arg) {
	if a.Value == nil {
		v.addf("Arg: nil Value")
		return
	}
	v.validateExpr(a.Value)
}

// ValidateDecisionTree reports structural issues in a compiled
// decision tree rooted at n. armCount bounds the valid arm indices.
func ValidateDecisionTree(n DecisionNode, armCount int) []error {
	if n == nil {
		return nil
	}
	v := &decisionValidator{armCount: armCount}
	v.walk(n)
	return v.errs
}

type decisionValidator struct {
	armCount int
	errs     []error
}

func (v *decisionValidator) walk(n DecisionNode) {
	switch n := n.(type) {
	case nil:
		v.errs = append(v.errs, errDecision("nil node"))
	case *DecisionLeaf:
		if n.ArmIndex < 0 || n.ArmIndex >= v.armCount {
			v.errs = append(v.errs, errDecisionf("leaf: arm %d out of range [0,%d)", n.ArmIndex, v.armCount))
		}
	case *DecisionFail:
		// terminal
	case *DecisionBind:
		if n.Next == nil {
			v.errs = append(v.errs, errDecision("bind: nil Next"))
		} else {
			v.walk(n.Next)
		}
	case *DecisionGuard:
		if n.Then == nil {
			v.errs = append(v.errs, errDecision("guard: nil Then"))
		} else {
			v.walk(n.Then)
		}
		if n.Else == nil {
			v.errs = append(v.errs, errDecision("guard: nil Else"))
		} else {
			v.walk(n.Else)
		}
	case *DecisionSwitch:
		if n.Default == nil {
			v.errs = append(v.errs, errDecision("switch: nil Default"))
		} else {
			v.walk(n.Default)
		}
		for i, c := range n.Cases {
			if c.Body == nil {
				v.errs = append(v.errs, errDecisionf("switch: case[%d] nil Body", i))
				continue
			}
			v.walk(c.Body)
		}
	}
}
