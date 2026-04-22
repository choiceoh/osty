package quicksortbench

var quicksortInput = buildInts(2048)

func buildInts(n int) []int64 {
	xs := make([]int64, n)
	state := int64(1)
	for i := 0; i < n; i++ {
		state = (state*1103515245 + 12345) & 0x7fffffff
		xs[i] = state % (1 << 20)
	}
	return xs
}

//go:noinline
func QuicksortChecksum(src []int64) int64 {
	xs := make([]int64, len(src))
	copy(xs, src)
	quicksort(xs, 0, len(xs)-1)
	var sum int64
	for i, x := range xs {
		sum += x * int64(i+1)
	}
	return sum
}

func quicksort(xs []int64, lo, hi int) {
	if lo >= hi {
		return
	}
	p := partition(xs, lo, hi)
	quicksort(xs, lo, p-1)
	quicksort(xs, p+1, hi)
}

func partition(xs []int64, lo, hi int) int {
	pivot := xs[hi]
	i := lo - 1
	for j := lo; j < hi; j++ {
		if xs[j] < pivot {
			i++
			xs[i], xs[j] = xs[j], xs[i]
		}
	}
	xs[i+1], xs[hi] = xs[hi], xs[i+1]
	return i + 1
}
