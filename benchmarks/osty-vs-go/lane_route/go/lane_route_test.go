package laneroutebench

import "testing"

var laneRouteSink int

func BenchmarkLaneRoute(b *testing.B) {
	for i := 0; i < b.N; i++ {
		laneRouteSink = AnalyzeLaneRoute(laneRouteCosts)
	}
}
