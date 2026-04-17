package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/osty/osty/internal/airepair"
	"github.com/osty/osty/internal/runner"
)

type aiRepairCaptureMode string

const (
	aiRepairCaptureResidual aiRepairCaptureMode = "residual"
	aiRepairCaptureChanged  aiRepairCaptureMode = "changed"
	aiRepairCaptureAlways   aiRepairCaptureMode = "always"
)

// parseAIRepairCaptureMode routes the --capture-if flag through
// toolchain/airepair_flags.osty and re-types the canonical name.
func parseAIRepairCaptureMode(value string) (aiRepairCaptureMode, bool) {
	res := runner.ParseAiRepairCaptureMode(value)
	if !res.Ok {
		return "", false
	}
	return aiRepairCaptureMode(res.Mode), true
}

// shouldCaptureAIRepairResult destructures the airepair.Result
// down to (changed, totalErrorsAfter) and lets the shared policy
// decide. Keeps the airepair.Result Go type out of Osty so the
// toolchain module stays pure.
func shouldCaptureAIRepairResult(result airepair.Result, mode aiRepairCaptureMode) bool {
	return runner.ShouldCaptureAiRepair(string(mode), result.Changed, result.After.TotalErrors)
}

func captureAIRepairCase(dir, base string, result airepair.Result) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	reportBytes, err := json.MarshalIndent(result.JSONReport(), "", "  ")
	if err != nil {
		return err
	}
	reportBytes = append(reportBytes, '\n')

	if err := os.WriteFile(filepath.Join(dir, base+".input.osty"), result.Original, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, base+".expected.osty"), result.Repaired, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, base+".report.json"), reportBytes, 0o644); err != nil {
		return err
	}
	return nil
}

func aiRepairCaptureBase(explicit, displayName string) string {
	name := strings.TrimSpace(explicit)
	if name == "" {
		name = filepath.Base(displayName)
		ext := filepath.Ext(name)
		name = strings.TrimSuffix(name, ext)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "airepair_case"
	}

	var b strings.Builder
	lastUnderscore := false
	for _, r := range name {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '-', r == '_', r == '.':
			b.WriteRune(r)
			lastUnderscore = false
		default:
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	out := strings.Trim(b.String(), "._-")
	if out == "" {
		return "airepair_case"
	}
	return out
}
