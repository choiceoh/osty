package fnvhashbench

import "testing"

var fnvSink int64

func BenchmarkFnvHash(b *testing.B) {
	for i := 0; i < b.N; i++ {
		fnvSink = FnvHash(fnvInput)
	}
}
