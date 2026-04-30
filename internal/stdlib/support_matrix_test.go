package stdlib

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
)

type stdlibSupportMatrix struct {
	production         []string
	productionAdjacent []string
}

// std.runtime.raw is intentionally internal support surface. It is bundled
// beside public modules but is not counted in STDLIB_MATRIX.md's public rows.
var stdlibSupportMatrixInternalModules = []string{
	"runtime.raw",
}

func TestStdlibSupportMatrixCoversEmbeddedModules(t *testing.T) {
	reg := LoadCached()
	matrix := loadStdlibSupportMatrix(t)
	got := sortedModuleNames(reg.Modules)
	want := sortedStrings(combineStringSlices(
		matrix.production,
		matrix.productionAdjacent,
		stdlibSupportMatrixInternalModules,
	))
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stdlib support matrix module set drifted\n got: %v\nwant: %v", got, want)
	}
}

func TestStdlibSupportMatrixModulesResolve(t *testing.T) {
	reg := LoadCached()
	matrix := loadStdlibSupportMatrix(t)
	for _, module := range combineStringSlices(matrix.production, matrix.productionAdjacent) {
		t.Run(module, func(t *testing.T) {
			mod := reg.Modules[module]
			if mod == nil {
				t.Fatalf("std.%s missing from registry", module)
			}
			if len(mod.Source) == 0 {
				t.Fatalf("std.%s source is empty", module)
			}
			if mod.File == nil {
				t.Fatalf("std.%s parsed file is nil", module)
			}
			if mod.Package == nil || mod.Package.PkgScope == nil {
				t.Fatalf("std.%s resolved package scope is nil", module)
			}
		})
	}
}

func loadStdlibSupportMatrix(t *testing.T) stdlibSupportMatrix {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	data, err := os.ReadFile(filepath.Join(root, "STDLIB_MATRIX.md"))
	if err != nil {
		t.Fatalf("read STDLIB_MATRIX.md: %v", err)
	}
	matrix := parseStdlibSupportMatrix(string(data))
	if len(matrix.production) == 0 {
		t.Fatal("STDLIB_MATRIX.md production section produced no modules")
	}
	if len(matrix.productionAdjacent) == 0 {
		t.Fatal("STDLIB_MATRIX.md production-adjacent section produced no modules")
	}
	return matrix
}

func parseStdlibSupportMatrix(markdown string) stdlibSupportMatrix {
	const (
		sectionNone = iota
		sectionProduction
		sectionProductionAdjacent
	)
	var matrix stdlibSupportMatrix
	section := sectionNone
	for _, line := range strings.Split(markdown, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "### ") {
			switch {
			case strings.Contains(trimmed, "Production-adjacent"):
				section = sectionProductionAdjacent
			case strings.Contains(trimmed, "Production"):
				section = sectionProduction
			default:
				section = sectionNone
			}
			continue
		}
		module, ok := stdlibSupportMatrixTableModule(trimmed)
		if !ok {
			continue
		}
		switch section {
		case sectionProduction:
			matrix.production = append(matrix.production, module)
		case sectionProductionAdjacent:
			matrix.productionAdjacent = append(matrix.productionAdjacent, module)
		}
	}
	return matrix
}

func stdlibSupportMatrixTableModule(line string) (string, bool) {
	if !strings.HasPrefix(line, "|") || strings.Contains(line, "---") {
		return "", false
	}
	cells := strings.Split(line, "|")
	if len(cells) < 3 {
		return "", false
	}
	module := strings.TrimSpace(cells[1])
	module = strings.Trim(module, "`")
	if module == "" || module == "모듈" || strings.Contains(module, " ") {
		return "", false
	}
	return module, true
}

func sortedModuleNames(mods map[string]*Module) []string {
	names := make([]string, 0, len(mods))
	for name := range mods {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func combineStringSlices(parts ...[]string) []string {
	var out []string
	for _, part := range parts {
		out = append(out, part...)
	}
	return out
}

func sortedStrings(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}
