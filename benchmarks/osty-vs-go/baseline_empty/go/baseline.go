// Package baselineemptybench measures the bench harness's own per-iteration
// overhead: testing.B's loop + timer calls on the Go side, the clock-pair
// + GC-odometer sample on the Osty side. Subtract this from other pairs
// to stop harness overhead from dominating nanosecond-scale bodies.
package baselineemptybench

//go:noinline
func NoOp() int64 {
	return 0
}
