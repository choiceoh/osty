package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/lint"
)

// runExplain implements `osty explain CODE`. It accepts any diagnostic
// code the compiler knows about (Exxxx, Wxxxx, E2xxx, Lxxxx). Lint
// codes (Lxxxx) and their aliases (unused_let, etc.) are served by
// lint.LookupRule so behaviour stays identical to the long-standing
// `osty lint --explain` shortcut. Everything else goes through
// diag.Explain, which parses the doc comments baked into the binary
// from internal/diag/codes.go.
//
// Without arguments, prints a compact index of every known code so
// readers can scan for the one they want.
func runExplain(args []string) {
	if len(args) == 0 {
		runExplainList()
		return
	}
	if len(args) > 1 {
		fmt.Fprintln(os.Stderr, "usage: osty explain CODE")
		os.Exit(2)
	}
	code := strings.TrimSpace(args[0])
	if code == "" {
		fmt.Fprintln(os.Stderr, "usage: osty explain CODE")
		os.Exit(2)
	}

	// Lint codes have their own registry with alias support. Let that
	// path handle both "L0001" and "unused_let".
	if strings.HasPrefix(code, "L") || strings.HasPrefix(code, "l") {
		runLintExplain(strings.ToUpper(code))
		return
	}
	// Aliases like `unused_let` — defer to the lint registry before
	// declaring the code unknown.
	if _, ok := lint.LookupRule(code); ok {
		runLintExplain(code)
		return
	}

	// Compiler diagnostic — parse from the embedded codes.go.
	normalized := strings.ToUpper(code)
	d, ok := diag.Explain(normalized)
	if !ok {
		fmt.Fprintf(os.Stderr, "osty explain: unknown code %q\n", code)
		fmt.Fprintln(os.Stderr, "use `osty explain` with no argument to list every known code")
		os.Exit(2)
	}
	printCodeDoc(d)
}

// printCodeDoc writes a CodeDoc to stdout in the same shape
// `osty lint --explain` uses: header, summary, optional spec ref,
// optional example, optional fix.
func printCodeDoc(d diag.CodeDoc) {
	fmt.Printf("%s  %s\n", d.Code, d.Name)
	if d.Summary != "" {
		fmt.Printf("summary:  %s\n", d.Summary)
	}
	if d.Spec != "" {
		fmt.Printf("spec:     %s\n", d.Spec)
	}
	fmt.Println()
	for _, para := range d.Body {
		fmt.Println(para)
		fmt.Println()
	}
	if d.Example != "" {
		fmt.Println("Example:")
		for _, line := range strings.Split(d.Example, "\n") {
			fmt.Printf("    %s\n", line)
		}
		fmt.Println()
	}
	if d.Fix != "" {
		fmt.Printf("Fix: %s\n", d.Fix)
	}
}

// runExplainList prints every compiler code and every lint code in a
// single tab-separated table, sorted by code. Intended for piping into
// grep/fzf when you don't remember the exact number.
func runExplainList() {
	for _, d := range diag.AllCodes() {
		summary := d.Summary
		if summary == "" {
			summary = "(no summary)"
		}
		fmt.Printf("%s\t%s\t%s\n", d.Code, d.Name, summary)
	}
	for _, r := range lint.Rules() {
		fmt.Printf("%s\t%s\t%s\n", r.Code, r.Name, r.Summary)
	}
}
