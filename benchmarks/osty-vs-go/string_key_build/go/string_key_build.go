package stringkeybuildbench

import "strconv"

//go:noinline
func BuildStringKeys(n int) int64 {
	var total int64
	state := 1
	for i := 0; i < n; i++ {
		state = (state*1103515245 + 12345) & 0x7fffffff
		target := state % 200000
		key := "key:" + strconv.Itoa(target)
		total += int64(len(key))
	}
	return total
}
