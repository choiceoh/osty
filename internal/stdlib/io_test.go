package stdlib

import (
	"strings"
	"testing"
)

func TestIoModuleExposesProtocolHelpers(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["io"]
	if mod == nil {
		t.Fatal("stdlib io module missing")
	}
	for _, want := range []string{
		"pub interface Reader",
		"pub interface Writer",
		"pub interface Closer",
		"pub interface ReadWriter",
		"pub interface ReadCloser",
		"pub interface WriteCloser",
		"pub interface ByteReader",
		"pub interface LineReader",
		"pub interface ByteWriter",
		"pub interface ReaderFrom",
		"pub interface WriterTo",
		"pub interface BufferedReader",
		"pub interface BufferedWriter",
		"pub struct BytesReader",
		"pub struct Buffer",
		"pub fn bytesReader(data: Bytes) -> BytesReader",
		"pub fn stringReader(s: String) -> BytesReader",
		"pub fn buffer() -> Buffer",
		"pub fn copy(dst: Writer, src: Reader) -> Result<Int, Error>",
		"pub fn copyN(dst: Writer, src: Reader, n: Int) -> Result<Int, Error>",
		"pub fn readAll(r: Reader) -> Result<Bytes, Error>",
		"pub fn readExact(r: Reader, n: Int) -> Result<Bytes, Error>",
		"pub fn readString(r: Reader) -> Result<String, Error>",
		"pub fn readLines(r: Reader) -> Result<List<String>, Error>",
		"pub fn readAllLines(r: LineReader) -> Result<List<String>, Error>",
		"pub fn discard(r: Reader) -> Result<Int, Error>",
		"pub fn writeAll(w: Writer, data: Bytes) -> Result<(), Error>",
		"pub fn writeString(w: Writer, s: String) -> Result<(), Error>",
		"pub fn writeLine(w: Writer, s: String) -> Result<(), Error>",
		"pub fn writeLines(w: ByteWriter, lines: List<String>) -> Result<Int, Error>",
	} {
		if !strings.Contains(string(mod.Source), want) {
			t.Fatalf("io module source missing %q", want)
		}
	}
	for _, name := range []string{
		"copy", "copyN", "readAll", "readExact", "readString", "readLines",
		"readAllLines", "discard", "writeAll", "writeString", "writeLine",
		"writeLines", "bytesReader", "stringReader", "buffer",
	} {
		fn := reg.LookupFnDecl("io", name)
		if fn == nil {
			t.Fatalf("LookupFnDecl(io, %s) = nil, want stdlib helper", name)
		}
		if fn.Body == nil {
			t.Fatalf("io.%s body = nil, want Osty helper body", name)
		}
	}
	for _, tc := range []struct {
		typeName string
		method   string
	}{
		{"BytesReader", "read"},
		{"BytesReader", "peek"},
		{"BytesReader", "readByte"},
		{"BytesReader", "unreadByte"},
		{"BytesReader", "skip"},
		{"BytesReader", "readLineBytes"},
		{"BytesReader", "readLine"},
		{"BytesReader", "remainingBytes"},
		{"Buffer", "write"},
		{"Buffer", "writeString"},
		{"Buffer", "clear"},
		{"Buffer", "truncate"},
		{"Buffer", "writeLine"},
		{"Buffer", "readFrom"},
		{"Buffer", "writeTo"},
		{"Buffer", "reader"},
		{"Buffer", "bytes"},
	} {
		if got := reg.LookupMethodDecl("io", tc.typeName, tc.method); got == nil {
			t.Fatalf("LookupMethodDecl(io, %s, %s) = nil", tc.typeName, tc.method)
		}
	}
}

func TestIoHelpersGuardInvalidShortWrites(t *testing.T) {
	src := ioModuleSource(t)
	for _, want := range []string{
		"wrote <= 0 || wrote > remaining",
		"error.new(\"io.{op}: writer reported invalid byte count {n} for {want}-byte buffer\")",
		"error.new(\"io.{op}: unexpected EOF after {got} of {want} bytes\")",
		"error.new(\"io.{kind}: closed\")",
		"error.new(\"io.bytesReader: cannot unread at start\")",
		"trimTrailingCR(bytes.slice(self.data, start, end))",
		"trimTrailingCR(bytes.slice(self.data, start, total))",
		"let mut line = r.readLine()?",
		"total = total + w.writeLine(line)?",
		"strings.trimSuffix(raw[i], \"\\r\")",
		"strings.endsWith(text, \"\\n\")",
		"dst.flush()?",
		"w.flush()?",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("io helper source missing %q", want)
		}
	}
}

func ioModuleSource(t *testing.T) string {
	t.Helper()
	reg := LoadCached()
	mod := reg.Modules["io"]
	if mod == nil {
		t.Fatal("stdlib io module missing")
	}
	return string(mod.Source)
}
