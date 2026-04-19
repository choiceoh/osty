package llvmgen

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/parser"
)

// TestSweepToolchainLargeTail walks all non-test toolchain/*.osty modules
// and reports each one's first lowering wall (or "clean"). Aggregates
// into a histogram so the class mix is visible at a glance instead of
// scrolling through dozens of per-file diagnostics.
//
// Info-only: never fails. Run with `go test -v -run
// TestSweepToolchainLargeTail` to see the histogram.
func TestSweepToolchainLargeTail(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	dir := filepath.Join(root, "toolchain")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read toolchain: %v", err)
	}

	type result struct {
		path  string
		clean bool
		wall  string
	}
	var results []result
	histogram := map[string]int{}

	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".osty") || strings.HasSuffix(name, "_test.osty") {
			continue
		}
		path := filepath.Join(dir, name)
		src, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		file, _ := parser.ParseDiagnostics(src)
		if file == nil {
			continue
		}
		_, err = generateFromAST(file, Options{PackageName: "main", SourcePath: path})
		r := result{path: name}
		if err == nil {
			r.clean = true
			histogram["CLEAN"]++
		} else {
			r.wall = wallCode(err.Error())
			histogram[r.wall]++
		}
		results = append(results, r)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].path < results[j].path
	})

	t.Logf("=== per-file ===")
	for _, r := range results {
		if r.clean {
			t.Logf("  %s: CLEAN", r.path)
		} else {
			t.Logf("  %s: %s", r.path, r.wall)
		}
	}
	t.Logf("=== histogram ===")
	keys := make([]string, 0, len(histogram))
	for k := range histogram {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Logf("  %s: %d", k, histogram[k])
	}
}

// TestSweepToolchainLargeTailLLVM011Subwalls drills into the LLVM011
// bucket by classifying each error message into a sub-pattern. Goal:
// identify the highest-leverage sub-wall to attack next.
//
// Info-only.
func TestSweepToolchainLargeTailLLVM011Subwalls(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	dir := filepath.Join(root, "toolchain")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read toolchain: %v", err)
	}
	typeIndex, err := buildToolchainTypeIndex(dir)
	if err != nil {
		t.Fatalf("build type index: %v", err)
	}

	type hit struct {
		file string
		msg  string
	}
	hits := []hit{}
	histogram := map[string]int{}

	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".osty") || strings.HasSuffix(name, "_test.osty") {
			continue
		}
		path := filepath.Join(dir, name)
		src, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		file, _ := parser.ParseDiagnostics(src)
		if file == nil {
			continue
		}
		_, err = generateFromAST(file, Options{PackageName: "main", SourcePath: path})
		if err == nil || !strings.Contains(err.Error(), "LLVM011") {
			continue
		}
		category := classifyLLVM011Rich(err.Error(), name, typeIndex)
		histogram[category]++
		hits = append(hits, hit{file: name, msg: err.Error()})
	}

	t.Logf("=== LLVM011 sub-walls ===")
	keys := make([]string, 0, len(histogram))
	for k := range histogram {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return histogram[keys[i]] > histogram[keys[j]]
	})
	for _, k := range keys {
		tag := ""
		if strings.HasPrefix(k, "cross_file_scope_") {
			tag = "  (scope artifact; type defined in sibling file)"
		}
		t.Logf("  %s: %d%s", k, histogram[k], tag)
	}
	t.Logf("=== examples (first per category) ===")
	seenCat := map[string]bool{}
	for _, h := range hits {
		c := classifyLLVM011Rich(h.msg, h.file, typeIndex)
		if seenCat[c] {
			continue
		}
		seenCat[c] = true
		t.Logf("  [%s] %s — %s", c, h.file, h.msg)
	}
}

// TestSweepToolchainLargeTailFocus prints the full wall message for
// ir.osty and check_env.osty so the first-hit signature is visible
// without scrolling the full sweep output. Info-only.
func TestSweepToolchainLargeTailFocus(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	dir := filepath.Join(root, "toolchain")
	typeIndex, err := buildToolchainTypeIndex(dir)
	if err != nil {
		t.Fatalf("build type index: %v", err)
	}
	focus := []string{"ir.osty", "check_env.osty"}
	for _, name := range focus {
		path := filepath.Join(dir, name)
		src, err := os.ReadFile(path)
		if err != nil {
			t.Logf("  %s: READ_ERR %v", name, err)
			continue
		}
		file, _ := parser.ParseDiagnostics(src)
		if file == nil {
			t.Logf("  %s: PARSE_FAIL", name)
			continue
		}
		_, err = generateFromAST(file, Options{PackageName: "main", SourcePath: path})
		t.Logf("  %s: %s", name, formatWallRich(err, name, typeIndex))
	}
}

