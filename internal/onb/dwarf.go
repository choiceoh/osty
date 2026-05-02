package onb

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"path/filepath"
)

// DWARF 4 emitter — Phase B.0/B.1 of the ONB DWARF rollout.
//
// This file owns the four sections lldb needs to discover an Osty
// compilation unit and resolve PC → `<file>:<line>:<col>` for it:
//
//   `.debug_line`  — per-CU PC→source row state machine (B.0)
//   `.debug_info`  — one DW_TAG_compile_unit DIE (B.1)
//   `.debug_abbrev` — abbreviation table for the CU DIE (B.1)
//   `.debug_str`   — string storage for filenames / producer / comp_dir (B.1)
//
// dwarfdump can read `.debug_line` standalone, but lldb only auto-loads a
// CU when `.debug_info` references the line program through DW_AT_stmt_list
// — that's the value B.1 unlocks.
//
// The encoder skips the special opcodes (DWARF spec §6.2.5.1) for now and
// always uses the "extended_op + advance_pc + advance_line + copy"
// sequence. That trades a few bytes per row for simpler logic that's easier
// to inspect with `dwarfdump --debug-line`.
//
// Design constraints worth knowing before editing:
//
//   - Aarch64 instructions are 4 bytes, but min_instruction_length is set
//     to 1 because the encoder feeds raw byte offsets, not "operations".
//   - DWARF file numbers are 1-indexed; index 0 is reserved as "no file".
//   - The line program must finish with `DW_LNE_end_sequence` per
//     contiguous PC range. We treat the whole text section as one range.
//   - Initial PC has to come through `DW_LNE_set_address` because no
//     standard opcode can deliver an absolute target address.
//
// References: DWARF 4 spec §6.2; binutils' `bfd/dwarf2.c` for state-machine
// behaviour.

const (
	dwarfVersion             = 4
	dwarfMinInstLength       = 1
	dwarfMaxOpsPerInst       = 1 // non-VLIW
	dwarfDefaultIsStmt       = 1
	dwarfLineRange           = 14
	dwarfOpcodeBase          = 13
	dwarfAddressSize         = 8 // aarch64 — 8-byte pointers
	dwarfLNSCopy             = 0x01
	dwarfLNSAdvancePC        = 0x02
	dwarfLNSAdvanceLine      = 0x03
	dwarfLNSSetFile          = 0x04
	dwarfLNSSetColumn        = 0x05
	dwarfLNSExtendedOp       = 0x00
	dwarfLNEEndSequence      = 0x01
	dwarfLNESetAddress       = 0x02
	dwarfLNEDefineFile       = 0x03
	dwarfLNESetDiscriminator = 0x04
)

// dwarfLineBase / dwarfLineBaseByte: the spec calls for a signed line
// base of -5 (DW_LNS_advance_line interpretation). DWARF stores it as a
// raw byte, so the two's-complement encoding is 0xFB. We keep both forms
// — the int8 stays around for any future special-opcode arithmetic, and
// the byte form is what the header actually emits.
const dwarfLineBase int8 = -5
const dwarfLineBaseByte byte = 0xFB

// DWARF tag / attribute / form constants used by the compile-unit DIE.
// Values are from the DWARF 4 spec, Appendix A. Only the ones the emitter
// actually writes are listed here — adding more later is fine, but every
// new attribute also needs a slot in the abbreviation table below.
const (
	dwarfTagCompileUnit = 0x11
	dwarfTagBaseType    = 0x24
	dwarfTagSubprogram  = 0x2e
	dwarfTagVariable    = 0x34

	dwarfChildrenNo  byte = 0
	dwarfChildrenYes byte = 1

	dwarfAtName      = 0x03
	dwarfAtByteSize  = 0x0b
	dwarfAtStmtList  = 0x10
	dwarfAtLowPC     = 0x11
	dwarfAtHighPC    = 0x12
	dwarfAtLanguage  = 0x13
	dwarfAtCompDir   = 0x1b
	dwarfAtEncoding  = 0x3e
	dwarfAtProducer  = 0x25
	dwarfAtFrameBase = 0x40
	dwarfAtLocation  = 0x02
	dwarfAtType      = 0x49

	dwarfFormAddr      = 0x01
	dwarfFormData8     = 0x07
	dwarfFormData1     = 0x0b
	dwarfFormStrp      = 0x0e
	dwarfFormRef4      = 0x13
	dwarfFormSecOffset = 0x17
	dwarfFormExprloc   = 0x18

	// DW_LANG_C99 is the closest spec-blessed language code for "C-like
	// imperative with statement-line attribution semantics that match
	// what Osty currently emits". A future slice can register a real
	// DW_LANG_Osty (0x8001+ vendor range) once the toolchain wants its
	// own debugger UX.
	dwarfLangC99 byte = 0x0c

	// DW_ATE_* base-type encoding kinds — used by `__debug_info` to
	// describe how a variable's bytes should be interpreted.
	dwarfATEBoolean    byte = 0x02
	dwarfATEFloat      byte = 0x04
	dwarfATESigned     byte = 0x05
	dwarfATESignedChar byte = 0x06

	dwarfTagPointerType = 0x0f

	// DW_OP_* expression opcodes.
	dwarfOpBreg31 byte = 0x8f // sp-based frame address
	dwarfOpFbreg  byte = 0x91 // frame_base + sleb128

	// abbrev codes. The compile unit is code 1; each function is a
	// DW_TAG_subprogram child encoded with abbrev code 2; locals get
	// DW_TAG_variable as code 3; primitive types use abbrev 4
	// (DW_TAG_base_type) and String adds 5 (DW_TAG_pointer_type that
	// references a `char` base type).
	dwarfAbbrevCompileUnit uint64 = 1
	dwarfAbbrevSubprogram  uint64 = 2
	dwarfAbbrevVariable    uint64 = 3
	dwarfAbbrevBaseType    uint64 = 4
	dwarfAbbrevPointerType uint64 = 5
)

