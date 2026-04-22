package simdstatsbench

import "testing"

var simdStatsSink int

func BenchmarkSimdStats(b *testing.B) {
	for i := 0; i < b.N; i++ {
		simdStatsSink = AnalyzeSimdStats(simdValues, simdWeights)
	}
}
