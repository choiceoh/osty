package wordfreqbench

import "sort"

var leftCorpus = []string{
	"osty", "compiler", "llvm", "runtime", "gc", "safepoint", "loop", "call",
	"osty", "compiler", "llvm", "runtime", "gc", "loop", "loop", "call",
	"map", "list", "sort", "entry", "roots", "stack", "module", "branch",
	"osty", "go", "bench", "profile", "parser", "lexer", "token", "codegen",
	"osty", "compiler", "llvm", "runtime", "gc", "safepoint", "loop", "call",
	"map", "list", "sort", "entry", "roots", "stack", "module", "branch",
	"inline", "arena", "token", "codegen", "bench", "profile", "parser", "lexer",
	"osty", "go", "gc", "runtime", "loop", "call", "map", "list",
}

var rightCorpus = []string{
	"go", "bench", "profile", "parser", "lexer", "token", "codegen", "arena",
	"map", "list", "sort", "entry", "roots", "stack", "module", "branch",
	"go", "bench", "profile", "parser", "lexer", "token", "codegen", "arena",
	"osty", "compiler", "llvm", "runtime", "gc", "safepoint", "loop", "call",
	"merge", "reduce", "filter", "group", "bucket", "symbol", "symbol", "module",
	"go", "bench", "profile", "parser", "lexer", "token", "codegen", "arena",
	"osty", "compiler", "llvm", "runtime", "gc", "loop", "call", "map",
	"merge", "reduce", "filter", "group", "bucket", "symbol", "roots", "stack",
}

func sortedWords(words []string) []string {
	out := append([]string(nil), words...)
	sort.Strings(out)
	return out
}

func buildIndex(words []string, base int) map[string]int {
	index := make(map[string]int, len(words))
	next := base
	for _, word := range words {
		if _, seen := index[word]; seen {
			continue
		}
		index[word] = next
		next++
	}
	return index
}

func sortedKeys(counts map[string]int) []string {
	keys := make([]string, 0, len(counts))
	for word := range counts {
		keys = append(keys, word)
	}
	sort.Strings(keys)
	return keys
}

//go:noinline
func AnalyzeCorpusIndex(left, right []string) int {
	leftIndex := buildIndex(sortedWords(left), 0)
	rightIndex := buildIndex(sortedWords(right), len(leftIndex))
	leftKeys := sortedKeys(leftIndex)
	rightKeys := sortedKeys(rightIndex)
	total := len(leftIndex) + len(rightIndex)
	for _, word := range leftKeys {
		total += leftIndex[word]
	}
	for _, word := range rightKeys {
		total += rightIndex[word]
	}
	return total
}
