package fibbench

import "testing"

func BenchmarkFib15(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = Fib(15)
	}
}
