package onb

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"
)

const (
	machoMagic64                       = 0xfeedfacf
	machoCPUTypeARM64                  = 0x0100000c
	machoCPUSubtypeARM64All            = 0
	machoFileTypeObject                = 1
	machoLCSegment64                   = 0x19
	machoLCSymtab                      = 0x2
	machoLCDysymtab                    = 0xb
	machoMHSubsectionsViaSyms          = 0x2000
	machoVMProtRead                    = 0x1
	machoVMProtExecute                 = 0x4
	machoSectionRegular                = 0x0
	machoSectionCStringLiterals        = 0x2
	machoSectionSomeInstr              = 0x00000400
	machoSectionPureInstr              = 0x80000000
	machoARM64RelocBranch26            = 2
	machoARM64RelocPage21              = 3
	machoARM64RelocPageOff12           = 4
	machoNExt                          = 0x01
	machoNSect                         = 0x0e
	machoHeader64Size                  = 32
	machoSegment64Size                 = 72
	machoSection64Size                 = 80
	machoSymtabCommandSize             = 24
	machoDysymtabCommandSize           = 80
	machoNlist64Size                   = 16
	machoRelocSize                     = 8
	machoTextAlignPower         uint32 = 2
	machoCStringSectionNumber   uint8  = 2
	machoTextSectionNumber      uint8  = 1
)

func emitMachOObject(program *Program) ([]byte, error) {
	// The minimal path only knows how to encode `mov w0, #0; ret` — it has
	// neither prologue/epilogue nor any of Phase A2's new opcodes. The
	// cstring-relocs path handles the full opcode set. Use the minimal path
	// only for the legacy `fn main() {}` shape (no cstrings, no frame).
	if len(program.CStrings) == 0 && len(program.Functions) == 1 && program.Functions[0].FrameSize == 0 {
		return emitMinimalMachOObject(program)
	}
	if len(program.CStrings) == 0 {
		// Still no cstrings but we have a frame — synthesise an empty
		// cstring-relocs encoding so the prologue path takes over.
		return emitMachOObjectWithCStringRelocs(program)
	}
	return emitMachOObjectWithCStringRelocs(program)
}

func emitMinimalMachOObject(program *Program) ([]byte, error) {
	code, err := encodeMachOText(program)
	if err != nil {
		return nil, err
	}

	sizeofcmds := uint32(machoSegment64Size + machoSection64Size + machoSymtabCommandSize)
	textOffset := uint32(machoHeader64Size) + sizeofcmds
	symoff := textOffset + uint32(len(code))
	stroff := symoff + machoNlist64Size
	strtab := []byte{0, '_', 'm', 'a', 'i', 'n', 0}

	var b bytes.Buffer
	writeU32(&b, machoMagic64)
	writeU32(&b, machoCPUTypeARM64)
	writeU32(&b, machoCPUSubtypeARM64All)
	writeU32(&b, machoFileTypeObject)
	writeU32(&b, 2)
	writeU32(&b, sizeofcmds)
	writeU32(&b, machoMHSubsectionsViaSyms)
	writeU32(&b, 0)

	writeU32(&b, machoLCSegment64)
	writeU32(&b, machoSegment64Size+machoSection64Size)
	writeName16(&b, "")
	writeU64(&b, 0)
	writeU64(&b, uint64(len(code)))
	writeU64(&b, uint64(textOffset))
	writeU64(&b, uint64(len(code)))
	writeU32(&b, machoVMProtRead|machoVMProtExecute)
	writeU32(&b, machoVMProtRead|machoVMProtExecute)
	writeU32(&b, 1)
	writeU32(&b, 0)

	writeName16(&b, "__text")
	writeName16(&b, "__TEXT")
	writeU64(&b, 0)
	writeU64(&b, uint64(len(code)))
	writeU32(&b, textOffset)
	writeU32(&b, machoTextAlignPower)
	writeU32(&b, 0)
	writeU32(&b, 0)
	writeU32(&b, machoSectionRegular|machoSectionSomeInstr|machoSectionPureInstr)
	writeU32(&b, 0)
	writeU32(&b, 0)
	writeU32(&b, 0)

	writeU32(&b, machoLCSymtab)
	writeU32(&b, machoSymtabCommandSize)
	writeU32(&b, symoff)
	writeU32(&b, 1)
	writeU32(&b, stroff)
	writeU32(&b, uint32(len(strtab)))

	if b.Len() != int(textOffset) {
		return nil, fmt.Errorf("onb: internal Mach-O layout mismatch: header=%d textOffset=%d", b.Len(), textOffset)
	}
	b.Write(code)

	writeU32(&b, 1)
	b.WriteByte(machoNExt | machoNSect)
	b.WriteByte(1)
	writeU16(&b, 0)
	writeU64(&b, 0)
	b.Write(strtab)
	return b.Bytes(), nil
}

