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

const beginMarker = "// BEGIN GENERATED NORMALIZATION TABLES. DO NOT EDIT."
const endMarker = "// END GENERATED NORMALIZATION TABLES."

type decomp struct {
	compat bool
	to     []rune
}

func main() {
	root := filepath.Join("internal", "stdlib", "unicode", unicodeVersion)
	ccc, decompMap := parseUnicodeData(filepath.Join(root, "UnicodeData.txt"))
	exclusions := parseCompositionExclusions(filepath.Join(root, "DerivedNormalizationProps.txt"))

	// Recursively expand canonical and compatibility decompositions.
	canonExpand := map[rune][]rune{}
	for r := range decompMap {
		canonExpand[r] = expand(r, decompMap, false, map[rune]bool{})
	}
	compatExpand := map[rune][]rune{}
	for r := range decompMap {
		compatExpand[r] = expand(r, decompMap, true, map[rune]bool{})
	}
	// Drop identity mappings (where expansion == [r]).
	for r, xs := range canonExpand {
		if len(xs) == 1 && xs[0] == r {
			delete(canonExpand, r)
		}
	}
	for r, xs := range compatExpand {
		if len(xs) == 1 && xs[0] == r {
			delete(compatExpand, r)
		}
	}

	// Composition pairs: only from 2-element canonical decompositions, and
	// only if the left char has CCC=0 (i.e., not a non-starter-decomposable),
	// and the target is not in the exclusion list.
	type pair struct{ a, b rune }
	composePairs := map[pair]rune{}
	for r, d := range decompMap {
		if d.compat {
			continue
		}
		if len(d.to) != 2 {
			continue
		}
		if exclusions[r] {
			continue
		}
		// Non-starter decomposables: if d.to[0] has CCC != 0, exclude per Unicode rule D117.
		if ccc[d.to[0]] != 0 {
			continue
		}
		composePairs[pair{d.to[0], d.to[1]}] = r
	}

	var block bytes.Buffer
	block.WriteString(beginMarker + "\n")
	block.WriteString("// Source: Unicode " + unicodeVersion + " UCD UnicodeData.txt + DerivedNormalizationProps.txt.\n")
	block.WriteString("// Run: go run internal/stdlib/gen_strings_normalize_tables.go\n\n")

	// CCC table: sorted keys, matching values.
	cccKeys := sortedRuneKeys(ccc)
	cccVals := make([]int, len(cccKeys))
	cccCP := make([]int, len(cccKeys))
	for i, r := range cccKeys {
		cccCP[i] = int(r)
		cccVals[i] = ccc[r]
	}
	writeFlatIntList(&block, "cccCode", cccCP)
	writeFlatIntList(&block, "cccValue", cccVals)

	// Canonical decomposition tables.
	canonKeys := sortedRuneKeys(canonExpand)
	canonFrom := make([]int, len(canonKeys))
	var canonOffsets []int
	var canonData []int
	for i, r := range canonKeys {
		canonFrom[i] = int(r)
		canonOffsets = append(canonOffsets, len(canonData))
		for _, x := range canonExpand[r] {
			canonData = append(canonData, int(x))
		}
	}
	canonOffsets = append(canonOffsets, len(canonData))
	writeFlatIntList(&block, "canonDecompFrom", canonFrom)
	writeFlatIntList(&block, "canonDecompOffsets", canonOffsets)
	writeFlatIntList(&block, "canonDecompData", canonData)

	// Compatibility decomposition tables.
	compatKeys := sortedRuneKeys(compatExpand)
	compatFrom := make([]int, len(compatKeys))
	var compatOffsets []int
	var compatData []int
	for i, r := range compatKeys {
		compatFrom[i] = int(r)
		compatOffsets = append(compatOffsets, len(compatData))
		for _, x := range compatExpand[r] {
			compatData = append(compatData, int(x))
		}
	}
	compatOffsets = append(compatOffsets, len(compatData))
	writeFlatIntList(&block, "compatDecompFrom", compatFrom)
	writeFlatIntList(&block, "compatDecompOffsets", compatOffsets)
	writeFlatIntList(&block, "compatDecompData", compatData)

	// Composition table: pack (a, b) → result. Sort by a then b for binary
	// search on a, then linear (or nested binary) search on b within the run.
	type triple struct{ a, b, r rune }
	var triples []triple
	for p, r := range composePairs {
		triples = append(triples, triple{p.a, p.b, r})
	}
	sort.Slice(triples, func(i, j int) bool {
		if triples[i].a != triples[j].a {
			return triples[i].a < triples[j].a
		}
		return triples[i].b < triples[j].b
	})
	composeA := make([]int, len(triples))
	composeB := make([]int, len(triples))
	composeR := make([]int, len(triples))
	for i, t := range triples {
		composeA[i] = int(t.a)
		composeB[i] = int(t.b)
		composeR[i] = int(t.r)
	}
	writeFlatIntList(&block, "composeFirst", composeA)
	writeFlatIntList(&block, "composeSecond", composeB)
	writeFlatIntList(&block, "composeResult", composeR)

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
		text = text + "\n" + block.String() + "\n"
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

func parseUnicodeData(path string) (map[rune]int, map[rune]decomp) {
	ccc := map[rune]int{}
	dec := map[rune]decomp{}

	f, err := os.Open(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 1<<20), 1<<20)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, ";")
		if len(fields) < 6 {
			continue
		}
		cp := parseHex(fields[0])
		if c, err := strconv.Atoi(fields[3]); err == nil && c != 0 {
			ccc[cp] = c
		}
		decStr := strings.TrimSpace(fields[5])
		if decStr == "" {
			continue
		}
		compat := false
		if strings.HasPrefix(decStr, "<") {
			compat = true
			if idx := strings.Index(decStr, ">"); idx >= 0 {
				decStr = strings.TrimSpace(decStr[idx+1:])
			}
		}
		var to []rune
		for _, tok := range strings.Fields(decStr) {
			to = append(to, parseHex(tok))
		}
		if len(to) > 0 {
			dec[cp] = decomp{compat: compat, to: to}
		}
	}
	if err := s.Err(); err != nil {
		panic(err)
	}
	return ccc, dec
}

