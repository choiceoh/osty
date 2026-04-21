package selfhost

import (
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
)

// Until internal/selfhost/generated.go regenerates cleanly with
// toolchain/check_gates.osty folded in, synthesize the subset of gate
// diagnostics that native package/file callers already rely on. The merge
// helper dedupes against newer generated bundles, so this stays safe once the
// selfhost output catches up.

func selfhostAppendIntrinsicBodyGateForSource(result *CheckResult, src []byte) {
	if result == nil || len(src) == 0 {
		return
	}
	file, parseDiags := Parse(src)
	if file == nil || selfhostHasErrorDiagnostics(parseDiags) {
		return
	}
	selfhostMergeDiagnosticRecords(result, selfhostIntrinsicBodyDiagnostics(file, 0, "")...)
}

func selfhostAppendIntrinsicBodyGateForPackage(result *CheckResult, input PackageCheckInput) {
	if result == nil || len(input.Files) == 0 {
		return
	}
	var records []CheckDiagnosticRecord
	for _, file := range input.Files {
		if len(file.Source) == 0 {
			continue
		}
		parsed, parseDiags := Parse(file.Source)
		if parsed == nil || selfhostHasErrorDiagnostics(parseDiags) {
			continue
		}
		records = append(records, selfhostIntrinsicBodyDiagnostics(parsed, file.Base, file.Path)...)
	}
	selfhostMergeDiagnosticRecords(result, records...)
}

func selfhostHasErrorDiagnostics(diags []*diag.Diagnostic) bool {
	for _, d := range diags {
		if d != nil && d.Severity == diag.Error {
			return true
		}
	}
	return false
}

func selfhostIntrinsicBodyDiagnostics(file *ast.File, base int, path string) []CheckDiagnosticRecord {
	if file == nil {
		return nil
	}
	var out []CheckDiagnosticRecord
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FnDecl:
			if rec := selfhostIntrinsicBodyRecord(d, base, path); rec != nil {
				out = append(out, *rec)
			}
		case *ast.StructDecl:
			selfhostAppendIntrinsicBodyMethodRecords(&out, d.Methods, base, path)
		case *ast.EnumDecl:
			selfhostAppendIntrinsicBodyMethodRecords(&out, d.Methods, base, path)
		case *ast.UseDecl:
			for _, member := range d.GoBody {
				switch m := member.(type) {
				case *ast.FnDecl:
					if rec := selfhostIntrinsicBodyRecord(m, base, path); rec != nil {
						out = append(out, *rec)
					}
				case *ast.StructDecl:
					selfhostAppendIntrinsicBodyMethodRecords(&out, m.Methods, base, path)
				case *ast.EnumDecl:
					selfhostAppendIntrinsicBodyMethodRecords(&out, m.Methods, base, path)
				}
			}
		}
	}
	return out
}

func selfhostAppendIntrinsicBodyMethodRecords(out *[]CheckDiagnosticRecord, methods []*ast.FnDecl, base int, path string) {
	if out == nil {
		return
	}
	for _, method := range methods {
		if rec := selfhostIntrinsicBodyRecord(method, base, path); rec != nil {
			*out = append(*out, *rec)
		}
	}
}

func selfhostIntrinsicBodyRecord(fn *ast.FnDecl, base int, path string) *CheckDiagnosticRecord {
	if fn == nil || !selfhostHasAnnotation(fn.Annotations, "intrinsic") {
		return nil
	}
	if fn.Body == nil || len(fn.Body.Stmts) == 0 {
		return nil
	}
	start := base + fn.Body.Pos().Offset
	end := base + fn.Body.End().Offset
	if end < start {
		end = start
	}
	return &CheckDiagnosticRecord{
		Code:     diag.CodeIntrinsicNonEmptyBody,
		Severity: "error",
		Message:  "`#[intrinsic]` function `" + fn.Name + "` must have an empty body",
		Start:    start,
		End:      end,
		File:     path,
		Notes: []string{
			"LANG_SPEC §19.6: intrinsic implementations are supplied by the lowering layer; the source body is ignored",
			"hint: keep the signature and drop the body, or use `{}`",
		},
	}
}

func selfhostHasAnnotation(annots []*ast.Annotation, name string) bool {
	for _, annot := range annots {
		if annot != nil && annot.Name == name {
			return true
		}
	}
	return false
}

func selfhostMergeDiagnosticRecords(result *CheckResult, records ...CheckDiagnosticRecord) {
	if result == nil || len(records) == 0 {
		return
	}
	for _, record := range records {
		if idx := selfhostFindDiagnosticRecord(result.Diagnostics, record); idx >= 0 {
			existing := &result.Diagnostics[idx]
			if existing.File == "" {
				existing.File = record.File
			}
			existing.Notes = selfhostMergeNotes(existing.Notes, record.Notes)
			continue
		}
		result.Diagnostics = append(result.Diagnostics, record)
		selfhostTallyDiagnosticRecord(&result.Summary, record)
	}
}

func selfhostFindDiagnosticRecord(records []CheckDiagnosticRecord, want CheckDiagnosticRecord) int {
	for i, record := range records {
		if record.Code != want.Code || record.Severity != want.Severity || record.Message != want.Message {
			continue
		}
		if record.Start != want.Start || record.End != want.End {
			continue
		}
		return i
	}
	return -1
}

func selfhostMergeNotes(existing []string, extra []string) []string {
	if len(extra) == 0 {
		return existing
	}
	seen := make(map[string]struct{}, len(existing))
	for _, note := range existing {
		seen[note] = struct{}{}
	}
	for _, note := range extra {
		if _, ok := seen[note]; ok {
			continue
		}
		existing = append(existing, note)
		seen[note] = struct{}{}
	}
	return existing
}

func selfhostTallyDiagnosticRecord(summary *CheckSummary, record CheckDiagnosticRecord) {
	if summary == nil || !strings.EqualFold(record.Severity, "error") {
		return
	}
	summary.Errors++
	ctx := strings.TrimSpace(record.Code)
	if ctx == "" {
		ctx = "error"
	}
	if summary.ErrorsByContext == nil {
		summary.ErrorsByContext = map[string]int{}
	}
	summary.ErrorsByContext[ctx]++
	detail := strings.TrimSpace(record.Message)
	if detail == "" {
		return
	}
	if summary.ErrorDetails == nil {
		summary.ErrorDetails = map[string]map[string]int{}
	}
	if summary.ErrorDetails[ctx] == nil {
		summary.ErrorDetails[ctx] = map[string]int{}
	}
	summary.ErrorDetails[ctx][detail]++
}
