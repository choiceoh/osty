// dep-resolver — synthetic build-system dependency analyzer.
//
// Generates 200 synthetic modules with deterministic deps, computes
// per-module dependency depth (longest path to a leaf), and emits
// summary stats.

package main

import (
	"fmt"
	"sort"
	"strings"
)

const N = 10000

func moduleNames() []string {
	prefix := []string{
		"alpha", "beta", "gamma", "delta", "epsilon",
		"zeta", "eta", "theta", "iota", "kappa",
		"lambda", "mu", "nu", "xi", "omicron",
		"pi", "rho", "sigma", "tau", "upsilon",
	}
	suffix := []string{
		"core", "net", "fs", "ipc", "log",
		"auth", "http", "json", "vm", "gc",
	}
	tag := []string{
		"a", "b", "c", "d", "e", "f", "g", "h", "i", "j",
		"k", "l", "m", "n", "o", "p", "q", "r", "s", "t",
		"u", "v", "w", "x", "y", "z", "aa", "bb", "cc", "dd",
		"ee", "ff", "gg", "hh", "ii", "jj", "kk", "ll", "mm", "nn",
		"oo", "pp", "qq", "rr", "ss", "tt", "uu", "vv", "ww", "xx",
	}
	out := make([]string, 0, len(prefix)*len(suffix)*len(tag))
	for _, p := range prefix {
		for _, s := range suffix {
			for _, t := range tag {
				out = append(out, p+"-"+s+"-"+t)
			}
		}
	}
	return out
}

func buildDeps(n int) [][]int {
	deps := make([][]int, n)
	state := int64(1)
	for i := 0; i < n; i++ {
		state = (state*1103515245 + 12345) % 2147483648
		my := []int{}
		if i > 0 {
			count := int(state % 4) // 0..3 deps
			for k := 0; k < count; k++ {
				state = (state*1103515245 + 12345) % 2147483648
				target := int((state / 4) % int64(i))
				dup := false
				for _, d := range my {
					if d == target {
						dup = true
						break
					}
				}
				if !dup {
					my = append(my, target)
				}
			}
		}
		deps[i] = my
	}
	return deps
}

func main() {
	names := moduleNames()
	deps := buildDeps(N)

	// Forward pass — i's deps point to earlier indices, so a single
	// linear scan computes longest-path depth.
	depth := make([]int, N)
	totalEdges := 0
	for i := 0; i < N; i++ {
		myDeps := deps[i]
		totalEdges += len(myDeps)
		maxD := 0
		for _, d := range myDeps {
			cand := depth[d] + 1
			if cand > maxD {
				maxD = cand
			}
		}
		depth[i] = maxD
	}

	maxDepth := 0
	deepCount := 0
	for _, d := range depth {
		if d > maxDepth {
			maxDepth = d
		}
		if d >= 5 {
			deepCount++
		}
	}

	// Prefix histogram (everything before "-").
	prefixCounts := map[string]int{}
	for _, name := range names {
		idx := strings.Index(name, "-")
		var p string
		if idx < 0 {
			p = name
		} else {
			p = name[:idx]
		}
		prefixCounts[p]++
	}

	// Top 3 prefixes by count desc, tie-broken by prefix asc.
	type pc struct {
		p string
		c int
	}
	pcs := make([]pc, 0, len(prefixCounts))
	for k, c := range prefixCounts {
		pcs = append(pcs, pc{k, c})
	}
	sort.Slice(pcs, func(i, j int) bool {
		if pcs[i].c != pcs[j].c {
			return pcs[i].c > pcs[j].c
		}
		return pcs[i].p < pcs[j].p
	})
	if len(pcs) > 3 {
		pcs = pcs[:3]
	}

	fmt.Printf("modules: %d\n", N)
	fmt.Printf("edges: %d\n", totalEdges)
	fmt.Printf("max_depth: %d\n", maxDepth)
	fmt.Printf("deep_modules: %d\n", deepCount)
	fmt.Printf("unique_prefixes: %d\n", len(prefixCounts))
	fmt.Println("top prefixes:")
	for _, x := range pcs {
		fmt.Printf("  %s count=%d\n", x.p, x.c)
	}
}
