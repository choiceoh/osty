//go:build ignore

package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const unicodeVersion = "17.0.0"

const beginMarker = "// BEGIN GENERATED CASE FOLDING TABLES. DO NOT EDIT."
const endMarker = "// END GENERATED CASE FOLDING TABLES."

func main() {
	root := filepath.Join("internal", "stdlib", "unicode", unicodeVersion)
	simple, full := parseCaseFolding(filepath.Join(root, "CaseFolding.txt"))

	var block bytes.Buffer
	block.WriteString(beginMarker + "\n")
	block.WriteString("// Source: Unicode " + unicodeVersion + " UCD CaseFolding.txt.\n")
	block.WriteString("// Run: go run internal/stdlib/gen_strings_casefold_tables.go\n\n")

	writeFlatIntList(&block, "caseFoldSimpleFrom", extractFrom(simple))
	writeFlatIntList(&block, "caseFoldSimpleTo", extractSimpleTo(simple))

	writeFlatIntList(&block, "caseFoldFullFrom", extractFrom(full))
	fullOffsets, fullData := packFull(full)
	writeFlatIntList(&block, "caseFoldFullOffsets", fullOffsets)
	writeFlatIntList(&block, "caseFoldFullData", fullData)

	block.WriteString(endMarker)

	path := filepath.Join("internal", "stdlib", "modules", "strings.osty")
	src, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	text := string(src)
	start := strings.Index(text, beginMarker)
	end := strings.Index(text, endMarker)
	if start < 0 || end < 0 || end < start {
		// First run: insert just before the predicates section (or at end).
		insertion := "\n" + block.String() + "\n\n"
		text = text + insertion
	} else {
		end += len(endMarker)
		next := text[end:]
		if strings.HasPrefix(next, "\r\n") {
			end += 2
		} else if strings.HasPrefix(next, "\n") {
			end++
		}
		text = text[:start] + block.String() + "\n\n" + text[end:]
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		panic(err)
	}
}

type foldEntry struct {
	from rune
	to   []rune
}

func parseCaseFolding(path string) (simple, full []foldEntry) {
	f, err := os.Open(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 1<<20), 1<<20)
	for s.Scan() {
		line := s.Text()
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, ";")
		if len(fields) < 3 {
			continue
		}
		from := parseHex(strings.TrimSpace(fields[0]))
		status := strings.TrimSpace(fields[1])
		toStr := strings.TrimSpace(fields[2])
		var to []rune
		for _, tok := range strings.Fields(toStr) {
			to = append(to, parseHex(tok))
		}
		if len(to) == 0 {
			continue
		}
		switch status {
		case "C":
			// Common = simple AND full.
			simple = append(simple, foldEntry{from, to})
			full = append(full, foldEntry{from, to})
		case "S":
			simple = append(simple, foldEntry{from, to})
		case "F":
			full = append(full, foldEntry{from, to})
		case "T":
			// Turkic — omitted in non-locale-aware fold.
		}
	}
	if err := s.Err(); err != nil {
		panic(err)
	}
	sort.Slice(simple, func(i, j int) bool { return simple[i].from < simple[j].from })
	sort.Slice(full, func(i, j int) bool { return full[i].from < full[j].from })
	return
}

func parseHex(s string) rune {
	n, err := strconv.ParseInt(s, 16, 32)
	if err != nil {
		panic(fmt.Errorf("bad hex %q: %w", s, err))
	}
	return rune(n)
}

func extractFrom(entries []foldEntry) []int {
	out := make([]int, len(entries))
	for i, e := range entries {
		out[i] = int(e.from)
	}
	return out
}

func extractSimpleTo(entries []foldEntry) []int {
	out := make([]int, len(entries))
	for i, e := range entries {
		if len(e.to) != 1 {
			panic(fmt.Errorf("simple fold %04X has %d targets", e.from, len(e.to)))
		}
		out[i] = int(e.to[0])
	}
	return out
}

func packFull(entries []foldEntry) (offsets []int, data []int) {
	offsets = make([]int, len(entries)+1)
	for i, e := range entries {
		offsets[i] = len(data)
		for _, r := range e.to {
			data = append(data, int(r))
		}
	}
	offsets[len(entries)] = len(data)
	return
}

func writeFlatIntList(w *bytes.Buffer, name string, xs []int) {
	fmt.Fprintf(w, "pub let %s: List<Int> = [", name)
	if len(xs) == 0 {
		w.WriteString("]\n\n")
		return
	}
	const perLine = 8
	w.WriteByte('\n')
	for i, v := range xs {
		if i%perLine == 0 {
			w.WriteString("    ")
		}
		fmt.Fprintf(w, "0x%X", v)
		if i < len(xs)-1 {
			w.WriteByte(',')
			if (i+1)%perLine == 0 {
				w.WriteByte('\n')
			} else {
				w.WriteByte(' ')
			}
		}
	}
	w.WriteString("\n]\n\n")
}
