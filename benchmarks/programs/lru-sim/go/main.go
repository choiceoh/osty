// lru-sim — bounded-capacity cache simulator.
//
// Simulates 50,000 access ops against a capacity-128 cache. Keys come
// from a synthetic LCG with a deliberate hot-set so we get realistic
// hit/miss ratios. Prints hit/miss totals + final cache size + count
// of cache entries with at least one re-access.

package main

import (
	"fmt"
	"sort"
)

const (
	N        = 50000
	Capacity = 12
)

var keyspace = []string{
	"users:alice", "users:bob", "users:carol", "users:dave", "users:eve",
	"users:frank", "users:grace", "users:heidi", "posts:101", "posts:102",
	"posts:103", "posts:104", "posts:105", "posts:201", "posts:202",
	"posts:203", "posts:204", "posts:205", "feed:home", "feed:trending",
	"feed:hot", "feed:cold", "tags:go", "tags:rust", "tags:osty",
	"tags:llvm", "tags:perf", "tags:bench", "search:1", "search:2",
	"search:3", "search:4", "search:5", "auth:cookie", "auth:bearer",
	"meta:version", "meta:build", "meta:host", "rate:default", "rate:burst",
	"sess:001", "sess:002", "sess:003", "sess:004", "sess:005",
}

func generateOps(n int) []string {
	out := make([]string, n)
	state := int64(1)
	hotSize := int64(8) // first 8 keys are "hot" — fits in cache
	coldSize := int64(len(keyspace)) - hotSize
	for i := 0; i < n; i++ {
		state = (state*1103515245 + 12345) % 2147483648
		// 70% hot, 30% cold — produces high but non-trivial hit rate.
		if state%100 < 70 {
			out[i] = keyspace[(state/4)%hotSize]
		} else {
			out[i] = keyspace[hotSize+((state/4)%coldSize)]
		}
	}
	return out
}

func main() {
	ops := generateOps(N)

	// Recency tracked via a List<String> + Map<String,Int> tick counter.
	// On access: bump key's tick. On overflow: evict the key with the
	// lowest tick. Mirrors the Osty implementation 1:1.
	tick := map[string]int{}
	accessed := map[string]int{}
	cache := make([]string, 0, Capacity+1)

	hits := 0
	misses := 0
	clock := 0
	evictions := 0

	for _, key := range ops {
		clock++
		if _, ok := tick[key]; ok {
			hits++
			tick[key] = clock
			accessed[key]++
			continue
		}
		misses++
		// Insert. accessed[] persists across evictions so re-admitted
		// keys keep their lifetime hit counter.
		tick[key] = clock
		accessed[key]++
		cache = append(cache, key)
		if len(cache) > Capacity {
			// Evict oldest by tick.
			oldestIdx := 0
			oldestTick := tick[cache[0]]
			for i := 1; i < len(cache); i++ {
				t := tick[cache[i]]
				if t < oldestTick {
					oldestTick = t
					oldestIdx = i
				}
			}
			evicted := cache[oldestIdx]
			cache = append(cache[:oldestIdx], cache[oldestIdx+1:]...)
			delete(tick, evicted)
			evictions++
		}
	}

	// "Re-access count" — keys that were touched more than once total.
	rebound := 0
	for _, c := range accessed {
		if c > 1 {
			rebound++
		}
	}

	// Top 5 keys by access count, then alphabetically for ties.
	type kc struct {
		k string
		c int
	}
	all := make([]kc, 0, len(accessed))
	for k, c := range accessed {
		all = append(all, kc{k, c})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].c != all[j].c {
			return all[i].c > all[j].c
		}
		return all[i].k < all[j].k
	})
	if len(all) > 5 {
		all = all[:5]
	}

	fmt.Printf("ops: %d\n", N)
	fmt.Printf("hits: %d\n", hits)
	fmt.Printf("misses: %d\n", misses)
	fmt.Printf("evictions: %d\n", evictions)
	fmt.Printf("final_size: %d\n", len(cache))
	fmt.Printf("unique_keys: %d\n", len(accessed))
	fmt.Printf("reaccessed_keys: %d\n", rebound)
	fmt.Println("top keys:")
	for _, x := range all {
		fmt.Printf("  %s count=%d\n", x.k, x.c)
	}
}