type machoReloc struct {
	address   uint32
	symbolnum uint32
	pcrel     bool
	length    uint8
	extern    bool
	typ       uint8
}

type machoStringTable struct {
	data []byte
}

func newMachOStringTable() *machoStringTable {
	return &machoStringTable{data: []byte{0}}
}

func (s *machoStringTable) add(name string) uint32 {
	off := uint32(len(s.data))
	s.data = append(s.data, name...)
	s.data = append(s.data, 0)
	return off
}

type machoTextEncoding struct {
	code            []byte
	relocs          []machoReloc
	externalSymbols []string
	// fnOffsets is the byte offset within __text where each lowered
	// function's code starts. Used by the symbol table emitter to point
	// each defined function symbol at its body. Order tracks
	// program.Functions so the symtab stays deterministic.
	fnOffsets []uint64
}

func emitMachOObjectWithCStringRelocs(program *Program) ([]byte, error) {
	cstringIndex := make(map[string]uint32, len(program.CStrings))
	for i, cstr := range program.CStrings {
		if cstr.Label == "" {
			return nil, fmt.Errorf("onb: Mach-O string literal without label")
		}
		if _, exists := cstringIndex[cstr.Label]; exists {
			return nil, fmt.Errorf("onb: duplicate Mach-O string literal label %q", cstr.Label)
		}
		cstringIndex[cstr.Label] = uint32(i)
	}
	enc, err := encodeMachOTextWithRelocs(program, cstringIndex)
	if err != nil {
		return nil, err
	}
	sort.Slice(enc.relocs, func(i, j int) bool {
		return enc.relocs[i].address > enc.relocs[j].address
	})

	sizeofcmds := uint32(machoSegment64Size + 2*machoSection64Size + machoSymtabCommandSize + machoDysymtabCommandSize)
	textOffset := uint32(machoHeader64Size) + sizeofcmds
	cstringOffset := textOffset + uint32(len(enc.code))
	cstringData, cstringAddrs := encodeMachOCStrings(program.CStrings, uint64(len(enc.code)))
	relocOffset := alignUp(cstringOffset+uint32(len(cstringData)), 4)
	symoff := relocOffset + uint32(len(enc.relocs))*machoRelocSize
	nsyms := uint32(len(program.CStrings) + len(program.Functions) + len(enc.externalSymbols))
	stroff := symoff + nsyms*machoNlist64Size
	strtab := newMachOStringTable()

	var b bytes.Buffer
	writeU32(&b, machoMagic64)
	writeU32(&b, machoCPUTypeARM64)
	writeU32(&b, machoCPUSubtypeARM64All)
	writeU32(&b, machoFileTypeObject)
	writeU32(&b, 3)
	writeU32(&b, sizeofcmds)
	writeU32(&b, machoMHSubsectionsViaSyms)
	writeU32(&b, 0)

	segmentSize := uint64(len(enc.code) + len(cstringData))
	writeU32(&b, machoLCSegment64)
	writeU32(&b, machoSegment64Size+2*machoSection64Size)
	writeName16(&b, "")
	writeU64(&b, 0)
	writeU64(&b, segmentSize)
	writeU64(&b, uint64(textOffset))
	writeU64(&b, segmentSize)
	writeU32(&b, machoVMProtRead|machoVMProtExecute)
	writeU32(&b, machoVMProtRead|machoVMProtExecute)
	writeU32(&b, 2)
	writeU32(&b, 0)

	writeName16(&b, "__text")
	writeName16(&b, "__TEXT")
	writeU64(&b, 0)
	writeU64(&b, uint64(len(enc.code)))
	writeU32(&b, textOffset)
	writeU32(&b, machoTextAlignPower)
	writeU32(&b, relocOffset)
	writeU32(&b, uint32(len(enc.relocs)))
	writeU32(&b, machoSectionRegular|machoSectionSomeInstr|machoSectionPureInstr)
	writeU32(&b, 0)
	writeU32(&b, 0)
	writeU32(&b, 0)

	writeName16(&b, "__cstring")
	writeName16(&b, "__TEXT")
	writeU64(&b, uint64(len(enc.code)))
	writeU64(&b, uint64(len(cstringData)))
	writeU32(&b, cstringOffset)
	writeU32(&b, 0)
	writeU32(&b, 0)
	writeU32(&b, 0)
	writeU32(&b, machoSectionCStringLiterals)
	writeU32(&b, 0)
	writeU32(&b, 0)
	writeU32(&b, 0)

	writeU32(&b, machoLCSymtab)
	writeU32(&b, machoSymtabCommandSize)
	writeU32(&b, symoff)
	writeU32(&b, nsyms)
	writeU32(&b, stroff)

	strtabLenOffset := b.Len()
	writeU32(&b, 0)

	writeU32(&b, machoLCDysymtab)
	writeU32(&b, machoDysymtabCommandSize)
	writeU32(&b, 0)
	writeU32(&b, uint32(len(program.CStrings)))
	writeU32(&b, uint32(len(program.CStrings)))
	writeU32(&b, uint32(len(program.Functions)))
	writeU32(&b, uint32(len(program.CStrings)+len(program.Functions)))
	writeU32(&b, uint32(len(enc.externalSymbols)))
	for i := 0; i < 12; i++ {
		writeU32(&b, 0)
	}

	if b.Len() != int(textOffset) {
		return nil, fmt.Errorf("onb: internal Mach-O layout mismatch: header=%d textOffset=%d", b.Len(), textOffset)
	}
	b.Write(enc.code)
	b.Write(cstringData)
	writePadding(&b, int(relocOffset)-b.Len())
	if b.Len() != int(relocOffset) {
		return nil, fmt.Errorf("onb: internal Mach-O layout mismatch: reloc=%d relocOffset=%d", b.Len(), relocOffset)
	}
	for _, reloc := range enc.relocs {
		writeMachOReloc(&b, reloc)
	}
	if b.Len() != int(symoff) {
		return nil, fmt.Errorf("onb: internal Mach-O layout mismatch: sym=%d symoff=%d", b.Len(), symoff)
	}
	for _, cstr := range program.CStrings {
		writeMachONlist64(&b, strtab.add(asmCStringLabel(program.Target, cstr.Label)), machoNSect, machoCStringSectionNumber, 0, cstringAddrs[cstr.Label])
	}
	for i, fn := range program.Functions {
		var fnOffset uint64
		if i < len(enc.fnOffsets) {
			fnOffset = enc.fnOffsets[i]
		}
		writeMachONlist64(&b, strtab.add(asmSymbolName(program.Target, fn.Name)), machoNExt|machoNSect, machoTextSectionNumber, 0, fnOffset)
	}
	for _, symbol := range enc.externalSymbols {
		writeMachONlist64(&b, strtab.add(asmSymbolName(program.Target, symbol)), machoNExt, 0, 0, 0)
	}
	if b.Len() != int(stroff) {
		return nil, fmt.Errorf("onb: internal Mach-O layout mismatch: str=%d stroff=%d", b.Len(), stroff)
	}
	b.Write(strtab.data)
	out := b.Bytes()
	binary.LittleEndian.PutUint32(out[strtabLenOffset:strtabLenOffset+4], uint32(len(strtab.data)))
	return out, nil
}