// TestSweepToolchainLargeTailLLVM015Subwalls — same as LLVM011 sweep
// but for LLVM015 (call dispatch). Info-only.
func TestSweepToolchainLargeTailLLVM015Subwalls(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	dir := filepath.Join(root, "toolchain")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read toolchain: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".osty") || strings.HasSuffix(name, "_test.osty") {
			continue
		}
		path := filepath.Join(dir, name)
		src, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		file, _ := parser.ParseDiagnostics(src)
		if file == nil {
			continue
		}
		_, err = generateFromAST(file, Options{PackageName: "main", SourcePath: path})
		if err == nil || !strings.Contains(err.Error(), "LLVM015") {
			continue
		}
		t.Logf("  %s — %s", name, err.Error())
	}
}

// TestSweepToolchainPackageLevel merges every non-test
// toolchain/*.osty file into a SINGLE combined `ast.File` before
// lowering. This matches what `osty build` does for a real package
// — cross-file fn references (e.g. `stringUnitCount` defined in
// `semver_parse.osty` but called from `frontend.osty`) resolve
// because every top-level decl is in the same `g.functions` map.
//
// Contrast with TestSweepToolchainLargeTail which lowers each file
// in isolation. The single-file view is useful for attributing a
// wall to a specific source file, but it over-reports LLVM015 on
// benign cross-file references (the "test-harness artifact" noted
// in PR #375).
//
// Info-only: never fails. Prints CLEAN or the single wall so the
// operator can tell whether the remaining single-file LLVM015 was a
// true capability gap or just missing-symbol noise.
func TestSweepToolchainPackageLevel(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	dir := filepath.Join(root, "toolchain")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read toolchain: %v", err)
	}

	var merged ast.File
	sourceCount := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".osty") || strings.HasSuffix(name, "_test.osty") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		file, _ := parser.ParseDiagnostics(src)
		if file == nil {
			continue
		}
		merged.Uses = append(merged.Uses, file.Uses...)
		merged.Decls = append(merged.Decls, file.Decls...)
		sourceCount++
	}
	t.Logf("merged %d source files into one package", sourceCount)

	_, err = generateFromAST(&merged, Options{
		PackageName: "toolchain",
		SourcePath:  filepath.Join(dir, "<package>"),
	})
	if err == nil {
		t.Logf("=== package-level wall === CLEAN")
		return
	}
	t.Logf("=== package-level wall === %s", formatWall(err))
}

func wallCode(msg string) string {
	for _, code := range []string{"LLVM011", "LLVM012", "LLVM013", "LLVM014", "LLVM015", "LLVM016", "LLVM017", "LLVM018"} {
		if strings.Contains(msg, code) {
			return code
		}
	}
	return "OTHER"
}

// classifyLLVM011 splits the LLVM011 bucket into the concrete backend
// features that would retire each sub-wall. Categories are named
// after what the backend would need to grow, not what the source
// looks like — keeps the histogram pointing at real work.
//
// Plain wrapper for callers without a cross-file type index. The
// single-file sweep sees "type \"FooStruct\"" errors on every named
// type that lives in a sibling toolchain file; those are scope
// artifacts, not backend gaps. Use classifyLLVM011Rich to tell the
// difference.
func classifyLLVM011(msg string) string {
	return classifyLLVM011Rich(msg, "", nil)
}

// toolchainTypeIndex maps a top-level type name (struct / enum /
// type-alias) to the toolchain/*.osty basename that declares it. The
// single-file sweep uses this index to re-label "type \"X\"" LLVM011
// errors as cross-file scope when X is defined in a sibling file.
type toolchainTypeIndex map[string]string

var toolchainTypeDeclRE = regexp.MustCompile(`^\s*(?:pub\s+)?(struct|enum|type)\s+([A-Za-z_][A-Za-z0-9_]*)`)

// buildToolchainTypeIndex scans dir for *.osty files and records each
// top-level struct / enum / type-alias declaration with its basename.
// Test-only files are skipped. Kept deliberately simple: matches
// declaration headers line-by-line, which is good enough for the
// well-formatted toolchain/ layout (no comment-embedded keywords,
// one declaration per header line).
func buildToolchainTypeIndex(dir string) (toolchainTypeIndex, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	idx := toolchainTypeIndex{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".osty") || strings.HasSuffix(name, "_test.osty") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(src), "\n") {
			m := toolchainTypeDeclRE.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			if _, dup := idx[m[2]]; !dup {
				idx[m[2]] = name
			}
		}
	}
	return idx, nil
}

