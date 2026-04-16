package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/osty/osty/internal/airepair"
	"github.com/osty/osty/internal/diag"
)

type aiRepairCapturedReport struct {
	Name   string
	Path   string
	Report airepair.Report
}

type aiRepairResidualCase struct {
	Name           string
	Status         airepair.ReportStatus
	PrimaryHabit   string
	ResidualErrors int
	Codes          []string
}

func runAIRepairTriageMain(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("airepair triage", flag.ContinueOnError)
	fs.SetOutput(stderr)
	topN := 10
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: osty airepair triage [--top N] DIR")
	}
	fs.IntVar(&topN, "top", topN, "show up to N entries in each summary section")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	if topN <= 0 {
		fmt.Fprintln(stderr, "osty airepair triage: --top must be greater than 0")
		return 2
	}

	dir := fs.Arg(0)
	reports, err := loadAIRepairCapturedReports(dir)
	if err != nil {
		fmt.Fprintf(stderr, "osty airepair triage: %v\n", err)
		return 1
	}
	captured, err := loadAIRepairCapturedCases(dir)
	if err != nil {
		fmt.Fprintf(stderr, "osty airepair triage: %v\n", err)
		return 1
	}
	corpus, err := loadAIRepairCorpusCases(defaultAIRepairCorpusDir())
	if err != nil {
		fmt.Fprintf(stderr, "osty airepair triage: %v\n", err)
		return 1
	}
	printAIRepairTriage(stdout, dir, reports, captured, corpus, topN)
	return 0
}

func runAIRepairPromoteMain(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("airepair promote", flag.ContinueOnError)
	fs.SetOutput(stderr)
	destDir := defaultAIRepairCorpusDir()
	destName := ""
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: osty airepair promote [--dest DIR] [--name NAME] CASE")
	}
	fs.StringVar(&destDir, "dest", destDir, "destination airepair corpus directory")
	fs.StringVar(&destName, "name", destName, "basename for promoted corpus files")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}

	base, err := resolveAIRepairCaseBase(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "osty airepair promote: %v\n", err)
		return 1
	}
	targetBase := aiRepairCaptureBase(destName, filepath.Base(base))
	if err := promoteAIRepairCase(base, destDir, targetBase); err != nil {
		fmt.Fprintf(stderr, "osty airepair promote: %v\n", err)
		return 1
	}

	destBase := filepath.Join(destDir, targetBase)
	fmt.Fprintf(stdout, "osty airepair promote: wrote %s.input.osty and %s.expected.osty\n", destBase, destBase)
	return 0
}

func defaultAIRepairCorpusDir() string {
	return filepath.Join("internal", "airepair", "testdata", "corpus")
}

func loadAIRepairCapturedReports(dir string) ([]aiRepairCapturedReport, error) {
	var reports []aiRepairCapturedReport
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".report.json") {
			return nil
		}
		report, err := readAIRepairCapturedReport(path)
		if err != nil {
			return err
		}
		reports = append(reports, aiRepairCapturedReport{
			Name:   strings.TrimSuffix(d.Name(), ".report.json"),
			Path:   path,
			Report: report,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(reports, func(i, j int) bool { return reports[i].Name < reports[j].Name })
	return reports, nil
}

func readAIRepairCapturedReport(path string) (airepair.Report, error) {
	var report airepair.Report
	data, err := os.ReadFile(path)
	if err != nil {
		return report, err
	}
	if err := json.Unmarshal(data, &report); err != nil {
		return report, fmt.Errorf("%s: %w", path, err)
	}
	return report, nil
}

func printAIRepairTriage(w io.Writer, dir string, reports []aiRepairCapturedReport, captured []aiRepairCapturedCase, corpus []aiRepairCorpusCase, topN int) {
	fmt.Fprintf(w, "osty airepair triage: scanned %d case(s) from %s\n", len(reports), dir)
	if len(reports) == 0 {
		return
	}

	statusCounts := map[string]int{}
	habitCounts := map[string]int{}
	residualCodeCounts := map[string]int{}
	residualPairCounts := map[string]int{}
	residualCases := make([]aiRepairResidualCase, 0)

	for _, entry := range reports {
		statusCounts[string(entry.Report.Status)]++

		habits := uniqueAIRepairSourceHabits(entry.Report)
		for _, habit := range habits {
			habitCounts[habit]++
		}

		if entry.Report.Summary.ResidualErrors <= 0 {
			continue
		}

		primaryHabit := aiRepairPrimaryHabit(entry.Report, habits)
		codes := uniqueAIRepairResidualCodes(entry.Report)
		for _, code := range codes {
			residualCodeCounts[code]++
			residualPairCounts[primaryHabit+" -> "+code]++
		}
		residualCases = append(residualCases, aiRepairResidualCase{
			Name:           entry.Name,
			Status:         entry.Report.Status,
			PrimaryHabit:   primaryHabit,
			ResidualErrors: entry.Report.Summary.ResidualErrors,
			Codes:          codes,
		})
	}

	printAIRepairCountSection(w, "status", statusCounts, topN)
	printAIRepairCountSection(w, "source habits", habitCounts, topN)
	if len(residualCases) == 0 {
		fmt.Fprintln(w, "residual diagnostic codes: none")
		return
	}
	printAIRepairCountSection(w, "residual diagnostic codes", residualCodeCounts, topN)
	printAIRepairCountSection(w, "residual habit/code pairs", residualPairCounts, topN)

	sort.Slice(residualCases, func(i, j int) bool {
		if residualCases[i].ResidualErrors != residualCases[j].ResidualErrors {
			return residualCases[i].ResidualErrors > residualCases[j].ResidualErrors
		}
		return residualCases[i].Name < residualCases[j].Name
	})
	if topN > len(residualCases) {
		topN = len(residualCases)
	}
	fmt.Fprintln(w, "residual cases:")
	for _, rc := range residualCases[:topN] {
		fmt.Fprintf(w, "  %s  status=%s residual_errors=%d primary_habit=%s codes=%s\n",
			rc.Name, rc.Status, rc.ResidualErrors, rc.PrimaryHabit, strings.Join(rc.Codes, ","))
	}
	printAIRepairLearningReport(w, buildAIRepairLearningReport(dir, defaultAIRepairCorpusDir(), captured, corpus, topN))
}

func printAIRepairCountSection(w io.Writer, title string, counts map[string]int, topN int) {
	fmt.Fprintf(w, "%s:\n", title)
	if len(counts) == 0 {
		fmt.Fprintln(w, "  none")
		return
	}
	type countEntry struct {
		name  string
		count int
	}
	entries := make([]countEntry, 0, len(counts))
	nameWidth := 0
	for name, count := range counts {
		entries = append(entries, countEntry{name: name, count: count})
		if len(name) > nameWidth {
			nameWidth = len(name)
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].count != entries[j].count {
			return entries[i].count > entries[j].count
		}
		return entries[i].name < entries[j].name
	})
	if topN > len(entries) {
		topN = len(entries)
	}
	for _, entry := range entries[:topN] {
		fmt.Fprintf(w, "  %-*s  %d\n", nameWidth, entry.name, entry.count)
	}
}