func encodeMachOCStrings(cstrings []CStringLiteral, baseAddr uint64) ([]byte, map[string]uint64) {
	var out []byte
	addrs := make(map[string]uint64, len(cstrings))
	for _, cstr := range cstrings {
		addrs[cstr.Label] = baseAddr + uint64(len(out))
		out = append(out, cstr.Value...)
		out = append(out, 0)
	}
	return out, addrs
}

func encodeMachOTextWithRelocs(program *Program, cstringIndex map[string]uint32) (machoTextEncoding, error) {
	if len(program.Functions) == 0 {
		return machoTextEncoding{}, fmt.Errorf("onb: Mach-O encoder requires at least one function")
	}
	if program.Functions[0].Name != "main" {
		return machoTextEncoding{}, fmt.Errorf("onb: Mach-O encoder requires main as the first function")
	}
	// Pre-build the local-function symbol index. BranchLink targets that
	// resolve here use the local symtab slot; only truly external symbols
	// (printf, puts, ...) flow into externalSymbols. Without this guard a
	// `bl _add` would create both a defined symbol (from the function) and
	// an undefined symbol (from the branch reloc), which Mach-O rejects.
	localFnIndex := map[string]uint32{}
	for i, fn := range program.Functions {
		// nextdefsym order matches program.Functions order: cstrings come
		// first in the symtab, so each fn slot is len(cstrings) + i.
		localFnIndex[fn.Name] = uint32(len(cstringIndex) + i)
	}
	externalIndex := map[string]uint32{}
	var enc machoTextEncoding
	for _, fn := range program.Functions {
		startOffset := uint64(len(enc.code))
		enc.fnOffsets = append(enc.fnOffsets, startOffset)
		if err := encodeMachOFunction(&enc, fn, cstringIndex, localFnIndex, externalIndex, len(program.Functions)); err != nil {
			return machoTextEncoding{}, err
		}
	}
	return enc, nil
}

