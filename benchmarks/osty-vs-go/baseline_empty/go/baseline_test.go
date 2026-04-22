package baselineemptybench

import "testing"

var baselineSink int64

func BenchmarkEmpty(b *testing.B) {
	for i := 0; i < b.N; i++ {
		baselineSink = NoOp()
	}
}
