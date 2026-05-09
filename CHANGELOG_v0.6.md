# CHANGELOG — v0.6

Tracks the user-visible slice of the v0.6 spec
([`LANG_SPEC_v0.6/`](./LANG_SPEC_v0.6/)) that has landed in the
compiler today, separate from the spec itself.

The spec is the authority on what v0.6 *will* be; this file is the
authority on what v0.6 *is* right now.

See [`LANG_SPEC_v0.6/00-revision.md`](./LANG_SPEC_v0.6/00-revision.md)
for the v0.5 → v0.6 decision log (9 resolved gaps after G38/G39/G43/
G46/G49 withdrawal) and [`SPEC_GAPS.md`](./SPEC_GAPS.md) §"Resolved
in v0.6" for per-gap rationale.

## Implementation phases

각 phase 는 spec 결정 baseline 위에서 *구현 진행도*. 본 changelog 의
`status` 컬럼이 phase 와 매핑.

| Phase | 영역 | Gaps |
|---|---|---|
| Phase 0 | Self-host 자력 사이클 (Tier A 5 개) | (v0.5 follow-up) |
| Phase 1 | Capability + ambient + stdlib migration | G36 |
| Phase 2 | Structured intent + osty context | G42, G47 |
| Phase 3 | Golden | G45 |
| Phase 4 | Sealed + ErrorContract + Evolution | G40, G41, G44 |
| Phase 5 | Taint / sanitize | G37 |

## Shipped in the compiler

> 본 섹션은 PR 머지 시점에 갱신. v0.6 spec 전체가 landed 라는 뜻이
> 아니라, 현재 컴파일러/stdlib이 실제로 받아들이는 조각만 적는다.

### Syntax

| Form | Status | Notes |
|---|---|---|
| Parameter annotation `fn f(#[taint("...")] x: T)` | **planned** (G37) | parameter 위치 annotation 신규 허용 |
| v0.6 declaration annotation vocabulary | **partial** (G36, G37, G40-G42, G44, G45, G47) | Go parser/resolver and selfhost resolver both recognize the declaration/field/method/variant annotation names. Per-feature semantic gates and parameter-position annotations still land by phase. |

### Capabilities (G36 — Phase 1, blocking everything else)

| Item | Status | Notes |
|---|---|---|
| `Clock` / `Rng` / `Env` / `Fs` / `Net` / `Process` / `Console` interface | **partial** | `std.capability` 에 7 canonical protocol surface 추가. `std.random.Rng.next/nextBytes` alias와 `examples/v06_capabilities` front-end proof 포함. |
| Host capability adapter factories | **partial** | `time.systemClock`, `random.host`, `env.host`, `fs.host`, `capability.hostNet`, `capability.hostProcess`, `io.console` 를 추가하고 `examples/v06_capability_adapters` 로 front-end/type-check proof 추가. `net.host` / `process.host` bridge factory 는 cross-module migration helper 로 제공. |
| Deterministic capability test fakes | **partial** | `std.capability.testing` 에 `FakeClock` / `FakeRng` / `FakeEnv` / `FakeFs` / `FakeNet` / `FakeProcess` / `FakeConsole` 추가. `examples/v06_capability_fakes` 로 fake injection proof 추가. |
| `#[ambient(...)]` annotation | **partial** | active checker gate now rejects non-entry usage (`E0780`), unknown canonical names (`E0781`), and user-defined ambient capabilities (`E0789`). Positive/negative spec corpus plus `examples/v06_ambient_boundaries` pin the boundary; auto-forward/desugar remains follow-up. |
| `--legacy-globals` 호환 모드 | **partial** | CLI flag (`cli.CliFlags.LegacyGlobals`) + W0750 deprecation pass land — `osty check/typecheck/resolve/lint --legacy-globals` 가 v0.5 글로벌 호출 (`time.now()`, `random.next()`, `env.get(...)`, `fs.read(...)`, `os.exec(...)`, `net.dial(...)` 등) 마다 W0750 발화. Auto-desugar to capability host adapters + manifest stability=experimental 강제 는 follow-up. v0.6.x 에서만, v0.7 제거. |
| stdlib `time.*` → `clock.*` migration | **partial** | migration catalog (`capability.legacyGlobalRewriteRules`) 와 host-boundary factories가 landing. 전역 호출 warning/desugar 는 compiler phase 후속. |

### Information flow (G37 — Phase 5)

| Item | Status | Notes |
|---|---|---|
| `#[taint("source")]` source annotation | **planned** | |
| `#[sanitizes("source", into = "trust")]` | **planned** | |
| `#[requires("trust")]` sink annotation | **planned** | |
| `#[trusted_declassify(reason)]` FFI escape | **planned** | audit 가능 |
| stdlib sink catalog (`db.query`, `process.exec`, `fs.path*`, `http.redirect`, `template.render`) | **planned** | Phase 5 baseline |

### Intent

