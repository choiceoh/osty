// diag_explain_policy.go is the Go snapshot of
// toolchain/diag_explain.osty. Osty is the source of truth;
// the drift test in this package keeps the two in sync.
package runner

import "strings"

// ParsedExplainDoc mirrors toolchain/diag_explain.osty's
// ParsedExplainDoc — the structured outcome of parsing one
// CodeXxx constant's doc comment.
//
// Osty: toolchain/diag_explain.osty:31
type ParsedExplainDoc struct {
	Summary string
	Body    []string
	Spec    string
	Example string
	Fix     string
}

// explainParseState is the Go mirror of the Osty
// `ExplainParseState` private struct. Threading it through pure
// helpers keeps the top-level loop a flat scan over `lines`.
type explainParseState struct {
	summary      string
	spec         string
	fix          string
	bodyParas    [][]string
	curPara      []string
	exampleLines []string
	section      string
}

// ParseExplainDoc walks a list of already-preprocessed comment
// lines (caller-side: `//` prefix and one optional leading space
// already stripped) and returns the structured doc.
//
// Osty: toolchain/diag_explain.osty:60
func ParseExplainDoc(lines []string) ParsedExplainDoc {
	st := explainParseState{}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "Spec:"):
			st = explainFlushPara(st)
			st.section = ""
			st.spec = strings.TrimSpace(strings.TrimPrefix(trimmed, "Spec:"))
			continue
		case strings.HasPrefix(trimmed, "Fix:"):
			st = explainFlushPara(st)
			st.section = ""
			st.fix = strings.TrimSpace(strings.TrimPrefix(trimmed, "Fix:"))
			continue
		case trimmed == "Example:":
			st = explainFlushPara(st)
			st.section = "example"
			continue
		}
		if st.section == "example" {
			st.exampleLines = append(st.exampleLines, line)
			continue
		}
		if trimmed == "" {
			st = explainFlushPara(st)
			continue
		}
		st.curPara = append(st.curPara, line)
	}
	st = explainFlushPara(st)

	doc := ParsedExplainDoc{
		Summary: st.summary,
		Spec:    st.spec,
		Fix:     st.fix,
	}
	for _, para := range st.bodyParas {
		doc.Body = append(doc.Body, strings.Join(trimAllLines(para), " "))
	}
	if len(st.exampleLines) > 0 {
		doc.Example = trimExampleBlock(st.exampleLines)
	}
	return doc
}

// explainFlushPara dispatches `st.curPara` to summary or body,
// then clears `curPara`. The first non-empty paragraph becomes
// the summary (single line, trailing `.` enforced).
//
// Osty: toolchain/diag_explain.osty:118
func explainFlushPara(st explainParseState) explainParseState {
	if len(st.curPara) == 0 {
		return st
	}
	if st.summary == "" {
		joined := strings.Join(trimAllLines(st.curPara), " ")
		stripped := strings.TrimSuffix(joined, ".")
		if stripped != "" {
			st.summary = stripped + "."
		}
	} else {
		paraCopy := append([]string(nil), st.curPara...)
		st.bodyParas = append(st.bodyParas, paraCopy)
	}
	st.curPara = nil
	return st
}

// trimAllLines applies strings.TrimSpace to every line.
//
// Osty: toolchain/diag_explain.osty:138
func trimAllLines(xs []string) []string {
	out := make([]string, len(xs))
	for i, s := range xs {
		out[i] = strings.TrimSpace(s)
	}
	return out
}

// trimExampleBlock strips leading/trailing blank lines and a
// common leading-space prefix, then joins with `\n`. Tabs do not
// count towards the common-indent calculation.
//
// Osty: toolchain/diag_explain.osty:146
func trimExampleBlock(lines []string) string {
	bs := lines
	for len(bs) > 0 && strings.TrimSpace(bs[len(bs)-1]) == "" {
		bs = bs[:len(bs)-1]
	}
	for len(bs) > 0 && strings.TrimSpace(bs[0]) == "" {
		bs = bs[1:]
	}
	minIndent := -1
	for _, l := range bs {
		if strings.TrimSpace(l) == "" {
			continue
		}
		count := len(l) - len(strings.TrimLeft(l, " "))
		if minIndent < 0 || count < minIndent {
			minIndent = count
		}
	}
	if minIndent <= 0 {
		return strings.Join(bs, "\n")
	}
	out := make([]string, len(bs))
	for i, l := range bs {
		if len(l) >= minIndent {
			out[i] = l[minIndent:]
		} else {
			out[i] = l
		}
	}
	return strings.Join(out, "\n")
}
