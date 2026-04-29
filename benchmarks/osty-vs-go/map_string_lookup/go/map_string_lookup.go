package mapstringlookupbench

import "strconv"

const (
	mapStringEntries    = 4096
	mapStringQueryCount = 16384
)

var (
	mapStringKeys    = buildStringKeys(mapStringEntries)
	mapStringQueries = buildStringQueries(mapStringQueryCount, mapStringEntries)
	mapStringIndex   = buildStringMap(mapStringKeys)
)

func buildStringKeys(n int) []string {
	keys := make([]string, n)
	for i := 0; i < n; i++ {
		keys[i] = "key:" + strconv.Itoa(i)
	}
	return keys
}

func buildStringQueries(n, modulo int) []string {
	keys := make([]string, n)
	state := 1
	for i := 0; i < n; i++ {
		state = (state*1103515245 + 12345) & 0x7fffffff
		keys[i] = "key:" + strconv.Itoa(state%modulo)
	}
	return keys
}

func buildStringMap(keys []string) map[string]int64 {
	m := make(map[string]int64, len(keys))
	for i, k := range keys {
		m[k] = int64(i + 1)
	}
	return m
}

//go:noinline
func StringLookupChecksum(m map[string]int64, queries []string) int64 {
	var total int64
	for _, key := range queries {
		total += m[key]
	}
	return total
}
