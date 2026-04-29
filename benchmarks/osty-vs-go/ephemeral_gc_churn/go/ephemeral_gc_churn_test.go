package ephemeralgcchurnbench

import "testing"

var ephemeralGCChurnSink int64

func BenchmarkEphemeralGCChurn(b *testing.B) {
	for i := 0; i < b.N; i++ {
		ephemeralGCChurnSink = EphemeralGCChurn(2048)
	}
}
