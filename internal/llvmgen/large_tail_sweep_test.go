package llvmgen

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

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
		category := classifyLLVM011(err.Error())
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
		t.Logf("  %s: %d", k, histogram[k])
	}
	t.Logf("=== examples (first per category) ===")
	seenCat := map[string]bool{}
	for _, h := range hits {
		c := classifyLLVM011(h.msg)
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
	focus := []string{"ir.osty", "check_env.osty"}
	for _, name := range focus {
		path := filepath.Join(root, "toolchain", name)
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
		t.Logf("  %s: %s", name, formatWall(err))
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
func classifyLLVM011(msg string) string {
	switch {
	case strings.Contains(msg, "interpolation of i64 value requires .toString()"):
		return "int_interp_toString"
	case strings.Contains(msg, "interpolation of f64 value requires .toString()"):
		return "float_interp_toString"
	case strings.Contains(msg, "interpolation"):
		return "interp_other"
	case strings.Contains(msg, "type \"T\""):
		return "generic_T"
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
func formatWall(err error) string {
	if err == nil {
		return "CLEAN"
	}
	msg := err.Error()
	code := wallCode(msg)
	sub := ""
	switch code {
	case "LLVM011":
		sub = classifyLLVM011(msg)
	case "LLVM015":
		sub = classifyLLVM015(msg)
	}
	if sub == "" {
		return fmt.Sprintf("%s — %s", code, msg)
	}
	return fmt.Sprintf("%s [%s] — %s", code, sub, msg)
}
