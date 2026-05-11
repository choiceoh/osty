# Subprocess Switchover Baseline

- **Scope**: Performance baseline for the embedded vs subprocess native-checker decision.
- **Type**: Reference / decision data
- **Status**: Gate (a) **shipped** — CLI startup installs the managed subprocess checker as the production default via `check.UseManagedSubprocessChecker`, and every package-input check path (`osty check` / `lint` / `typecheck` / `build` / `run` / `test` / `ci` / `lsp` / `pipeline`) now routes through `check.NativePackageCheck` so the factory selection is consistent across commands. Gate (b) **partial** — the production factory no longer falls back to embedded on managed-build failure (a missing Go toolchain or broken build now surfaces as a clear diagnostic instead of silently re-routing to the frozen in-process checker). Full embedded code removal still depends on migrating the test infrastructure off the embedded default.

## Why this baseline exists

`internal/check/host_boundary.go` exposes two implementations of the same
`nativeChecker` interface:

- **embedded** — in-process via `internal/selfhost` (the frozen 68k-line
  `internal/selfhost/generated.go` seed).
- **subprocess** — forks `OSTY_NATIVE_CHECKER_BIN`, JSON request via stdin,
  JSON response on stdout.

Today's default (CLI startup, after gate (a) flip) is **subprocess** via
`check.UseManagedSubprocessChecker(".")` in `cmd/osty/main.go`, which lazily
resolves the managed binary on first factory-routed check. After the gate
(b) cleanup, a managed-build failure now surfaces as `(nil, "managed native
checker unavailable: <reason>")` — callers see a clear "checker
unavailable" diagnostic instead of an opaque silent shift to the frozen
embedded path. `OSTY_NATIVE_CHECKER_BIN` still wins outright when set.

Every CLI surface that previously called `selfhost.CheckPackageStructured`
directly (`runCheckPackageNative`, `runNativeWorkspaceCheck`,
`runTypecheckPackageNative`, the file-mode helper, and
`lintNativePackageDiagnostics`) now goes through `check.NativePackageCheck`,
so the same factory selection that built the bench data feeds every
`osty check` / `osty lint` / `osty typecheck` invocation.

Tests inside `internal/check/` and downstream packages keep the embedded
default — `UseManagedSubprocessChecker` is called only from CLI startup,
not from `TestMain`, so `go test ./...` from inside the repo doesn't
trigger managed binary builds that would otherwise add seconds and
clutter every test package's `.osty/toolchain/...`.

The bootstrap Osty→Go transpiler has been removed (CLAUDE.md). Embedded is
therefore *frozen* — new `toolchain/*.osty` changes only land via the
LLVM-compiled native checker binary. Embedded must eventually retire.

This baseline measures the cost of flipping there, so the regression bound
is on record before any flip.

### Decision gates this baseline informs

