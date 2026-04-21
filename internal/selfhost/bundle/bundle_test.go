package bundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestToolchainCheckerBundleIsToolchainOnly(t *testing.T) {
	toolchain := ToolchainCheckerFiles()
	if !contains(toolchain, "toolchain/frontend.osty") {
		t.Fatalf("toolchain checker bundle missing toolchain frontend: %#v", toolchain)
	}
	for _, entry := range toolchain {
		if strings.HasPrefix(entry, "examples/selfhost-core/") {
			t.Fatalf("toolchain checker bundle unexpectedly references legacy selfhost-core path %q: %#v", entry, toolchain)
		}
	}
}

func TestMergeFilesPrependsPreludeAndNormalizesStringsUsage(t *testing.T) {
	root := t.TempDir()
	writeBundleFile(t, root, "go_strings.osty", `use go "strings" as strings {
    fn Split(s: String, sep: String) -> List<String>
}

fn demoGo() -> Int {
    strings.Split("a,b", ",").len()
}
`)
	writeBundleFile(t, root, "std_strings.osty", `use std.strings as strings

fn demoStd() -> String {
    strings.join(["a", "b"], ",")
}

fn demoSlice() -> String {
    strings.slice("abcd", 1, 3)
}
`)

	merged, err := MergeFiles(root, []string{"go_strings.osty", "std_strings.osty"})
	if err != nil {
		t.Fatalf("MergeFiles() error = %v", err)
	}
	got := string(merged)
	if !strings.HasPrefix(got, stringsPrelude+"\n") {
		t.Fatalf("merged source missing shared strings prelude:\n%s", got)
	}
	if count := strings.Count(got, `use go "strings" as strings {`); count != 1 {
		t.Fatalf("merged source should keep exactly one shared Go strings import, got %d:\n%s", count, got)
	}
	if strings.Contains(got, "use std.strings as strings") {
		t.Fatalf("merged source kept per-file std.strings import:\n%s", got)
	}
	if strings.Contains(got, "strings.join(") {
		t.Fatalf("merged source kept std.strings camelCase call:\n%s", got)
	}
	if strings.Contains(got, "strings.slice(") {
		t.Fatalf("merged source kept std.strings slice call:\n%s", got)
	}
	if !strings.Contains(got, `strings.Join(["a", "b"], ",")`) {
		t.Fatalf("merged source missing normalized PascalCase strings call:\n%s", got)
	}
	if !strings.Contains(got, `ostyStringsSlice("abcd", 1, 3)`) {
		t.Fatalf("merged source missing std.strings slice shim:\n%s", got)
	}
}

func TestMergeFilesNormalizesWhileLoopsForBootstrap(t *testing.T) {
	root := t.TempDir()
	writeBundleFile(t, root, "while_loop.osty", `fn countdown(n: Int) -> Int {
    let mut cur = n
    while cur > 0 {
        cur = cur - 1
    }
    cur
}
`)

	merged, err := MergeFiles(root, []string{"while_loop.osty"})
	if err != nil {
		t.Fatalf("MergeFiles() error = %v", err)
	}
	got := string(merged)
	if strings.Contains(got, "while cur > 0 {") {
		t.Fatalf("merged source kept while loop sugar:\n%s", got)
	}
	if !strings.Contains(got, "for cur > 0 {") {
		t.Fatalf("merged source missing bootstrap for-loop rewrite:\n%s", got)
	}
}

func writeBundleFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
