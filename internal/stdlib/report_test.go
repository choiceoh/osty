package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/resolve"
)

func TestReportModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["report"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.report not loaded")
	}
	for name, kind := range map[string]resolve.SymbolKind{
		"Align":         resolve.SymEnum,
		"CardTone":      resolve.SymEnum,
		"SummaryCard":   resolve.SymStruct,
		"TableColumn":   resolve.SymStruct,
		"TableRow":      resolve.SymStruct,
		"Table":         resolve.SymStruct,
		"ReportSection": resolve.SymStruct,
		"ChartPoint":    resolve.SymStruct,
		"Report":        resolve.SymStruct,
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.report missing type %q", name)
		}
		if sym.Kind != kind {
			t.Fatalf("std.report.%s kind = %s, want %s", name, sym.Kind, kind)
		}
		if !sym.Pub {
			t.Fatalf("std.report.%s not public", name)
		}
	}
	for _, name := range []string{
		"report", "section", "card", "cardDetail", "column", "alignedColumn", "row", "table",
		"chartPoint", "seriesPoint", "renderMarkdown", "renderHtml", "renderCardsMarkdown",
		"renderCardsHtml", "renderTableMarkdown", "renderTableHtml", "exportChartCsv",
		"exportChartJson", "escapeHtml", "escapeMarkdownText",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.report missing export %q", name)
		}
		if sym.Kind != resolve.SymFn {
			t.Fatalf("std.report.%s kind = %s, want fn", name, sym.Kind)
		}
		if !sym.Pub {
			t.Fatalf("std.report.%s not public", name)
		}
	}
}

func TestReportModuleSourcePinsRenderers(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["report"]
	if mod == nil {
		t.Fatal("std.report module missing")
	}
	src := string(mod.Source)
	for _, want := range []string{
		`pub struct SummaryCard`,
		`pub struct TableColumn`,
		`pub struct ChartPoint`,
		`pub fn renderMarkdown(report: Report) -> String`,
		`pub fn renderHtml(report: Report) -> String`,
		`renderTableMarkdown(table)`,
		`renderTableHtml(table)`,
		`exportChartCsv(report.chart)`,
		`exportChartJson(report.chart)`,
		`data-report-chart`,
		`let joined = strings.join(cells, " | ")`,
		`strings.replaceAll(text, "\"", "\"\"")`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.report source missing %q", want)
		}
	}
}
