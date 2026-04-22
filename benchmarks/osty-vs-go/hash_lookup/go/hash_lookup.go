package hashlookupbench

const (
	hashLookupEntries = 2048
	hashLookupQueries = 8192
)

var (
	hashLookupKeys    = buildKeys(hashLookupEntries)
	hashLookupQueryKs = buildKeys(hashLookupQueries)
)

func buildKeys(n int) []int64 {
	xs := make([]int64, n)
	state := int64(1)
	for i := 0; i < n; i++ {
		state = (state*2654435761 + 13) & 0x7fffffff
		xs[i] = state
	}
	return xs
}

//go:noinline
func HashLookupChecksum(entries, queries []int64) int64 {
	m := make(map[int64]int64, len(entries))
	for i, k := range entries {
		m[k] = int64(i + 1)
	}
	var sum int64
	for _, q := range queries {
		if v, ok := m[q]; ok {
			sum += v
		} else {
			sum += 7
		}
	}
	return sum
}
