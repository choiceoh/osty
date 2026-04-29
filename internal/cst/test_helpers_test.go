package cst_test

// corpus mirrors the parser snapshot oracle so byte-coverage and round-trip
// are verified on the same set of inputs the parser snapshot locks.
var corpus = []string{
	"testdata/spec/positive/01-lexical.osty",
	"testdata/spec/positive/02-types.osty",
	"testdata/spec/positive/03-declarations.osty",
	"testdata/spec/positive/04-expressions.osty",
	"testdata/spec/positive/05-modules.osty",
	"testdata/spec/positive/06-scripts.osty",
	"testdata/spec/positive/07-errors.osty",
	"testdata/spec/positive/08-concurrency.osty",
	"testdata/spec/positive/10-collections.osty",
	"testdata/spec/positive/11-testing.osty",
	"testdata/spec/negative/reject.osty",
	"testdata/full.osty",
	"testdata/hello.osty",
	"testdata/resolve_ok.osty",
	"word_freq.osty",
	"word_freq_test.osty",
}

func firstDiff(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return n
	}
	return -1
}

func preview(src []byte, start int) string {
	const span = 20
	if start < 0 {
		start = 0
	}
	if start >= len(src) {
		return ""
	}
	end := start + span
	if end > len(src) {
		end = len(src)
	}
	return string(src[start:end])
}
