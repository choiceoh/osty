package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckCLIHandlesStdUrlAndCharImports(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	source := `use std.char
use std.url

fn main() {
    let parsed = url.parse("https://[2001:DB8::1]:8443/a%20b/c?z=last&x=1#frag%20ment").unwrap()
    let rebuilt = parsed.toString()
    let joined = url.join("https://example.com/a/b/c?q=1#old", "../d?x=1#new").unwrap()
    let digit = char.hexDigitValue('f').unwrap()
    let same = char.eqIgnoreAsciiCase('A', 'a')
    let mapped = char.fromDigit(35, 36).unwrap()

    println(parsed.scheme)
    println(parsed.host)
    println(rebuilt)
    println(joined)
    println(digit)
    println(same)
    println(mapped)
}
`
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	got := runOstyCLI(t, "check", "--no-airepair", path)
	if got.exit != 0 {
		t.Fatalf("osty check exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if strings.Contains(got.stderr, "code generation is not implemented yet") {
		t.Fatalf("stderr = %q, did not want backend-lowering failure during check", got.stderr)
	}
}
