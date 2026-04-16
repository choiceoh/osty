package main

import (
	"bytes"
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
)

type aiRepairCapturedCase struct {
	Name         string
	Base         string
	ReportPath   string
	InputPath    string
	ExpectedPath string
	Report       airepair.Report
	Input        []byte
	Expected     []byte
}

type aiRepairCorpusCase struct {
	Name     string
	Base     string
	Input    []byte
	Expected []byte
}

type aiRepairLearningPriority struct {
	Key                  string                `json:"key"`
	Stage                string                `json:"stage"`
	PrimaryHabit         string                `json:"primary_habit"`
	PrimaryCode          string                `json:"primary_code"`
	Cases                int                   `json:"cases"`
	ResidualErrors       int                   `json:"residual_errors"`
	TotalErrorsReduced   int                   `json:"total_errors_reduced"`
	CoveredByCorpus      bool                  `json:"covered_by_corpus"`
	UncoveredCases       int                   `json:"uncovered_cases"`
	RepresentativeCase   string                `json:"representative_case"`
	RepresentativeStatus airepair.ReportStatus `json:"representative_status"`
	CorpusCase           string                `json:"corpus_case,omitempty"`
	SuggestedAction      string                `json:"suggested_action"`
	SuggestedNextStep    string                `json:"suggested_next_step"`
	SuggestedCorpusName  string                `json:"suggested_corpus_name"`
	Score                int                   `json:"score"`
}

type aiRepairLearningReport struct {
	Directory     string                     `json:"directory"`
	CorpusDir     string                     `json:"corpus_dir"`
	CapturedCases int                        `json:"captured_cases"`
	ResidualCases int                        `json:"residual_cases"`
	Priorities    []aiRepairLearningPriority `json:"priorities"`
}

type aiRepairLearningGroup struct {
	key                 string
	stage               string
	primaryHabit        string
	primaryCode         string
	cases               int
	residualErrors      int
	totalErrorsReduced  int
	uncoveredCases      int
	coveredCases        int
	representative      *aiRepairCapturedCase
	representativeMatch string
}

func runAIRepairLearnMain(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("airepair learn", flag.ContinueOnError)
	fs.SetOutput(stderr)
	topN := 10
	corpusDir := defaultAIRepairCorpusDir()
	jsonMode := false
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: osty airepair learn [--top N] [--corpus DIR] [--json] DIR")
	}
	fs.IntVar(&topN, "top", topN, "show up to N ranked learning priorities")
	fs.StringVar(&corpusDir, "corpus", corpusDir, "checked-in airepair corpus directory for coverage matching")
	fs.BoolVar(&jsonMode, "json", false, "emit ranked learning priorities as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	if topN <= 0 {
		fmt.Fprintln(stderr, "osty airepair learn: --top must be greater than 0")
		return 2
	}

	dir := fs.Arg(0)
	captured, err := loadAIRepairCapturedCases(dir)
	if err != nil {
		fmt.Fprintf(stderr, "osty airepair learn: %v\n", err)
		return 1
	}
	corpus, err := loadAIRepairCorpusCases(corpusDir)
	if err != nil {
		fmt.Fprintf(stderr, "osty airepair learn: %v\n", err)
		return 1
	}
	report := buildAIRepairLearningReport(dir, corpusDir, captured, corpus, topN)
	if jsonMode {
		if err := json.NewEncoder(stdout).Encode(report); err != nil {
			fmt.Fprintf(stderr, "osty airepair learn: %v\n", err)
			return 1
		}
		return 0
	}
	printAIRepairLearningReport(stdout, report)
	return 0
}

func loadAIRepairCapturedCases(dir string) ([]aiRepairCapturedCase, error) {
	reports, err := loadAIRepairCapturedReports(dir)
	if err != nil {
		return nil, err
	}
	cases := make([]aiRepairCapturedCase, 0, len(reports))
	for _, report := range reports {
		base := strings.TrimSuffix(report.Path, ".report.json")
		inputPath := base + ".input.osty"
		expectedPath := base + ".expected.osty"
		input, err := os.ReadFile(inputPath)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", inputPath, err)
		}
		expected, err := os.ReadFile(expectedPath)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", expectedPath, err)
		}
		cases = append(cases, aiRepairCapturedCase{
			Name:         report.Name,
			Base:         base,
			ReportPath:   report.Path,
			InputPath:    inputPath,
			ExpectedPath: expectedPath,
			Report:       report.Report,
			Input:        input,
			Expected:     expected,
		})
	}
	return cases, nil
}

