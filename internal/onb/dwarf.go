package onb

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"path/filepath"
)

// DWARF 4 line-number program emitter — Phase B.0 of the ONB DWARF rollout.
//
// This file owns ONLY the `.debug_line` section: a per-compile-unit byte
// stream that maps native PC offsets back to source `<file>:<line>:<col>`
// triples. lldb / gdb both read it directly; for full lldb integration on
// darwin the `.dSYM` workflow also wants `.debug_info` + `.debug_abbrev`
// + `.debug_str`, which a follow-up slice will add.
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

// emitDwarfLine encodes one CU's line-number program as a flat byte slice
// suitable for the `__debug_line` Mach-O section.
func emitDwarfLine(prog dwarfLineProgram) ([]byte, error) {
	if len(prog.Files) == 0 {
		return nil, fmt.Errorf("onb: dwarf line program needs at least one file entry")
	}
	header := encodeDwarfLineHeader(prog)
	body, err := encodeDwarfLineBody(prog)
	if err != nil {
		return nil, err
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
	return out.Bytes(), nil
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
func encodeDwarfLineBody(prog dwarfLineProgram) ([]byte, error) {
	var b bytes.Buffer
	if len(prog.Rows) == 0 {
		// Even an empty function still needs an end_sequence so the
		// section is well-formed.
		writeDwarfExtendedSetAddress(&b, 0)
		writeDwarfExtendedEndSequence(&b)
		return b.Bytes(), nil
	}
	state := struct {
		PC   uint64
		Line int32
		File uint32
	}{
		Line: 1,
		File: 1,
	}
	for i, row := range prog.Rows {
		if i > 0 && row.PC < prog.Rows[i-1].PC {
			return nil, fmt.Errorf("onb: dwarf line rows out of order at index %d", i)
		}
		if row.Line == 0 {
			// Skip rows the lowerer couldn't attribute to a source line —
			// the previous mapping stays in effect, which matches what
			// debuggers expect for prologue/epilogue boilerplate.
			continue
		}
		if i == 0 {
			writeDwarfExtendedSetAddress(&b, row.PC)
			state.PC = row.PC
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
	return b.Bytes(), nil
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

// functionPrologueWords returns the number of 4-byte prologue instructions
// the encoder inserts at the start of a function. The line emitter steps
// past these so the first MIR-derived row points at the function body, not
// the stack-setup boilerplate.
func functionPrologueWords(fn Function) int {
	if functionFrameSize(fn) == 0 {
		return 0
	}
	if functionFPOffset(fn) < 0 {
		return 2 // stp x29, x30, [sp, #-16]!  +  mov x29, sp
	}
	return 3 // sub sp, sp, #N  +  stp x29, x30, [sp, #N-16]  +  add x29, sp, #N-16
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
