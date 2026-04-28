package check

import (
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/stdlib"
)

func TestNativeBoundaryExecRecognizesIoProtocolHelpers(t *testing.T) {
	src := []byte(`use std.io as io
use std.net

fn copyAll(src: io.Reader, dst: io.Writer) -> Result<Int, Error> {
    io.copy(dst, src)
}

fn roundtrip(stream: net.TcpStream) -> Result<(), Error> {
    let payload = io.readAll(stream)?
    io.writeAll(stream, payload)?
    let copied = copyAll(stream, stream)?
    Ok(())
}
`)
	file, res := parseResolvedFile(t, src)
	roundtripDecl := file.Decls[1].(*ast.FnDecl)
	payloadLet := roundtripDecl.Body.Stmts[0].(*ast.LetStmt)
	copiedLet := roundtripDecl.Body.Stmts[2].(*ast.LetStmt)

	bin := buildRepoNativeChecker(t)
	t.Setenv(nativeCheckerEnv, bin)

	oldFactory := nativeCheckerFactory
	nativeCheckerFactory = defaultNativeChecker
	t.Cleanup(func() { nativeCheckerFactory = oldFactory })

	chk := SelfhostFile(file, res, Opts{Source: src, Stdlib: stdlib.LoadCached()})
	if len(chk.Diags) != 0 {
		t.Fatalf("expected no diagnostics, got %v", chk.Diags)
	}
	for _, tc := range []struct {
		name     string
		stmt     *ast.LetStmt
		wantType string
	}{
		{"payload", payloadLet, "Bytes"},
		{"copied", copiedLet, "Int"},
	} {
		if got := chk.LetTypes[tc.stmt]; got == nil || got.String() != tc.wantType {
			t.Errorf("%s binding type = %v, want %s", tc.name, got, tc.wantType)
		}
	}
}

func TestNativeBoundaryExecRecognizesIoBufferedHelpers(t *testing.T) {
	src := []byte(`use std.io as io

fn main() -> Result<(), Error> {
    let reader = io.stringReader("alpha\r\nbeta\n")
    let lines = io.readLines(reader)?

    let exact = io.readExact(io.stringReader("abcdef"), 3)?
    let mut bytesReader = io.stringReader("gamma\r\ndelta\n")
    let head = bytesReader.peek(2)?
    let first = bytesReader.readByte()?
    bytesReader.unreadByte()?
    let line = bytesReader.readLine()?
    let skipped = bytesReader.skip(2)?
    let rest = bytesReader.remainingBytes()

    let mut sink = io.buffer()
    sink.writeString("head")?
    io.writeLine(sink, "tail")?
    let loaded = sink.readFrom(io.stringReader("++"))?
    sink.truncate(6)
    let copied = io.copyN(sink, io.stringReader("xyz"), 2)?
    let dumped = io.discard(io.stringReader("rest"))?
    let rendered = sink.toString()?
    let bytesOut = sink.bytes()
    let snapText = io.readString(sink.reader())?
    let wrote = sink.writeTo(io.buffer())?
    Ok(())
}
`)
	file, res := parseResolvedFile(t, src)
	mainDecl := file.Decls[0].(*ast.FnDecl)
	linesLet := mainDecl.Body.Stmts[1].(*ast.LetStmt)
	exactLet := mainDecl.Body.Stmts[2].(*ast.LetStmt)
	headLet := mainDecl.Body.Stmts[4].(*ast.LetStmt)
	firstLet := mainDecl.Body.Stmts[5].(*ast.LetStmt)
	lineLet := mainDecl.Body.Stmts[7].(*ast.LetStmt)
	skippedLet := mainDecl.Body.Stmts[8].(*ast.LetStmt)
	restLet := mainDecl.Body.Stmts[9].(*ast.LetStmt)
	loadedLet := mainDecl.Body.Stmts[13].(*ast.LetStmt)
	copiedLet := mainDecl.Body.Stmts[15].(*ast.LetStmt)
	dumpedLet := mainDecl.Body.Stmts[16].(*ast.LetStmt)
	renderedLet := mainDecl.Body.Stmts[17].(*ast.LetStmt)
	bytesOutLet := mainDecl.Body.Stmts[18].(*ast.LetStmt)
	snapTextLet := mainDecl.Body.Stmts[19].(*ast.LetStmt)
	wroteLet := mainDecl.Body.Stmts[20].(*ast.LetStmt)

	bin := buildRepoNativeChecker(t)
	t.Setenv(nativeCheckerEnv, bin)

	oldFactory := nativeCheckerFactory
	nativeCheckerFactory = defaultNativeChecker
	t.Cleanup(func() { nativeCheckerFactory = oldFactory })

	chk := SelfhostFile(file, res, Opts{Source: src, Stdlib: stdlib.LoadCached()})
	if len(chk.Diags) != 0 {
		t.Fatalf("expected no diagnostics, got %v", chk.Diags)
	}
	for _, tc := range []struct {
		name     string
		stmt     *ast.LetStmt
		wantType string
	}{
		{"lines", linesLet, "List<String>"},
		{"exact", exactLet, "Bytes"},
		{"head", headLet, "Bytes"},
		{"first", firstLet, "Byte?"},
		{"line", lineLet, "String?"},
		{"skipped", skippedLet, "Int"},
		{"rest", restLet, "Bytes"},
		{"loaded", loadedLet, "Int"},
		{"copied", copiedLet, "Int"},
		{"dumped", dumpedLet, "Int"},
		{"rendered", renderedLet, "String"},
		{"bytesOut", bytesOutLet, "Bytes"},
		{"snapText", snapTextLet, "String"},
		{"wrote", wroteLet, "Int"},
	} {
		if got := chk.LetTypes[tc.stmt]; got == nil || got.String() != tc.wantType {
			t.Errorf("%s binding type = %v, want %s", tc.name, got, tc.wantType)
		}
	}
}