| Item | Status | Notes |
|---|---|---|
| `#[purpose("...")]` | **partial** (G42, Phase 2) | active checker gate now rejects non-string-literal arguments (`E0434`). Interpolated, raw, triple-quoted, and `key = value` forms are caught with a structured detail message. |
| `#[example(input=, output=, uses=)]` | **partial** (G42, Phase 2) | active checker gate enforces the `input` / `output` / `uses` key catalog (`E0435`); preserves the `input` arity (`E0431`) and `uses` fixture (`E0433`) gates. Runtime `osty test --example` execution and output mismatch (`E0430`) remain follow-up. |
| `#[fixture(name=)]` | **partial** (G42, Phase 2) | active checker gate restricts `#[fixture]` to function declarations (`E0436`) and keeps the zero-arity rule (`E0432`). Gate runs at struct / enum / interface / type alias / let / variant / field positions. |
| `osty context <symbol>` | **planned** (G47, Phase 2) | JSON output |

### Determinism / Construction / Errors

| Item | Status | Notes |
|---|---|---|
| `#[pure]` capability parameter rejection | **partial** (carried from v0.5 §3.8 + v0.6 capability gate) | active signature gate rejects all capability parameters on `#[pure]` (`E0785`). G39 `#[reproducible]` was withdrawn pre-release; the gate alone is the v0.6 effect-free attestation. |
| `#[sealed_construct(name)]` | **planned** (G40, Phase 4) | parse-don't-validate primitive |
| `#[trusted_construct(reason)]` | **planned** (G40, Phase 4) | stdlib escape |
| `#[test_construct]` | **planned** (G40, Phase 4) | test profile escape |
| `#[error_contract(Variant when "...")]` | **planned** (G41, Phase 4) | failure mode catalog |

### Evolution / Testing / Performance

| Item | Status | Notes |
|---|---|---|
| `#[since("X.Y")]` | **partial** (G44, Phase 4) | format gate landed: SemVer-shape regex enforced (`E0452`); arg must be exactly one non-interpolated string literal. `osty publish` API surface gate is still pending. |
| `#[stability("level", until/since/remove)]` | **planned** (G44, Phase 4) | osty publish 게이트 |
| `#[match_compat("X.Y", fallback=)]` | **planned** (G44, Phase 4) | non-silent fallback 강제 |
| `osty publish` API surface diff | **planned** (G44, Phase 4) | major bump 강제 |
| `#[golden("path", mode="text"|"ast")]` | **planned** (G45, Phase 3) | snapshot determinism is caller responsibility (G39 `#[reproducible]` withdrawn) |
| `osty test --golden` / `--update-golden` | **planned** (G45, Phase 3) | |

## Migration path from v0.5

### Breaking changes (사용자 0 단계의 acceptable break)

1. **`while` reserved** — 식별자 `while` 사용 코드 rename 필요. 100 PR 코드
   감사 결과 0 사용 (`rg '\bwhile\b' --type=osty` 결과 없음 가정)
2. **stdlib capability migration** — `time.now()` → `clock.now()` 등.
   `--legacy-globals` v0.6.x 호환 모드 제공, v0.7 제거.
3. **stdlib sealed types** — `Email` / `Url` / `Path` / `SqlIdent` 등이
   `#[sealed_construct]` 화. 외부 literal 생성 코드 → `Type.parse(...)` 경유로
   변경. `--legacy-construct` 호환 모드 v0.6.x 한정.
4. **stdlib sink annotation** — Phase 5 에서 `db.query` / `process.exec` 등에
   `#[requires]` 추가. 미-sanitize 코드는 컴파일 에러 (의도된 보안 회귀 노출).
   임시 우회 `#[trusted_declassify]` 제공, audit 으로 enumerate.

### Additive changes (호환)

- `#[since]` / `#[stability]` / `#[match_compat]` — 옵션 어노테이션, 미사용
  코드 영향 없음
- `#[purpose]` / `#[example]` / `#[fixture]` — 메타데이터, 옵션
- `#[golden]` — 옵션, 미사용 영향 없음

## Spec corpus 확장

각 Phase 종료 시 `testdata/spec/positive/` + `testdata/spec/negative/reject.osty`
에 신규 케이스 추가.

| Phase | Positive 케이스 추가 | Negative 케이스 추가 |
|---|---|---|
| 1 | capability typed signature | E0780-E0782, E0785, E0789 (capability misuse) |
| 2 | `osty context` 출력 | (intent annotations metadata only) |
| 3 | golden snapshot | E0445, W0444 |
| 4 | sealed/ErrorContract/Evolution | E0410-E0451, E2100-E2101 |
| 5 | taint flow | E0900-E0903 |

각 phase 의 `LLVM E2E` 통과까지 확인 후 phase 종료.

## v0.6 → v0.7 forecast

v0.7 에서 다룰 예정 (현 시점 미결정):

- **Implicit information flow** (covert channel) — opt-in `#[strict_flow]`
- **Row-polymorphic error union** — `Result<T, EmailError | DbError | ...>` 의
  open form
- **Cross-capability flow analysis** — `Clock` 결과가 `Rng` seed 가 되는 패턴
- **Capability composition** — `Clock & Rng` 합성 capability 의 derived interface
- **Implementation-defined: 1.0 cut 전 stability promise** — `stable` 약속 활성화

각 항목은 v0.6 사용 corpus 누적 후 G51+ 으로 등재 검토.
