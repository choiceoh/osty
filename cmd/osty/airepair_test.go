package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var (
	ostyCLIBuildOnce sync.Once
	ostyCLIBuildPath string
	ostyCLIBuildErr  error
)

type cliRunResult struct {
	stdout string
	stderr string
	exit   int
}

func TestAIRepairCommandAndLegacyAliasRewriteSource(t *testing.T) {
	for _, cmdName := range []string{"airepair", "repair"} {
		t.Run(cmdName, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "main.osty")
			if err := os.WriteFile(path, []byte("func main() {}\n"), 0o644); err != nil {
				t.Fatalf("write source: %v", err)
			}

			got := runOstyCLI(t, cmdName, "--write", path)
			if got.exit != 0 {
				t.Fatalf("%s exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", cmdName, got.exit, got.stdout, got.stderr)
			}
			if got.stdout != "" {
				t.Fatalf("%s stdout = %q, want empty stdout", cmdName, got.stdout)
			}
			if !strings.Contains(got.stderr, "osty airepair: applied 1 repair(s)") {
				t.Fatalf("%s stderr = %q, want canonical airepair summary", cmdName, got.stderr)
			}

			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read repaired source: %v", err)
			}
			if string(src) != "fn main() {}\n" {
				t.Fatalf("%s repaired source = %q, want %q", cmdName, string(src), "fn main() {}\n")
			}
		})
	}
}

func TestFmtAIRepairFlagAliasesDisableAutomaticFixes(t *testing.T) {
	for _, flagArg := range []string{"--airepair=false", "--repair=false", "--no-airepair", "--no-repair"} {
		t.Run(flagArg, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "main.osty")
			original := "func main() {}\n"
			if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
				t.Fatalf("write source: %v", err)
			}

			got := runOstyCLI(t, "fmt", flagArg, "--write", path)
			if got.exit != 2 {
				t.Fatalf("fmt %s exit = %d, want 2\nstdout:\n%s\nstderr:\n%s", flagArg, got.exit, got.stdout, got.stderr)
			}
			if !strings.Contains(got.stderr, "osty fmt:") {
				t.Fatalf("fmt %s stderr = %q, want osty fmt parse failure", flagArg, got.stderr)
			}

			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read source: %v", err)
			}
			if string(src) != original {
				t.Fatalf("fmt %s rewrote source = %q, want unchanged %q", flagArg, string(src), original)
			}
		})
	}
}

