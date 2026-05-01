package onb

import (
	"fmt"
	"strings"
)

// RenderAssembly renders the current ONB LIR slice as readable aarch64
// assembly. It stays deliberately readable so ONB has a debuggable artifact
// alongside the native object writer.
func RenderAssembly(program *Program) ([]byte, error) {
	if program == nil {
		return nil, fmt.Errorf("onb: missing program")
	}
	var b strings.Builder
	b.WriteString(".text\n")
	for _, fn := range program.Functions {
		if err := renderFunctionAssembly(&b, program.Target, fn); err != nil {
			return nil, err
		}
	}
	if err := renderCStringSection(&b, program.Target, program.CStrings); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

func renderFunctionAssembly(b *strings.Builder, target Target, fn Function) error {
	if fn.Name == "" {
		return fmt.Errorf("onb: function without name")
	}
	sym := asmSymbolName(target, fn.Name)
	if target.ObjectFormat == "elf" {
		fmt.Fprintf(b, ".globl %s\n.type %s, %%function\n", sym, sym)
	} else {
		fmt.Fprintf(b, ".globl %s\n.p2align 2\n", sym)
	}
	fmt.Fprintf(b, "%s:\n", sym)
	frameSize := functionFrameSize(fn)
	fpOffset := functionFPOffset(fn)
	if frameSize > 0 {
		if fpOffset < 0 {
			b.WriteString("\tstp x29, x30, [sp, #-16]!\n")
			b.WriteString("\tmov x29, sp\n")
		} else {
			fmt.Fprintf(b, "\tsub sp, sp, #%d\n", frameSize)
			fmt.Fprintf(b, "\tstp x29, x30, [sp, #%d]\n", fpOffset)
			fmt.Fprintf(b, "\tadd x29, sp, #%d\n", fpOffset)
		}
	}
	for _, block := range fn.Blocks {
		// Per-function block label so multiple functions in the same .s file
		// don't collide on bare `bb0`/`bb1` names. The encoder doesn't care
		// — it tracks block offsets internally — but readable assembly
		// matters for debugging.
		fmt.Fprintf(b, "%s:\n", asmBlockLabel(target, fn.Name, block.OriginalIndex))
		for _, instr := range block.Instrs {
			if err := renderInstrAssembly(b, target, fn, instr, frameSize, fpOffset); err != nil {
				return err
			}
		}
	}
	if target.ObjectFormat == "elf" {
		fmt.Fprintf(b, ".size %s, .-%s\n", sym, sym)
	}
	return nil
}

func asmBlockLabel(target Target, fnName string, blockIdx int) string {
	prefix := "L"
	if target.ObjectFormat == "elf" {
		prefix = ".L"
	}
	return fmt.Sprintf("%s_%s_bb%d", prefix, fnName, blockIdx)
}

func renderInstrAssembly(b *strings.Builder, target Target, fn Function, instr Instr, frameSize int64, fpOffset int64) error {
	switch i := instr.(type) {
	case *LoadCStringAddress:
		label := asmCStringLabel(target, i.Label)
		switch target.ObjectFormat {
		case "mach-o":
			fmt.Fprintf(b, "\tadrp %s, %s@PAGE\n", i.Dst, label)
			fmt.Fprintf(b, "\tadd %s, %s, %s@PAGEOFF\n", i.Dst, i.Dst, label)
		case "elf":
			fmt.Fprintf(b, "\tadrp %s, %s\n", i.Dst, label)
			fmt.Fprintf(b, "\tadd %s, %s, :lo12:%s\n", i.Dst, i.Dst, label)
		default:
			return fmt.Errorf("onb: assembly renderer does not support %s object format", target.ObjectFormat)
		}
	case *BranchLink:
		fmt.Fprintf(b, "\tbl %s\n", asmSymbolName(target, i.Symbol))
	case *MovImm32:
		fmt.Fprintf(b, "\tmov %s, #%d\n", i.Dst, i.Imm)
	case *MovImm64:
		if err := renderMovImm64Assembly(b, i.Dst, uint64(i.Imm)); err != nil {
			return err
		}
	case *MovRegReg:
		fmt.Fprintf(b, "\tmov %s, %s\n", i.Dst, i.Src)
	case *Store64Stack:
		if i.Offset == 0 {
			fmt.Fprintf(b, "\tstr %s, [sp]\n", i.Src)
		} else {
			fmt.Fprintf(b, "\tstr %s, [sp, #%d]\n", i.Src, i.Offset)
		}
	case *Load64Stack:
		if i.Offset == 0 {
			fmt.Fprintf(b, "\tldr %s, [sp]\n", i.Dst)
		} else {
			fmt.Fprintf(b, "\tldr %s, [sp, #%d]\n", i.Dst, i.Offset)
		}
	case *AddReg:
		fmt.Fprintf(b, "\tadd %s, %s, %s\n", i.Dst, i.Lhs, i.Rhs)
	case *SubReg:
		fmt.Fprintf(b, "\tsub %s, %s, %s\n", i.Dst, i.Lhs, i.Rhs)
	case *MulReg:
		fmt.Fprintf(b, "\tmul %s, %s, %s\n", i.Dst, i.Lhs, i.Rhs)
	case *Cmp:
		fmt.Fprintf(b, "\tcmp %s, %s\n", i.Lhs, i.Rhs)
	case *Cset:
		fmt.Fprintf(b, "\tcset %s, %s\n", i.Dst, i.Cond.AsmName())
	case *Branch:
		fmt.Fprintf(b, "\tb %s\n", asmBlockLabel(target, fn.Name, i.Target))
	case *BranchCondNotZero:
		fmt.Fprintf(b, "\tcbnz %s, %s\n", i.Src, asmBlockLabel(target, fn.Name, i.Target))
	case *BranchCond:
		fmt.Fprintf(b, "\tb.%s %s\n", i.Cond.AsmName(), asmBlockLabel(target, fn.Name, i.Target))
	case *Ret:
		if frameSize > 0 {
			if fpOffset < 0 {
				b.WriteString("\tldp x29, x30, [sp], #16\n")
			} else {
				fmt.Fprintf(b, "\tldp x29, x30, [sp, #%d]\n", fpOffset)
				fmt.Fprintf(b, "\tadd sp, sp, #%d\n", frameSize)
			}
		}
		b.WriteString("\tret\n")
	default:
		return fmt.Errorf("onb: assembly renderer does not support %T", instr)
	}
	return nil
}

func renderMovImm64Assembly(b *strings.Builder, dst Reg, imm uint64) error {
	if _, ok := xRegisterNumber(dst); !ok {
		return fmt.Errorf("onb: mov64 destination %s is not an x register", dst)
	}
	wrote := false
	for shift := 0; shift < 64; shift += 16 {
		chunk := uint16(imm >> shift)
		if chunk == 0 && wrote {
			continue
		}
		if !wrote {
			fmt.Fprintf(b, "\tmovz %s, #%d", dst, chunk)
			wrote = true
		} else {
			fmt.Fprintf(b, "\tmovk %s, #%d", dst, chunk)
		}
		if shift != 0 {
			fmt.Fprintf(b, ", lsl #%d", shift)
		}
		b.WriteByte('\n')
	}
	if !wrote {
		fmt.Fprintf(b, "\tmovz %s, #0\n", dst)
	}
	return nil
}

func renderCStringSection(b *strings.Builder, target Target, cstrings []CStringLiteral) error {
	if len(cstrings) == 0 {
		return nil
	}
	switch target.ObjectFormat {
	case "mach-o":
		b.WriteString(".section __TEXT,__cstring,cstring_literals\n")
	case "elf":
		b.WriteString(".section .rodata\n")
	default:
		return fmt.Errorf("onb: assembly renderer does not support %s string section", target.ObjectFormat)
	}
	for _, cstr := range cstrings {
		if cstr.Label == "" {
			return fmt.Errorf("onb: string literal without label")
		}
		fmt.Fprintf(b, "%s:\n\t.asciz %s\n", asmCStringLabel(target, cstr.Label), asmCStringLiteral(cstr.Value))
	}
	return nil
}

func asmSymbolName(target Target, name string) string {
	if target.ObjectFormat == "mach-o" {
		return "_" + name
	}
	return name
}

func asmCStringLabel(target Target, label string) string {
	if target.ObjectFormat == "mach-o" {
		return "L_." + label
	}
	return ".L." + label
}

func asmCStringLiteral(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\':
			b.WriteString("\\\\")
		case '"':
			b.WriteString("\\\"")
		case '\n':
			b.WriteString("\\n")
		case '\r':
			b.WriteString("\\r")
		case '\t':
			b.WriteString("\\t")
		default:
			if c >= 0x20 && c <= 0x7e {
				b.WriteByte(c)
				continue
			}
			fmt.Fprintf(&b, "\\%03o", c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func xRegisterNumber(reg Reg) (uint32, bool) {
	switch reg {
	case RegX0:
		return 0, true
	case RegX1:
		return 1, true
	case RegX2:
		return 2, true
	case RegX3:
		return 3, true
	case RegX4:
		return 4, true
	case RegX5:
		return 5, true
	case RegX6:
		return 6, true
	case RegX7:
		return 7, true
	case RegX9:
		return 9, true
	case RegX10:
		return 10, true
	default:
		return 0, false
	}
}

// aarch64RegEncoding extends xRegisterNumber to also recognise SP, X29, and
// X30. It is used by the Mach-O encoder for prologue/epilogue ops where SP
// and the frame-pointer pair are first-class operands.
func aarch64RegEncoding(reg Reg) (uint32, bool) {
	if n, ok := xRegisterNumber(reg); ok {
		return n, true
	}
	switch reg {
	case RegSP:
		return 31, true
	case RegX29:
		return 29, true
	case RegX30:
		return 30, true
	default:
		return 0, false
	}
}
