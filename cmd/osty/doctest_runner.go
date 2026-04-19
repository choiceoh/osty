package main

// v0.5 (G32) §11 — doctest runner integration.
//
// Given a package that `osty test --doc` selected, this module:
//   (1) extracts every ` ```osty ``` ` fenced block from doc
//       comments via internal/doctest,
//   (2) synthesises a single `__osty_doctest_runner__.osty` source
//       file that wraps each block in `fn test_doc_<owner>_<n>() {
//       <block> }`,
//   (3) registers the synthesised file as another `PackageFile` on
//       the shared package so the rest of the test pipeline (parser
//       + resolver + checker + backend) sees the doctest fns
//       exactly like hand-written ones, and
//   (4) returns the list of nativeTestCase entries that point at the
//       synthesised file.
//
// The synthesis step preserves the owner + ordinal names so failures
// report which block of which declaration broke. Because the
// wrappers carry `#[test]`, they flow through the inline-`#[test]`
// discovery path from Phase 3.1 with no further wiring.

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/osty/osty/internal/canonical"
	"github.com/osty/osty/internal/doctest"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
)

// doctestRunnerFilename is the synthetic file name registered on the
// package when `--doc` is active. The sentinel prefix matches the
// existing `__osty_test_runner__.osty` convention so tooling that
// hides internal files can filter on the double-underscore prefix.
const doctestRunnerFilename = "__osty_doctest_runner__.osty"

// appendDoctestCases walks the package for doctests, synthesises a
// single runner file, attaches it to the package, and returns the
// additional test cases for the runner to execute. Returns
// (additional tests, nil) when doctests were found; (nil, nil) when
// the package has none.
//
// Mutation: extends pkg.Files with one entry. Safe to call once per
// run — subsequent callers will see the doctest cases as regular
// tests that happen to live in the synthetic file.
func appendDoctestCases(pkg *resolve.Package, filters []string) ([]nativeTestCase, error) {
	if pkg == nil {
		return nil, nil
	}
	var collected []doctest.Doctest
	for _, pf := range pkg.Files {
		if pf == nil || pf.File == nil {
			continue
		}
		collected = append(collected, doctest.Extract(pf.File)...)
	}
	if len(collected) == 0 {
		return nil, nil
	}

	// Stable name sort so test ordering — before the seeded
	// shuffle that runTestMain applies — is reproducible. Owner
	// primary, ordinal secondary.
	sort.SliceStable(collected, func(i, j int) bool {
		if collected[i].Owner != collected[j].Owner {
			return collected[i].Owner < collected[j].Owner
		}
		return collected[i].OrdinalInOwner < collected[j].OrdinalInOwner
	})

	runnerPath := filepath.Join(pkg.Dir, doctestRunnerFilename)
	source := doctest.BuildRunnerSource(collected)
	file, diags := parser.ParseDiagnostics(source)
	if file == nil {
		return nil, fmt.Errorf("parse doctest runner: %v", diags)
	}
	for _, d := range diags {
		if d != nil && d.Severity.String() == "error" {
			return nil, fmt.Errorf("doctest runner has parse errors; the offending block is one of %d blocks synthesised from package %s — fix the Osty code inside the ```osty fence", len(collected), pkg.Dir)
		}
	}
	canonicalSrc, canonicalMap := canonical.SourceWithMap(source, file)
	pkg.Files = append(pkg.Files, &resolve.PackageFile{
		Path:            runnerPath,
		Source:          source,
		CanonicalSource: canonicalSrc,
		CanonicalMap:    canonicalMap,
		File:            file,
		ParseDiags:      diags,
	})

	tests := make([]nativeTestCase, 0, len(collected))
	for _, d := range collected {
		name := doctest.FnName(d)
		if !matchesTestFilters(name, filters) {
			continue
		}
		tests = append(tests, nativeTestCase{
			Name: name,
			Path: runnerPath,
		})
	}
	return tests, nil
}
