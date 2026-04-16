package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/osty/osty/internal/airepair"
)

type aiRepairCaptureMode string

const (
	aiRepairCaptureResidual aiRepairCaptureMode = "residual"
	aiRepairCaptureChanged  aiRepairCaptureMode = "changed"
	aiRepairCaptureAlways   aiRepairCaptureMode = "always"
)

func parseAIRepairCaptureMode(value string) (aiRepairCaptureMode, bool) {
	switch value {
	case "", string(aiRepairCaptureResidual):
		return aiRepairCaptureResidual, true
	case string(aiRepairCaptureChanged):
		return aiRepairCaptureChanged, true
	case string(aiRepairCaptureAlways):
		return aiRepairCaptureAlways, true
	default:
		return "", false
	}
}

func shouldCaptureAIRepairResult(result airepair.Result, mode aiRepairCaptureMode) bool {
	switch mode {
	case aiRepairCaptureAlways:
		return true
	case aiRepairCaptureChanged:
		return result.Changed
	default:
		return result.After.TotalErrors > 0
	}
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
