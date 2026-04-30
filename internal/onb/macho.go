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
	if len(program.CStrings) == 0 {
		return emitMinimalMachOObject(program)
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
	nsyms := uint32(len(program.CStrings) + 1 + len(enc.externalSymbols))
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
	writeU32(&b, 1)
	writeU32(&b, uint32(len(program.CStrings)+1))
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
	writeMachONlist64(&b, strtab.add(asmSymbolName(program.Target, "main")), machoNExt|machoNSect, machoTextSectionNumber, 0, 0)
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
	if len(program.Functions) != 1 || program.Functions[0].Name != "main" {
		return machoTextEncoding{}, fmt.Errorf("onb: Mach-O phase 1 only supports main")
	}
	fn := program.Functions[0]
	if len(fn.Blocks) != 1 {
		return machoTextEncoding{}, fmt.Errorf("onb: Mach-O phase 1 only supports one block")
	}
	externalIndex := map[string]uint32{}
	var enc machoTextEncoding
	needsFrame := functionNeedsFrame(fn)
	needsStackArgs := functionNeedsStackArgs(fn)
	if needsFrame {
		if needsStackArgs {
			enc.code = appendU32LE(enc.code, 0xd10083ff) // sub sp, sp, #32
			enc.code = appendU32LE(enc.code, 0xa9017bfd) // stp x29, x30, [sp, #16]
			enc.code = appendU32LE(enc.code, 0x910043fd) // add x29, sp, #16
		} else {
			enc.code = appendU32LE(enc.code, 0xa9bf7bfd) // stp x29, x30, [sp, #-16]!
			enc.code = appendU32LE(enc.code, 0x910003fd) // mov x29, sp
		}
	}
	for _, instr := range fn.Blocks[0].Instrs {
		switch i := instr.(type) {
		case *LoadCStringAddress:
			if i.Dst != RegX0 {
				return machoTextEncoding{}, fmt.Errorf("%w: Mach-O encoder only supports cstring loads into x0", ErrNotImplemented)
			}
			sym, ok := cstringIndex[i.Label]
			if !ok {
				return machoTextEncoding{}, fmt.Errorf("onb: unknown Mach-O string literal label %q", i.Label)
			}
			addr := uint32(len(enc.code))
			enc.code = appendU32LE(enc.code, 0x90000000)
			enc.relocs = append(enc.relocs, machoReloc{address: addr, symbolnum: sym, pcrel: true, length: 2, extern: true, typ: machoARM64RelocPage21})
			addr = uint32(len(enc.code))
			enc.code = appendU32LE(enc.code, 0x91000000)
			enc.relocs = append(enc.relocs, machoReloc{address: addr, symbolnum: sym, length: 2, extern: true, typ: machoARM64RelocPageOff12})
		case *BranchLink:
			if i.Symbol == "" {
				return machoTextEncoding{}, fmt.Errorf("onb: Mach-O branch without symbol")
			}
			sym, ok := externalIndex[i.Symbol]
			if !ok {
				sym = uint32(len(cstringIndex) + 1 + len(enc.externalSymbols))
				externalIndex[i.Symbol] = sym
				enc.externalSymbols = append(enc.externalSymbols, i.Symbol)
			}
			addr := uint32(len(enc.code))
			enc.code = appendU32LE(enc.code, 0x94000000)
			enc.relocs = append(enc.relocs, machoReloc{address: addr, symbolnum: sym, pcrel: true, length: 2, extern: true, typ: machoARM64RelocBranch26})
		case *MovImm32:
			if i.Dst != RegW0 || i.Imm != 0 {
				return machoTextEncoding{}, fmt.Errorf("onb: Mach-O phase 1 only supports mov w0, #0")
			}
			enc.code = append(enc.code, 0x00, 0x00, 0x80, 0x52)
		case *MovImm64:
			words, err := encodeMachOMovImm64(i.Dst, uint64(i.Imm))
			if err != nil {
				return machoTextEncoding{}, err
			}
			for _, word := range words {
				enc.code = appendU32LE(enc.code, word)
			}
		case *Store64Stack:
			word, err := encodeMachOStore64Stack(i)
			if err != nil {
				return machoTextEncoding{}, err
			}
			enc.code = appendU32LE(enc.code, word)
		case *Ret:
			if needsFrame {
				if needsStackArgs {
					enc.code = appendU32LE(enc.code, 0xa9417bfd) // ldp x29, x30, [sp, #16]
					enc.code = appendU32LE(enc.code, 0x910083ff) // add sp, sp, #32
				} else {
					enc.code = appendU32LE(enc.code, 0xa8c17bfd) // ldp x29, x30, [sp], #16
				}
			}
			enc.code = append(enc.code, 0xc0, 0x03, 0x5f, 0xd6)
		default:
			return machoTextEncoding{}, fmt.Errorf("%w: Mach-O encoder does not support %T", ErrNotImplemented, instr)
		}
	}
	return enc, nil
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
