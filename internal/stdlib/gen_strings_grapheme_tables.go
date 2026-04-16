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

const beginMarker = "// BEGIN GENERATED UAX29 GRAPHEME TABLES. DO NOT EDIT."
const endMarker = "// END GENERATED UAX29 GRAPHEME TABLES."

type runeRange struct {
	lo rune
	hi rune
}

func main() {
	root := filepath.Join("internal", "stdlib", "unicode", unicodeVersion)
	gcb := parsePropertyRanges(filepath.Join(root, "GraphemeBreakProperty.txt"))
	emoji := parsePropertyRanges(filepath.Join(root, "emoji-data.txt"))
	incb := parseIndicConjunctBreak(filepath.Join(root, "DerivedCoreProperties.txt"))

	var block bytes.Buffer
	block.WriteString(beginMarker + "\n")
	block.WriteString("// Source: Unicode " + unicodeVersion + " UCD GraphemeBreakProperty.txt, emoji-data.txt, DerivedCoreProperties.txt.\n")
	block.WriteString("// Run: go run internal/stdlib/gen_strings_grapheme_tables.go\n\n")
	for _, prop := range []string{"CR", "LF", "Control", "Extend", "ZWJ", "Regional_Indicator", "Prepend", "SpacingMark", "L", "V", "T", "LV", "LVT"} {
		writeOstyRangeTable(&block, "graphemeBreak"+identSuffix(prop)+"Ranges", gcb[prop])
	}
	writeOstyRangeTable(&block, "graphemeExtendedPictographicRanges", emoji["Extended_Pictographic"])
	for _, prop := range []string{"Consonant", "Extend", "Linker"} {
		writeOstyRangeTable(&block, "graphemeInCB"+identSuffix(prop)+"Ranges", incb[prop])
	}
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
		panic("generated table markers not found in strings.osty")
	}
	end += len(endMarker)
	next := text[end:]
	if strings.HasPrefix(next, "\r\n") {
		end += 2
	} else if strings.HasPrefix(next, "\n") {
		end++
	}
	updated := text[:start] + block.String() + "\n\n" + text[end:]
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		panic(err)
	}
}

func parsePropertyRanges(path string) map[string][]runeRange {
	out := map[string][]runeRange{}
	scanData(path, func(fields []string) {
		if len(fields) < 2 {
			return
		}
		prop := strings.TrimSpace(fields[1])
		out[prop] = append(out[prop], parseCodeRange(strings.TrimSpace(fields[0])))
	})
	for prop, ranges := range out {
		out[prop] = mergeRanges(ranges)
	}
	return out
}

func parseIndicConjunctBreak(path string) map[string][]runeRange {
	out := map[string][]runeRange{}
	scanData(path, func(fields []string) {
		if len(fields) < 3 || strings.TrimSpace(fields[1]) != "InCB" {
			return
		}
		prop := strings.TrimSpace(fields[2])
		out[prop] = append(out[prop], parseCodeRange(strings.TrimSpace(fields[0])))
	})
	for prop, ranges := range out {
		out[prop] = mergeRanges(ranges)
	}
	return out
}

func scanData(path string, fn func([]string)) {
	f, err := os.Open(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "@missing:") {
			continue
		}
		fn(strings.Split(line, ";"))
	}
	if err := sc.Err(); err != nil {
		panic(err)
	}
}

func parseCodeRange(s string) runeRange {
	parts := strings.Split(s, "..")
	lo := parseHexRune(parts[0])
	hi := lo
	if len(parts) == 2 {
		hi = parseHexRune(parts[1])
	}
	return runeRange{lo: lo, hi: hi}
}

func parseHexRune(s string) rune {
	v, err := strconv.ParseInt(strings.TrimSpace(s), 16, 32)
	if err != nil {
		panic(err)
	}
	return rune(v)
}

func mergeRanges(ranges []runeRange) []runeRange {
	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].lo == ranges[j].lo {
			return ranges[i].hi < ranges[j].hi
		}
		return ranges[i].lo < ranges[j].lo
	})
	out := make([]runeRange, 0, len(ranges))
	for _, r := range ranges {
		if len(out) == 0 || r.lo > out[len(out)-1].hi+1 {
			out = append(out, r)
			continue
		}
		if r.hi > out[len(out)-1].hi {
			out[len(out)-1].hi = r.hi
		}
	}
	return out
}

func writeOstyRangeTable(buf *bytes.Buffer, name string, ranges []runeRange) {
	fmt.Fprintf(buf, "pub let %s: List<Int> = [\n", name)
	for i := 0; i < len(ranges); i += 4 {
		buf.WriteString("    ")
		lineEnd := i + 4
		if lineEnd > len(ranges) {
			lineEnd = len(ranges)
		}
		for j := i; j < lineEnd; j++ {
			if j > i {
				buf.WriteString(", ")
			}
			r := ranges[j]
			fmt.Fprintf(buf, "0x%04X, 0x%04X", r.lo, r.hi)
		}
		if lineEnd < len(ranges) {
			buf.WriteByte(',')
		}
		buf.WriteByte('\n')
	}
	buf.WriteString("]\n\n")
}

func identSuffix(prop string) string {
	prop = strings.ReplaceAll(prop, "_", " ")
	parts := strings.Fields(prop)
	for i, p := range parts {
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, "")
}
