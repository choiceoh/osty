// log-aggregator — Go side of the end-to-end runtime program benchmark.
//
// Mirrors the Osty implementation byte-for-byte (deterministic LCG
// generator, identical filtering rules, identical output format) so
// hyperfine can compare wall-clock cost on real-app shape: parse →
// filter → group → sort → format.
//
// No I/O dependencies — the corpus is generated in-process from the
// same LCG seeded both sides, so there's no fixture file in source
// control to drift between languages.

package main

import (
	"fmt"
	"sort"
	"strings"
)

const N = 50000

func buildMins() []string {
	digits := []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9"}
	out := make([]string, 60)
	for i := 0; i < 60; i++ {
		out[i] = digits[i/10] + digits[i%10]
	}
	return out
}

func generateLogs(n int) []string {
	hours := []string{
		"00", "01", "02", "03", "04", "05", "06", "07", "08", "09",
		"10", "11", "12", "13", "14", "15", "16", "17", "18", "19",
		"20", "21", "22", "23",
	}
	mins := buildMins()
	levels := []string{"INFO", "DEBUG", "WARN", "ERROR"}
	users := []string{"alice", "bob", "carol", "dave", "eve"}
	actions := []string{"login", "logout", "query", "update", "delete"}

	out := make([]string, n)
	state := int64(1)
	for i := 0; i < n; i++ {
		state = (state*1103515245 + 12345) % 2147483648
		h := hours[(int64(i)/100)%24]
		m := mins[(state/256)%60]
		s := mins[(state/65536)%60]
		lvl := levels[(state/16)%4]
		u := users[(state/4096)%5]
		a := actions[(state/1048576)%5]
		out[i] = h + ":" + m + ":" + s + " " + lvl + " user=" + u + " action=" + a
	}
	return out
}

func main() {
	logs := generateLogs(N)
	levelCounts := map[string]int{}
	hourCounts := map[string]int{}
	total := 0
	kept := 0
	for _, line := range logs {
		total++
		parts := strings.SplitN(line, " ", 4)
		if len(parts) < 3 {
			continue
		}
		ts := parts[0]
		level := parts[1]
		if level == "DEBUG" {
			continue
		}
		kept++
		levelCounts[level]++
		// "HH:MM:SS" — first 2 chars are hour. Use Split for parity with the Osty side.
		hour := strings.Split(ts, ":")[0]
		hourCounts[hour]++
	}

	// Levels: sort by name for deterministic output.
	levelKeys := make([]string, 0, len(levelCounts))
	for k := range levelCounts {
		levelKeys = append(levelKeys, k)
	}
	sort.Strings(levelKeys)

	// Top 10 hours: sort by count desc, then hour asc.
	hourKeys := make([]string, 0, len(hourCounts))
	for k := range hourCounts {
		hourKeys = append(hourKeys, k)
	}
	sort.Slice(hourKeys, func(i, j int) bool {
		ci := hourCounts[hourKeys[i]]
		cj := hourCounts[hourKeys[j]]
		if ci != cj {
			return ci > cj
		}
		return hourKeys[i] < hourKeys[j]
	})
	if len(hourKeys) > 10 {
		hourKeys = hourKeys[:10]
	}

	fmt.Printf("total: %d\n", total)
	fmt.Printf("kept: %d\n", kept)
	fmt.Println("levels:")
	for _, k := range levelKeys {
		fmt.Printf("  %s: %d\n", k, levelCounts[k])
	}
	fmt.Println("top hours:")
	for _, k := range hourKeys {
		fmt.Printf("  hour=%s count=%d\n", k, hourCounts[k])
	}
}
