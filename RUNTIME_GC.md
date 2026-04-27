# LLVM Runtime GC

This document is the source of truth for Osty's active GC implementation path.

The live implementation is the LLVM/native runtime path:

- lowering and root/safepoint insertion:
  [`internal/llvmgen/llvmgen.go`](./internal/llvmgen/llvmgen.go)
- bundled runtime implementation:
  [`internal/backend/runtime/osty_runtime.c`](./internal/backend/runtime/osty_runtime.c)
- LLVM backend/runtime integration tests:
  [`internal/backend/llvm_test.go`](./internal/backend/llvm_test.go)
  and
  [`internal/backend/llvm_runtime_gc_test.go`](./internal/backend/llvm_runtime_gc_test.go)

## Current scope

Today the runtime collector is a precise root-driven mark/sweep collector with:

- managed runtime allocation via `osty.gc.alloc_v1`
- explicit root bind/release via `osty.gc.root_bind_v1` and `osty.gc.root_release_v1`
- global root slot registration via `osty.gc.global_root_register_v1` and `osty.gc.global_root_unregister_v1` (slot-address based — reassigning the slot automatically protects the new payload)
- safepoint-driven stack-slot scanning via `osty.gc.safepoint_v1`
- allocation-pressure-triggered collection requests fulfilled at safepoints
- write-barrier edge logging — `osty.gc.pre_write_v1` accumulates a SATB log of overwritten values, `osty.gc.post_write_v1` records deduplicated (owner, value) edges; both cleared at the end of each collection. The live STW marker does not yet consume these logs, but they are produced so that generational minor collection and concurrent / incremental marking can plug in without revisiting emit sites.
- managed temporary protection in LLVM lowering for params, locals, aggregates, loop iterables, and nested call arguments
- **opt-in background marker thread** (Phase 0a) — when `OSTY_GC_BG_MARKER=1`, `osty_gc_collect_incremental_start_with_stack_roots` lazily spawns a single OS thread that drains the grey queue while the cycle is in `MARK_INCREMENTAL`. Initial root scan and final remark/sweep stay STW; mid-cycle mark work runs concurrently with the mutator. Telemetry: `osty_gc_debug_bg_marker_{started,kicks,drained,loops,naps,active}`. Lock-skip fast paths in alloc / load / post_write / map ops route through `osty_gc_serialized_now()` so they re-engage the recursive mutex while the marker is live.

This is the implementation we are extending toward end-to-end native execution.

## Status of `examples/gc`

[`examples/gc`](./examples/gc) remains a large executable model and invariant lab. It is useful as:

- a semantic prototype for future collector features
- a validator/testbed for heap invariants and stack-map ideas
- a historical design record

It is not the active implementation path for new native runtime GC work. New implementation effort should land in the LLVM lowering/runtime path first, then selectively retire or archive overlapping model-only material.

The per-feature gap between the model and the live runtime is tabulated in
[`RUNTIME_GC_DELTA.md`](./RUNTIME_GC_DELTA.md). That document is a planning aid,
not a spec — P1 items mark places where LLVM lowering already emits calls that
the runtime treats as no-ops, so they have the highest leverage.

## Working rule

When there is tension between the model and the live runtime path:

1. the runtime ABI and LLVM lowering win
2. backend tests are the implementation gate
3. `examples/gc` should be updated only when it clarifies or archives behavior, not as the primary place to add new GC features

## Useful checks

```sh
go test ./cmd/osty ./internal/llvmgen ./internal/backend
go run ./cmd/osty check examples/calc
./scripts/with-native-checker go run ./cmd/osty build --backend llvm --emit llvm-ir examples/calc
```