// dwarfStdOpcodeLengths is the per-opcode operand-count table the line
// header advertises. Indexed by `opcode - 1` because opcode 0 is the
// extended-op escape and thus has no entry.
var dwarfStdOpcodeLengths = []byte{
	0, // 0x01 DW_LNS_copy
	1, // 0x02 DW_LNS_advance_pc
	1, // 0x03 DW_LNS_advance_line
	1, // 0x04 DW_LNS_set_file
	1, // 0x05 DW_LNS_set_column
	0, // 0x06 DW_LNS_negate_stmt
	0, // 0x07 DW_LNS_set_basic_block
	0, // 0x08 DW_LNS_const_add_pc
	1, // 0x09 DW_LNS_fixed_advance_pc — single 16-bit operand
	0, // 0x0a DW_LNS_set_prologue_end
	0, // 0x0b DW_LNS_set_epilogue_begin
	1, // 0x0c DW_LNS_set_isa
}

// dwarfLineRow is one (PC, file, line, column) point the encoder writes
// into the line program. PC is a byte offset within the function the row
// belongs to; the encoder converts it to text-relative once it knows the
// per-function start offset.
type dwarfLineRow struct {
	PC     uint64 // byte offset relative to start of __text
	File   uint32 // 1-indexed file table entry
	Line   uint32 // 1-indexed source line; 0 means "unknown"
	Column uint32 // 1-indexed source column; 0 means "unknown"
}

// dwarfLineFile is one entry in the line program's file table. Dir is an
// index into include_directories (1-indexed; 0 = compilation directory).
type dwarfLineFile struct {
	Name  string
	Dir   uint32
	MTime uint64 // unused — always 0
	Size  uint64 // unused — always 0
}

// dwarfLineProgram is the per-CU input to the line emitter. The caller is
// responsible for sorting rows by PC; the encoder asserts that order on
// the way out.
type dwarfLineProgram struct {
	IncludeDirs []string
	Files       []dwarfLineFile
	Rows        []dwarfLineRow
	TextSize    uint64 // total __text size in bytes — used for end_sequence PC
}

// dwarfLineEncoded carries the byte stream for `__debug_line` plus the
// section-relative offsets where address fields appear. Callers (the
// Mach-O writer in particular) thread `SetAddressOffset` into a
// relocation that retargets the value at link time — without it,
// dsymutil treats the section as orphan debug data and skips the .o.
type dwarfLineEncoded struct {
	Bytes            []byte
	SetAddressOffset uint32 // section-relative byte offset of the 8-byte address arg of the first DW_LNE_set_address
}