func uniqueAIRepairSourceHabits(report airepair.Report) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(report.ChangeDetails))
	for _, detail := range report.ChangeDetails {
		habit := strings.TrimSpace(detail.SourceHabit)
		if habit == "" {
			continue
		}
		if _, ok := seen[habit]; ok {
			continue
		}
		seen[habit] = struct{}{}
		out = append(out, habit)
	}
	sort.Strings(out)
	return out
}

func aiRepairPrimaryHabit(report airepair.Report, habits []string) string {
	if habit := strings.TrimSpace(report.ResidualPrimaryHabit); habit != "" {
		return habit
	}
	for i := len(report.ChangeDetails) - 1; i >= 0; i-- {
		habit := strings.TrimSpace(report.ChangeDetails[i].SourceHabit)
		if habit != "" {
			return habit
		}
	}
	if len(habits) == 0 {
		return "unknown"
	}
	return habits[0]
}

func uniqueAIRepairResidualCodes(report airepair.Report) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, d := range report.DiagnosticsAfter {
		if d == nil || d.Severity != diag.Error {
			continue
		}
		code := strings.TrimSpace(d.Code)
		if code == "" {
			code = "(uncoded)"
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		out = append(out, code)
	}
	if len(out) == 0 && report.Summary.ResidualErrors > 0 {
		out = append(out, "(uncoded)")
	}
	sort.Strings(out)
	return out
}

func resolveAIRepairCaseBase(path string) (string, error) {
	base := path
	switch {
	case strings.HasSuffix(base, ".input.osty"):
		base = strings.TrimSuffix(base, ".input.osty")
	case strings.HasSuffix(base, ".expected.osty"):
		base = strings.TrimSuffix(base, ".expected.osty")
	case strings.HasSuffix(base, ".report.json"):
		base = strings.TrimSuffix(base, ".report.json")
	}

	inputPath := base + ".input.osty"
	expectedPath := base + ".expected.osty"
	if _, err := os.Stat(inputPath); err != nil {
		return "", fmt.Errorf("missing captured input artifact %s", inputPath)
	}
	if _, err := os.Stat(expectedPath); err != nil {
		return "", fmt.Errorf("missing captured expected artifact %s", expectedPath)
	}
	return base, nil
}

func promoteAIRepairCase(sourceBase, destDir, destName string) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	destBase := filepath.Join(destDir, destName)
	for _, path := range []string{destBase + ".input.osty", destBase + ".expected.osty"} {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("destination already exists: %s", path)
		} else if !os.IsNotExist(err) {
			return err
		}
	}

	if err := copyAIRepairArtifact(sourceBase+".input.osty", destBase+".input.osty"); err != nil {
		return err
	}
	if err := copyAIRepairArtifact(sourceBase+".expected.osty", destBase+".expected.osty"); err != nil {
		return err
	}
	return nil
}

func copyAIRepairArtifact(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}
