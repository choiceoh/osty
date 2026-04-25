// id-tracker — Go side of the end-to-end runtime program benchmark.
//
// Mirrors the Osty implementation byte-for-byte (deterministic LCG
// generator, identical aggregation, identical output format) so
// hyperfine can compare wall-clock cost on a Map<int64, V>-heavy
// workload: each iteration does containsKey + getOr + insert on
// Map<int64, int64> for the count and sum aggregations, then a
// sort + top-K format pass.
//
// No I/O — the event stream is generated in-process from the LCG
// state seeded the same on both sides.

package main

import (
	"fmt"
	"sort"
)

const N = 50000

func generateEvents(n int) []int64 {
	out := make([]int64, n)
	state := int64(1)
	for i := 0; i < n; i++ {
		state = (state*1103515245 + 12345) % 2147483648
		out[i] = state
	}
	return out
}

func main() {
	events := generateEvents(N)
	// id space: 256 distinct ids. Skewed: hot-id is selected ~70% of
	// the time, cold ids the rest. id is `state % hotKeySpace` for
	// hot, `hotKeySpace + (state / 8) % coldKeySpace` for cold.
	const hotKeys = 32
	const coldKeys = 224
	counts := map[int64]int64{}
	sums := map[int64]int64{}
	firstSeen := map[int64]int64{}

	for i, ev := range events {
		var id int64
		if ev%100 < 70 {
			id = ev % hotKeys
		} else {
			id = hotKeys + (ev/8)%coldKeys
		}
		value := (ev / 1024) % 1000
		counts[id] = counts[id] + 1
		sums[id] = sums[id] + value
		if _, ok := firstSeen[id]; !ok {
			firstSeen[id] = int64(i)
		}
	}

	// Top 10 ids by count desc, id asc tiebreak.
	ids := make([]int64, 0, len(counts))
	for k := range counts {
		ids = append(ids, k)
	}
	sort.Slice(ids, func(i, j int) bool {
		ci := counts[ids[i]]
		cj := counts[ids[j]]
		if ci != cj {
			return ci > cj
		}
		return ids[i] < ids[j]
	})
	topK := 10
	if len(ids) < topK {
		topK = len(ids)
	}

	// Aggregate totals as a sanity column the verifier diffs.
	var totalCount int64
	var totalSum int64
	for _, c := range counts {
		totalCount += c
	}
	for _, s := range sums {
		totalSum += s
	}

	fmt.Printf("events: %d\n", len(events))
	fmt.Printf("distinct: %d\n", len(counts))
	fmt.Printf("total_count: %d\n", totalCount)
	fmt.Printf("total_sum: %d\n", totalSum)
	fmt.Println("top:")
	for i := 0; i < topK; i++ {
		id := ids[i]
		fmt.Printf("  id=%d count=%d sum=%d first=%d\n",
			id, counts[id], sums[id], firstSeen[id])
	}
}