// emitDwarfLine encodes one CU's line-number program as a flat byte slice
// suitable for the `__debug_line` Mach-O section.
func emitDwarfLine(prog dwarfLineProgram) (dwarfLineEncoded, error) {
	if len(prog.Files) == 0 {
		return dwarfLineEncoded{}, fmt.Errorf("onb: dwarf line program needs at least one file entry")
	}
	header := encodeDwarfLineHeader(prog)
	body, setAddressBodyOffset, err := encodeDwarfLineBody(prog)
	if err != nil {
		return dwarfLineEncoded{}, err
	}
	// Compose the unit. unit_length excludes the 4-byte length field
	// itself (DWARF 4 32-bit form).
	var out bytes.Buffer
	unitLen := uint32(2 /*version*/ + 4 /*header_length*/ + len(header) + len(body))
	binary.Write(&out, binary.LittleEndian, unitLen)
	binary.Write(&out, binary.LittleEndian, uint16(dwarfVersion))
	binary.Write(&out, binary.LittleEndian, uint32(len(header)))
	out.Write(header)
	out.Write(body)
	// 4 (unit_length) + 2 (version) + 4 (header_length) + headerLen +
	// setAddressBodyOffset = absolute section offset of the 8-byte address.
	setAddrOff := uint32(4+2+4+len(header)) + setAddressBodyOffset
	return dwarfLineEncoded{Bytes: out.Bytes(), SetAddressOffset: setAddrOff}, nil
}

// encodeDwarfLineHeader returns just the bytes between header_length and
// the start of the line program — i.e. the part header_length itself
// counts. Header layout per DWARF 4 §6.2.4:
//
//	min_instruction_length         u8
//	max_operations_per_instruction u8   (DWARF 4 — non-VLIW = 1)
//	default_is_stmt                u8
//	line_base                      i8
//	line_range                     u8
//	opcode_base                    u8
//	standard_opcode_lengths        u8 × (opcode_base - 1)
//	include_directories            NUL-terminated strings, terminated by NUL
//	file_names                     entries terminated by NUL byte
func encodeDwarfLineHeader(prog dwarfLineProgram) []byte {
	var b bytes.Buffer
	b.WriteByte(dwarfMinInstLength)
	b.WriteByte(dwarfMaxOpsPerInst)
	b.WriteByte(dwarfDefaultIsStmt)
	b.WriteByte(dwarfLineBaseByte)
	b.WriteByte(dwarfLineRange)
	b.WriteByte(dwarfOpcodeBase)
	b.Write(dwarfStdOpcodeLengths)
	for _, dir := range prog.IncludeDirs {
		b.WriteString(dir)
		b.WriteByte(0)
	}
	b.WriteByte(0) // include_directories terminator
	for _, file := range prog.Files {
		b.WriteString(file.Name)
		b.WriteByte(0)
		writeULEB128(&b, uint64(file.Dir))
		writeULEB128(&b, file.MTime)
		writeULEB128(&b, file.Size)
	}
	b.WriteByte(0) // file_names terminator
	return b.Bytes()
}

// encodeDwarfLineBody walks the rows and emits one
// `set_address + advance_line + copy` block per row, then a final
// `extended end_sequence` per contiguous PC range. The encoder always uses
// extended `set_address` for the first row so we don't have to teach the
// reader about "implicit zero base" semantics.
//
// Returns the body bytes and the body-relative offset of the 8-byte
// address argument of the first set_address (needed for relocation).
func encodeDwarfLineBody(prog dwarfLineProgram) ([]byte, uint32, error) {
	var b bytes.Buffer
	if len(prog.Rows) == 0 {
		// Even an empty function still needs an end_sequence so the
		// section is well-formed. The set_address payload bytes start
		// after the 3-byte extended-op preamble (0x00, length, opcode).
		writeDwarfExtendedSetAddress(&b, 0)
		writeDwarfExtendedEndSequence(&b)
		return b.Bytes(), 3, nil
	}
	state := struct {
		PC   uint64
		Line int32
		File uint32
	}{
		Line: 1,
		File: 1,
	}
	var setAddressOff uint32
	emittedSetAddress := false
	for i, row := range prog.Rows {
		if i > 0 && row.PC < prog.Rows[i-1].PC {
			return nil, 0, fmt.Errorf("onb: dwarf line rows out of order at index %d", i)
		}
		if row.Line == 0 {
			// Skip rows the lowerer couldn't attribute to a source line —
			// the previous mapping stays in effect, which matches what
			// debuggers expect for prologue/epilogue boilerplate.
			continue
		}
		if !emittedSetAddress {
			// Body offset where the 8-byte address payload starts: current
			// buffer length (extended op header) + 3 (escape, length, opcode).
			setAddressOff = uint32(b.Len()) + 3
			writeDwarfExtendedSetAddress(&b, row.PC)
			state.PC = row.PC
			emittedSetAddress = true
		} else if row.PC > state.PC {
			b.WriteByte(dwarfLNSAdvancePC)
			writeULEB128(&b, row.PC-state.PC)
			state.PC = row.PC
		}
		if row.File != state.File {
			b.WriteByte(dwarfLNSSetFile)
			writeULEB128(&b, uint64(row.File))
			state.File = row.File
		}
		if int32(row.Line) != state.Line {
			b.WriteByte(dwarfLNSAdvanceLine)
			writeSLEB128(&b, int64(int32(row.Line)-state.Line))
			state.Line = int32(row.Line)
		}
		if row.Column != 0 {
			b.WriteByte(dwarfLNSSetColumn)
			writeULEB128(&b, uint64(row.Column))
		}
		b.WriteByte(dwarfLNSCopy)
	}
	// Advance to the end of the text section before end_sequence — debuggers
	// use the final address as the upper bound of the last row's PC range.
	if prog.TextSize > state.PC {
		b.WriteByte(dwarfLNSAdvancePC)
		writeULEB128(&b, prog.TextSize-state.PC)
	}
	writeDwarfExtendedEndSequence(&b)
	return b.Bytes(), setAddressOff, nil
}

