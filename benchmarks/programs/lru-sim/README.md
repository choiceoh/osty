# lru-sim — runtime program benchmark (3 of 5)

One of five end-to-end CLI programs in `benchmarks/programs/`. See
the [suite README](../README.md) for the overall shape; this file
documents the workload only.

50,000 access ops against a capacity-12 LRU cache over a 45-key
namespace with a 70%-hot / 30%-cold distribution, producing ~61% hit
rate with realistic eviction churn. Stresses List<String> mutation +
Map<String,Int> recency tracking + linear-scan eviction over the
cache. Final summary: hits, misses, evictions, top-5 keys.