func TestUsageOutputUsesCanonicalAIRepairNames(t *testing.T) {
	t.Run("top-level", func(t *testing.T) {
		got := runOstyCLI(t)
		if got.exit != 2 {
			t.Fatalf("osty exit = %d, want 2\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
		}
		if !strings.Contains(got.stderr, "(parse|tokens|resolve|check|typecheck|lint|fmt|airepair|gen)") {
			t.Fatalf("stderr = %q, want top-level airepair command in usage", got.stderr)
		}
		if !strings.Contains(got.stderr, "--no-airepair") {
			t.Fatalf("stderr = %q, want canonical --no-airepair flag in help", got.stderr)
		}
		if !strings.Contains(got.stderr, "--capture-dir DIR") {
			t.Fatalf("stderr = %q, want airepair capture-dir flag in help", got.stderr)
		}
		if !strings.Contains(got.stderr, "--capture-if MODE") {
			t.Fatalf("stderr = %q, want airepair capture-if flag in help", got.stderr)
		}
		if !strings.Contains(got.stderr, "osty airepair triage DIR") {
			t.Fatalf("stderr = %q, want airepair triage command in usage", got.stderr)
		}
		if !strings.Contains(got.stderr, "osty airepair promote CASE") {
			t.Fatalf("stderr = %q, want airepair promote command in usage", got.stderr)
		}
		if !strings.Contains(got.stderr, "airepair-specific flags") {
			t.Fatalf("stderr = %q, want airepair-specific help header", got.stderr)
		}
		if !strings.Contains(got.stderr, "front-end airepair flags") {
			t.Fatalf("stderr = %q, want front-end airepair help header", got.stderr)
		}
		if strings.Contains(got.stderr, "\nrepair-specific flags") {
			t.Fatalf("stderr = %q, did not want legacy repair-specific header", got.stderr)
		}
	})

	t.Run("legacy-alias-subcommand-usage", func(t *testing.T) {
		got := runOstyCLI(t, "repair")
		if got.exit != 2 {
			t.Fatalf("osty repair exit = %d, want 2\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
		}
		if !strings.Contains(got.stderr, "usage: osty airepair [--check] [--write] [--json] [--capture-dir DIR] [--capture-name NAME] [--capture-if residual|changed|always] [--stdin-name NAME] [--mode auto|rewrite|parse|frontend] FILE|-") {
			t.Fatalf("stderr = %q, want canonical airepair subcommand usage", got.stderr)
		}
		if !strings.Contains(got.stderr, "osty airepair triage [--top N] DIR") {
			t.Fatalf("stderr = %q, want canonical airepair triage usage", got.stderr)
		}
		if !strings.Contains(got.stderr, "osty airepair promote [--dest DIR] [--name NAME] CASE") {
			t.Fatalf("stderr = %q, want canonical airepair promote usage", got.stderr)
		}
		if strings.Contains(got.stderr, "usage: osty repair [--check] [--write] FILE") {
			t.Fatalf("stderr = %q, did not want legacy repair usage text", got.stderr)
		}
	})
}

func TestAIRepairJSONReportFromStdin(t *testing.T) {
	got := runOstyCLIWithInput(t, "func main() {}\n", "airepair", "--json", "--stdin-name", "agent.osty", "-")
	if got.exit != 0 {
		t.Fatalf("airepair --json exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if strings.TrimSpace(got.stderr) != "" {
		t.Fatalf("stderr = %q, want empty stderr in json mode", got.stderr)
	}

	var report struct {
		Filename             string `json:"filename"`
		Mode                 string `json:"mode"`
		Status               string `json:"status"`
		Changed              bool   `json:"changed"`
		Improved             bool   `json:"improved"`
		Accepted             bool   `json:"accepted"`
		AcceptedReason       string `json:"accepted_reason"`
		RejectedReason       string `json:"rejected_reason"`
		ResidualPrimaryCode  string `json:"residual_primary_code"`
		ResidualPrimaryHabit string `json:"residual_primary_habit"`
		ChangeDetails        []struct {
			Kind        string  `json:"kind"`
			Phase       string  `json:"phase"`
			SourceHabit string  `json:"source_habit"`
			Confidence  float64 `json:"confidence"`
		} `json:"change_details"`
		Summary struct {
			TotalErrorsReduced int `json:"total_errors_reduced"`
			ResidualErrors     int `json:"residual_errors"`
		} `json:"summary"`
		Before struct {
			Parse struct {
				Errors int `json:"errors"`
			} `json:"parse"`
		} `json:"before"`
		After struct {
			Parse struct {
				Errors int `json:"errors"`
			} `json:"parse"`
		} `json:"after"`
		Source string `json:"source"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &report); err != nil {
		t.Fatalf("decode report: %v\nstdout:\n%s", err, got.stdout)
	}
	if report.Filename != "agent.osty" {
		t.Fatalf("filename = %q, want agent.osty", report.Filename)
	}
	if report.Mode != "auto" {
		t.Fatalf("mode = %q, want auto", report.Mode)
	}
	if report.Status != "repaired_clean" {
		t.Fatalf("status = %q, want repaired_clean", report.Status)
	}
	if !report.Changed || !report.Improved || !report.Accepted {
		t.Fatalf("report flags = changed:%v improved:%v accepted:%v, want all true", report.Changed, report.Improved, report.Accepted)
	}
	if got, want := report.AcceptedReason, "parse_errors_reduced"; got != want {
		t.Fatalf("accepted_reason = %q, want %q", got, want)
	}
	if report.RejectedReason != "" {
		t.Fatalf("rejected_reason = %q, want empty", report.RejectedReason)
	}
	if report.ResidualPrimaryCode != "" {
		t.Fatalf("residual_primary_code = %q, want empty for clean repair", report.ResidualPrimaryCode)
	}
	if report.ResidualPrimaryHabit != "" {
		t.Fatalf("residual_primary_habit = %q, want empty for clean repair", report.ResidualPrimaryHabit)
	}
	if report.Before.Parse.Errors == 0 {
		t.Fatalf("before.parse.errors = %d, want parse failures before repair", report.Before.Parse.Errors)
	}
	if report.After.Parse.Errors != 0 {
		t.Fatalf("after.parse.errors = %d, want 0 after repair", report.After.Parse.Errors)
	}
	if report.Source != "fn main() {}\n" {
		t.Fatalf("source = %q, want repaired source", report.Source)
	}
	if report.Summary.TotalErrorsReduced <= 0 {
		t.Fatalf("summary.total_errors_reduced = %d, want > 0", report.Summary.TotalErrorsReduced)
	}
	if report.Summary.ResidualErrors != 0 {
		t.Fatalf("summary.residual_errors = %d, want 0", report.Summary.ResidualErrors)
	}
	if len(report.ChangeDetails) != 1 {
		t.Fatalf("len(change_details) = %d, want 1", len(report.ChangeDetails))
	}
	if got := report.ChangeDetails[0].Kind; got != "function_keyword" {
		t.Fatalf("change_details[0].kind = %q, want function_keyword", got)
	}
	if got := report.ChangeDetails[0].Phase; got != "lexical" {
		t.Fatalf("change_details[0].phase = %q, want lexical", got)
	}
	if got := report.ChangeDetails[0].SourceHabit; got != "foreign_function_keyword" {
		t.Fatalf("change_details[0].source_habit = %q, want foreign_function_keyword", got)
	}
	if report.ChangeDetails[0].Confidence <= 0.0 {
		t.Fatalf("change_details[0].confidence = %v, want > 0", report.ChangeDetails[0].Confidence)
	}
}

func TestAIRepairCaptureResidualCaseWritesCorpusArtifacts(t *testing.T) {
	captureDir := filepath.Join(t.TempDir(), "captures")
	input := "func main() {\n    let pair = (1, 2)\n    let first = pair[0]\n    println(first)\n}\n"

	got := runOstyCLIWithInput(t,
		input,
		"airepair",
		"--json",
		"--capture-dir", captureDir,
		"--capture-name", "foreign_fn_tuple_index_case",
		"-")
	if got.exit != 0 {
		t.Fatalf("airepair capture exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}

	inputPath := filepath.Join(captureDir, "foreign_fn_tuple_index_case.input.osty")
	expectedPath := filepath.Join(captureDir, "foreign_fn_tuple_index_case.expected.osty")
	reportPath := filepath.Join(captureDir, "foreign_fn_tuple_index_case.report.json")
	for _, path := range []string{inputPath, expectedPath, reportPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected captured artifact %s: %v", path, err)
		}
	}

	inputBytes, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatalf("read captured input: %v", err)
	}
	if string(inputBytes) != input {
		t.Fatalf("captured input = %q, want original input", string(inputBytes))
	}

	expectedBytes, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("read captured expected: %v", err)
	}
	if got, want := string(expectedBytes), "fn main() {\n    let pair = (1, 2)\n    let first = pair[0]\n    println(first)\n}\n"; got != want {
		t.Fatalf("captured expected = %q, want %q", got, want)
	}

	var report struct {
		Status               string `json:"status"`
		ResidualPrimaryCode  string `json:"residual_primary_code"`
		ResidualPrimaryHabit string `json:"residual_primary_habit"`
		Summary              struct {
			ResidualErrors     int `json:"residual_errors"`
			TotalErrorsReduced int `json:"total_errors_reduced"`
		} `json:"summary"`
	}
	reportBytes, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read captured report: %v", err)
	}
	if err := json.Unmarshal(reportBytes, &report); err != nil {
		t.Fatalf("decode captured report: %v\nreport:\n%s", err, string(reportBytes))
	}
	if report.Status != "repaired_residual" {
		t.Fatalf("captured status = %q, want repaired_residual", report.Status)
	}
	if report.ResidualPrimaryCode == "" {
		t.Fatal("captured residual_primary_code = empty, want populated")
	}
	if got, want := report.ResidualPrimaryHabit, "foreign_function_keyword"; got != want {
		t.Fatalf("captured residual_primary_habit = %q, want %q", got, want)
	}
	if report.Summary.ResidualErrors <= 0 {
		t.Fatalf("captured summary.residual_errors = %d, want > 0", report.Summary.ResidualErrors)
	}
}

func TestAIRepairCaptureResidualDefaultSkipsCleanCases(t *testing.T) {
	captureDir := filepath.Join(t.TempDir(), "captures")

	got := runOstyCLIWithInput(t,
		"func main() {}\n",
		"airepair",
		"--json",
		"--capture-dir", captureDir,
		"--capture-name", "clean_case",
		"-")
	if got.exit != 0 {
		t.Fatalf("airepair capture exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}

	_, err := os.Stat(filepath.Join(captureDir, "clean_case.input.osty"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("capture residual default created clean artifact unexpectedly: %v", err)
	}
}

func TestAIRepairCaptureChangedWritesCleanRepairedCase(t *testing.T) {
	captureDir := filepath.Join(t.TempDir(), "captures")

	got := runOstyCLIWithInput(t,
		"func main() {}\n",
		"airepair",
		"--json",
		"--capture-dir", captureDir,
		"--capture-name", "changed_case",
		"--capture-if", "changed",
		"-")
	if got.exit != 0 {
		t.Fatalf("airepair capture exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}

	if _, err := os.Stat(filepath.Join(captureDir, "changed_case.input.osty")); err != nil {
		t.Fatalf("expected changed capture artifact: %v", err)
	}
}

func TestAIRepairCaptureFlagsRequireCaptureDir(t *testing.T) {
	for _, args := range [][]string{
		{"airepair", "--capture-name", "sample", "-"},
		{"airepair", "--capture-if", "changed", "-"},
	} {
		got := runOstyCLIWithInput(t, "func main() {}\n", args...)
		if got.exit != 2 {
			t.Fatalf("%v exit = %d, want 2\nstdout:\n%s\nstderr:\n%s", args, got.exit, got.stdout, got.stderr)
		}
		if !strings.Contains(got.stderr, "--capture") {
			t.Fatalf("%v stderr = %q, want capture-dir validation", args, got.stderr)
		}
	}
}

func TestAIRepairTriageSummarizesCapturedReports(t *testing.T) {
	captureDir := filepath.Join(t.TempDir(), "captures")

	residual := runOstyCLIWithInput(t,
		"func main() {\n    let pair = (1, 2)\n    let first = pair[0]\n    println(first)\n}\n",
		"airepair",
		"--json",
		"--capture-dir", captureDir,
		"--capture-name", "foreign_fn_tuple_index_case",
		"-")
	if residual.exit != 0 {
		t.Fatalf("capture residual exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", residual.exit, residual.stdout, residual.stderr)
	}

	changed := runOstyCLIWithInput(t,
		"func main() {}\n",
		"airepair",
		"--json",
		"--capture-dir", captureDir,
		"--capture-name", "foreign_fn_case",
		"--capture-if", "changed",
		"-")
	if changed.exit != 0 {
		t.Fatalf("capture changed exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", changed.exit, changed.stdout, changed.stderr)
	}

	got := runOstyCLI(t, "airepair", "triage", captureDir)
	if got.exit != 0 {
		t.Fatalf("airepair triage exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if strings.TrimSpace(got.stderr) != "" {
		t.Fatalf("triage stderr = %q, want empty stderr", got.stderr)
	}
	for _, want := range []string{
		"scanned 2 case(s)",
		"repaired_clean",
		"repaired_residual",
		"foreign_function_keyword",
		"E0700",
		"foreign_function_keyword -> E0700",
		"foreign_fn_tuple_index_case",
	} {
		if !strings.Contains(got.stdout, want) {
			t.Fatalf("triage stdout missing %q\nstdout:\n%s", want, got.stdout)
		}
	}
}

func TestAIRepairPromoteCopiesCapturedCaseIntoCorpus(t *testing.T) {
	captureDir := filepath.Join(t.TempDir(), "captures")
	destDir := filepath.Join(t.TempDir(), "corpus")

	captured := runOstyCLIWithInput(t,
		"func main() {\n    let pair = (1, 2)\n    let first = pair[0]\n    println(first)\n}\n",
		"airepair",
		"--json",
		"--capture-dir", captureDir,
		"--capture-name", "foreign_fn_tuple_index_case",
		"-")
	if captured.exit != 0 {
		t.Fatalf("capture residual exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", captured.exit, captured.stdout, captured.stderr)
	}

	got := runOstyCLI(t,
		"airepair",
		"promote",
		"--dest", destDir,
		"--name", "promoted_fn_tuple_index_case",
		filepath.Join(captureDir, "foreign_fn_tuple_index_case.report.json"))
	if got.exit != 0 {
		t.Fatalf("airepair promote exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if strings.TrimSpace(got.stderr) != "" {
		t.Fatalf("promote stderr = %q, want empty stderr", got.stderr)
	}
	if !strings.Contains(got.stdout, "promoted_fn_tuple_index_case.input.osty") {
		t.Fatalf("promote stdout = %q, want promoted path summary", got.stdout)
	}

	inputBytes, err := os.ReadFile(filepath.Join(destDir, "promoted_fn_tuple_index_case.input.osty"))
	if err != nil {
		t.Fatalf("read promoted input: %v", err)
	}
	expectedBytes, err := os.ReadFile(filepath.Join(destDir, "promoted_fn_tuple_index_case.expected.osty"))
	if err != nil {
		t.Fatalf("read promoted expected: %v", err)
	}
	if got, want := string(inputBytes), "func main() {\n    let pair = (1, 2)\n    let first = pair[0]\n    println(first)\n}\n"; got != want {
		t.Fatalf("promoted input = %q, want %q", got, want)
	}
	if got, want := string(expectedBytes), "fn main() {\n    let pair = (1, 2)\n    let first = pair[0]\n    println(first)\n}\n"; got != want {
		t.Fatalf("promoted expected = %q, want %q", got, want)
	}
}

func TestCheckWithAIRepairPassesForeignSyntax(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	if err := os.WriteFile(path, []byte("func main() {}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	without := runOstyCLI(t, "check", "--no-airepair", path)
	if without.exit == 0 {
		t.Fatalf("check --no-airepair exit = %d, want non-zero parse failure", without.exit)
	}

	with := runOstyCLI(t, "check", path)
	if with.exit != 0 {
		t.Fatalf("check auto airepair exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", with.exit, with.stdout, with.stderr)
	}
	if !strings.Contains(with.stderr, "osty check --airepair: applied 1 repair(s)") {
		t.Fatalf("stderr = %q, want in-memory airepair summary", with.stderr)
	}
}

func TestCheckWithAIRepairPassesPythonStyleBlocks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	source := "fn main():\n    while true:\n        println(1)\n"
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	without := runOstyCLI(t, "check", "--no-airepair", path)
	if without.exit == 0 {
		t.Fatalf("check --no-airepair exit = %d, want non-zero parse failure", without.exit)
	}

	with := runOstyCLI(t, "check", path)
	if with.exit != 0 {
		t.Fatalf("check auto airepair exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", with.exit, with.stdout, with.stderr)
	}
	if !strings.Contains(with.stderr, "osty check --airepair: applied 2 repair(s)") {
		t.Fatalf("stderr = %q, want multi-phase airepair summary", with.stderr)
	}
}

func TestCheckWithAIRepairPassesPythonElifBlocks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	source := "fn main() {\n    if true:\n        println(1)\n    elif false:\n        println(2)\n    else:\n        println(0)\n}\n"
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	without := runOstyCLI(t, "check", "--no-airepair", path)
	if without.exit == 0 {
		t.Fatalf("check --no-airepair exit = %d, want non-zero parse failure", without.exit)
	}

	with := runOstyCLI(t, "check", path)
	if with.exit != 0 {
		t.Fatalf("check auto airepair exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", with.exit, with.stdout, with.stderr)
	}
	if !strings.Contains(with.stderr, "osty check --airepair: applied 3 repair(s)") {
		t.Fatalf("stderr = %q, want multi-phase elif airepair summary", with.stderr)
	}
}

func TestCheckWithAIRepairPassesPythonBareTupleForBlocks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	source := "fn main() {\n    let items = [(1, 2)]\n    for k, v in items:\n        println(k)\n}\n"
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	without := runOstyCLI(t, "check", "--no-airepair", path)
	if without.exit == 0 {
		t.Fatalf("check --no-airepair exit = %d, want non-zero parse failure", without.exit)
	}

	with := runOstyCLI(t, "check", path)
	if with.exit != 0 {
		t.Fatalf("check auto airepair exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", with.exit, with.stdout, with.stderr)
	}
	if !strings.Contains(with.stderr, "osty check --airepair: applied 2 repair(s)") {
		t.Fatalf("stderr = %q, want tuple-loop airepair summary", with.stderr)
	}
}

func TestCheckWithAIRepairPassesJSForOfLoops(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	source := "fn main() {\n    let items = [1, 2]\n    for (const item of items) {\n        println(item)\n    }\n}\n"
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	without := runOstyCLI(t, "check", "--no-airepair", path)
	if without.exit == 0 {
		t.Fatalf("check --no-airepair exit = %d, want non-zero parse failure", without.exit)
	}

	with := runOstyCLI(t, "check", path)
	if with.exit != 0 {
		t.Fatalf("check auto airepair exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", with.exit, with.stdout, with.stderr)
	}
	if !strings.Contains(with.stderr, "osty check --airepair: applied 1 repair(s)") {
		t.Fatalf("stderr = %q, want JS for-of airepair summary", with.stderr)
	}
}

func TestCheckWithAIRepairPassesJSDestructuringForOfLoops(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	source := "fn main() {\n    let entries = [(1, 2)]\n    for (const [k, v] of entries) {\n        println(k)\n    }\n}\n"
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	without := runOstyCLI(t, "check", "--no-airepair", path)
	if without.exit == 0 {
		t.Fatalf("check --no-airepair exit = %d, want non-zero parse failure", without.exit)
	}

	with := runOstyCLI(t, "check", path)
	if with.exit != 0 {
		t.Fatalf("check auto airepair exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", with.exit, with.stdout, with.stderr)
	}
	if !strings.Contains(with.stderr, "osty check --airepair: applied 1 repair(s)") {
		t.Fatalf("stderr = %q, want JS destructuring for-of airepair summary", with.stderr)
	}
}

func TestCheckWithAIRepairPassesPythonRangeLoops(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	source := "fn main() {\n    for i in range(3):\n        println(i)\n}\n"
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	without := runOstyCLI(t, "check", "--no-airepair", path)
	if without.exit == 0 {
		t.Fatalf("check --no-airepair exit = %d, want non-zero parse failure", without.exit)
	}

	with := runOstyCLI(t, "check", path)
	if with.exit != 0 {
		t.Fatalf("check auto airepair exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", with.exit, with.stdout, with.stderr)
	}
	if !strings.Contains(with.stderr, "osty check --airepair: applied 2 repair(s)") {
		t.Fatalf("stderr = %q, want Python range airepair summary", with.stderr)
	}
}

func TestCheckWithAIRepairPassesPythonEnumerateLoops(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	source := "fn main() {\n    let items = [1, 2]\n    for i, item in enumerate(items):\n        println(item)\n}\n"
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	without := runOstyCLI(t, "check", "--no-airepair", path)
	if without.exit == 0 {
		t.Fatalf("check --no-airepair exit = %d, want non-zero parse failure", without.exit)
	}

	with := runOstyCLI(t, "check", path)
	if with.exit != 0 {
		t.Fatalf("check auto airepair exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", with.exit, with.stdout, with.stderr)
	}
	if !strings.Contains(with.stderr, "osty check --airepair: applied 3 repair(s)") {
		t.Fatalf("stderr = %q, want Python enumerate airepair summary", with.stderr)
	}
}

func TestCheckWithAIRepairPassesSemanticForeignHelpers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	source := "fn main() {\n    let mut items = [1, 2]\n    let count = len(items)\n    let size = items.length\n    items = append(items, count + size)\n    println(items)\n}\n"
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	without := runOstyCLI(t, "check", "--no-airepair", path)
	if without.exit == 0 {
		t.Fatalf("check --no-airepair exit = %d, want non-zero failure", without.exit)
	}

	with := runOstyCLI(t, "check", path)
	if with.exit != 0 {
		t.Fatalf("check auto airepair exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", with.exit, with.stdout, with.stderr)
	}
	if !strings.Contains(with.stderr, "osty check --airepair: applied 3 repair(s)") {
		t.Fatalf("stderr = %q, want semantic-helper airepair summary", with.stderr)
	}
}

func TestCheckWithAIRepairPassesPythonMatchCaseBlocks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	source := "fn main():\n    let value = 0\n    match value:\n        case 0:\n            println(0)\n        default:\n            println(1)\n"
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	without := runOstyCLI(t, "check", "--no-airepair", path)
	if without.exit == 0 {
		t.Fatalf("check --no-airepair exit = %d, want non-zero parse failure", without.exit)
	}

	with := runOstyCLI(t, "check", path)
	if with.exit != 0 {
		t.Fatalf("check auto airepair exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", with.exit, with.stdout, with.stderr)
	}
	if !strings.Contains(with.stderr, "osty check --airepair: applied 6 repair(s)") {
		t.Fatalf("stderr = %q, want multi-phase match/case airepair summary", with.stderr)
	}
}

func runOstyCLI(t *testing.T, args ...string) cliRunResult {
	t.Helper()
	return runOstyCLIWithInput(t, "", args...)
}

func runOstyCLIWithInput(t *testing.T, input string, args ...string) cliRunResult {
	t.Helper()
	bin := buildOstyCLI(t)
	cmd := exec.Command(bin, args...)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	} else {
		cmd.Stdin = bytes.NewReader(nil)
	}
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	result := cliRunResult{}
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("run osty %v: %v", args, err)
		}
		result.exit = exitErr.ExitCode()
	}
	result.stdout = stdout.String()
	result.stderr = stderr.String()
	return result
}

func buildOstyCLI(t *testing.T) string {
	t.Helper()

	ostyCLIBuildOnce.Do(func() {
		tmpDir, err := os.MkdirTemp("", "osty-cli-*")
		if err != nil {
			ostyCLIBuildErr = fmt.Errorf("mktemp: %w", err)
			return
		}
		ostyCLIBuildPath = filepath.Join(tmpDir, "osty")
		cmd := exec.Command("go", "build", "-o", ostyCLIBuildPath, ".")
		cmd.Dir, ostyCLIBuildErr = os.Getwd()
		if ostyCLIBuildErr != nil {
			ostyCLIBuildErr = fmt.Errorf("getwd: %w", ostyCLIBuildErr)
			return
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			ostyCLIBuildErr = fmt.Errorf("go build: %w\n%s", err, strings.TrimSpace(string(out)))
			return
		}
	})

	if ostyCLIBuildErr != nil {
		t.Fatal(ostyCLIBuildErr)
	}
	return ostyCLIBuildPath
}