// writeDwarfExtendedSetAddress emits the variable-length extended opcode
// `0x00 <length> DW_LNE_set_address <8-byte address>`. The length field is
// uleb128 of `1 (opcode) + 8 (address)` = 9 — single byte.
func writeDwarfExtendedSetAddress(b *bytes.Buffer, addr uint64) {
	b.WriteByte(dwarfLNSExtendedOp)
	writeULEB128(b, 1+uint64(dwarfAddressSize))
	b.WriteByte(dwarfLNESetAddress)
	binary.Write(b, binary.LittleEndian, addr)
}

func writeDwarfExtendedEndSequence(b *bytes.Buffer) {
	b.WriteByte(dwarfLNSExtendedOp)
	writeULEB128(b, 1)
	b.WriteByte(dwarfLNEEndSequence)
}

// writeULEB128 writes an unsigned LEB128 integer.
func writeULEB128(b *bytes.Buffer, v uint64) {
	for {
		c := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			b.WriteByte(c | 0x80)
			continue
		}
		b.WriteByte(c)
		return
	}
}

// writeSLEB128 writes a signed LEB128 integer.
func writeSLEB128(b *bytes.Buffer, v int64) {
	for {
		c := byte(v & 0x7f)
		// Arithmetic shift retains the sign bit.
		v >>= 7
		signBitSet := c&0x40 != 0
		if (v == 0 && !signBitSet) || (v == -1 && signBitSet) {
			b.WriteByte(c)
			return
		}
		b.WriteByte(c | 0x80)
	}
}

// programLineRows walks every Function in a Program and produces the
// per-block rows the line-program encoder consumes. PC is computed as a
// running byte offset across all functions in `program.Functions` order
// (which the Mach-O encoder also follows). prologueBytes is the byte cost
// of the function prologue; the encoder accounts for it so the first row
// of each function points at the function's first MIR-derived line.
func programLineRows(program *Program, fileIdx uint32, fnSizes []uint64) []dwarfLineRow {
	var rows []dwarfLineRow
	if program == nil || len(fnSizes) != len(program.Functions) {
		return rows
	}
	var pc uint64
	for fnIdx, fn := range program.Functions {
		startPC := pc
		fnRows := functionLineRows(fn, fileIdx, startPC)
		rows = append(rows, fnRows...)
		pc = startPC + fnSizes[fnIdx]
	}
	return rows
}

// functionLineRows turns one Function's instruction stream into line-program
// rows. Each LIR instruction is 4 bytes; prologue/epilogue extra
// instructions inherit the line of the surrounding block in lower.go's
// LineSpan emission, so we just multiply the index by 4 here.
func functionLineRows(fn Function, fileIdx uint32, basePC uint64) []dwarfLineRow {
	var rows []dwarfLineRow
	pc := basePC
	prologueWords := functionPrologueWords(fn)
	pc += uint64(prologueWords) * 4
	for _, block := range fn.Blocks {
		for i, instr := range block.Instrs {
			var span LineSpan
			if i < len(block.LineSpans) {
				span = block.LineSpans[i]
			}
			rows = appendLineRow(rows, dwarfLineRow{
				PC:     pc,
				File:   fileIdx,
				Line:   uint32(span.Line),
				Column: uint32(span.Column),
			})
			pc += instructionByteSize(instr)
		}
	}
	return rows
}

// appendLineRow drops back-to-back duplicates so the encoder only emits a
// row when something actually changed. Coalescing here keeps the output
// readable in `dwarfdump` and shrinks the section meaningfully on real
// programs — every prologue store would otherwise emit its own row.
func appendLineRow(rows []dwarfLineRow, row dwarfLineRow) []dwarfLineRow {
	if len(rows) == 0 {
		return append(rows, row)
	}
	prev := rows[len(rows)-1]
	if prev.File == row.File && prev.Line == row.Line && prev.Column == row.Column {
		return rows
	}
	return append(rows, row)
}

