package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/osty/osty/internal/airepair"
	"github.com/osty/osty/internal/repair"
)

// runAIRepair implements `osty airepair`: a small pre-parser source fixer
// for common AI-authored Osty mistakes.
func runAIRepair(args []string) {
	os.Exit(runAIRepairMain(args, os.Stdin, os.Stdout, os.Stderr))
}

func runAIRepairMain(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "triage":
			return runAIRepairTriageMain(args[1:], stdout, stderr)
		case "learn":
			return runAIRepairLearnMain(args[1:], stdout, stderr)
		case "promote":
			return runAIRepairPromoteMain(args[1:], stdout, stderr)
		}
	}
	fs := flag.NewFlagSet("airepair", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: osty airepair [--check] [--write] [--json] [--capture-dir DIR] [--capture-name NAME] [--capture-if residual|changed|always] [--stdin-name NAME] [--mode auto|rewrite|parse|frontend] FILE|-")
		fmt.Fprintln(stderr, "       osty airepair triage [--top N] DIR")
		fmt.Fprintln(stderr, "       osty airepair learn [--top N] [--corpus DIR] [--json] DIR")
		fmt.Fprintln(stderr, "       osty airepair promote [--dest DIR] [--name NAME] CASE")
	}
	var checkMode, writeMode, jsonMode bool
	stdinName := "<stdin>"
	modeName := string(airepair.ModeAutoAssist)
	captureDir := ""
	captureName := ""
	captureModeName := string(aiRepairCaptureResidual)
	fs.BoolVar(&checkMode, "check", false, "exit 1 if FILE would be repaired")
	fs.BoolVar(&checkMode, "c", false, "alias for --check")
	fs.BoolVar(&writeMode, "write", false, "overwrite FILE in place")
	fs.BoolVar(&writeMode, "w", false, "alias for --write")
	fs.BoolVar(&jsonMode, "json", false, "emit a structured airepair report as JSON (including accepted/residual metadata)")
	fs.StringVar(&captureDir, "capture-dir", captureDir, "write captured airepair artifacts to DIR")
	fs.StringVar(&captureName, "capture-name", captureName, "basename for captured airepair artifacts")
	fs.StringVar(&captureModeName, "capture-if", captureModeName, "capture when residual, changed, or always")
	fs.StringVar(&stdinName, "stdin-name", stdinName, "filename to use in reports when reading from stdin")
	fs.StringVar(&modeName, "mode", modeName, "debug acceptance mode (auto, rewrite, parse, frontend)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	if checkMode && writeMode {
		fmt.Fprintln(stderr, "osty airepair: --check and --write are mutually exclusive")
		return 2
	}
	if captureDir == "" {
		if captureName != "" {
			fmt.Fprintln(stderr, "osty airepair: --capture-name requires --capture-dir")
			return 2
		}
		if captureModeName != string(aiRepairCaptureResidual) {
			fmt.Fprintln(stderr, "osty airepair: --capture-if requires --capture-dir")
			return 2
		}
	}

	mode, ok := parseAIRepairMode(modeName)
	if !ok {
		fmt.Fprintf(stderr, "osty airepair: unknown mode %q (want auto, rewrite, parse, or frontend)\n", modeName)
		return 2
	}
	captureMode, ok := parseAIRepairCaptureMode(captureModeName)
	if !ok {
		fmt.Fprintf(stderr, "osty airepair: unknown capture mode %q (want residual, changed, or always)\n", captureModeName)
		return 2
	}

	path := fs.Arg(0)
	displayName, src, err := readAIRepairInput(path, stdin, stdinName)
	if err != nil {
		fmt.Fprintf(stderr, "osty airepair: %v\n", err)
		return 1
	}
	if writeMode && path == "-" {
		fmt.Fprintln(stderr, "osty airepair: --write requires a real file path, not stdin")
		return 2
	}
	res := airepair.Analyze(airepair.Request{
		Source:   src,
		Filename: displayName,
		Mode:     mode,
	})
	if captureDir != "" && shouldCaptureAIRepairResult(res, captureMode) {
		if err := captureAIRepairCase(captureDir, aiRepairCaptureBase(captureName, displayName), res); err != nil {
			fmt.Fprintf(stderr, "osty airepair: %v\n", err)
			return 1
		}
	}
	changed := res.Changed

	if checkMode {
		if jsonMode {
			if err := json.NewEncoder(stdout).Encode(res.JSONReport()); err != nil {
				fmt.Fprintf(stderr, "osty airepair: %v\n", err)
				return 1
			}
		}
		if changed {
			if !jsonMode {
				reportRepairSummary(stderr, "osty airepair", displayName, res.Repair)
			}
			return 1
		}
		return 0
	}
	if writeMode {
		if changed {
			if err := os.WriteFile(path, res.Repaired, 0o644); err != nil {
				fmt.Fprintf(stderr, "osty airepair: %v\n", err)
				return 1
			}
		}
		if jsonMode {
			if err := json.NewEncoder(stdout).Encode(res.JSONReport()); err != nil {
				fmt.Fprintf(stderr, "osty airepair: %v\n", err)
				return 1
			}
			return 0
		}
		reportRepairSummary(stderr, "osty airepair", displayName, res.Repair)
		return 0
	}

	if jsonMode {
		if err := json.NewEncoder(stdout).Encode(res.JSONReport()); err != nil {
			fmt.Fprintf(stderr, "osty airepair: %v\n", err)
			return 1
		}
		return 0
	}

	if _, err := stdout.Write(res.Repaired); err != nil {
		fmt.Fprintf(stderr, "osty airepair: %v\n", err)
		return 1
	}
	reportRepairSummary(stderr, "osty airepair", displayName, res.Repair)
	return 0
}

