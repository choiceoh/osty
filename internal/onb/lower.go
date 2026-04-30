package onb

import (
	"fmt"

	"github.com/osty/osty/internal/mir"
)

// LowerMIR lowers the supported phase 1 MIR slice into ONB's own LIR.
// The initial slice is intentionally tiny: parameterless unit-returning main
// functions, plus string-literal println calls. That is enough to turn "MIR
// consumer" from a paper stage into a debuggable compiler boundary.
func LowerMIR(mod *mir.Module, target Target) (*Program, error) {
	if mod == nil {
		return nil, fmt.Errorf("onb: missing MIR module")
	}
	mainFn := mod.LookupFunction("main")
	if mainFn == nil {
		return nil, fmt.Errorf("onb: missing main function")
	}
	state := &lowerState{target: target}
	fn, err := state.lowerMainFunction(mainFn)
	if err != nil {
		return nil, err
	}
	return &Program{
		Target:    target,
		Functions: []Function{fn},
		CStrings:  state.cstrings,
	}, nil
}

type lowerState struct {
	target   Target
	cstrings []CStringLiteral
}

func (s *lowerState) lowerMainFunction(fn *mir.Function) (Function, error) {
	if fn == nil {
		return Function{}, fmt.Errorf("onb: nil main function")
	}
	if len(fn.Params) != 0 {
		return Function{}, fmt.Errorf("onb: main parameters are outside phase 1")
	}
	if fn.ReturnType != mir.TUnit {
		return Function{}, fmt.Errorf("onb: main return type %s is outside phase 1", fn.ReturnType)
	}
	out := Function{Name: fn.Name}
	for _, block := range fn.Blocks {
		lowered, err := s.lowerBlock(fn, block)
		if err != nil {
			return Function{}, err
		}
		out.Blocks = append(out.Blocks, lowered)
	}
	if len(out.Blocks) == 0 {
		return Function{}, fmt.Errorf("onb: main has no basic blocks")
	}
	return out, nil
}

func (s *lowerState) lowerBlock(fn *mir.Function, block *mir.BasicBlock) (Block, error) {
	if block == nil {
		return Block{}, fmt.Errorf("onb: nil basic block")
	}
	var instrs []Instr
	for _, instr := range block.Instrs {
		lowered, err := s.lowerInstr(fn, instr)
		if err != nil {
			return Block{}, err
		}
		instrs = append(instrs, lowered...)
	}
	switch block.Term.(type) {
	case *mir.ReturnTerm:
		instrs = append(instrs,
			&MovImm32{Dst: RegW0, Imm: 0},
			&Ret{},
		)
		return Block{
			Label:  blockLabel(block.ID),
			Instrs: instrs,
		}, nil
	default:
		return Block{}, fmt.Errorf("onb: terminator %T is outside phase 1", block.Term)
	}
}

func blockLabel(id mir.BlockID) string {
	return fmt.Sprintf("bb%d", int(id))
}

func (s *lowerState) lowerInstr(fn *mir.Function, instr mir.Instr) ([]Instr, error) {
	switch i := instr.(type) {
	case *mir.StorageLiveInstr, *mir.StorageDeadInstr:
		return nil, nil
	case *mir.AssignInstr:
		if isUnitReturnAssignment(fn, i) {
			return nil, nil
		}
	case *mir.IntrinsicInstr:
		return s.lowerIntrinsic(i)
	}
	return nil, fmt.Errorf("onb: instruction %T is outside phase 1", instr)
}

func (s *lowerState) lowerIntrinsic(instr *mir.IntrinsicInstr) ([]Instr, error) {
	switch instr.Kind {
	case mir.IntrinsicPrintln:
		if text, ok := stringLiteralOperand(instr.Args); ok {
			label := s.addCString(text)
			return []Instr{
				&LoadCStringAddress{Dst: RegX0, Label: label},
				&BranchLink{Symbol: "puts"},
			}, nil
		}
		if value, ok := intLiteralOperand(instr.Args); ok {
			label := s.addCString("%lld\n")
			out := []Instr{
				&LoadCStringAddress{Dst: RegX0, Label: label},
				&MovImm64{Dst: RegX1, Imm: value},
			}
			if s.target.ObjectFormat == "mach-o" {
				out = append(out, &Store64Stack{Src: RegX1})
			}
			out = append(out, &BranchLink{Symbol: "printf"})
			return out, nil
		}
		return nil, fmt.Errorf("onb: println currently requires one string or int literal argument")
	default:
		return nil, fmt.Errorf("onb: intrinsic %s is outside phase 1", instr.Kind)
	}
}

func (s *lowerState) addCString(value string) string {
	label := fmt.Sprintf("str%d", len(s.cstrings))
	s.cstrings = append(s.cstrings, CStringLiteral{Label: label, Value: value})
	return label
}

func isUnitReturnAssignment(fn *mir.Function, instr *mir.AssignInstr) bool {
	if fn == nil || instr == nil || instr.Dest.HasProjections() || instr.Dest.Local != fn.ReturnLocal {
		return false
	}
	use, ok := instr.Src.(*mir.UseRV)
	if !ok {
		return false
	}
	c, ok := use.Op.(*mir.ConstOp)
	if !ok {
		return false
	}
	_, ok = c.Const.(*mir.UnitConst)
	return ok
}

func stringLiteralOperand(args []mir.Operand) (string, bool) {
	if len(args) != 1 {
		return "", false
	}
	c, ok := args[0].(*mir.ConstOp)
	if !ok {
		return "", false
	}
	s, ok := c.Const.(*mir.StringConst)
	if !ok {
		return "", false
	}
	return s.Value, true
}

func intLiteralOperand(args []mir.Operand) (int64, bool) {
	if len(args) != 1 {
		return 0, false
	}
	c, ok := args[0].(*mir.ConstOp)
	if !ok {
		return 0, false
	}
	i, ok := c.Const.(*mir.IntConst)
	if !ok {
		return 0, false
	}
	return i.Value, true
}