// instructionByteSize returns the encoded size of the given LIR
// instruction. Almost everything is a single 4-byte aarch64 word; MovImm64
// expands to up to four `movz`/`movk` instructions and Ret carries an
// optional epilogue.
func instructionByteSize(instr Instr) uint64 {
	switch i := instr.(type) {
	case *MovImm64:
		return uint64(movImm64WordCount(i.Imm)) * 4
	case *LoadCStringAddress:
		return 8 // adrp + add (Mach-O variant) / adrp + add (ELF)
	default:
		return 4
	}
}

// movImm64WordCount mirrors the encoder's chunking logic so the line-row
// emitter can predict how many bytes a `mov #imm` actually takes.
func movImm64WordCount(imm int64) int {
	v := uint64(imm)
	count := 0
	for shift := 0; shift < 64; shift += 16 {
		chunk := uint16(v >> shift)
		if chunk == 0 && count != 0 {
			continue
		}
		count++
	}
	if count == 0 {
		count = 1
	}
	return count
}

// dwarfFileEntry returns one Mach-O-suitable file table entry for the
// program. Empty SourcePath becomes "<unknown>" — debuggers will still
// load the section, just with no file resolution.
func dwarfFileEntry(program *Program) dwarfLineFile {
	name := program.SourcePath
	if name == "" {
		if program.Package != "" {
			name = program.Package + ".osty"
		} else {
			name = "<unknown>"
		}
	}
	return dwarfLineFile{Name: filepath.Base(name)}
}

// dwarfIncludeDir returns the single include-directory entry the line
// program needs. We use the source file's directory; an empty path falls
// back to the compilation directory (DWARF treats the empty string as
// "current directory at compile time").
func dwarfIncludeDir(program *Program) string {
	if program.SourcePath == "" {
		return ""
	}
	return filepath.Dir(program.SourcePath)
}

// dwarfStringTable accumulates NUL-terminated strings for the `.debug_str`
// section while handing back stable byte offsets that the emitter writes
// into DIE attribute slots. The empty string sits at offset 0 by
// convention so DW_AT_name = 0 means "absent" rather than "empty".
type dwarfStringTable struct {
	buf  []byte
	offs map[string]uint32
}

func newDwarfStringTable() *dwarfStringTable {
	return &dwarfStringTable{
		buf:  []byte{0},
		offs: map[string]uint32{"": 0},
	}
}

// Add returns the offset of `s` in the string table, appending it if it
// isn't already there. Empty strings map to offset 0.
func (t *dwarfStringTable) Add(s string) uint32 {
	if off, ok := t.offs[s]; ok {
		return off
	}
	off := uint32(len(t.buf))
	t.buf = append(t.buf, s...)
	t.buf = append(t.buf, 0)
	t.offs[s] = off
	return off
}

// dwarfCompileUnitInputs collects the metadata the CU DIE needs. Strings
// are pre-resolved into `__debug_str` offsets so the encoder can write
// raw uint32s without re-traversing the table — except the type-name
// strings, which the encoder adds opportunistically as it discovers new
// base types (so the caller hands over the live table via StringTable).
type dwarfCompileUnitInputs struct {
	NameStrOffset     uint32
	CompDirStrOffset  uint32
	ProducerStrOffset uint32
	LowPC             uint64
	HighPCSize        uint64 // DWARF 4 high_pc as constant offset from low_pc
	StmtListOffset    uint32
	Language          byte
	StringTable       *dwarfStringTable
}