// machoBranchFixup records a placeholder branch instruction's location and
// the original (MIR) block index it should jump to. The encoder writes a
// zero-immediate branch first, walks all blocks to learn each block's start
// offset, then patches the immediates in a second pass.
type machoBranchFixup struct {
	codeOffset  uint32 // byte offset within enc.code where the branch lives
	targetBlock int    // original MIR block index of the jump target
	kind        machoBranchKind
}

type machoBranchKind uint8

const (
	machoBranchUncond      machoBranchKind = iota // `b imm26`
	machoBranchCondNotZero                        // `cbnz Xt, imm19`
)

// encodeMachOFunction appends one lowered function's prologue, body, and
// epilogue to enc.code, threading any new relocations through enc.relocs and
// any new external symbols through enc.externalSymbols + externalIndex. It
// also performs the branch-fixup pass: each `Branch` / `BranchCondNotZero`
// emits a placeholder instruction word and a fixup record, then a final
// pass patches the immediate using the recorded per-block start offset.
func encodeMachOFunction(enc *machoTextEncoding, fn Function, cstringIndex, localFnIndex, externalIndex map[string]uint32, totalFns int) error {
	if len(fn.Blocks) == 0 {
		return fmt.Errorf("%w: Mach-O encoder requires at least one block", ErrUnsupportedShape)
	}
	fnStart := uint32(len(enc.code))
	blockOffsets := map[int]uint32{}
	var fixups []machoBranchFixup
	frameSize := functionFrameSize(fn)
	fpOffset := functionFPOffset(fn)
	if frameSize > 0 {
		if fpOffset < 0 {
			enc.code = appendU32LE(enc.code, 0xa9bf7bfd) // stp x29, x30, [sp, #-16]!
			enc.code = appendU32LE(enc.code, 0x910003fd) // mov x29, sp
		} else {
			subWord, err := encodeAddSubImm(0xd1000000, RegSP, RegSP, uint64(frameSize))
			if err != nil {
				return fmt.Errorf("onb: prologue sub sp: %w", err)
			}
			enc.code = appendU32LE(enc.code, subWord)
			stpWord, err := encodeStpFPLR(uint32(fpOffset))
			if err != nil {
				return fmt.Errorf("onb: prologue stp: %w", err)
			}
			enc.code = appendU32LE(enc.code, stpWord)
			addWord, err := encodeAddSubImm(0x91000000, RegX29, RegSP, uint64(fpOffset))
			if err != nil {
				return fmt.Errorf("onb: prologue fp: %w", err)
			}
			enc.code = appendU32LE(enc.code, addWord)
		}
	}
	for _, block := range fn.Blocks {
		blockOffsets[block.OriginalIndex] = uint32(len(enc.code))
		for _, instr := range block.Instrs {
			switch i := instr.(type) {
			case *LoadCStringAddress:
				if i.Dst != RegX0 {
					return fmt.Errorf("%w: Mach-O encoder only supports cstring loads into x0", ErrNotImplemented)
				}
				sym, ok := cstringIndex[i.Label]
				if !ok {
					return fmt.Errorf("onb: unknown Mach-O string literal label %q", i.Label)
				}
				addr := uint32(len(enc.code))
				enc.code = appendU32LE(enc.code, 0x90000000)
				enc.relocs = append(enc.relocs, machoReloc{address: addr, symbolnum: sym, pcrel: true, length: 2, extern: true, typ: machoARM64RelocPage21})
				addr = uint32(len(enc.code))
				enc.code = appendU32LE(enc.code, 0x91000000)
				enc.relocs = append(enc.relocs, machoReloc{address: addr, symbolnum: sym, length: 2, extern: true, typ: machoARM64RelocPageOff12})
			case *BranchLink:
				if i.Symbol == "" {
					return fmt.Errorf("onb: Mach-O branch without symbol")
				}
				sym, isLocal := localFnIndex[i.Symbol]
				if !isLocal {
					ext, ok := externalIndex[i.Symbol]
					if !ok {
						ext = uint32(len(cstringIndex) + totalFns + len(enc.externalSymbols))
						externalIndex[i.Symbol] = ext
						enc.externalSymbols = append(enc.externalSymbols, i.Symbol)
					}
					sym = ext
				}
				addr := uint32(len(enc.code))
				enc.code = appendU32LE(enc.code, 0x94000000)
				enc.relocs = append(enc.relocs, machoReloc{address: addr, symbolnum: sym, pcrel: true, length: 2, extern: true, typ: machoARM64RelocBranch26})
			case *MovImm32:
				if i.Dst != RegW0 || i.Imm != 0 {
					return fmt.Errorf("onb: Mach-O phase 1 only supports mov w0, #0")
				}
				enc.code = append(enc.code, 0x00, 0x00, 0x80, 0x52)
			case *MovImm64:
				words, err := encodeMachOMovImm64(i.Dst, uint64(i.Imm))
				if err != nil {
					return err
				}
				for _, word := range words {
					enc.code = appendU32LE(enc.code, word)
				}
			case *MovRegReg:
				word, err := encodeMachOMovRegReg(i.Dst, i.Src)
				if err != nil {
					return err
				}
				enc.code = appendU32LE(enc.code, word)
			case *Store64Stack:
				word, err := encodeMachOStore64Stack(i)
				if err != nil {
					return err
				}
				enc.code = appendU32LE(enc.code, word)
			case *Load64Stack:
				word, err := encodeMachOLoad64Stack(i)
				if err != nil {
					return err
				}
				enc.code = appendU32LE(enc.code, word)
			case *AddReg:
				word, err := encodeMachOArithReg(0x8b000000, i.Dst, i.Lhs, i.Rhs)
				if err != nil {
					return err
				}
				enc.code = appendU32LE(enc.code, word)
			case *SubReg:
				word, err := encodeMachOArithReg(0xcb000000, i.Dst, i.Lhs, i.Rhs)
				if err != nil {
					return err
				}
				enc.code = appendU32LE(enc.code, word)
			case *MulReg:
				word, err := encodeMachOMulReg(i.Dst, i.Lhs, i.Rhs)
				if err != nil {
					return err
				}
				enc.code = appendU32LE(enc.code, word)
			case *Cmp:
				word, err := encodeMachOCmp(i.Lhs, i.Rhs)
				if err != nil {
					return err
				}
				enc.code = appendU32LE(enc.code, word)
			case *Cset:
				word, err := encodeMachOCset(i.Dst, i.Cond)
				if err != nil {
					return err
				}
				enc.code = appendU32LE(enc.code, word)
			case *Branch:
				fixups = append(fixups, machoBranchFixup{
					codeOffset:  uint32(len(enc.code)),
					targetBlock: i.Target,
					kind:        machoBranchUncond,
				})
				enc.code = appendU32LE(enc.code, 0x14000000) // placeholder b imm26=0
			case *BranchCondNotZero:
				fixups = append(fixups, machoBranchFixup{
					codeOffset:  uint32(len(enc.code)),
					targetBlock: i.Target,
					kind:        machoBranchCondNotZero,
				})
				cbnzWord, err := encodeMachOCbnzPlaceholder(i.Src)
				if err != nil {
					return err
				}
				enc.code = appendU32LE(enc.code, cbnzWord)
			case *Ret:
				if frameSize > 0 {
					if fpOffset < 0 {
						enc.code = appendU32LE(enc.code, 0xa8c17bfd) // ldp x29, x30, [sp], #16
					} else {
						ldpWord, err := encodeLdpFPLR(uint32(fpOffset))
						if err != nil {
							return fmt.Errorf("onb: epilogue ldp: %w", err)
						}
						enc.code = appendU32LE(enc.code, ldpWord)
						addWord, err := encodeAddSubImm(0x91000000, RegSP, RegSP, uint64(frameSize))
						if err != nil {
							return fmt.Errorf("onb: epilogue add sp: %w", err)
						}
						enc.code = appendU32LE(enc.code, addWord)
					}
				}
				enc.code = append(enc.code, 0xc0, 0x03, 0x5f, 0xd6)
			default:
				return fmt.Errorf("%w: Mach-O encoder does not support %T", ErrNotImplemented, instr)
			}
		}
	}
	// Branch fixup pass — every placeholder branch now knows its target's
	// byte offset and we can patch the immediate in place.
	for _, fx := range fixups {
		target, ok := blockOffsets[fx.targetBlock]
		if !ok {
			return fmt.Errorf("onb: branch target block %d not found in fn %s", fx.targetBlock, fn.Name)
		}
		displacement := int32(target) - int32(fx.codeOffset)
		if err := patchMachOBranch(enc.code, fx, displacement); err != nil {
			return err
		}
	}
	_ = fnStart
	return nil
}