func loadAIRepairCorpusCases(dir string) ([]aiRepairCorpusCase, error) {
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var out []aiRepairCorpusCase
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".input.osty") {
			return nil
		}
		base := strings.TrimSuffix(path, ".input.osty")
		expectedPath := base + ".expected.osty"
		input, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		expected, err := os.ReadFile(expectedPath)
		if err != nil {
			return fmt.Errorf("%s: %w", expectedPath, err)
		}
		out = append(out, aiRepairCorpusCase{
			Name:     strings.TrimSuffix(filepath.Base(path), ".input.osty"),
			Base:     base,
			Input:    input,
			Expected: expected,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func buildAIRepairLearningReport(dir, corpusDir string, captured []aiRepairCapturedCase, corpus []aiRepairCorpusCase, topN int) aiRepairLearningReport {
	groups := map[string]*aiRepairLearningGroup{}
	residualCases := 0
	for i := range captured {
		c := &captured[i]
		if c.Report.Summary.ResidualErrors <= 0 {
			continue
		}
		residualCases++
		habits := uniqueAIRepairSourceHabits(c.Report)
		primaryHabit := aiRepairPrimaryHabit(c.Report, habits)
		primaryCode := aiRepairPrimaryResidualCode(c.Report)
		stage := aiRepairDominantResidualStage(c.Report)
		key := primaryHabit + " -> " + primaryCode
		group := groups[key]
		if group == nil {
			group = &aiRepairLearningGroup{
				key:          key,
				stage:        stage,
				primaryHabit: primaryHabit,
				primaryCode:  primaryCode,
			}
			groups[key] = group
		}
		group.cases++
		group.residualErrors += c.Report.Summary.ResidualErrors
		group.totalErrorsReduced += c.Report.Summary.TotalErrorsReduced
		matchName, covered := aiRepairCorpusMatch(*c, corpus)
		if covered {
			group.coveredCases++
		} else {
			group.uncoveredCases++
		}
		if aiRepairPreferLearningRepresentative(group.representative, group.representativeMatch, c, matchName) {
			group.representative = c
			group.representativeMatch = matchName
		}
	}

	priorities := make([]aiRepairLearningPriority, 0, len(groups))
	for _, group := range groups {
		if group == nil || group.representative == nil {
			continue
		}
		priority := aiRepairLearningPriority{
			Key:                  group.key,
			Stage:                group.stage,
			PrimaryHabit:         group.primaryHabit,
			PrimaryCode:          group.primaryCode,
			Cases:                group.cases,
			ResidualErrors:       group.residualErrors,
			TotalErrorsReduced:   group.totalErrorsReduced,
			CoveredByCorpus:      group.uncoveredCases == 0,
			UncoveredCases:       group.uncoveredCases,
			RepresentativeCase:   group.representative.Name,
			RepresentativeStatus: group.representative.Report.Status,
			CorpusCase:           group.representativeMatch,
			SuggestedCorpusName:  aiRepairCaptureBase("", group.representative.Name),
		}
		priority.Score = aiRepairLearningScore(priority)
		priority.SuggestedAction, priority.SuggestedNextStep = aiRepairLearningAction(priority)
		priorities = append(priorities, priority)
	}

	sort.Slice(priorities, func(i, j int) bool {
		if priorities[i].Score != priorities[j].Score {
			return priorities[i].Score > priorities[j].Score
		}
		if priorities[i].Cases != priorities[j].Cases {
			return priorities[i].Cases > priorities[j].Cases
		}
		if priorities[i].ResidualErrors != priorities[j].ResidualErrors {
			return priorities[i].ResidualErrors > priorities[j].ResidualErrors
		}
		return priorities[i].Key < priorities[j].Key
	})
	if topN > 0 && len(priorities) > topN {
		priorities = priorities[:topN]
	}

	return aiRepairLearningReport{
		Directory:     dir,
		CorpusDir:     corpusDir,
		CapturedCases: len(captured),
		ResidualCases: residualCases,
		Priorities:    priorities,
	}
}

func aiRepairPrimaryResidualCode(report airepair.Report) string {
	if code := strings.TrimSpace(report.ResidualPrimaryCode); code != "" {
		return code
	}
	codes := uniqueAIRepairResidualCodes(report)
	if len(codes) == 0 {
		return "(uncoded)"
	}
	return codes[0]
}

func aiRepairDominantResidualStage(report airepair.Report) string {
	stages := []struct {
		name  string
		count int
	}{
		{name: "parser", count: report.Summary.ResidualParseErrors},
		{name: "resolve", count: report.Summary.ResidualResolveErrors},
		{name: "check", count: report.Summary.ResidualCheckErrors},
	}
	sort.SliceStable(stages, func(i, j int) bool { return stages[i].count > stages[j].count })
	if len(stages) == 0 || stages[0].count == 0 {
		return "mixed"
	}
	if len(stages) > 1 && stages[0].count == stages[1].count && stages[1].count > 0 {
		return "mixed"
	}
	return stages[0].name
}

func aiRepairCorpusMatch(captured aiRepairCapturedCase, corpus []aiRepairCorpusCase) (string, bool) {
	for _, fixture := range corpus {
		if bytes.Equal(captured.Input, fixture.Input) && bytes.Equal(captured.Expected, fixture.Expected) {
			return fixture.Name, true
		}
	}
	return "", false
}

func aiRepairPreferLearningRepresentative(current *aiRepairCapturedCase, currentMatch string, next *aiRepairCapturedCase, nextMatch string) bool {
	if next == nil {
		return false
	}
	if current == nil {
		return true
	}
	currentCovered := currentMatch != ""
	nextCovered := nextMatch != ""
	if currentCovered != nextCovered {
		return currentCovered && !nextCovered
	}
	if current.Report.Summary.ResidualErrors != next.Report.Summary.ResidualErrors {
		return next.Report.Summary.ResidualErrors > current.Report.Summary.ResidualErrors
	}
	if current.Report.Summary.TotalErrorsReduced != next.Report.Summary.TotalErrorsReduced {
		return next.Report.Summary.TotalErrorsReduced > current.Report.Summary.TotalErrorsReduced
	}
	return next.Name < current.Name
}

func aiRepairLearningScore(priority aiRepairLearningPriority) int {
	score := priority.Cases * 100
	score += priority.ResidualErrors * 10
	score += priority.TotalErrorsReduced * 3
	score += priority.UncoveredCases * 25
	return score
}

func aiRepairLearningAction(priority aiRepairLearningPriority) (string, string) {
	if priority.CoveredByCorpus {
		return "fix_repeated_" + priority.Stage,
			fmt.Sprintf("investigate the repeated %s-stage residual for %s; the corpus already covers this pattern", priority.Stage, priority.Key)
	}
	return "promote_and_fix_" + priority.Stage,
		fmt.Sprintf("promote %s into the corpus, then investigate the %s-stage residual for %s", priority.RepresentativeCase, priority.Stage, priority.Key)
}

func printAIRepairLearningReport(w io.Writer, report aiRepairLearningReport) {
	fmt.Fprintf(w, "osty airepair learn: ranked %d priority group(s) from %d residual case(s) in %s\n",
		len(report.Priorities), report.ResidualCases, report.Directory)
	if len(report.Priorities) == 0 {
		return
	}
	fmt.Fprintln(w, "learning priorities:")
	for i, priority := range report.Priorities {
		fmt.Fprintf(w, "  %d. %s  score=%d cases=%d residual_errors=%d stage=%s action=%s representative=%s",
			i+1,
			priority.Key,
			priority.Score,
			priority.Cases,
			priority.ResidualErrors,
			priority.Stage,
			priority.SuggestedAction,
			priority.RepresentativeCase,
		)
		if priority.CorpusCase != "" {
			fmt.Fprintf(w, " corpus=%s", priority.CorpusCase)
		} else {
			fmt.Fprintf(w, " corpus=uncovered")
		}
		fmt.Fprintln(w)
		fmt.Fprintf(w, "     next: %s\n", priority.SuggestedNextStep)
	}
}