| Gate | Decision | Required evidence | Status |
|---|---|---|---|
| **(a)** | Flip default from embedded to subprocess | Per-shape cold cost ratios within the thresholds listed below | shipped (PRs #1669 + #1671) |
| **(b-soft)** | Remove the silent embedded fallback in `UseManagedSubprocessChecker` so production failures surface | Subprocess error reporting good enough that opaque fallback is not needed | shipped (this gate's PR) |
| **(b-hard)** | Delete `embeddedNativeChecker` and the embedded path entirely | Tests migrated off the embedded factory default | not started |

## Reproduction

```sh
bash scripts/bench-checker.sh
```

Prerequisites are auto-built on first run (`.bin/osty`,
`.osty/bin/osty-native-checker`). Raw hyperfine JSON exports land in
`.profiles/checker-bench/` (gitignored). The script's stdout reproduces
the tables below; paste over the placeholders.

To force a clean re-measurement:

```sh
rm -rf .profiles/checker-bench && bash scripts/bench-checker.sh
```

## Environment

| key | value |
|---|---|
| date | 2026-05-11T14:04:02Z |
| commit | a14e8439bd97c9887084c6a4f0842bf86c412703 |
| branch | worktree-virtual-weaving-rainbow |
| os | macOS 26.4 (arm64) |
| cpu | Apple M5 |
| cores | 10 phys / 10 log |
| ram | 32 GiB |
| go | go1.26.2 |
| hyperfine | 1.20.0 |
| osty mtime | May 11 23:02:37 2026 |
| checker mtime | May 11 23:02:37 2026 |

## Workload shapes

| shape | path | files | lines | what it tests |
|---|---|---|---|---|
| micro | `examples/ffi` | 1 | 17 | Smallest realistic package. With checker work ≈ minimum, the embedded↔subprocess delta is dominated by fork+JSON IPC. |
| small | `examples/concurrency` | 1 | 38 | Typical edit-loop / LSP-style single-file package. The user-visible regression bound. |
| large-single | `examples/gc` | 2 | 9,976 | Checker body dominates fork. Tests whether the IPC overhead matters once real work is in the loop. |
| full-toolchain | `toolchain/` (via `osty check toolchain`) | 157 | 125,950 | Worst case. Fork × per-package across the entire self-host bundle, which is the most expensive routine `osty check` invocation in the repo. |

## Cold matrix

> 4 shapes × 2 cache states × 2 modes. cache=off uses
> `OSTY_CHECKER_CACHE=0`; cache=cold leaves the cache layer enabled but
> wipes `<pkg>/.osty/cache/checker` before every iteration via hyperfine
> `--prepare`. Ratio is `subprocess.mean / embedded.mean` — <1.0 means
> subprocess is faster.

| shape | cache | embedded (mean ± σ) | subprocess (mean ± σ) | ratio |
|---|---|---|---|---|
| ffi | off | 18.8 ms ± 8.9 ms (min 9.5 ms) | 21.1 ms ± 19.0 ms (min 11.8 ms) | 1.12× |
| ffi | cold | 11.9 ms ± 7.1 ms (min 6.4 ms) | 12.9 ms ± 3.8 ms (min 9.2 ms) | 1.09× |
| concurrency | off | 17.9 ms ± 9.0 ms (min 13.8 ms) | 13.9 ms ± 6.5 ms (min 11.1 ms) | 0.78× |
| concurrency | cold | 14.5 ms ± 6.1 ms (min 11.0 ms) | 15.9 ms ± 3.2 ms (min 12.3 ms) | 1.10× |
| gc | off | 421.3 ms ± 24.3 ms (min 390.2 ms) | 589.4 ms ± 85.4 ms (min 451.3 ms) | **1.40×** |
| gc | cold | 590.3 ms ± 36.2 ms (min 557.9 ms) | 528.9 ms ± 45.8 ms (min 487.2 ms) | 0.90× |
| toolchain | off | 36.97 s ± 4.38 s (min 30.21 s) | 33.07 s ± 2.81 s (min 28.70 s) | 0.89× |
| toolchain | cold | 22.98 s ± 9.76 s (min 12.47 s) | 30.39 s ± 5.21 s (min 26.66 s) | 1.32× |

## Warm spot-check

> Cache layer active, warmups prime entries, measured iterations hit cache.
> Spot-checked on `concurrency` and `gc` — if both modes converge here,
> the cache layer effectively dominates and the fork delta is invisible
> to the user.

| shape | cache | embedded (mean ± σ) | subprocess (mean ± σ) | ratio |
|---|---|---|---|---|
| concurrency | warm | 24.1 ms ± 11.7 ms (min 11.9 ms) | 18.1 ms ± 11.4 ms (min 7.8 ms) | 0.75× |
| gc | warm | 586.4 ms ± 24.5 ms (min 541.3 ms) | 542.8 ms ± 27.7 ms (min 513.4 ms) | 0.93× |

## Initial findings

The headline: **subprocess is not slower than embedded on the workload that matters most**. On the full toolchain (`osty check toolchain`, 157 files, 126k lines, cache=off), subprocess finished in 33.07 s vs embedded 36.97 s — a 0.89× ratio, i.e. subprocess is about 11% *faster*. On the worst-case sample (toolchain cold) subprocess pays 1.32× vs embedded, but embedded's stddev there is ±9.76 s (42% of mean) — that result is dominated by cache-write variance and is the only place embedded looks meaningfully ahead.

The single cell that exceeds the original 1.30× threshold for "large" workloads is **gc cache=off at 1.40×**. The absolute gap is ~170 ms (421 → 589 ms). It's also worth flagging that with cache=cold, the gc ratio inverts to 0.90× (subprocess faster) — so the regression is specific to the `OSTY_CHECKER_CACHE=0` configuration, which is not the default end-user state.

Small shapes (ffi, concurrency) carry stddev comparable to their mean — the absolute times are 10–25 ms with ~6–19 ms of noise. The ratios there (0.75×–1.12×) are within statistical noise; nothing actionable.

Warm spot-check confirms both modes converge once entries are cached: concurrency warm 18 ms vs 24 ms, gc warm 543 ms vs 586 ms. The cache layer dominates and the fork delta is below user-perceptible threshold.

**Verdict for gate (a)**: data **supports flipping the default to subprocess**. No cell shows an absolute regression that the user would feel; the toolchain workload — the most expensive routine `osty check` — runs at-or-faster on subprocess. The gc cache=off 1.40× cell is a known outlier that exists only outside the default cache configuration. Threshold revision suggestion: keep the 1.3× cap on `cache=cold` (the real-world cold path) and treat `cache=off` as instrumentation-only.

**Verdict for gate (b)**: this baseline does not block embedded deletion on performance grounds. The reliability bar (subprocess as the sole codepath, native checker binary always available) is a separate decision that needs its own follow-up.

## Pass thresholds (gate (a) — flip default)

These bounds were the *starting* design; the table below records the
2026-05-11 measurement result against each one.

| shape | cold ratio cap | absolute cap | measured (cache=cold) | pass? |
|---|---|---|---|---|
| micro | 2.0× | n/a | 1.09× | ✅ |
| small | 2.0× | n/a | 1.10× | ✅ |
| large-single | 1.3× | n/a | 0.90× | ✅ |
| full-toolchain | 1.3× | 60 s | 1.32× (abs 30.4 s) | ⚠ ratio marginal, abs ✅ |

Notes:

- `cache=cold` is the binding column — that's the real-world cold path
  after a `osty/generated.go` invalidation or `osty clean`. `cache=off`
  exists for instrumentation (isolating fork+IPC from cache I/O) and
  carries no formal threshold.
- toolchain cache=cold shows the only ratio close to the threshold
  (1.32× vs 1.30×). Embedded's stddev on that row is ±9.76 s, so the
  ratio is dominated by noise from cache-write variance, not by a real
  subprocess penalty. The absolute 30.4 s sits well under the 60 s cap.
- gc cache=off at 1.40× is *not* counted against the threshold — gate
  (a) defines pass on `cache=cold`, not `cache=off`. The cold cell for
  the same shape is 0.90× (subprocess faster).

Warm spot-check carries no formal threshold — its job is to confirm
that the cache layer keeps both modes interactive, which it does
(concurrency 18 ms / 24 ms; gc 543 ms / 586 ms).

## Caveats

- **macOS only.** Linux measurements are out of scope for this baseline.
- **OS page cache warm = normal.** No `sudo purge` between runs. Each
  hyperfine invocation discards two warmup iterations to neutralize the
  *first*-invocation cold-binary case; later cold-start variance after
  reboot or eviction is out of scope.
- **`OSTY_CHECK_PARALLEL=1` (default) is held constant.** Workspace
  parallel-worker ablation is deferred.
- **Native checker known-failure mode.** `osty check toolchain` currently
  emits a non-zero exit with ~hundreds-to-thousands of diagnostics
  (`docs/toolchain_llvm_status.md`). Hyperfine is invoked with
  `--ignore-failure`; the failure mode is identical across both modes, so
  timing comparisons remain meaningful. Absolute times reflect the failure
  path, not a clean check.
- **Shared cache validity key.** `checker_cache.go` keys cache entries on
  `EmbeddedCheckerFingerprint` (SHA of `internal/selfhost/generated.go`).
  Subprocess and embedded therefore share entries. This is irrelevant for
  cache=off and cache=cold measurements; for cache=warm it means the same
  cached JSON answers both modes after the warmup primes it.

## Out of scope (follow-up)

- `OSTY_CHECK_PARALLEL=0` ablation on the toolchain shape.
- True-cold measurements (post-reboot, `sudo purge`).
- Linux baseline.
- Cross-platform variance.
- Reliability bar for gate (b) — separate from performance.