// patchMachOBranch rewrites the placeholder branch instruction at fx.codeOffset
// with the correct PC-relative immediate.
func patchMachOBranch(code []byte, fx machoBranchFixup, displacement int32) error {
	if displacement%4 != 0 {
		return fmt.Errorf("onb: branch displacement %d is not 4-aligned", displacement)
	}
	imm := displacement / 4
	off := fx.codeOffset
	switch fx.kind {
	case machoBranchUncond:
		// b imm26 — 26-bit signed immediate
		if imm < -(1<<25) || imm >= (1<<25) {
			return fmt.Errorf("onb: b imm26 out of range (%d)", imm)
		}
		word := uint32(0x14000000) | (uint32(imm) & 0x03ffffff)
		writeU32LE(code, off, word)
	case machoBranchCondNotZero:
		// cbnz Xt, imm19 — 19-bit signed immediate
		if imm < -(1<<18) || imm >= (1<<18) {
			return fmt.Errorf("onb: cbnz imm19 out of range (%d)", imm)
		}
		// Preserve the Xt field (lower 5 bits) emitted earlier; only the
		// imm19 field at bits [23:5] needs patching.
		old := readU32LE(code, off)
		word := (old &^ (uint32(0x7ffff) << 5)) | ((uint32(imm) & 0x7ffff) << 5)
		writeU32LE(code, off, word)
	default:
		return fmt.Errorf("onb: unknown branch fixup kind %d", fx.kind)
	}
	return nil
}

