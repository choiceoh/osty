package backend

import (
	"context"
	"os/exec"
	"testing"
)

func TestLLVMBackendBinaryRunsStdZipStoredArchive(t *testing.T) {
	requireClangForBackendTest(t)
	t.Setenv("OSTY_STDLIB_BODY_LOWER", "1")

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

func TestLLVMBackendBinaryRunsStdImageMetadata(t *testing.T) {
	requireClangForBackendTest(t)
	t.Setenv("OSTY_STDLIB_BODY_LOWER", "1")

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
	t.Setenv("OSTY_STDLIB_BODY_LOWER", "1")

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

func logBackendWarnings(t *testing.T, result *Result) {
	t.Helper()
	if result == nil {
		return
	}
	for i, warning := range result.Warnings {
		t.Logf("warning[%d]: %v", i, warning)
	}
}
