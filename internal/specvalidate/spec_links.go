package specvalidate

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/token"
)

var (
	specIndexOnce sync.Once
	specIndex     map[string]bool
)

// Diagnostics verifies declaration-level #[spec("...")] links against the
// checked-in LANG_SPEC_v0.6 markdown section index.
func Diagnostics(src []byte, path string) []*diag.Diagnostic {
	anchors := defaultSpecIndex()
	if len(anchors) == 0 {
		return nil
	}
	lineStarts := lineStartOffsets(src)
	var out []*diag.Diagnostic
	for _, ref := range scanSpecRefs(src) {
		key := normalizeSpecRef(ref.value)
		if key == "" || anchors[key] {
			continue
		}
		start := posAt(lineStarts, ref.start)
		end := posAt(lineStarts, ref.end)
		d := diag.New(diag.Error, "spec anchor `"+ref.value+"` was not found").
			Code(diag.CodeSpecAnchorNotFound).
			Primary(diag.Span{Start: start, End: end}, "unknown spec link").
			Note("`#[spec(...)]` links must point at a section heading in `LANG_SPEC_v0.6/`.").
			Hint("correct the section reference, or add the missing spec heading").
			Build()
		d.File = path
		out = append(out, d)
	}
	return out
}

type specRef struct {
	value string
	start int
	end   int
}

func scanSpecRefs(src []byte) []specRef {
	var refs []specRef
	for i := 0; i < len(src); {
		if i+1 < len(src) && src[i] == '/' && src[i+1] == '/' {
			i = skipLineComment(src, i+2)
			continue
		}
		if i+1 < len(src) && src[i] == '/' && src[i+1] == '*' {
			i = skipBlockComment(src, i+2)
			continue
		}
		if src[i] == '"' {
			i = skipString(src, i)
			continue
		}
		if src[i] == '#' && i+1 < len(src) && src[i+1] == '[' {
			if ref, next, ok := parseSpecAnnotation(src, i); ok {
				refs = append(refs, ref)
				i = next
				continue
			}
		}
		i++
	}
	return refs
}

func parseSpecAnnotation(src []byte, start int) (specRef, int, bool) {
	i := skipSpaces(src, start+2)
	nameStart := i
	for i < len(src) && isIdentByte(src[i]) {
		i++
	}
	if string(src[nameStart:i]) != "spec" {
		return specRef{}, start + 1, false
	}
	i = skipSpaces(src, i)
	if i >= len(src) || src[i] != '(' {
		return specRef{}, start + 1, false
	}
	i = skipSpaces(src, i+1)
	value, litStart, litEnd, ok := parseQuotedString(src, i)
	if !ok {
		return specRef{}, start + 1, false
	}
	return specRef{value: value, start: litStart, end: litEnd}, litEnd, true
}

func parseQuotedString(src []byte, start int) (string, int, int, bool) {
	if start >= len(src) || src[start] != '"' {
		return "", 0, 0, false
	}
	if start+2 < len(src) && src[start+1] == '"' && src[start+2] == '"' {
		end := bytes.Index(src[start+3:], []byte(`"""`))
		if end < 0 {
			return "", 0, 0, false
		}
		litEnd := start + 3 + end + 3
		return string(src[start+3 : litEnd-3]), start, litEnd, true
	}
	i := start + 1
	for i < len(src) {
		if src[i] == '\\' {
			i += 2
			continue
		}
		if src[i] == '"' {
			litEnd := i + 1
			raw := string(src[start:litEnd])
			value, err := strconv.Unquote(raw)
			if err != nil {
				value = strings.Trim(raw, `"`)
			}
			return value, start, litEnd, true
		}
		i++
	}
	return "", 0, 0, false
}

func skipLineComment(src []byte, i int) int {
	for i < len(src) && src[i] != '\n' {
		i++
	}
	return i
}

func skipBlockComment(src []byte, i int) int {
	for i+1 < len(src) {
		if src[i] == '*' && src[i+1] == '/' {
			return i + 2
		}
		i++
	}
	return len(src)
}

func skipString(src []byte, start int) int {
	if start+2 < len(src) && src[start+1] == '"' && src[start+2] == '"' {
		if end := bytes.Index(src[start+3:], []byte(`"""`)); end >= 0 {
			return start + 3 + end + 3
		}
		return len(src)
	}
	i := start + 1
	for i < len(src) {
		if src[i] == '\\' {
			i += 2
			continue
		}
		if src[i] == '"' {
			return i + 1
		}
		i++
	}
	return len(src)
}

func skipSpaces(src []byte, i int) int {
	for i < len(src) {
		switch src[i] {
		case ' ', '\t', '\r', '\n':
			i++
		default:
			return i
		}
	}
	return i
}

func isIdentByte(b byte) bool {
	return b == '_' || b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9'
}

func defaultSpecIndex() map[string]bool {
	specIndexOnce.Do(func() {
		specIndex = loadSpecIndex(defaultSpecRoot())
	})
	return specIndex
}

func defaultSpecRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if ok {
		root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "LANG_SPEC_v0.6"))
		if st, err := os.Stat(root); err == nil && st.IsDir() {
			return root
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		for dir := cwd; ; dir = filepath.Dir(dir) {
			root := filepath.Join(dir, "LANG_SPEC_v0.6")
			if st, err := os.Stat(root); err == nil && st.IsDir() {
				return root
			}
			next := filepath.Dir(dir)
			if next == dir {
				break
			}
		}
	}
	return ""
}

func loadSpecIndex(root string) map[string]bool {
	if root == "" {
		return nil
	}
	entries, err := filepath.Glob(filepath.Join(root, "*.md"))
	if err != nil {
		return nil
	}
	out := make(map[string]bool, len(entries)*8)
	for _, path := range entries {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if id := sectionIDFromHeading(line); id != "" {
				out[normalizeSpecRef(id)] = true
			}
		}
	}
	return out
}

var headingSectionRe = regexp.MustCompile(`^#{1,6}\s+` + "`?" + `§?([A-Za-z0-9]+(?:\.[A-Za-z0-9]+)*)\b`)

func sectionIDFromHeading(line string) string {
	m := headingSectionRe.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return ""
	}
	return m[1]
}

func normalizeSpecRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if idx := strings.LastIndexByte(ref, '#'); idx >= 0 {
		ref = ref[idx+1:]
	}
	ref = strings.TrimPrefix(ref, "§")
	ref = strings.TrimSpace(ref)
	ref = strings.Trim(ref, "`")
	return strings.ToLower(ref)
}

func lineStartOffsets(src []byte) []int {
	starts := []int{0}
	for i, b := range src {
		if b == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

func posAt(lineStarts []int, off int) token.Pos {
	if off < 0 {
		off = 0
	}
	line := sort.Search(len(lineStarts), func(i int) bool { return lineStarts[i] > off }) - 1
	if line < 0 {
		line = 0
	}
	col := 1
	if line < len(lineStarts) {
		col = off - lineStarts[line] + 1
	}
	return token.Pos{Line: line + 1, Column: col, Offset: off}
}