func writeU32LE(buf []byte, off uint32, v uint32) {
	buf[off] = byte(v)
	buf[off+1] = byte(v >> 8)
	buf[off+2] = byte(v >> 16)
	buf[off+3] = byte(v >> 24)
}

func readU32LE(buf []byte, off uint32) uint32 {
	return uint32(buf[off]) | uint32(buf[off+1])<<8 | uint32(buf[off+2])<<16 | uint32(buf[off+3])<<24
}

// encodeMachOCmp encodes `cmp Xn, Xm` (alias for SUBS XZR, Xn, Xm).
//
//	layout: 0xeb00001f | (Rm << 16) | (Rn << 5)
func encodeMachOCmp(lhs, rhs Reg) (uint32, error) {
	n, ok := xRegisterNumber(lhs)
	if !ok {
		return 0, fmt.Errorf("%w: Mach-O cmp lhs %s", ErrNotImplemented, lhs)
	}
	m, ok := xRegisterNumber(rhs)
	if !ok {
		return 0, fmt.Errorf("%w: Mach-O cmp rhs %s", ErrNotImplemented, rhs)
	}
	return 0xeb00001f | (m << 16) | (n << 5), nil
}

// encodeMachOCset encodes `cset Xd, <cond>` (alias for CSINC Xd, XZR, XZR, !cond).
//
//	layout: 0x9a9f07e0 | ((cond ^ 1) << 12) | Rd
//
// The condition field is inverted because CSINC writes Xn when cond holds
// and Xm+1 otherwise; with both source registers as XZR (= 31), the result
// is 0 when cond holds and 1 otherwise. To get 1-when-cond we invert.
func encodeMachOCset(dst Reg, cond Cond) (uint32, error) {
	d, ok := xRegisterNumber(dst)
	if !ok {
		return 0, fmt.Errorf("%w: Mach-O cset dst %s", ErrNotImplemented, dst)
	}
	return 0x9a9f07e0 | ((uint32(cond) ^ 1) << 12) | d, nil
}

// encodeMachOCbnzPlaceholder emits `cbnz Xt, #0` — the imm19 stays zero
// until the fixup pass patches it.
//
//	layout: 0xb5000000 | (imm19 << 5) | Rt
func encodeMachOCbnzPlaceholder(src Reg) (uint32, error) {
	t, ok := xRegisterNumber(src)
	if !ok {
		return 0, fmt.Errorf("%w: Mach-O cbnz src %s", ErrNotImplemented, src)
	}
	return 0xb5000000 | t, nil
}

func encodeMachOStore64Stack(instr *Store64Stack) (uint32, error) {
	if instr.Offset < 0 || instr.Offset%8 != 0 {
		return 0, fmt.Errorf("%w: Mach-O stack store offset %d", ErrNotImplemented, instr.Offset)
	}
	reg, ok := xRegisterNumber(instr.Src)
	if !ok {
		return 0, fmt.Errorf("%w: Mach-O stack store source %s", ErrNotImplemented, instr.Src)
	}
	scaled := uint32(instr.Offset / 8)
	if scaled > 0xfff {
		return 0, fmt.Errorf("%w: Mach-O stack store offset %d", ErrNotImplemented, instr.Offset)
	}
	return 0xf90003e0 | (scaled << 10) | reg, nil
}

func encodeMachOLoad64Stack(instr *Load64Stack) (uint32, error) {
	if instr.Offset < 0 || instr.Offset%8 != 0 {
		return 0, fmt.Errorf("%w: Mach-O stack load offset %d", ErrNotImplemented, instr.Offset)
	}
	reg, ok := xRegisterNumber(instr.Dst)
	if !ok {
		return 0, fmt.Errorf("%w: Mach-O stack load dest %s", ErrNotImplemented, instr.Dst)
	}
	scaled := uint32(instr.Offset / 8)
	if scaled > 0xfff {
		return 0, fmt.Errorf("%w: Mach-O stack load offset %d", ErrNotImplemented, instr.Offset)
	}
	// LDR Xt, [SP, #imm]: 0xf94003e0 | (imm12 << 10) | Rt
	return 0xf94003e0 | (scaled << 10) | reg, nil
}