// emitDwarfAbbrev returns the byte stream for `__debug_abbrev`. Four
// entries today — CU (1), subprogram (2), variable (3), base type (4).
// Subprograms own variable DIEs as children; types live as siblings of
// subprograms under the CU. Each DIE list is closed by a 0-byte sentinel
// when its abbrev declared DW_CHILDREN_yes.
func emitDwarfAbbrev() []byte {
	var b bytes.Buffer
	// Code 1: DW_TAG_compile_unit, has children (subprograms + types).
	writeULEB128(&b, dwarfAbbrevCompileUnit)
	writeULEB128(&b, dwarfTagCompileUnit)
	b.WriteByte(dwarfChildrenYes)
	writeULEB128AttrPair(&b, dwarfAtProducer, dwarfFormStrp)
	writeULEB128AttrPair(&b, dwarfAtLanguage, dwarfFormData1)
	writeULEB128AttrPair(&b, dwarfAtName, dwarfFormStrp)
	writeULEB128AttrPair(&b, dwarfAtCompDir, dwarfFormStrp)
	writeULEB128AttrPair(&b, dwarfAtLowPC, dwarfFormAddr)
	writeULEB128AttrPair(&b, dwarfAtHighPC, dwarfFormData8)
	writeULEB128AttrPair(&b, dwarfAtStmtList, dwarfFormSecOffset)
	writeULEB128(&b, 0)
	writeULEB128(&b, 0)

	// Code 2: DW_TAG_subprogram, has children (variable DIEs).
	writeULEB128(&b, dwarfAbbrevSubprogram)
	writeULEB128(&b, dwarfTagSubprogram)
	b.WriteByte(dwarfChildrenYes)
	writeULEB128AttrPair(&b, dwarfAtName, dwarfFormStrp)
	writeULEB128AttrPair(&b, dwarfAtLowPC, dwarfFormAddr)
	writeULEB128AttrPair(&b, dwarfAtHighPC, dwarfFormData8)
	writeULEB128AttrPair(&b, dwarfAtFrameBase, dwarfFormExprloc)
	writeULEB128(&b, 0)
	writeULEB128(&b, 0)

	// Code 3: DW_TAG_variable, no children.
	writeULEB128(&b, dwarfAbbrevVariable)
	writeULEB128(&b, dwarfTagVariable)
	b.WriteByte(dwarfChildrenNo)
	writeULEB128AttrPair(&b, dwarfAtName, dwarfFormStrp)
	writeULEB128AttrPair(&b, dwarfAtType, dwarfFormRef4)
	writeULEB128AttrPair(&b, dwarfAtLocation, dwarfFormExprloc)
	writeULEB128(&b, 0)
	writeULEB128(&b, 0)

	// Code 4: DW_TAG_base_type, no children.
	writeULEB128(&b, dwarfAbbrevBaseType)
	writeULEB128(&b, dwarfTagBaseType)
	b.WriteByte(dwarfChildrenNo)
	writeULEB128AttrPair(&b, dwarfAtName, dwarfFormStrp)
	writeULEB128AttrPair(&b, dwarfAtByteSize, dwarfFormData1)
	writeULEB128AttrPair(&b, dwarfAtEncoding, dwarfFormData1)
	writeULEB128(&b, 0)
	writeULEB128(&b, 0)

	// Code 5: DW_TAG_pointer_type, no children. Used for String, which
	// sits at the ABI boundary as a pointer to UTF-8 char data.
	writeULEB128(&b, dwarfAbbrevPointerType)
	writeULEB128(&b, dwarfTagPointerType)
	b.WriteByte(dwarfChildrenNo)
	writeULEB128AttrPair(&b, dwarfAtType, dwarfFormRef4)
	writeULEB128AttrPair(&b, dwarfAtByteSize, dwarfFormData1)
	writeULEB128(&b, 0)
	writeULEB128(&b, 0)

	// End of abbreviation table.
	writeULEB128(&b, 0)
	return b.Bytes()
}

// writeULEB128AttrPair is a helper that writes one (attribute, form) pair.
func writeULEB128AttrPair(b *bytes.Buffer, attr, form uint64) {
	writeULEB128(b, attr)
	writeULEB128(b, form)
}

// dwarfInfoEncoded carries the byte stream for `__debug_info` plus the
// section-relative offsets of every DW_AT_low_pc value. The Mach-O writer
// attaches a relocation at each offset so dsymutil can rewrite the
// addresses to the binary's runtime base. Without these relocations
// dsymutil treats the DWARF as orphan and skips the .o.
type dwarfInfoEncoded struct {
	Bytes        []byte
	LowPCOffsets []uint32 // section-relative offsets of every 8-byte DW_AT_low_pc
}

// dwarfSubprogramInput is the per-function metadata the encoder needs to
// emit a `DW_TAG_subprogram` DIE underneath the compile unit.
type dwarfSubprogramInput struct {
	NameStrOffset uint32
	LowPC         uint64
	SizeBytes     uint64 // DWARF 4 high_pc as constant offset from low_pc
	Variables     []dwarfVariableInput
}

// dwarfVariableInput describes one local variable visible to lldb's
// `frame variable` command. SlotOffset is the byte offset from the
// function's frame base (which Phase B.3 sets to the current sp value)
// where the value lives.
type dwarfVariableInput struct {
	NameStrOffset uint32
	SlotOffset    int64
	TypeKind      dwarfBaseTypeKind
}

