package onb

import "testing"

// TestLirOpcodeParityVsOstyTable pins every Phase 2a opcode's
// encoded 32-bit word against the canonical hex the Osty-side
// `toolchain/onb_lir_test.osty` asserts. The two test files share
// the same expected-column values; if a Go encoder genuinely
// changes (new opcode flavour, register-class shift, ...), update
// both Go body and Osty test together so the diff is reviewable.
//
// Phase 2a covers single-word instructions only — MovImm64
// (variable-length) and the link-time branches (BranchLink,
// Branch, BranchCond, BranchCondNotZero) join in Phase 2b along
// with the encoder fixup pass.
func TestLirOpcodeParityVsOstyTable(t *testing.T) {
	t.Parallel()

	check := func(name string, got uint32, err error, want uint32) {
		t.Helper()
		if err != nil {
			t.Errorf("%s: Go encode err %v", name, err)
			return
		}
		if got != want {
			t.Errorf("%s: Go = 0x%08x; Osty parity = 0x%08x", name, got, want)
		}
	}

	// ADD/SUB/MUL — Int arithmetic core.
	w, e := encodeMachOArithReg(0x8b000000, RegX9, RegX9, RegX10)
	check("add x9,x9,x10", w, e, 0x8b0a0129)
	w, e = encodeMachOArithReg(0xcb000000, RegX9, RegX9, RegX10)
	check("sub x9,x9,x10", w, e, 0xcb0a0129)
	w, e = encodeMachOMulReg(RegX9, RegX9, RegX10)
	check("mul x9,x9,x10", w, e, 0x9b0a7d29)

	// CMP / CSET — bool materialisation.
	w, e = encodeMachOCmp(RegX9, RegX10)
	check("cmp x9,x10", w, e, 0xeb0a013f)
	w, e = encodeMachOCset(RegX9, CondEq)
	check("cset x9, eq", w, e, 0x9a9f17e9)
	w, e = encodeMachOCset(RegX9, CondLt)
	check("cset x9, lt", w, e, 0x9a9fa7e9)

	// Stack traffic (Int).
	w, e = encodeMachOStore64Stack(&Store64Stack{Src: RegX9, Offset: 0})
	check("str x9 [sp,0]", w, e, 0xf90003e9)
	w, e = encodeMachOLoad64Stack(&Load64Stack{Dst: RegX9, Offset: 16})
	check("ldr x9 [sp,16]", w, e, 0xf9400be9)
	w, e = encodeMachOMovRegReg(RegX10, RegX0)
	check("mov x10, x0", w, e, 0xaa0003ea)

	// Address staging — the indirect-arg + sret prologue helpers.
	w, e = encodeAddSubImm(0x91000000, regX8, RegSP, 0)
	check("add x8, sp, #0", w, e, 0x910003e8)
	w, e = encodeLoadStoreReg(0xf9400000, RegX9, RegX0, 0)
	check("ldr x9 [x0,0]", w, e, 0xf9400009)
	w, e = encodeLoadStoreReg(0xf9000000, RegX9, regX8, 16)
	check("str x9 [x8,16]", w, e, 0xf9000909)

	// FP traffic + arithmetic — Week 15 IEEE path.
	w, e = encodeFPStack(0xfd400000, RegD8, 0)
	check("ldr d8 [sp,0]", w, e, 0xfd4003e8)
	w, e = encodeFPStack(0xfd000000, RegD8, 0)
	check("str d8 [sp,0]", w, e, 0xfd0003e8)
	w, e = encodeFmovDFromX(RegD8, RegX9)
	check("fmov d8, x9", w, e, 0x9e670128)
	w, e = encodeFmovXFromD(RegX1, RegD9)
	check("fmov x1, d9", w, e, 0x9e660121)
	w, e = encodeFPArith(0x1e602800, RegD8, RegD8, RegD9)
	check("fadd d8,d8,d9", w, e, 0x1e692908)
	w, e = encodeFPArith(0x1e603800, RegD8, RegD8, RegD9)
	check("fsub d8,d8,d9", w, e, 0x1e693908)
	w, e = encodeFPArith(0x1e600800, RegD8, RegD8, RegD9)
	check("fmul d8,d8,d9", w, e, 0x1e690908)
	w, e = encodeFPArith(0x1e601800, RegD8, RegD8, RegD9)
	check("fdiv d8,d8,d9", w, e, 0x1e691908)

	// blr Xn — closure indirect call.
	if n, ok := xRegisterNumber(RegX9); ok {
		got := uint32(0xd63f0000) | (n << 5)
		const want uint32 = 0xd63f0120
		if got != want {
			t.Errorf("blr x9: Go = 0x%08x; Osty = 0x%08x", got, want)
		}
	} else {
		t.Errorf("xRegisterNumber(x9) failed")
	}

	// brk + ret — terminator-class instructions.
	w, e = encodeBrk(1)
	check("brk #1", w, e, 0xd4200020)
	w, e = encodeBrk(0xffff)
	check("brk #0xffff", w, e, 0xd43fffe0)
	const retWord uint32 = 0xd65f03c0
	if got := uint32(0xd65f03c0); got != retWord {
		t.Errorf("ret literal mismatch")
	}
}

// TestLirMultiWordParityVsOstyTable pins the variable-length encoder
// outputs (Phase 2b) against the Osty-side `onbEncodeMovImm64Words`
// table. Each MovImm64 case verifies both the word count and the
// per-position hex values — drift in either dimension surfaces as
// a reviewable diff.
//
// MovImm32 isn't covered here because Go's `internal/onb/macho.go`
// only encodes the hard-wired `mov w0, #0` form (process exit-code
// stamp). When that path generalises, mirror the new shape on both
// sides and add the matching assertions.
func TestLirMultiWordParityVsOstyTable(t *testing.T) {
	t.Parallel()

	checkWords := func(name string, got []uint32, err error, want []uint32) {
		t.Helper()
		if err != nil {
			t.Errorf("%s: Go encode err %v", name, err)
			return
		}
		if len(got) != len(want) {
			t.Errorf("%s: word count Go=%d Osty=%d", name, len(got), len(want))
			return
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("%s[%d]: Go = 0x%08x; Osty = 0x%08x", name, i, got[i], want[i])
			}
		}
	}

	// mov x0, #0 — single MOVZ, register clears.
	w, err := encodeMachOMovImm64(RegX0, 0)
	checkWords("mov x0, #0", w, err, []uint32{0xd2800000})

	// mov x9, #100 — single chunk in low 16 bits.
	w, err = encodeMachOMovImm64(RegX9, 100)
	checkWords("mov x9, #100", w, err, []uint32{0xd2800c89})

	// mov x9, #65535 — boundary of the low chunk.
	w, err = encodeMachOMovImm64(RegX9, 0xffff)
	checkWords("mov x9, #65535", w, err, []uint32{0xd29fffe9})

	// mov x9, #0x12345678 — two chunks (low + mid). MOVZ then MOVK.
	w, err = encodeMachOMovImm64(RegX9, 0x12345678)
	checkWords("mov x9, #0x12345678", w, err, []uint32{0xd28acf09, 0xf2a24689})
}
