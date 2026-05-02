package onb

import "testing"

// TestEncoderParityVsOstyTable pins the canonical 32-bit aarch64
// instruction word the Go-side `internal/onb/macho.go` encoder
// produces for every entry in the Osty-side
// `toolchain/onb_encoding.osty` parity table. The Osty file is the
// future-canonical source of truth: when the LLVM self-host LLVMgen
// can compile `toolchain/onb_*.osty`, this test guarantees the two
// encoders agree byte-for-byte.
//
// Update protocol: if a Go encoder genuinely changes (new opcode
// flavour, register-class shift, etc.), update both the Go body and
// the matching `assertEncodedEq` line in
// `toolchain/onb_encoding_test.osty` together. A drift in either
// direction is a reviewable diff in this test's expected column.
func TestEncoderParityVsOstyTable(t *testing.T) {
	t.Parallel()

	check := func(name string, got uint32, err error, want uint32) {
		t.Helper()
		if err != nil {
			t.Errorf("%s: Go encode err %v", name, err)
			return
		}
		if got != want {
			t.Errorf("%s: Go encoder = 0x%08x; Osty parity table = 0x%08x", name, got, want)
		}
	}

	// brk imm16 — UnreachableTerm trap. Three sentinel values cover
	// the imm-zero, mid-range, and upper-bound encodings.
	w, e := encodeBrk(1)
	check("brk #1", w, e, 0xd4200020)
	w, e = encodeBrk(0)
	check("brk #0", w, e, 0xd4200000)
	w, e = encodeBrk(0xffff)
	check("brk #0xffff", w, e, 0xd43fffe0)

	// fmov bridges between integer and FP register classes.
	w, e = encodeFmovDFromX(RegD8, RegX9)
	check("fmov d8, x9", w, e, 0x9e670128)
	w, e = encodeFmovXFromD(RegX1, RegD9)
	check("fmov x1, d9", w, e, 0x9e660121)

	// fadd/fsub/fmul/fdiv — IEEE 754 double binary arithmetic.
	w, e = encodeFPArith(0x1e602800, RegD8, RegD8, RegD9)
	check("fadd d8,d8,d9", w, e, 0x1e692908)
	w, e = encodeFPArith(0x1e603800, RegD8, RegD8, RegD9)
	check("fsub d8,d8,d9", w, e, 0x1e693908)
	w, e = encodeFPArith(0x1e600800, RegD8, RegD8, RegD9)
	check("fmul d8,d8,d9", w, e, 0x1e690908)
	w, e = encodeFPArith(0x1e601800, RegD8, RegD8, RegD9)
	check("fdiv d8,d8,d9", w, e, 0x1e691908)

	// FP stack store/load — float local spill and reload.
	w, e = encodeFPStack(0xfd000000, RegD8, 0)
	check("str d8 [sp,0]", w, e, 0xfd0003e8)
	w, e = encodeFPStack(0xfd400000, RegD9, 16)
	check("ldr d9 [sp,16]", w, e, 0xfd400be9)

	// Reg-relative load/store — sret epilogue + indirect arg
	// memcpy through caller-provided pointers.
	w, e = encodeLoadStoreReg(0xf9400000, RegX9, RegX0, 0)
	check("ldr x9 [x0,0]", w, e, 0xf9400009)
	w, e = encodeLoadStoreReg(0xf9000000, RegX9, regX8, 16)
	check("str x9 [x8,16]", w, e, 0xf9000909)

	// mov reg, reg — env-pointer stash before staging captures.
	w, e = encodeMachOMovRegReg(RegX10, RegX0)
	check("mov x10, x0", w, e, 0xaa0003ea)

	// blr Xn — closure indirect branch. Encoder lives inline in
	// `macho.go`'s `case *BranchLinkReg` arm; mirror the same
	// `0xd63f0000 | (n << 5)` formula here so the Osty parity
	// table stays comparable.
	if n, ok := xRegisterNumber(RegX9); ok {
		got := uint32(0xd63f0000) | (n << 5)
		const want uint32 = 0xd63f0120
		if got != want {
			t.Errorf("blr x9: Go = 0x%08x; Osty = 0x%08x", got, want)
		}
	} else {
		t.Errorf("xRegisterNumber(x9) failed")
	}
}
