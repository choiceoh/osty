package loopsumbench

//go:noinline
func SumTo(n int64) int64 {
	var acc int64
	for i := int64(0); i < n; i++ {
		acc += i
	}
	return acc
}
