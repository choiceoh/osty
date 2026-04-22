package quicksortbench

import "testing"

var quicksortSink int64

func BenchmarkQuicksort(b *testing.B) {
	for i := 0; i < b.N; i++ {
		quicksortSink = QuicksortChecksum(quicksortInput)
	}
}
