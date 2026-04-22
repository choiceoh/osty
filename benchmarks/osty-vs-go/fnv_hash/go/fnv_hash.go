package fnvhashbench

const (
	fnvOffset int64 = 0x01000000
	fnvPrime  int64 = 16777619
	fnvBytes        = 4096
)

var fnvInput = buildFnvInput(fnvBytes)

func buildFnvInput(n int) []int64 {
	xs := make([]int64, n)
	state := int64(1)
	for i := 0; i < n; i++ {
		state = (state*1103515245 + 12345) & 0x7fffffff
		xs[i] = state & 0xff
	}
	return xs
}

//go:noinline
func FnvHash(bytes []int64) int64 {
	hash := fnvOffset
	for _, b := range bytes {
		hash ^= b
		hash *= fnvPrime
	}
	return hash
}
