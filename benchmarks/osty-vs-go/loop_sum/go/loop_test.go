package loopsumbench

import "testing"

func BenchmarkSumTo100(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = SumTo(100)
	}
}