var llvm011TypeQuoteRE = regexp.MustCompile(`type "([^"]+)"`)

// classifyLLVM011Rich refines classifyLLVM011 with cross-file scope
// detection. When currentFile is non-empty and index is non-nil, a
// "type \"X\"" error whose X is defined in a sibling toolchain file
// is re-labelled cross_file_scope_{field|param|return}. Real backend
// gaps keep their original category.
func classifyLLVM011Rich(msg, currentFile string, index toolchainTypeIndex) string {
	if index != nil && currentFile != "" {
		if role, typeName, ok := extractLLVM011TypeRole(msg); ok {
			if defFile, found := index[typeName]; found && defFile != currentFile {
				return "cross_file_scope_" + role
			}
		}
	}
	switch {
	case strings.Contains(msg, "interpolation of i64 value requires .toString()"):
		return "int_interp_toString"
	case strings.Contains(msg, "interpolation of f64 value requires .toString()"):
		return "float_interp_toString"
	case strings.Contains(msg, "interpolation"):
		return "interp_other"
	case strings.Contains(msg, "type \"T\""):
		return "generic_T"
	case strings.Contains(msg, "list literal mixes"):
		return "list_mixed_ptr"
	case strings.Contains(msg, "plain String literals currently require ASCII"):
		return "string_non_ascii"
	case strings.Contains(msg, "field ") && strings.Contains(msg, ": type "):
		return "struct_field_type"
	case strings.Contains(msg, "parameter ") && strings.Contains(msg, ": type "):
		return "fn_param_struct_type"
	case strings.Contains(msg, "return type: type "):
		return "fn_return_struct_type"
	case strings.Contains(msg, "field type"):
		return "field_type_legacy"
	case strings.Contains(msg, "return type"):
		return "return_type_mismatch"
	case strings.Contains(msg, "struct literal type "):
		return "struct_literal_type"
	case strings.Contains(msg, "unknown struct "):
		return "unknown_struct"
	case strings.Contains(msg, "generic type alias "):
		return "generic_type_alias"
	case strings.Contains(msg, "cyclic type alias "):
		return "cyclic_type_alias"
	case strings.Contains(msg, "runtime ABI type "):
		return "runtime_abi_type"
	case strings.Contains(msg, "LLVM enum payloads"):
		return "enum_payload_type"
	case strings.Contains(msg, "type %T") || strings.Contains(msg, " type *ast."):
		return "ast_type_node"
	default:
		return "other"
	}
}

// extractLLVM011TypeRole pulls the "type \"X\"" payload from a
// struct-field / fn-param / fn-return LLVM011 message and returns
// the role marker used in cross_file_scope_{role} categories.
func extractLLVM011TypeRole(msg string) (role, typeName string, ok bool) {
	m := llvm011TypeQuoteRE.FindStringSubmatch(msg)
	if m == nil {
		return "", "", false
	}
	switch {
	case strings.Contains(msg, "return type: type "):
		return "return", m[1], true
	case strings.Contains(msg, "parameter ") && strings.Contains(msg, ": type "):
		return "param", m[1], true
	case strings.Contains(msg, "field ") && strings.Contains(msg, ": type "):
		return "field", m[1], true
	}
	return "", "", false
}

func classifyLLVM015(msg string) string {
	switch {
	case strings.Contains(msg, "*ast.FieldExpr"):
		return "method_call_field"
	case strings.Contains(msg, "*ast.Ident"):
		return "dynamic_ident_call"
	default:
		return "other"
	}
}

// formatWall renders a generateFromAST error as "CODE [subclass] — msg"
// (or "CLEAN" on nil). Shared by the focus/parity probes so every
// sweep speaks the same line format.
//
// For single-file sweep callers with a toolchain type index, use
// formatWallRich instead — it re-labels cross-file scope artifacts.
func formatWall(err error) string {
	return formatWallRich(err, "", nil)
}

// formatWallRich is formatWall with cross-file scope awareness.
// currentFile is the toolchain/*.osty basename that produced the
// error; index is built from buildToolchainTypeIndex. When either
// is zero-valued, behaves identically to formatWall.
func formatWallRich(err error, currentFile string, index toolchainTypeIndex) string {
	if err == nil {
		return "CLEAN"
	}
	msg := err.Error()
	code := wallCode(msg)
	sub := ""
	switch code {
	case "LLVM011":
		sub = classifyLLVM011Rich(msg, currentFile, index)
	case "LLVM015":
		sub = classifyLLVM015(msg)
	}
	if sub == "" {
		return fmt.Sprintf("%s — %s", code, msg)
	}
	return fmt.Sprintf("%s [%s] — %s", code, sub, msg)
}
