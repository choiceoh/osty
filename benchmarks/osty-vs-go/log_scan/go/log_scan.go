package logscanbench

import "strings"

var logLines = buildLogLines(1024)

func buildLogLines(n int) []string {
	levels := []string{"INFO", "WARN", "ERROR", "DEBUG", "INFO", "WARN", "INFO"}
	services := []string{"auth", "api", "db", "cache", "worker"}
	messages := []string{
		"request completed",
		"connection closed",
		"retry scheduled",
		"cache miss",
		"query executed",
		"timeout reached",
	}
	out := make([]string, n)
	for i := 0; i < n; i++ {
		lvl := levels[i%len(levels)]
		svc := services[(i*3)%len(services)]
		msg := messages[(i*7)%len(messages)]
		out[i] = "2026-04-22T12:00:00Z " + lvl + " " + svc + ": " + msg
	}
	return out
}

//go:noinline
func LogScan(lines []string) int64 {
	counts := make(map[string]int64, 8)
	for _, line := range lines {
		first := strings.IndexByte(line, ' ')
		if first < 0 {
			continue
		}
		rest := line[first+1:]
		second := strings.IndexByte(rest, ' ')
		if second < 0 {
			continue
		}
		level := rest[:second]
		counts[level]++
	}
	// Deterministic fold: sum counts multiplied by a per-level weight so
	// the result is stable regardless of map iteration order.
	var checksum int64
	checksum += counts["INFO"] * 1
	checksum += counts["WARN"] * 3
	checksum += counts["ERROR"] * 7
	checksum += counts["DEBUG"] * 11
	return checksum
}
