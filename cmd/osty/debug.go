package main

import (
	"fmt"
	"os"
	"strings"
)

func reportTranspileWarning(tool, sourcePath, generatedPath string, err error) {
	if err == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "%s: backend warning: %v\n", tool, err)
	if sourcePath != "" {
		fmt.Fprintf(os.Stderr, "  source: %s\n", sourcePath)
	}
	if generatedPath != "" {
		fmt.Fprintf(os.Stderr, "  generated artifact: %s\n", generatedPath)
	}
	if sourcePath != "" && generatedPath != "" {
		fmt.Fprintf(os.Stderr, "  reproduce: osty gen %s -o %s\n",
			shellQuote(sourcePath), shellQuote(generatedPath))
	}
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n'\"\\$&;()<>|*?[]{}!") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