func parseCompositionExclusions(path string) map[rune]bool {
	out := map[rune]bool{}
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
		if len(fields) < 2 {
			continue
		}
		prop := strings.TrimSpace(fields[1])
		if prop != "Full_Composition_Exclusion" {
			continue
		}
		rng := strings.TrimSpace(fields[0])
		lo, hi := parseRange(rng)
		for r := lo; r <= hi; r++ {
			out[r] = true
		}
	}
	if err := s.Err(); err != nil {
		panic(err)
	}
	return out
}

func expand(r rune, dec map[rune]decomp, compat bool, seen map[rune]bool) []rune {
	if seen[r] {
		return []rune{r}
	}
	d, ok := dec[r]
	if !ok {
		return []rune{r}
	}
	if !compat && d.compat {
		return []rune{r}
	}
	seen[r] = true
	var out []rune
	for _, x := range d.to {
		out = append(out, expand(x, dec, compat, seen)...)
	}
	delete(seen, r)
	return out
}

func parseHex(s string) rune {
	n, err := strconv.ParseInt(s, 16, 32)
	if err != nil {
		panic(fmt.Errorf("bad hex %q: %w", s, err))
	}
	return rune(n)
}

func parseRange(s string) (rune, rune) {
	if i := strings.Index(s, ".."); i >= 0 {
		return parseHex(s[:i]), parseHex(s[i+2:])
	}
	r := parseHex(s)
	return r, r
}

func sortedRuneKeys[V any](m map[rune]V) []rune {
	keys := make([]rune, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
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
