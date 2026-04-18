package main

// Helpers for the `osty check --inspect` flag. The inspector itself
// lives in internal/check/inspect.go; this file only wires it to the
// CLI: it picks the output format (text vs NDJSON) based on --json and
// prefixes output with file paths when more than one file is involved.
//
// Algorithm reference: LANG_SPEC_v0.5/02a-type-inference.md.

import (
	"fmt"
	"os"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/resolve"
)

// runInspect emits inspector records for a single file to stdout.
// Chooses NDJSON when the global --json flag is set, otherwise the
// tab-separated text form.
func runInspect(file *ast.File, chk *check.Result, flags cliFlags) {
	if file == nil || chk == nil {
		return
	}
	recs := check.Inspect(file, chk)
	writeInspect(recs, flags)
}

// runInspectPackage emits inspector records for every file in pkg,
// prefixing each file's section with its path so merged output remains
// readable. The chk Result is shared across the package's files, so we
// call Inspect once per file using the same Result.
func runInspectPackage(pkg *resolve.Package, chk *check.Result, flags cliFlags) {
	if pkg == nil || chk == nil {
		return
	}
	for _, pf := range pkg.Files {
		if pf == nil || pf.File == nil {
			continue
		}
		if !flags.jsonOutput {
			fmt.Printf("# %s\n", pf.Path)
		}
		recs := check.Inspect(pf.File, chk)
		writeInspect(recs, flags)
	}
}

func writeInspect(recs []check.InspectRecord, flags cliFlags) {
	if flags.jsonOutput {
		if err := check.FormatInspectJSON(os.Stdout, recs); err != nil {
			fmt.Fprintf(os.Stderr, "osty check --inspect: %v\n", err)
		}
		return
	}
	if err := check.FormatInspectText(os.Stdout, recs); err != nil {
		fmt.Fprintf(os.Stderr, "osty check --inspect: %v\n", err)
	}
}