// dwarfBaseTypeKind enumerates the primitive ABI-level types the DWARF
// emitter can describe to lldb. Each kind owns a single DIE in the CU's
// type list — except String, which is encoded as a `DW_TAG_pointer_type`
// pointing at a `DW_TAG_base_type "char"` so lldb prints the contents
// instead of just the address.
type dwarfBaseTypeKind int

const (
	dwarfBaseTypeNone dwarfBaseTypeKind = iota
	dwarfBaseTypeInt
	dwarfBaseTypeBool
	dwarfBaseTypeFloat
	dwarfBaseTypeString
)

// emitDwarfInfo returns the byte stream for `__debug_info`. The unit
// header layout (DWARF 4 §7.5.1.1):
//
//	unit_length         (4 bytes, 32-bit form)
//	version             (2 bytes)
//	debug_abbrev_offset (4 bytes, sec_offset into __debug_abbrev)
//	address_size        (1 byte = 8 for aarch64)
//
// then DIEs in tree order:
//
//	CU
//	├── base_type Int
//	├── subprogram f
//	│   ├── variable v1
//	│   └── variable v2
//	└── subprogram g
//
// The base_type comes first so subsequent DW_AT_type ref4 fields can use
// its CU-relative offset. Each tree level with DW_CHILDREN_yes ends with
// a 0-byte sentinel.
func emitDwarfInfo(cu dwarfCompileUnitInputs, subs []dwarfSubprogramInput) dwarfInfoEncoded {
	const headerLen = 11 // 4 + 2 + 4 + 1
	var die bytes.Buffer

	// CU DIE
	writeULEB128(&die, dwarfAbbrevCompileUnit)
	binary.Write(&die, binary.LittleEndian, cu.ProducerStrOffset)
	die.WriteByte(cu.Language)
	binary.Write(&die, binary.LittleEndian, cu.NameStrOffset)
	binary.Write(&die, binary.LittleEndian, cu.CompDirStrOffset)
	cuLowPCDieOff := uint32(die.Len())
	binary.Write(&die, binary.LittleEndian, cu.LowPC)
	binary.Write(&die, binary.LittleEndian, cu.HighPCSize)
	binary.Write(&die, binary.LittleEndian, cu.StmtListOffset)

	// Type DIEs first so variable DIEs can reference them by stable
	// CU-relative offset. We register every kind a variable in this CU
	// references; unused kinds stay out so the .o doesn't carry dead
	// debug info.
	used := collectUsedTypeKinds(subs)
	typeOff := map[dwarfBaseTypeKind]uint32{}

	emitBaseType := func(kind dwarfBaseTypeKind, name string, byteSize byte, encoding byte) {
		strx := stringTableLookupOrAdd(cu.StringTable, name)
		typeOff[kind] = headerLen + uint32(die.Len())
		writeULEB128(&die, dwarfAbbrevBaseType)
		binary.Write(&die, binary.LittleEndian, strx)
		die.WriteByte(byteSize)
		die.WriteByte(encoding)
	}

	if used[dwarfBaseTypeInt] {
		emitBaseType(dwarfBaseTypeInt, "Int", 8, dwarfATESigned)
	}
	if used[dwarfBaseTypeBool] {
		// DWARF spec encodes Bool as byte_size 1 with DW_ATE_boolean;
		// lldb refuses any larger boolean and falls back to "void".
		// Our slot is 8 bytes wide but the value lives in the low byte,
		// so reading 1 byte from the slot's address is correct.
		emitBaseType(dwarfBaseTypeBool, "Bool", 1, dwarfATEBoolean)
	}
	if used[dwarfBaseTypeFloat] {
		emitBaseType(dwarfBaseTypeFloat, "Float", 8, dwarfATEFloat)
	}
	if used[dwarfBaseTypeString] {
		// String is a pointer to UTF-8 char data. Two DIEs: a `char`
		// base type (byte_size 1, signed_char) plus a pointer DIE that
		// references it. lldb prints the pointee as a C string.
		charStrx := stringTableLookupOrAdd(cu.StringTable, "char")
		charOff := headerLen + uint32(die.Len())
		writeULEB128(&die, dwarfAbbrevBaseType)
		binary.Write(&die, binary.LittleEndian, charStrx)
		die.WriteByte(1)
		die.WriteByte(dwarfATESignedChar)

		typeOff[dwarfBaseTypeString] = headerLen + uint32(die.Len())
		writeULEB128(&die, dwarfAbbrevPointerType)
		binary.Write(&die, binary.LittleEndian, charOff)
		die.WriteByte(8) // pointer width on aarch64
	}

	subLowPCDieOffs := make([]uint32, 0, len(subs))
	for _, sub := range subs {
		writeULEB128(&die, dwarfAbbrevSubprogram)
		binary.Write(&die, binary.LittleEndian, sub.NameStrOffset)
		subLowPCDieOffs = append(subLowPCDieOffs, uint32(die.Len()))
		binary.Write(&die, binary.LittleEndian, sub.LowPC)
		binary.Write(&die, binary.LittleEndian, sub.SizeBytes)
		// frame_base = DW_OP_breg31 0 (current sp). Locals encode their
		// own slot offsets via DW_OP_fbreg <slot>.
		writeULEB128(&die, 2) // exprloc length
		die.WriteByte(dwarfOpBreg31)
		writeSLEB128(&die, 0)

		// Variable DIE children — one per local whose type the encoder
		// has registered above. Unsupported kinds (DebugTypeNone or
		// composite) are silently skipped so they don't confuse lldb
		// with half-described variables.
		for _, v := range sub.Variables {
			off, ok := typeOff[v.TypeKind]
			if !ok {
				continue
			}
			writeULEB128(&die, dwarfAbbrevVariable)
			binary.Write(&die, binary.LittleEndian, v.NameStrOffset)
			binary.Write(&die, binary.LittleEndian, off)
			// location: DW_OP_fbreg <sleb128 slotOffset>
			var loc bytes.Buffer
			loc.WriteByte(dwarfOpFbreg)
			writeSLEB128(&loc, v.SlotOffset)
			writeULEB128(&die, uint64(loc.Len()))
			die.Write(loc.Bytes())
		}
		die.WriteByte(0) // close subprogram children
	}
	die.WriteByte(0) // close CU children

	var unit bytes.Buffer
	unitLen := uint32(2 + 4 + 1 + die.Len())
	binary.Write(&unit, binary.LittleEndian, unitLen)
	binary.Write(&unit, binary.LittleEndian, uint16(dwarfVersion))
	binary.Write(&unit, binary.LittleEndian, uint32(0))
	unit.WriteByte(dwarfAddressSize)
	unit.Write(die.Bytes())

	lowPCs := make([]uint32, 0, 1+len(subLowPCDieOffs))
	lowPCs = append(lowPCs, headerLen+cuLowPCDieOff)
	for _, off := range subLowPCDieOffs {
		lowPCs = append(lowPCs, headerLen+off)
	}
	return dwarfInfoEncoded{Bytes: unit.Bytes(), LowPCOffsets: lowPCs}
}