func encodeMachOMovRegReg(dst, src Reg) (uint32, error) {
	dstNum, ok := xRegisterNumber(dst)
	if !ok {
		return 0, fmt.Errorf("%w: Mach-O mov dst %s", ErrNotImplemented, dst)
	}
	srcNum, ok := xRegisterNumber(src)
	if !ok {
		return 0, fmt.Errorf("%w: Mach-O mov src %s", ErrNotImplemented, src)
	}
	// MOV Xd, Xm is the alias for ORR Xd, XZR, Xm:
	// 0xaa0003e0 | (Xm << 16) | Xd. XZR encodes as 31.
	return 0xaa0003e0 | (srcNum << 16) | dstNum, nil
}

// encodeMachOArithReg encodes ADD/SUB (shifted register) for 64-bit registers
// with no shift. Base is 0x8b000000 (ADD) or 0xcb000000 (SUB).
//
//	ADD Xd, Xn, Xm : sf=1, op=0, S=0, shift=0, imm6=0
//	SUB Xd, Xn, Xm : sf=1, op=1, S=0
//	layout: base | (Rm << 16) | (Rn << 5) | Rd
func encodeMachOArithReg(base uint32, dst, lhs, rhs Reg) (uint32, error) {
	d, ok := xRegisterNumber(dst)
	if !ok {
		return 0, fmt.Errorf("%w: Mach-O arith dst %s", ErrNotImplemented, dst)
	}
	n, ok := xRegisterNumber(lhs)
	if !ok {
		return 0, fmt.Errorf("%w: Mach-O arith lhs %s", ErrNotImplemented, lhs)
	}
	m, ok := xRegisterNumber(rhs)
	if !ok {
		return 0, fmt.Errorf("%w: Mach-O arith rhs %s", ErrNotImplemented, rhs)
	}
	return base | (m << 16) | (n << 5) | d, nil
}

// encodeMachOMulReg encodes MUL Xd, Xn, Xm — alias for MADD Xd, Xn, Xm, XZR.
//
//	layout: 0x9b007c00 | (Rm << 16) | (Rn << 5) | Rd  (Ra = 31 = XZR)
func encodeMachOMulReg(dst, lhs, rhs Reg) (uint32, error) {
	d, ok := xRegisterNumber(dst)
	if !ok {
		return 0, fmt.Errorf("%w: Mach-O mul dst %s", ErrNotImplemented, dst)
	}
	n, ok := xRegisterNumber(lhs)
	if !ok {
		return 0, fmt.Errorf("%w: Mach-O mul lhs %s", ErrNotImplemented, lhs)
	}
	m, ok := xRegisterNumber(rhs)
	if !ok {
		return 0, fmt.Errorf("%w: Mach-O mul rhs %s", ErrNotImplemented, rhs)
	}
	return 0x9b007c00 | (m << 16) | (n << 5) | d, nil
}

// encodeAddSubImm encodes ADD/SUB (immediate) for 64-bit registers with no
// left-shift. Base is 0x91000000 (ADD) or 0xd1000000 (SUB). imm must fit in
// 12 unsigned bits.
//
//	layout: base | (imm12 << 10) | (Rn << 5) | Rd
//
// Accepts SP / X29 / X30 in addition to the regular X registers because the
// prologue and epilogue plumb FP/SP through these helpers.
func encodeAddSubImm(base uint32, dst, src Reg, imm uint64) (uint32, error) {
	if imm > 0xfff {
		return 0, fmt.Errorf("%w: imm %d does not fit in 12 bits", ErrNotImplemented, imm)
	}
	d, ok := aarch64RegEncoding(dst)
	if !ok {
		return 0, fmt.Errorf("%w: addsub dst %s", ErrNotImplemented, dst)
	}
	n, ok := aarch64RegEncoding(src)
	if !ok {
		return 0, fmt.Errorf("%w: addsub src %s", ErrNotImplemented, src)
	}
	return base | (uint32(imm) << 10) | (n << 5) | d, nil
}

// encodeStpFPLR encodes STP X29, X30, [SP, #fpOffset] (signed offset variant).
// fpOffset must be a non-negative multiple of 8 in [0, 504].
//
//	layout: 0xa9000000 | (imm7 << 15) | (X30 << 10) | (SP << 5) | X29
//	      = 0xa9007bfd | (imm7 << 15)
func encodeStpFPLR(fpOffset uint32) (uint32, error) {
	if fpOffset%8 != 0 {
		return 0, fmt.Errorf("%w: stp offset %d not 8-aligned", ErrNotImplemented, fpOffset)
	}
	imm := fpOffset / 8
	if imm > 0x3f {
		return 0, fmt.Errorf("%w: stp offset %d exceeds signed-7-bit range", ErrNotImplemented, fpOffset)
	}
	return 0xa9007bfd | (imm << 15), nil
}