func TestNativeBoundaryExecRecognizesIoCapabilityProtocols(t *testing.T) {
	src := []byte(`use std.io as io

fn collect(r: io.LineReader) -> Result<List<String>, Error> {
    io.readAllLines(r)
}

fn emit(w: io.ByteWriter) -> Result<Int, Error> {
    io.writeLines(w, ["a", "b"])
}

fn mirror(dst: io.ReaderFrom, src: io.Reader) -> Result<Int, Error> {
    dst.readFrom(src)
}

fn spill(src: io.WriterTo, dst: io.Writer) -> Result<Int, Error> {
    src.writeTo(dst)
}

fn main() -> Result<(), Error> {
    let lines = collect(io.stringReader("x\ny\n"))?
    let mut sink = io.buffer()
    let wrote = emit(sink)?
    let mirrored = mirror(sink, io.stringReader("tail"))?
    let spilled = spill(sink, io.buffer())?
    Ok(())
}
`)
	file, res := parseResolvedFile(t, src)
	mainDecl := file.Decls[4].(*ast.FnDecl)
	linesLet := mainDecl.Body.Stmts[0].(*ast.LetStmt)
	wroteLet := mainDecl.Body.Stmts[2].(*ast.LetStmt)
	mirroredLet := mainDecl.Body.Stmts[3].(*ast.LetStmt)
	spilledLet := mainDecl.Body.Stmts[4].(*ast.LetStmt)

	bin := buildRepoNativeChecker(t)
	t.Setenv(nativeCheckerEnv, bin)

	oldFactory := nativeCheckerFactory
	nativeCheckerFactory = defaultNativeChecker
	t.Cleanup(func() { nativeCheckerFactory = oldFactory })

	chk := SelfhostFile(file, res, Opts{Source: src, Stdlib: stdlib.LoadCached()})
	if len(chk.Diags) != 0 {
		t.Fatalf("expected no diagnostics, got %v", chk.Diags)
	}
	for _, tc := range []struct {
		name     string
		stmt     *ast.LetStmt
		wantType string
	}{
		{"lines", linesLet, "List<String>"},
		{"wrote", wroteLet, "Int"},
		{"mirrored", mirroredLet, "Int"},
		{"spilled", spilledLet, "Int"},
	} {
		if got := chk.LetTypes[tc.stmt]; got == nil || got.String() != tc.wantType {
			t.Errorf("%s binding type = %v, want %s", tc.name, got, tc.wantType)
		}
	}
}