// collectUsedTypeKinds walks every variable across every subprogram and
// records the set of base-type kinds the CU's type DIE list needs to
// publish. Skipping unused kinds keeps the .o lean and lets future
// composite-type slices add new kinds without paying their cost on
// programs that don't use them.
func collectUsedTypeKinds(subs []dwarfSubprogramInput) map[dwarfBaseTypeKind]bool {
	used := map[dwarfBaseTypeKind]bool{}
	for _, sub := range subs {
		for _, v := range sub.Variables {
			if v.TypeKind != dwarfBaseTypeNone {
				used[v.TypeKind] = true
			}
		}
	}
	return used
}

// stringTableLookupOrAdd returns the offset of `s` in the supplied table,
// adding it if missing. We can't import dwarfStringTable from inside the
// type definition above (Go's order-of-declaration rules) so this helper
// shim keeps the encoder readable while the table itself stays in its
// existing location.
func stringTableLookupOrAdd(t *dwarfStringTable, s string) uint32 {
	if t == nil {
		return 0
	}
	return t.Add(s)
}

// dwarfCompileUnitMeta packages the three string-table inputs along with
// the resolved `__debug_str` table so the macho writer can later emit the
// section content. Used by the macho integration only.
type dwarfCompileUnitMeta struct {
	Strings *dwarfStringTable
	CU      dwarfCompileUnitInputs
}

// buildDwarfCompileUnitMeta resolves the program's metadata into a
// pre-baked string table and CU input record. Callers fill in HighPCSize
// and StmtListOffset later (those depend on the line program size and the
// `__debug_line` section's file offset).
func buildDwarfCompileUnitMeta(program *Program) *dwarfCompileUnitMeta {
	if program == nil {
		return nil
	}
	strs := newDwarfStringTable()
	name := dwarfFileEntry(program).Name
	compDir := dwarfIncludeDir(program)
	producer := "osty (onb dev backend)"
	return &dwarfCompileUnitMeta{
		Strings: strs,
		CU: dwarfCompileUnitInputs{
			NameStrOffset:     strs.Add(name),
			CompDirStrOffset:  strs.Add(compDir),
			ProducerStrOffset: strs.Add(producer),
			Language:          dwarfLangC99,
		},
	}
}
