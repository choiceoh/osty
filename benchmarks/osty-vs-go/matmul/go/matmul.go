package matmulbench

const matmulDim = 64

var (
	matmulA = buildMatrix(matmulDim, 1)
	matmulB = buildMatrix(matmulDim, 7)
)

func buildMatrix(n, seed int) []int64 {
	m := make([]int64, n*n)
	state := int64(seed)
	for i := 0; i < n*n; i++ {
		state = (state*48271 + 2147483) % 2147483647
		m[i] = state%31 - 15
	}
	return m
}

//go:noinline
func MatmulChecksum(a, b []int64, n int) int64 {
	c := make([]int64, n*n)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			var sum int64
			for k := 0; k < n; k++ {
				sum += a[i*n+k] * b[k*n+j]
			}
			c[i*n+j] = sum
		}
	}
	var checksum int64
	for i, v := range c {
		checksum += v * int64((i%7)+1)
	}
	return checksum
}
