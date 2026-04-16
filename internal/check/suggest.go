//go:build selfhostgen

package check

// suggestion utilities: for every "unknown field / method / variant"
// diagnostic the checker emits, it consults this module for a
// best-guess alternative within Levenshtein distance 2 and surfaces
// it as a "did you mean `x`?" hint. Mirrors the resolver's own
// typo-suggestion strategy and reads roughly as well.

// suggestFrom returns the name closest to `query` among `candidates`
// within Levenshtein distance ≤ 2, or "" when nothing qualifies.
// Short-circuits on an exact-length distance-1 hit so the scan
// narrows quickly in type-rich contexts.
func suggestFrom(query string, candidates []string) string {
	if query == "" || len(candidates) == 0 {
		return ""
	}
	best := ""
	bestDist := 3
	var buf1, buf2 []int
	for _, c := range candidates {
		if diff := len(c) - len(query); diff > bestDist-1 || -diff > bestDist-1 {
			continue
		}
		d := levBounded(query, c, bestDist, &buf1, &buf2)
		if d < bestDist {
			best = c
			bestDist = d
			if d == 1 {
				return best
			}
		}
	}
	return best
}

// levBounded computes Levenshtein distance ≤ limit. Returns limit when
// the bound is exceeded — early termination keeps the scan cheap in
// large codebases where many names never beat the bound.
func levBounded(a, b string, limit int, pbuf1, pbuf2 *[]int) int {
	if a == b {
		return 0
	}
	la, lb := len(a), len(b)
	if la-lb >= limit || lb-la >= limit {
		return limit
	}
	prev := growInts(pbuf1, lb+1)
	cur := growInts(pbuf2, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		cur[0] = i
		rowMin := cur[0]
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			d := prev[j] + 1
			if x := cur[j-1] + 1; x < d {
				d = x
			}
			if x := prev[j-1] + cost; x < d {
				d = x
			}
			cur[j] = d
			if d < rowMin {
				rowMin = d
			}
		}
		if rowMin >= limit {
			return limit
		}
		prev, cur = cur, prev
	}
	return prev[lb]
}

func growInts(p *[]int, n int) []int {
	if cap(*p) < n {
		*p = make([]int, n)
		return *p
	}
	s := (*p)[:n]
	for i := range s {
		s[i] = 0
	}
	return s
}