func reportRepairSummary(w io.Writer, prefix, path string, res repair.Result) {
	for _, c := range res.Changes {
		fmt.Fprintf(w, "%s:%d:%d: repair %s: %s\n",
			path, c.Pos.Line, c.Pos.Column, c.Kind, c.Message)
	}
	if len(res.Changes) == 0 && res.Skipped == 0 {
		fmt.Fprintf(w, "%s: %s already clean\n", prefix, path)
		return
	}
	if res.Skipped > 0 {
		fmt.Fprintf(w, "%s: applied %d repair(s), skipped %d overlapping edit(s)\n",
			prefix,
			len(res.Changes), res.Skipped)
		return
	}
	fmt.Fprintf(w, "%s: applied %d repair(s)\n", prefix, len(res.Changes))
}

func parseAIRepairMode(value string) (airepair.Mode, bool) {
	switch value {
	case "", string(airepair.ModeAutoAssist):
		return airepair.ModeAutoAssist, true
	case string(airepair.ModeRewriteOnly):
		return airepair.ModeRewriteOnly, true
	case string(airepair.ModeParseAssist):
		return airepair.ModeParseAssist, true
	case string(airepair.ModeFrontEndAssist):
		return airepair.ModeFrontEndAssist, true
	default:
		return "", false
	}
}

func registerAIRepairCommandFlags(fs *flag.FlagSet, enabled *bool, mode *string) {
	fs.BoolVar(enabled, "airepair", true, "adapt common AI-authored foreign syntax in memory (enabled by default)")
	fs.BoolVar(enabled, "repair", true, "alias for --airepair")
	fs.StringVar(mode, "airepair-mode", string(airepair.ModeAutoAssist), "debug acceptance mode (auto, rewrite, parse, or frontend)")
	fs.StringVar(mode, "repair-mode", string(airepair.ModeAutoAssist), "alias for --airepair-mode")
}

func readAIRepairInput(path string, stdin io.Reader, stdinName string) (string, []byte, error) {
	if path == "-" {
		src, err := io.ReadAll(stdin)
		if err != nil {
			return "", nil, err
		}
		return stdinName, src, nil
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	return path, src, nil
}
