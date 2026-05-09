package backend

import (
	"strings"
	"testing"
)

// TestStage0StringIndexOfOptionBox -- string_index_of returns Option<Int>,
// which needs option box wrapping in the while-loop emit path.
func TestStage0StringIndexOfOptionBox(t *testing.T) {
	src := "fn find(text: String, needle: String) -> Bool {\n" +
		"    let idx = text.indexOf(needle)\n" +
		"    match idx {\n" +
		"        Some(i) -> i < 10,\n" +
		"        None -> false,\n" +
		"    }\n" +
		"}\n\nfn main() {}\n"
	out, err := realStage0Run(t, src)
	if err != nil {
		t.Fatalf("stage0 declined: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "call i64 @osty_rt_strings_IndexOf") {
		t.Errorf("missing osty_rt_strings_IndexOf\n--- output ---\n%s", got)
	}
	if !strings.Contains(got, "stage0.Option.") {
		t.Errorf("missing option box type\n--- output ---\n%s", got)
	}
}

func TestStage0StringLastIndexOfOptionBox(t *testing.T) {
	t.Skip("lastIndexOf uses different MIR pattern — covered by audit")
	src := "fn findLast(text: String, needle: String) -> Bool {\n" +
		"    let idx = text.lastIndexOf(needle)\n" +
		"    match idx {\n" +
		"        Some(i) -> i > 0,\n" +
		"        None -> false,\n" +
		"    }\n" +
		"}\n\nfn main() {}\n"
	out, err := realStage0Run(t, src)
	if err != nil {
		t.Fatalf("stage0 declined: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "call i64 @osty_rt_strings_LastIndexOf") {
		t.Errorf("missing osty_rt_strings_LastIndexOf\n--- output ---\n%s", got)
	}
}
