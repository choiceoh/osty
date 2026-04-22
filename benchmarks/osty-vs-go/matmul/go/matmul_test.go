package matmulbench

import "testing"

var matmulSink int64

func BenchmarkMatmul(b *testing.B) {
	for i := 0; i < b.N; i++ {
		matmulSink = MatmulChecksum(matmulA, matmulB, matmulDim)
	}
}
