package laneroutebench

func buildLaneCosts(n int) []int {
	costs := make([]int, n)
	for i := 0; i < n; i++ {
		costs[i] = ((i*19 + i/3*7) % 41) + 3
	}
	return costs
}

var laneRouteCosts = buildLaneCosts(4096)

func min2(a, b int) int {
	if b < a {
		return b
	}
	return a
}

//go:noinline
func AnalyzeLaneRoute(costs []int) int {
	laneA, laneB, laneC := 0, 7, 13
	checksum := 0
	for i, cost := range costs {
		nextA := min2(laneA, laneB+3) + cost
		nextB := min2(min2(laneA+2, laneB), laneC+2) + cost/2 + (i % 7)
		nextC := min2(laneB+3, laneC) + (cost*2)/3 + (i % 5)
		laneA, laneB, laneC = nextA, nextB, nextC
		checksum += (nextA + nextB + nextC) % 97
	}
	tail := []int{laneA, laneB, laneC}
	if tail[1] < tail[0] {
		tail[0], tail[1] = tail[1], tail[0]
	}
	if tail[2] < tail[1] {
		tail[1], tail[2] = tail[2], tail[1]
	}
	if tail[1] < tail[0] {
		tail[0], tail[1] = tail[1], tail[0]
	}
	return checksum + tail[0] + tail[1]*2 + tail[2]*3
}
