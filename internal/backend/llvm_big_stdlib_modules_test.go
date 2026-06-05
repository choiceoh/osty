package backend

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLLVMBackendBinaryRunsStdZipStoredArchive(t *testing.T) {
	requireClangForBackendTest(t)
	requireRealLLVMEmission(t)
	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use std.zip

fn main() {
    let entry = zip.file("hello.txt", "hello".toBytes()).unwrap()
    let archive = zip.encode([entry]).unwrap()
    println(zip.isArchive(archive))
    println(zip.contains(archive, "hello.txt"))
    let names = zip.list(archive).unwrap()
    println(names.len())
    println(names[0])
    let payload = zip.extract(archive, "hello.txt").unwrap().unwrap()
    println(payload.toHex())
    println(zip.crc32("hello".toBytes()))
    println(zip.methodName(entry.method))
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		logBackendWarnings(t, result)
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	want := "true\ntrue\n1\nhello.txt\n68656c6c6f\n907060870\nstored\n"
	if got := string(output); got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryRunsStdXlsxRowsEncode(t *testing.T) {
	requireClangForBackendTest(t)
	requireRealLLVMEmission(t)
	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use std.xlsx
use std.zip

fn main() {
    let archive = xlsx.encodeRows("Report", [
        ["name", "score"],
        ["Ada", "42"],
    ]).unwrap()
    println(zip.isArchive(archive))
    println(zip.contains(archive, "xl/workbook.xml"))
    println(zip.contains(archive, "xl/worksheets/sheet1.xml"))
    let names = zip.list(archive).unwrap()
    println(names.len())
    println(names[0])
    let sheet = zip.extract(archive, "xl/worksheets/sheet1.xml").unwrap().unwrap().toString().unwrap()
    println(sheet.contains("Ada"))
    println(sheet.contains("inlineStr"))
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		logBackendWarnings(t, result)
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	want := "true\ntrue\ntrue\n7\n[Content_Types].xml\ntrue\ntrue\n"
	if got := string(output); got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryRunsStdImageMetadata(t *testing.T) {
	requireClangForBackendTest(t)
	requireRealLLVMEmission(t)
	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use std.bytes as bytes
use std.image

fn main() {
    let raw: List<Byte> = [b'G', b'I', b'F', b'8', b'9', b'a', 2.toByte(), 0.toByte(), 3.toByte(), 0.toByte(), 0.toByte()]
    let gif = bytes.from(raw)
    println(image.formatName(image.identify(gif)))
    println(image.isImage(gif))
    let meta = image.parse(gif).unwrap()
    println(meta.width)
    println(meta.height)
    println(meta.bitsPerPixel)
    println(meta.hasAlpha)
    let dims = image.dimensions(gif).unwrap()
    println(dims.width)
    println(dims.height)
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		logBackendWarnings(t, result)
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	want := "gif\ntrue\n2\n3\n1\ntrue\n2\n3\n"
	if got := string(output); got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryRunsStdSmtpPlanning(t *testing.T) {
	requireClangForBackendTest(t)
	requireRealLLVMEmission(t)
	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use std.email
use std.smtp

fn main() {
    let cfg = smtp.withAuth(
        smtp.withSecurity(
            smtp.config("smtp.example.com", smtp.defaultPort(StartTls), "client.local").unwrap(),
            StartTls,
        ),
        smtp.plainAuth("user", "secret"),
    )
    let from = email.address("sender@example.com").unwrap()
    let recipients = [email.address("rcpt@example.com").unwrap()]
    let msg = email.message(from, recipients, "Subject", "Body")
    let env = email.envelope(msg).unwrap()
    let cmds = smtp.commands(smtp.transaction(cfg, env)).unwrap()
    println(cmds.len())
    println(cmds[0])
    println(cmds[1])
    println(cmds[2])
    println(cmds[3])
    println(cmds[4])
    println(cmds[cmds.len() - 1])

    let reply = smtp.parseReply("250-STARTTLS").unwrap()
    println(reply.code)
    println(reply.continuation)
    let caps = smtp.capabilities(smtp.parseReplies("250-localhost\r\n250-AUTH PLAIN LOGIN\r\n250 OK\r\n").unwrap())
    println(smtp.hasCapability(caps, "auth"))
    println(smtp.supportsAuth(caps, "login"))
    println(smtp.dataBlock(".line").startsWith("..line"))
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		logBackendWarnings(t, result)
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	want := "" +
		"9\n" +
		"EHLO client.local\n" +
		"STARTTLS\n" +
		"EHLO client.local\n" +
		"AUTH PLAIN AHVzZXIAc2VjcmV0\n" +
		"MAIL FROM:<sender@example.com>\n" +
		"QUIT\n" +
		"250\n" +
		"true\n" +
		"true\n" +
		"true\n" +
		"true\n"
	if got := string(output); got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryRunsStdKvFileStore(t *testing.T) {
	requireClangForBackendTest(t)
	requireRealLLVMEmission(t)
	path := filepath.Join(t.TempDir(), "cache.jsonl")
	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, fmt.Sprintf(`use std.kv

fn main() {
    let store = kv.open(%q).unwrap()
    match kv.putString(store, "name", "osty") {
        Ok(_) -> {},
        Err(err) -> {
            println(err.message())
            return
        },
    }
    match kv.putInt(store, "count", 2) {
        Ok(_) -> {},
        Err(err) -> {
            println(err.message())
            return
        },
    }
    println(kv.getString(store, "name").unwrap().unwrap())
    println(kv.getInt(store, "count").unwrap().unwrap())
    println(kv.contains(store, "name").unwrap())
    match kv.remove(store, "name") {
        Ok(_) -> {},
        Err(err) -> {
            println(err.message())
            return
        },
    }
    println(kv.contains(store, "name").unwrap())
    println(kv.compact(store).unwrap())
    let keys = kv.keys(store).unwrap()
    println(keys.len())
    println(keys[0])
}
`, path))

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		logBackendWarnings(t, result)
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	want := "osty\n2\ntrue\nfalse\n1\n1\ncount\n"
	if got := string(output); got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryRunsStdWatchDiffPlanning(t *testing.T) {
	requireClangForBackendTest(t)
	requireRealLLVMEmission(t)
	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use std.watch as watch

fn main() {
    let before = watch.parseRows(".", ["F\t5\t1\t./src/a.md", "F\t2\t1\t./src/old.md"]).unwrap()
    let after = watch.parseRows(".", ["F\t6\t2\t./src/a.md", "F\t1\t1\t./src/b.md"]).unwrap()
    let events = watch.diff(before, after)
    println(events.len())
    println(watch.kindName(events[0].kind))
    println(events[0].relativePath)
    println(watch.kindName(events[1].kind))
    println(events[1].relativePath)
    println(watch.kindName(events[2].kind))
    println(events[2].relativePath)
    println(watch.summary(events))
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		logBackendWarnings(t, result)
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	want := "" +
		"3\n" +
		"removed\n" +
		"src/old.md\n" +
		"modified\n" +
		"src/a.md\n" +
		"created\n" +
		"src/b.md\n" +
		"1 created, 1 modified, 1 removed\n"
	if got := string(output); got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func logBackendWarnings(t *testing.T, result *Result) {
	t.Helper()
	if result == nil {
		return
	}
	for i, warning := range result.Warnings {
		t.Logf("warning[%d]: %v", i, warning)
	}
}