// encodeLdpFPLR encodes LDP X29, X30, [SP, #fpOffset] (signed offset variant).
//
//	layout: 0xa9400000 | (imm7 << 15) | (X30 << 10) | (SP << 5) | X29
//	      = 0xa9407bfd | (imm7 << 15)
func encodeLdpFPLR(fpOffset uint32) (uint32, error) {
	if fpOffset%8 != 0 {
		return 0, fmt.Errorf("%w: ldp offset %d not 8-aligned", ErrNotImplemented, fpOffset)
	}
	imm := fpOffset / 8
	if imm > 0x3f {
		return 0, fmt.Errorf("%w: ldp offset %d exceeds signed-7-bit range", ErrNotImplemented, fpOffset)
	}
	return 0xa9407bfd | (imm << 15), nil
}

func encodeMachOMovImm64(dst Reg, imm uint64) ([]uint32, error) {
	reg, ok := xRegisterNumber(dst)
	if !ok {
		return nil, fmt.Errorf("%w: Mach-O mov64 destination %s", ErrNotImplemented, dst)
	}
	var out []uint32
	for shift := 0; shift < 64; shift += 16 {
		chunk := uint32((imm >> shift) & 0xffff)
		if chunk == 0 && len(out) != 0 {
			continue
		}
		hw := uint32(shift / 16)
		if len(out) == 0 {
			out = append(out, 0xd2800000|(hw<<21)|(chunk<<5)|reg)
			continue
		}
		out = append(out, 0xf2800000|(hw<<21)|(chunk<<5)|reg)
	}
	if len(out) == 0 {
		out = append(out, 0xd2800000|reg)
	}
	return out, nil
}

func encodeMachOText(program *Program) ([]byte, error) {
	if len(program.Functions) != 1 || program.Functions[0].Name != "main" {
		return nil, fmt.Errorf("onb: Mach-O phase 1.0 only supports main")
	}
	fn := program.Functions[0]
	if len(fn.Blocks) != 1 {
		return nil, fmt.Errorf("onb: Mach-O phase 1.0 only supports one block")
	}
	var out []byte
	for _, instr := range fn.Blocks[0].Instrs {
		switch i := instr.(type) {
		case *MovImm32:
			if i.Dst != RegW0 || i.Imm != 0 {
				return nil, fmt.Errorf("onb: Mach-O phase 1.0 only supports mov w0, #0")
			}
			out = append(out, 0x00, 0x00, 0x80, 0x52)
		case *Ret:
			out = append(out, 0xc0, 0x03, 0x5f, 0xd6)
		default:
			return nil, fmt.Errorf("%w: Mach-O encoder does not support %T", ErrNotImplemented, instr)
		}
	}
	return out, nil
}

func appendU32LE(out []byte, v uint32) []byte {
	return append(out, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

func alignUp(v, align uint32) uint32 {
	if align == 0 {
		return v
	}
	rem := v % align
	if rem == 0 {
		return v
	}
	return v + align - rem
}

func writePadding(b *bytes.Buffer, n int) {
	for i := 0; i < n; i++ {
		b.WriteByte(0)
	}
}

func writeMachOReloc(b *bytes.Buffer, reloc machoReloc) {
	writeU32(b, reloc.address)
	word := reloc.symbolnum & 0x00ffffff
	if reloc.pcrel {
		word |= 1 << 24
	}
	word |= uint32(reloc.length&0x3) << 25
	if reloc.extern {
		word |= 1 << 27
	}
	word |= uint32(reloc.typ&0xf) << 28
	writeU32(b, word)
}

func writeMachONlist64(b *bytes.Buffer, strx uint32, typ uint8, sect uint8, desc uint16, value uint64) {
	writeU32(b, strx)
	b.WriteByte(typ)
	b.WriteByte(sect)
	writeU16(b, desc)
	writeU64(b, value)
}

func writeU16(b *bytes.Buffer, v uint16) {
	_ = binary.Write(b, binary.LittleEndian, v)
}

func writeU32(b *bytes.Buffer, v uint32) {
	_ = binary.Write(b, binary.LittleEndian, v)
}

func writeU64(b *bytes.Buffer, v uint64) {
	_ = binary.Write(b, binary.LittleEndian, v)
}

func writeName16(b *bytes.Buffer, name string) {
	var buf [16]byte
	copy(buf[:], []byte(name))
	b.Write(buf[:])
}
