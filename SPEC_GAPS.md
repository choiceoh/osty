# Osty 스펙·Grammar 갭 트래킹

- **Scope**: Resolved spec gaps — v0.4 edge-case decisions, archived by language version
- **Type**: Decision log
`LANG_SPEC_v0.6/` + `OSTY_GRAMMAR_v0.6.md` 기준.

**v0.6 시점 open gap: 없음.** v0.4 사용 코퍼스에서 관찰된 15 개
개선점(G20-G34) 을 v0.5 에서 일괄 결정했고, v0.6 에서는 G36-G48 중
G36/G37/G40-G42/G44/G45/G47/G48 (9 결정) 을 baseline 동결했다.

스펙은 v0.2 부터 폴더 구조이다. §X (X = 1..21) 는 `LANG_SPEC_v0.6/NN-*.md`
파일. §10 의 서브섹션은 `LANG_SPEC_v0.6/10-standard-library/NN-*.md`.
§19 는 v0.4 additive minor 로 도입된 toolchain-only 서브언어.

---

## Open Gaps

### `vectorize-hint` — Vectorize 계열 backend 전제조건 (v0.6 A5 / A5.1 / A5.2 / A6 / A7)

**상태:** v0.6 Vectorize 트랙 전체 착륙. A5.2 에서 기본값을 뒤집어
vectorize 가 default-ON 이 됐고, `#[no_vectorize]` 로만 opt-out.
`#[vectorize(width=N, scalable, predicate)]` 는 tuning args, `#[parallel]`
와 `#[unroll]` 은 별개 opt-in 힌트. Safepoint poll blocker 해소됨
(§3.8.6 공통 GC contract). AVX-512 cost-model 우회와 Apple Silicon
SVE fallback 은 §3.8.3 에 문서화 완료. 남은 gap 은 iterator-protocol
루프 커버리지뿐이다.

1. ~~**Safepoint poll hoisting.**~~ **해소됨 (A5).** `#[vectorize]` /
   `#[parallel]` / `#[unroll]` 중 하나라도 붙은 함수는
   `emitLoopSafepoint` (HIR) / `emitLoopSafepointKind` (MIR) 호출을
   스킵한다. 함수 entry 의 safepoint 와 caller 의 safepoint 사이에
   루프가 bracketed 되어 GC liveness 는 유지. 실측: XOR reduction 1M
   × 200 iterations — scalar 0.28s vs vectorized 0.06s = **4.7× 속도
   향상**, ARM NEON `eor.16b` / `add.2d` emission 확인.
1a. ~~**Local scalar List subscript fast path.**~~ **해소됨.** 이전에는
   `emitVectorListPreamble` 이 함수 entry 에서 파라미터만 snapshot 해서
   `let mut xs: List<Int> = []; push…; for i in 0..xs.len() { xs[i] }`
   같은 "build-then-read" 패턴의 read 루프가 매 iteration 에서
   `@osty_rt_list_get_i64` 를 호출했고, LLVM vectorizer 가 "call
   instruction cannot be vectorized" 로 bail 했다. 해소 경로는
   `emitVectorListFastLoad` 에 lazy snapshot 을 추가하고 forward-CFG
   reachability gate (`mirLocalMutationReachableFrom`) 로 뮤테이션이
   snapshot site 로부터 도달 가능하면 slow call 로 fallback — realloc
   으로 인한 stale data ptr 캐싱을 막는다. 동시에 런타임 data / len /
   get 선언에 `memory(read)` 를 부여해 LLVM LICM 이 snapshot call 을
   hoist 할 수 있게 한다. 회귀 테스트는 `vectorize_real_simd_test.go`
   의 `TestVectorizeAppliesToListLocalReadLoop` (happy path) 와
   `TestVectorizeLazySnapshotRespectsInLoopMutation` (soundness gate).
2. **Iterator-protocol loops.** `for x in iter` (iter 가 `List<T>` /
   range / `Map<K, V>` 가 아닌 경우) 는 callback-driven shape 로
   lower 되어 LLVM 이 trip count 를 볼 수 없다. 해결 경로는
   `Iterable<T>` 프로토콜을 구현하는 타입들 중 countable 가능한 것들
   (예: `Array`, `Slice`) 에 한해 index-driven lowering 으로 전환하는
   것. 언어 쪽 스펙은 그대로 둔다 — 어디까지나 backend 구현 선택지.
3. ~~**AVX-512 cost model 우회 문서화.**~~ **해소됨.** `#[vectorize(width = 8)]` 가 i64 loop
   에 걸려도 LLVM 의 x86 cost model 이 historically downclocking
   penalty 때문에 256-bit YMM 만 선택하는 경우가 있다 (실측: width=8
   요청했으나 vectorizer 가 width=4 선택, ZMM 미사용). 사용자가
   `-mllvm -force-vector-width=8` build-time flag 로 override 할 수
   있음을 §3.8.3 `width` 행에 명시했다. 향후 `osty build` 가 target
   profile (예: `--target x86_64-avx512-server`) 에서 이 flag 를 자동
   주입하는 manifest 단축키를 제공하는 것은 별도 target-profile 개선
   작업으로 추적한다.
4. ~~**SVE / Apple Silicon target caveat 문서화.**~~ **해소됨.** `#[vectorize(scalable)]` + `-mcpu=neoverse-v1`
   조합이 `vscale x 8` SVE 루프 + 45 개 `z<N>.d` 명령을 생성하는 것을
   실측 확인. 그러나 **macOS/iOS aarch64 는 SVE 를 ISA 로 노출하지
   않으므로** (Apple Silicon 은 NEON 만) scalable hint 가 NEON 2-wide
   로 폴백한다. 이건 hardware 한계이지 gap 이 아니며, §3.8.3
   `scalable` 행에 Apple Silicon fallback 을 명시했다.

향후 gap 을 닫을 때 같은 entry 에 해결 요지 + 관련 PR 을 기록한다.

### ~~`pure-enforce`~~ — `#[pure]` checker enforcement (v0.6 A13) — **이미 해소됨 (2026-05-05 doc fix)**

**상태:** 갭 문서가 stale. `#[pure]` checker enforcement 는 이미
구현되어 있고 E0775 코드로 발화한다 — `toolchain/check_gates.osty::runPureGate`
(seed mirror at `internal/selfhost/generated.go`).

`runPureGate` 가 `#[pure]` 어노테이션 함수 / 메서드 본문을 walk 해서
다음 카테고리를 모두 거부:

  (a) non-local write — `AstNAssign` 타깃이 fn-local binding 이 아닐 때
  (b) I/O — `println` / `print` / `eprint` / `eprintln` / `fmt.*` / fs.* 등
      `pureCalleeLooksLikeIO` 가 잡는 패턴
  (c) impure call — callee 가 같은 파일의 `#[pure]` fn 이 아닐 때
  (d) volatile/atomic — §9.5 기준 v0.6 atomics 는 `std.sync` effectful
      primitive 이므로 `#[pure]` 본문에서 거부되어야 함
  (e) allocation — `AstNList` / `AstNMap` / `AstNStructLit` /
      `AstNClosure` / 보간 / 문자열 concat (관리되는 GC 할당)

추가로 잡히는 것:
  - `AstNChanSend` — 채널 send (관찰 가능 부작용)
  - `AstNDefer` — 함수 반환 후 실행, pure 본문에 부적절

진정 pure 한 본문은 통과. 회귀 테스트는
`internal/selfhost/pure_enforcement_test.go::TestPureAnnotationEnforcedByE0775`
가 4개 카테고리를 모두 핀.

**관련 PR**: TBD (이번 작업 — 문서 fix + 회귀 픽스).

### ~~`a12-branch-hints`~~ — `likely(x)` / `unlikely(x)` 빌트인 (v0.6 A12 후속) — **해소됨 (2026-05-05)**

**픽스**: 빌트인 함수 접근으로 착륙. Surface:
- `likely(cond: Bool) -> Bool`
- `unlikely(cond: Bool) -> Bool`

5곳 변경:
1. `internal/mir/mir.go` — `IntrinsicLikely` / `IntrinsicUnlikely` 추가
2. `internal/mir/lower.go::lowerCallExprInto` — bare ident dispatch 가
   `likely`/`unlikely` 인식해서 IntrinsicInstr 로 lower
3. `internal/llvmgen/mir_generator.go::emitIntrinsic` — 새 kind 들이
   `emitBranchHintIntrinsic` 으로 라우팅, `call i1 @llvm.expect.i1(i1
   %cond, i1 <expected>)` 발화
4. `internal/llvmgen/expr.go::emitCall` — 레거시 AST 경로용
   `emitBranchHintCall` 추가 (같은 IR 셰이프)
5. `internal/llvmgen/ir_native_entry.go::nativeExprFromIR` — native-owned
   경로용 `nativeBranchHintExprFromIR` 추가 (역시 같은 IR)

체커 사이드:
- `internal/selfhost/generated.go` (frozen seed) — `likely`/`unlikely`
  을 prelude fn 으로 등록 + `srIsBuiltinName` 화이트리스트에 추가
- `toolchain/check_env.osty` + `toolchain/resolve.osty` — Osty mirror

런타임 시맨틱은 identity (cond 그대로 반환); LLVM 이 `@llvm.expect.i1`
힌트를 받아서 block layout 만 biases.

**관련 PR**: TBD (이번 작업).

### `log-fields-sugar` — `Fields { "k": v }` 리터럴 + `ToLogValue` 자동 derive (spec §10.10)

**상태:** spec §10.10 line 9 의 canonical 호출 형태
`log.info("msg", Fields { "userId": 42, "ip": "1.2.3.4" })` 가
컴파일 안 됨. 두 가지 컴파일러 기능이 누락:

1. **Type-alias struct-literal sugar.** `pub type Fields =
   Map<String, LogValue>` 는 type alias 인데, `Fields { ... }` 의
   struct-literal head 위치에서 alias 가 허용되지 않아 parser 가
   `E0204 expected ), got {` 로 거부. 또는 alias 로 해석되지 않아
   `E0745 cannot find Fields in this scope` 발화.
2. **`ToLogValue` 자동 derive.** §10.10 line 56-67 은 `Fields` 리터럴
   값 위치마다 컴파일러가 implicit `.toLogValue()` 변환을 삽입한다고
   명시. `ToLogValue` 는 모든 primitive / `String` / `Bytes` /
   `Instant` / `Duration` / `Option<T> where T: ToLogValue` /
   `List<T> where T: ToLogValue` / `Map<String, V> where V: ToLogValue`
   에 자동 구현돼야 하지만 현재 컴파일러에 derive 경로 없음.
   `internal/check`, `internal/resolve`, `internal/llvmgen` 어디에도
   `ToLogValue` 토큰 등장 안 함.

**현재 워크어라운드.** 호출자가 explicit constructor 사용:
```osty
let f: Map<String, log.LogValue> = {
    "userId": log.intValue(42),
    "ip": log.stringValue("1.2.3.4"),
}
log.info("user logged in", f)
```

**구현 규모.**
- (1) parser 에서 type-alias 를 struct-literal head 로 허용. resolver
  가 alias target (`Map<K,V>`) 으로 unwrap 하고 map literal 로 해석.
- (2) checker 에서 `ToLogValue` 자동 derive — `cmp.Equal/Ordered/Hashable`
  자동 derive (§2.6.5) 와 동일 메커니즘. `Fields` 값 위치에서 implicit
  conversion 삽입은 별도 hook.

**연관.** `internal/stdlib/modules/log.osty` 본문은 spec 명시 동작
(stderr, level prefix, fields rendering, JSON handler)을 갖췄지만
canonical caller 가 위 두 갭 때문에 호출 안 됨. 갭 해소 시 즉시
spec 동급 동작.

### `stdlib-body-llvm-wall` — pure-Osty stdlib 본문이 LLVM 백엔드에서 lower 안 됨 (2026-05-05 부분 해소)

**상태 업데이트 (2026-05-05 재검증)**: 2026-05-02 audit 의 8개 실패 항목 중 **4개가 이미 해소됨** (그 사이 다른 PR 들이 백엔드 / shim 을 키운 결과). 남은 항목 + 새로 발견된 사항:

| 모듈 | 2026-05-02 status | 2026-05-05 재검증 |
|---|---|---|
| compress | `gzip.encode(bytes)` 실패 | ✅ **PASS** (`bytes.fromString("hello")` 로 round-trip 동작) |
| url | `url.parse(s)` 실패 | ✅ **PASS** (RFC 3986 본문 통과) |
| json | `json.parse(s)` 실패 | ⚠️ generic `parse<T>` 가 type 추론 못 — turbofish 필요 (사용성 issue, 백엔드 wall 아님) |
| encoding | `hexEncode(b)` 실패 | ✅ **PASS** (`encoding.hex.encode(bytes)` 동작) |
| iter | `iter.map(xs, ...)` 실패 | ⚠️ gap doc API 오류 — iter 는 `Iter<T>` struct 메서드 (free fn 아님). 사용성 mismatch, 백엔드 wall 아님 |
| option / result combinator | `.map(\|x\| ...)` 실패 | ❌ **여전히 실패** — `Option__map` / `Result__map` 미해소 symbol (closure body) |
| crypto | `sha256(b)` 실패 | ✅ **PASS** (`crypto.sha256(bytes)` 동작) |
| bytes | `b"abc"` 인자 위치 parser 거부 | ✅ **PASS (2026-05-05)** — `FrontByteString` token kind 추가. Lexer 인식 + parser `AstNStringLit` 으로 lower 완료. 잔여: `b"..."` 의 type 을 `Bytes` 로 inference 하는 `AstNByteStringLit` 노드 작업은 follow-up. |

**남은 실 wall 1개**:

1. **option/result combinator closure** — `.map(\|x\| ...)` 가 LLVM-route 에서 unresolved. closure body monomorphization + generic param 의 backend coverage 가 필요.

**해소됨 (2026-05-05)**: `b"..."` byte string literal (`FrontByteString` token kind). Lexer 가 `b"hello"` 를 인식, `BYTESTRING` token 생성, parser 가 `AstNStringLit` 으로 lower. 변경사항:
- `internal/selfhost/generated.go` — `FrontTokenKind_FrontByteString` type + dispatch in `frontInterpolationTokenScan` + `frontStringLikeScan` + `frontStringContentStart` + `ostyPublicTokenText` + `isFrontLiteralKind` + `frontKindInsertsTerm` + `frontTokenKindName` + parser `opParsePrimary` + `astLowerKind`
- `internal/token/token.go` — `BYTESTRING` token kind (name + entry)
- `internal/selfhost/adapter.go` — `FrontByteString` → `token.BYTESTRING` mapping + `fillLiteralParts`
- `internal/cst/parse.go` — `BYTESTRING` in literal pattern + expression
- `toolchain/frontend.osty` — same dispatch logic (self-hosting convergence)
- `internal/docgen/generated.go` — matching changes for docgen

**잔여**: `b"..."` 가 현재 `AstNStringLit` 으로 lower 되어 type checker 가 `Bytes` 대신 `String` 으로 추론. `b"..."` 가 spec대로 `Bytes` literal 이 되려면 `AstNByteStringLit` 노드 추가 + type checker + codegen 작업 필요. Follow-up track.

`log` shim 패턴 (`internal/llvmgen/stdlib_<m>_shim.go`) 으로 우회한 것들이 그동안 catch up 했다는 게 가장 큰 변화 — 매트릭스 재평가 2026-05-02 이후의 progress 가 자연 추적 안 됐던 것.

### ~~`duration-builtin-methods`~~ — `Duration` builtin 의 메서드/필드 미등록 — **해소됨 (2026-05-05)**

**픽스**: `internal/selfhost/primitive_arith_register.go` 의
`registerSupplementalStdlibSurface` 에 `registerDurationMembers` 추가
— `Int.s` 같은 prelude-route 에서 `Duration` 이 등장할 때 그 struct
의 method/field 도 함께 checker 에 등록되도록 했다. 메서드는 `abs() →
Duration`, `micros() / millis() / seconds() → Int`, `toString() →
String` 다섯 개 + `nanoseconds: Int64` 필드 — 모두 `internal/stdlib/modules/time.osty`
의 struct 정의에서 그대로 흡수.

**관련 PR**: TBD (이번 작업).

### ~~`default-arg-resolve`~~ — defaulted parameter 가 함수 scope 에 등록 안 됨 — **해소됨 (2026-05-05)**

**진단**: resolver 가 아니라 **체커**의 결함이었다.
`toolchain/check.osty::collectFnDecl` 이 default 를 가진 파라미터
이름을 `?`-prefix 메타데이터로 `paramNames` 에 저장 (arity-tracking
용; `paramDefaultCount` 가 trailing default 개수 카운트). 그런데
`elabFnDecl` 의 body-scope 바인딩 루프가 그 prefix 를 strip 하지 않고
그대로 `checkBindSpan(env, "?timeout", ...)` 으로 등록 — body 가
`timeout` 을 찾으면 `?timeout` 만 바운드되어 있어서 E0745. 진단의
`hint: did you mean ?timeout?` 도 같은 누설.

**픽스**: `?` prefix 를 strip 한 후 body scope 에 바인드. 다른 곳에서
`?` prefix 를 보는 사이트는 `paramDefaultCount` 한 곳뿐이라 strip 의
부작용 없음.

**관련 PR**: TBD (이번 작업).

**운영 정책.** 새 gap 은 기존 처리 절차대로 G 번호를 부여해 `Open Gaps`
섹션에서 추적. 버그 수정 / 명확화 / 성능 최적화 / 의미 중립 변경은
G 번호 없이 일반 이슈 트래커에서 처리. 언어 surface 변경은 정식 버전
업 (minor / major) 과 함께 문서화.

---

## Resolved in v0.6

v0.6 은 100 PR sprint 직후 *사용자 0 인 마지막 기회*에 hidden dependency 를
모두 surface 로 끌어올리는 batch 결정을 한 릴리스다. 13 개 신규 결정 + 2 개
ergonomics 개선. 일부는 *breaking* 임 — `--legacy-globals` 호환 모드를
v0.6.x 에서만 제공 후 v0.7 에서 제거. 자세한 spec 본문은
[`LANG_SPEC_v0.6/00-revision.md`](LANG_SPEC_v0.6/00-revision.md).

**Design north star**: *Hidden dependency is forbidden* — 시간 / 난수 / 환경
/ 보안 흐름 / 진화 규칙 / 성능 계약 / 의도 / 명세 어느 것도 암묵으로 두지
않는다.

| ID | 영역 | 결정 | 상태 |
|---|---|---|---|
| **G36** | Capabilities (§20) | 환경 effect (`Clock`, `Rng`, `Env`, `Fs`, `Net`, `Process`, `Console`) 를 stdlib interface 로 정의하고 함수 파라미터로 전달. 값의 interface 만족은 structural 이지만 effect/determinism 분류는 parameter 의 resolved interface identity 기준. `#[ambient(...)]` 어노테이션이 script / `main` / test 진입점에서 process-singleton default instance 를 scope 에 주입. `#[pure]` 검사가 capability 시그니처 기반 *allow-list* 로 sound. v0.5 의 전역 함수 (`time.now()` / `random.next()` 등) 는 `--legacy-globals` 로 v0.6.x 호환, v0.7 제거. | decided |
| **G37** | Information flow (§21) | `#[taint("source")]` source 표시, `#[sanitizes("source", into = "trust")]` sanitizer, `#[requires("trust")]` sink 요구. 1-bit + N-tag value-flow tracking 을 checker 에 추가하되 tag 는 type identity / monomorphization key 에 포함하지 않는다. Generic / closure 통한 propagation 자동. Implicit flow 는 *explicit-only* 정책 (Jif 와 동일). FFI/opaque 경계는 sibling `#[sanitizes]` + `#[trusted_declassify(reason)]` 로 audit. stdlib sink catalog 는 baseline 이며 user-defined sink 는 sanitizer-produced tag 에 한해 허용. mainstream 산업언어 첫 정적 IFC. | decided |
| **G40** | Sealed construct (§3.4.5) | `#[sealed_construct(name)]` — 명시한 constructor 만 허용. 외부 struct literal / spread update / 직접 mutation / generic deserialize / FFI default 모두 차단. `#[trusted_construct(reason)]` stdlib escape, `#[test_construct]` test profile escape. parse-don't-validate 패턴을 언어 primitive 로. v0.6 baseline 에서 stdlib `Email` / `Url` / `Path` / `SqlIdent` / `Duration` / `Uuid` 등 sealed 화. | decided |
| **G41** | Error contract (§7.5) | `#[error_contract(Variant when "condition", ...)]` — concrete enum error 의 failure mode catalog. Contract inclusion 은 `(EnumTypeIdentity, VariantName)` 정확 일치. `Err(...)` return path 정합성 정적 검증 (`E0410`), 미발화 variant `W0411`, `?` propagation subset mismatch 는 `E0414`. 호출자 측 match exhaustiveness 가 contract variant 기반. erased `Error` 에는 `#[error_contract(any)]` 만 가능하며 concrete caller superset 을 자동 만족하지 않는다. | decided |
| **G42** | Structured intent (§3.12) | `#[purpose("...")]` 자유 텍스트 의도, `#[example(input=, output=, uses=)]` 자동 검증 예시, `#[fixture(name=)]` canonical instance. 모두 `osty doc` / `osty context` / LSP / property test seed 가 공유. AI hype 비의존 framing — *machine-readable intent*. | decided |
| **G44** | API evolution (§3.14) | `#[since("X.Y")]` 도입 버전, `#[stability(level, until?, since?, remove?)]` 에서 level ∈ {`stable`, `experimental`, `deprecated`, `internal`}. `osty publish` 가 stable API breaking change 시 major bump 강제 (`E2100`). `#[match_compat("X.Y", fallback=)]` 로 enum shape pin — silent fallback 금지 (`E0450`), `unsafe_silent` 명시 시 `W0902`. | decided |
| **G45** | Golden tests (§11.5) | `#[golden("path", mode="text"|"ast"|"json"|"diag")]` snapshot 비교. text mode 는 BOM 제거 + CRLF/CR→LF 정규화 후 byte 비교(trailing newline 보존). AST mode 는 reparse 후 비교 (whitespace/comment 무시). `osty test --golden` / `--update-golden`. Snapshot determinism 보장은 호출자 책임 (G39 `#[reproducible]` withdrawn). | decided |
| **G47** | Machine-readable context (§13.4 / §13.9) | `osty context <symbol> [--format=json] [--recursive]` — purpose / examples / error_contract / fixtures / stability / since 단일 JSON 으로 추출. LSP `textDocument/hover` 통합. AI agent 첫 시민. | decided |
| **G48** | Annotation surface 통합 | 신규 어노테이션 14 개 (G36, G37, G40-G42, G44, G45, G47 합계) 가 기존 fixed annotation set 에 추가, `#[name(args)]` form 그대로 — 신규 grammar 0. parameter 위치 annotation (`#[taint]` / `#[requires]`) 은 v0.6 grammar 변경. | decided |

#### Withdrawn from v0.6 baseline

다음 5 결정은 v0.6 release 전에 *low utility* 사유로 withdraw:

| ID | 영역 | 철회 사유 |
|---|---|---|
| **G38** | Spec link `#[spec("§X.Y")]` | user-code use case narrow. doc comment + 외부 link checker 로 충분. |
| **G39** | Reproducibility `#[reproducible(scope=...)]` | over-design — 3-tier scope (`run`/`target`/`portable`) 의 use site 가 cache key / 빌드 해시 / migration ID 로 narrow. `#[pure]` (LLVM `readnone`, capability 수신 차단 = `E0785`) + `#[golden]` (snapshot) implication 으로 흡수. Pre-release 수정 (어노테이션 surface 16→14, E0783/E0784/E0786/E0787/E0788/E0444 회수). 동반 회수: `#[reproducible_capability]` interface attestation. |
| **G43** | Spec block `spec { example: / law: / invariant: }` | `example:` 은 `#[example]` 과 기능 중복. `law:` / `invariant:` 는 v0 doc-only 였음. |
| **G46** | Performance contract `#[budget(...)]` | `#[pure]` 가 effect bound covers. perf bound 는 외부 벤치/CI 로 충분. |
| **G49** | `while` keyword | `for cond { }` 와 동의어. 순수 cosmetic. |

이 9 개 결정 (G36, G37, G40-G42, G44, G45, G47, G48) 은 [`LANG_SPEC_v0.6/00-revision.md`](LANG_SPEC_v0.6/00-revision.md)
가 권위. 구현 phase 는 동 문서 §2 참조. 호환성 영향이 있는 항목 — G36 (capability
migration, breaking in v0.7), G37 (taint sink rollout, breaking for unsanitized
code), G40 (stdlib sealed types) — 은 `--legacy-globals` 또는 `--legacy-construct`
호환 모드 v0.6.x 한정 제공.

---

## Resolved in v0.5

v0.5 는 v0.4 이후 일정 기간 사용하며 누적된 15 개 개선점을 한 번에
결정해서 접는 릴리스다. 모두 additive — v0.4 프로그램은 바이트 단위
변경 없이 컴파일된다.

| ID | 영역 | 결정 | 상태 |
|---|---|---|---|
| **G20** | Function value parameter-name preservation | `fn(...) -> ...` 타입에 파라미터 이름을 **type-equality-neutral metadata** 로 보존. 이름이 일치하는 키워드 호출을 함수값에서도 허용. 기본값 metadata 는 G15 대로 여전히 erase. 타입 동등성은 이름을 무시하므로 `fn(String, Int)` 타입으로의 대입은 그대로 가능. | decided |
| **G21** | Default argument literal definition extension | `DefaultLiteral` 집합이 (a) 필드가 모두 literal 인 struct literal 과 (b) `const fn` 호출 반환값을 포함. 평가 시점은 기존 규칙 (each call site) 유지. `const fn` 본문 허용 범위는 §3.1.1 capability matrix 로 고정 — literal / 산술 / 비교 / 논리 / 직접 `const fn` 호출 / 구성자만 허용, 제어 흐름·재귀·문자열 연결·제네릭은 금지. Call graph 는 acyclic (`E0767`), 제네릭 `const fn` 은 `E0768`, 매트릭스 밖 구문은 `E0766`. | decided |
| **G22** | `loop` expression | `loop { ... break value }` — 값 반환 가능한 무한 루프. `for cond { }` (Unit 반환 while-style) 와 역할 분리. `break value` 의 타입이 루프 전체 타입. | decided |
| **G23** | Trailing closure call | 마지막 인자가 함수 타입일 때 `(...)` 바깥으로 closure 를 뺄 수 있다. 괄호 + closure 공존 가능 (`f(a, b) \|x\| { ... }`). Closure 시작 `\|` 다음에 ident 가 오지 않으면 block expression 으로 파싱 (fallthrough). | decided |
| **G24** | Labeled break/continue | `'label: for / loop` prefix. `break 'label [value]?` / `continue 'label`. 없는 라벨은 `E0763`. 라벨 없으면 가장 가까운 enclosing 루프 대상. | decided |
| **G25** | Range step `by` | `0..100 by 2`, `10..=0 by -1`. `by` 는 range expression 문맥의 contextual keyword — 그 외 위치에선 식별자. 0 step 은 런타임 abort. | decided |
| **G26** | Struct update shorthand | receiver 로컬 식별자가 struct 타입이면 `receiver { field: value, ... }` 가 `Type { ..receiver, field: value, ... }` 와 등가. Expression path (비-ident) 수신은 기존 `Type { ..expr, ... }` 형식 유지. | decided |
| **G27** | `as?` downcast syntax | `err as? T` 는 `err.downcast::<T>()` 와 정확 등가. 좌측 피연산자가 `Error` 구현체여야 함; 그 외는 `E0727`. 새 키워드 없음 — `as?` 는 single 토큰으로 lex. | decided |
| **G28** | Scoped / grouped imports | `use path::{a, b as c, d}`. 기존 `use Path (as Ident)?` 와 공존. 중복 이름은 `E0554`. | decided |
| **G29** | Conditional compilation `#[cfg(...)]` | pre-resolve filter — cfg 평가가 false 인 선언은 resolve 전에 제거되어 type check 도 안 됨. Key 네 종류만 허용 (`os`, `target`, `arch`, `feature`). 조합은 `all(a, b)` / `any(a, b)` / `not(x)`. 미인식 key 는 `E0405`. | decided |
| **G30** | `pub use` re-export | 해당 심볼의 가시성과 안정성/error/flow 계약을 re-export 위치에 상속. 순환은 `E0552`. target 이 private 인데 `pub use` 로 노출 시도는 `E0553`. public 이름 충돌은 shadowing 하지 않고 `E0554`. | decided |
| **G31** | Enum integer discriminants | `pub enum X: IntN { A = 1, B = 2 }` — discriminant 타입은 `Int` / `Int32` / `Int16` / `Int8` 네 개. Payload variant 에는 할당 금지 (`E0721`). 중복 값은 `E0722`. `.discriminant()` / `X.fromDiscriminant(n)` 자동 파생. | decided |
| **G32** | Inline `#[test]` + doctest | `#[test]` 어노테이션이 붙은 `fn` 은 프로덕션 바이너리에서 제외되고 `osty test` 에서만 수집. 기존 `_test.osty` 경로는 backward compat 으로 유지. `///` 블록 안의 ` ```osty ``` ` fence 에서 doctest 추출 — `osty test --doc`. | decided |
| **G33** | Property-based testing | `std.testing.gen` 서브모듈 신설 — `Gen<T>` + 조합자 (`oneOf` / `map` / `filter` / `pair` / `list` / `record`), 기본 타입에 대한 shrinker. `testing.property(name, gen, pred)` 러너. | decided |
| **G34** | Numeric widening and float promotion | 암묵 변환: `Int8 → Int16 → Int32 → Int`, `Int32 → Float64`, `Int → Float64`, `Float32 → Float64`. `Int → Float64` 는 precision-tolerant edge 이며 lossless 보장은 없다. Narrowing 은 접미사 강제 — `.toInt32() / .toInt16() / .toInt8() / .toIntTrunc() / .toIntRound() / .toIntFloor() / .toIntCeil() / .toFloat32()`. 암묵 narrowing 은 `E0765`. | decided |
| **G35** | Bounded operator overloading | `#[op(+)] / #[op(-)] / #[op(*)] / #[op(/)] / #[op(%)]` (binary) + `#[op(-)]` (unary) 여섯 개만 허용. 어노테이션 없으면 디스패치 안 됨 (우연 오버로딩 차단). 메서드 시그니처 불일치는 `E0723`, 중복 구현은 `E0724`, 허용되지 않은 연산자는 `E0725`. `== / != / < / <= / > / >= / [] / () / << / >> / & / \| / ^` 는 primitive-only. | decided |

### Final v0.5 Decisions

이 표는 v0.5 에 올린 결정안이다. 원칙 네 가지:
문법 확장은 모두 additive 일 것, 새 키워드는 contextual 일 것,
체커 변경은 기존 타입 equality 를 깨지 않을 것, 기본 인자 / 오버로드
같은 확장은 opt-in annotation 을 요구할 것.

| ID | 추천 결정안 | 이유 | 구현 체크 |
|---|---|---|---|
| **G20** | 함수값 타입이 파라미터 이름을 metadata 로 보존한다. 이름 일치 시 키워드 호출을 허용하지만 타입 equality 에서는 무시한다. 기본값은 G15 대로 erase. | HOF 체이닝에서 API 이름을 유지하되, 함수값을 다른 같은-shape 타입으로 alias 하면 이름은 사라지도록 한다. 최소 변경으로 실전 체감을 올림. | `FnType` 구조에 `paramNames: List<String>?` 추가. equality 는 이름 skip. 키워드 호출 경로에서 이름 일치 체크 후 positional 로 강등. |
| **G21** | `DefaultLiteral` 을 재귀적으로 정의해서 fields 가 literal 인 struct literal 과 `const fn` 호출을 포함한다. `const fn` 본문 범위는 §3.1.1 capability matrix 로 확정. | 팩토리 함수로 우회해야 했던 default 인자 케이스를 직접 작성하게 한다. "각 call site 에서 재평가" 규칙은 그대로 유지. 경계를 matrix 로 박아 확장은 정식 버전 업에서만 — 패치 내 조용한 완화로 인한 de facto dialect drift 차단. | default expr 파서에 struct literal / `const fn` 호출 허용. checker 가 `isDefaultLiteral` 을 재귀 정의. `const fn` 본문은 §3.1.1 matrix 검사 (`E0766`), call graph 는 resolver 에서 acyclic 검증 (`E0767`), 제네릭 `const fn` 은 parser/checker 에서 거부 (`E0768`). |
| **G22** | `loop { ... }` 은 값 반환. `for cond { }` 는 Unit. | `for cond` 에 break-value 를 얹으면 `for` / `loop` 역할이 섞여 사용자가 혼동한다. 두 형태를 분리. | parser: `LoopExpr`. checker: `break value` 의 타입 수집 → 루프 타입. LLVM: SSA result via phi. |
| **G23** | 마지막 인자가 함수 타입일 때 trailing closure 허용. 괄호와 공존. | Kotlin/Swift 패턴으로 검증됨. `taskGroup` / `testing.context` / `List.map` 같은 DSL 체이닝이 시각적으로 깔끔해진다. | parser disambiguation: callee 직후 같은 줄에 `\|ident` 가 오면 closure 인자로 해석. `\|` 다음 ident 없으면 block expression fall-through. 포매터는 `foo(args) { body }` 류 선호 레이아웃 제공. |
| **G24** | `'label:` prefix + `break/continue 'label`. 라벨 없으면 innermost. | sentinel flag 패턴 제거. 라벨 범위는 lexical 하므로 resolver 부담 낮음. | lexer: `'` prefix → label token. parser: loop prefix label. resolver: label stack. `break 'undefined` → `E0763`. |
| **G25** | `by` contextual keyword — range expression 문맥에서만. step = 0 은 abort. | 역방향 + 스텝 표현을 자연스럽게. 기존 식별자 `by` 쓰는 프로그램 호환 위해 contextual. | parser: `..` / `..=` 다음에 `by expr` optional. checker: step 타입 = element 타입. 런타임: step == 0 abort. |
| **G26** | receiver 식별자 + struct literal = spread shorthand. 비-ident 는 기존 `..expr` 만. | 가장 흔한 패턴 (같은 변수 갱신) 에서 `..self` 잉크 제거. Ident 한정이라 파서 구현 단순. | parser: `Ident '{' FieldInit ... '}'` 에서 Ident 가 해당 scope 의 struct 변수이면 shorthand. resolver 가 타입 look up 후 desugar. |
| **G27** | `as?` single 토큰. `Error` downcast 한정. | `.downcast::<T>()` 가 자주 쓰이는 곳에서 타이핑 절약. 임의 타입 변환용으로 확장하지 않음 → `as` 키워드 금지 정책 유지. | lexer: `as?` 단일 토큰. parser: postfix. checker: 피연산자 타입이 `Error` 구현체인지 확인; 아니면 `E0727`. MIR: `downcast` intrinsic 과 동일 lowering. |
| **G28** | `use path::{a, b as c}`. 기존 `use path as alias` 와 공존. | 명시 import 로 scope 오염을 줄이고, rename 조합도 같은 구문에 포함. | parser: UseItem = `Path ('as' Ident)?`. resolver: UseItem 각각이 바인딩 생성. 중복은 `E0554`. |
| **G29** | `#[cfg(...)]` pre-resolve 필터. Key 4 종, 조합 `all/any/not`. | 플랫폼 분기를 언어 단에서 지원해야 stdlib 자체 (std.os 등) 가 작성 가능. Key 화이트리스트로 scope 확산 방지. | resolver: cfg 평가는 file 단위 scan 으로 resolve 전 수행. 제외된 선언은 AST 에서 제거. 미인식 key `E0405`. cfg 조합은 literal DSL. |
| **G30** | `pub use` 는 원본 가시성과 공개 계약을 re-export 위치로 상속. 순환은 `E0552`, private 노출은 `E0553`, duplicate export/local name 은 `E0554`. | facade 패턴 기본 요구사항. public surface 는 한 이름에 한 의미만 허용한다. | resolver: re-export 체인 topological sort. `pub use` target 이 private → `E0553`. self-cycle / mutual-cycle → `E0552`. duplicate export name → `E0554`. |
| **G31** | discriminant 할당은 payload-free variant 만. 타입은 네 정수 타입만. | FFI / 프로토콜 상수 매핑에서 자주 쓰이는 패턴을 선언 안으로 가져옴. payload variant 에도 허용하면 C-enum 과 semantics 가 꼬임. | parser: `EnumDecl` 확장. checker: 각 variant payload 여부 확인 후 할당 허용/금지. auto-derive `.discriminant()` / `.fromDiscriminant(n)`. LLVM: explicit tag 저장. |
| **G32** | `#[test]` inline + ` ```osty ``` ` doctest. `_test.osty` 파일은 계속 유효. | 작은 유틸 테스트의 진입 장벽 제거. Doctest 는 문서가 곧 테스트 보장. | checker: `#[test] fn` 표식 → 테스트 컬렉션 등록, 프로덕션 exclude. doctest extractor: `///` fence 파싱 → implicit test fn 생성. `osty test --doc` 플래그 추가. |
| **G33** | `std.testing.gen` 서브모듈 + `testing.property` 러너. 기본 타입 shrinker 포함. | Fuzz 전 단계에서 invariant 검증을 싸게 돌릴 수 있는 경로. stdlib 에 표준 reducer / generator 를 박아 사용자 프로젝트 간 일관성 확보. | `std.testing.gen` 신설. `Gen<T>` 인터페이스 + 기본 조합자. shrinker 는 `T: Shrinkable` interface 로 opt-in. `property` 러너는 100 회 기본, seed CLI 노출. |
| **G34** | 암묵 widening / float-promotion graph + narrowing 접미사 강제. 암묵 narrowing 은 `E0765` + `.toXxx()` 힌트. | 수식 가독성은 유지하되 `Int → Float64` 를 lossless 로 부르지 않는다. 방향성 규칙이 고정 graph 이므로 사용자 예측 가능. | checker: 이항 연산 이전에 `commonWiden(lhs, rhs)` 삽입. `applyLiteralAdapt` 는 여전히 먼저 시도 후 fallback. MIR: widening 을 `sext` / `zext` / `sitofp` / `fpext` 로 lowering. |
| **G35** | `#[op(...)]` 6 연산자 opt-in. 허용되지 않은 연산자 시도는 `E0725`. | 수학 코드 가독성 회복 + iostream-style 오남용 차단. 6개는 finite 하고 우선순위 / 결합성이 primitive 와 동일. | parser: `#[op(+)]` 등 annotation value 화이트리스트. checker: 이항 / 단항 연산 디스패치 훅. 우선순위 테이블은 primitive 와 공유. 중복 구현 `E0724`, 시그니처 오류 `E0723`, 비허용 연산자 `E0725`. |

### Closed during v0.5 prep

| 항목 | 해결 위치 | 비고 |
|---|---|---|
| E07xx 진단 네임스페이스 정리 | `internal/diag/codes.go`, `toolchain/check_diag.osty` | Go / osty 양쪽 drift 된 E07xx 를 codes.go 기준으로 canonical 화. 9 개 Osty-native 개념에 신규 코드 (E0744-E0749 / E0751-E0753) 배정. |
| G14 generic callable reference 구현 | `toolchain/elab.osty` | 제네릭 최상위 fn / 제네릭 메서드를 값으로 꺼내는 걸 `E0742` 로 거부. turbofish 값 참조 경로 (`let g = f::<Int>`) 도 동일하게 막음. |
| Struct spread 구현 | `toolchain/elab.osty` | `P { ..p, x: 5 }` 가 `..p` 가 제공하는 필드를 `missing-field` 로 잘못 잡던 버그 수정. spread operand 를 owner type hint 로 elaborate 후 필드 이름 수집. |
| G15 behavior lock | `toolchain/elab.osty`, tests | 함수값 호출이 default / keyword metadata 없이 positional + exact arity 만 받음을 3 개 전용 테스트로 pin. |
| List.take / drop 추가 | `internal/stdlib/modules/collections.osty` | toolchain 코드 슬라이싱 관용구를 stdlib 에 노출. |
| testing.assertTrue / assertFalse | `internal/stdlib/modules/testing.osty`, `internal/llvmgen/stmt.go` | Boolean assertion API 완성. LLVM 하네스가 assertTrue / assertFalse 를 intercept. |
| std.strings chapter 구현 | `internal/stdlib/modules/strings.osty` | Unicode grapheme 테이블 + trim / split / replace / toUpper / toLower / hasPrefix / hasSuffix 전 함수 구현. |
| Option / Result full combinator surface | `internal/stdlib/modules/option.osty`, `result.osty` | `isSome` / `andThen` / `mapErr` / `orElse` 등 26 개 + 24 개 메서드 서명 체크. |

### Not Language-Decision Gaps

아래는 검색에서 나온 `TODO` / `future` 항목이지만, 언어 의미론의
미정사항으로 취급하지 않는다. 필요하면 별도 implementation backlog 로
관리하고, `SPEC_GAPS.md` 의 G 번호를 부여하지 않는다.

| 항목 | 분류 | 처리 |
|---|---|---|
| generic type parameter defaults (`struct Pair<T, U = T>`) | excluded feature | v0.5 에서 제외. 재오픈하려면 정식 버전에서 설계 제안 필요. |
| Historical Go transpiler TODO audit / unsupported lowering forms | backend archive | 언어 스펙 미정이 아니라 retired backend parity 문맥이다. `LLVM_GEN_TODO_AUDIT.md` 는 역사 기록으로만 유지한다. |
| IR 가 아직 gen 의 입력이 아님 | backend architecture | `internal/ir/doc.go` 의 follow-on work. 언어 결정 아님. |
| package-manager SAT solver, multi-binary manifests, build artifact convention | tooling / product backlog | manifest / pkgmgr / build 문서나 이슈로 관리. 언어 의미론 아님. |
| LSP incremental sync, richer doc / comment transport | tooling backlog | LSP / editor 품질 항목. 언어 의미론 아님. |
| `BytesSlice` / `StringView` zero-copy 뷰 | performance backlog | v0.5 excluded 로 명시. 컴파일러 최적화 (escape analysis) 로 대체. |
| Phantom type params (user-facing) | excluded feature | `Builder<T>` 내부 phantom 외 user-visible phantom 은 v0.5 excluded. |

---

## Resolved in v0.4

v0.4 의 초점은 새 문법을 크게 늘리는 것이 아니라, v0.3 에서 의미는
정했지만 구현·진단·테스트 경계가 아직 부드럽던 엣지케이스를
명시적으로 닫는 것이다. 아래 항목은 v0.4 baseline 에 포함된 최종
결정이다.

| ID | 영역 | 필요한 결정 | 상태 |
|---|---|---|---|
| **G13** | `Handle<T>` / `TaskGroup` escape | `taskGroup` 밖으로 handle/group capability 가 빠져나가는 경우를 finite front-end rule 로 금지한다. 반환, 필드 저장, channel send, escaping closure capture, collection 저장 후 group 밖 사용은 `E0743` non-escaping capability violation 으로 본다. | decided |
| **G14** | generic method turbofish + method refs | `obj.method::<T>(args)` 의 explicit type args 는 method-local generics 만 가리킨다. owner generics 는 receiver 타입에서 결정된다. partial explicit 은 금지. generic method 를 `obj.method` 함수값으로 빼는 것은 금지하고 wrapper closure 를 사용한다. | decided |
| **G15** | callable arity after erasure | 선언 함수의 default/keyword metadata 는 `fn(...) -> ...` 값으로 흐르지 않는다. function value 호출은 positional-only, exact arity 이며 too-few/too-many 모두 `E0701`. | decided |
| **G16** | closure parameter patterns | closure parameter 는 `LetPattern (':' Type)?` 이되 irrefutable pattern 만 허용한다. ident, wildcard, tuple, struct, nested irrefutable 조합은 허용하고 literal/range/variant/or pattern 은 `E0741`. | decided |
| **G17** | nested pattern witness diagnostics | exhaustiveness witness 는 한 개의 최소 missing pattern 만 출력한다. tuple/struct 는 좌측부터 첫 missing component 를 구체화하고 나머지는 `_`; closed enum/Option/Result payload 는 재귀 witness, 열린 타입은 `_`; guard arm 은 coverage 에 기여하지 않는다. | decided |
| **G18** | stdlib protocol executable stubs | §10/§15/§16/§17 protocol 은 checked signature stubs 우선으로 관리한다. bodies 는 dummy 여도 parse/resolve/check 되는 `.osty` stub 이어야 하며, runtime/gen parity 는 language gap 이 아니라 implementation backlog 로 분리한다. | decided |
| **G19** | runtime sublanguage capability surface | C 로 작성된 GC/allocator (`internal/backend/runtime/osty_runtime.c`) 를 Osty 로 셀프호스트할 수 있도록, **package-gated** 한 작은 surface 를 v0.4 에 additive minor 로 더한다. 사용자 prelude/문법은 변경 없음. opaque type `RawPtr` + marker trait `Pod` + 6 개 어노테이션 (`#[intrinsic]`, `#[pod]`, `#[repr(c)]`, `#[export("...")]`, `#[c_abi]`, `#[no_alloc]`) + `std.runtime.raw.*` 13 개 intrinsic (null/fromBits/bits/alloc/free/zero/copy/offset/read/write/cas/sizeOf/alignOf). 진단 `E0770` / `E0771` / `E0772` 추가. safepoint ABI 는 §19.10 에 명시 (compiler-emitted root array). 구현 진행 상황은 LANG_SPEC §19.11 (Implementation Status) 표가 권위적이며, 새 piece 가 land 할 때마다 같은 PR 에서 표를 갱신한다. | decided |

---

## Resolved in v0.3

| 갭 | 해결 위치 | 비고 |
|---|---|---|
| **G4** 클로저 파라미터 패턴 | `04-expressions.md` §4.7 | `LetPattern` 확장, irrefutable 제한. v0.4 에서 parser/checker 구현 parity 완료. |
| **G8** 채널 close semantics | `08-concurrency.md` §8.5 | 누구든 close, 두 번째 abort. drain 후 `None`. |
| **G9** `Builder<T>` phantom | `03-declarations.md` §3.4 | 완전 추상화. 에러 메시지에 누락 필드 명시. |
| **G10** Char / surrogate | `02-type-system.md` §2.1, `10-standard-library/05-standard-numeric-methods.md` §10.5 | surrogate 불허, `Char.fromInt` 안전 변환. |
| **G11** Generic 컴파일 모델 | `02-type-system.md` §2.7.3 | Monomorphization. Interface 값은 fat pointer vtable. |
| **G12** Cancellation 전파 | `08-concurrency.md` §8.4 | Task-group 자동 전파. `Cancelled(cause: Error)`. stdlib 전체 cancel-aware. |

v0.3 는 추가로 v0.2 전수 감사에서 나온 91건의 세부 모호성을 모두
결정. 주요 항목은 v0.3 change-history §18.1 참조.

---

## Resolved in v0.2

| 갭 | 해결 위치 | 비고 |
|---|---|---|
| **G1** 컬렉션 `Equal`/`Hashable` 자동 구현 조건 | `02-type-system.md` §2.6.5 | Built-in instances 표 + 컬렉션 derivation 규칙 |
| **G2** Duration toString, log 표현 | `10-standard-library/20-time-extensions.md`, `10-standard-library/10-logging.md` | `Duration.toString()` 추가, `LogValue::Duration` 명시 |
| **G3** `LogValue` 합 타입 정의 | `10-standard-library/10-logging.md` §10.10 | Concrete enum + `ToLogValue` trait + 자동 promotion |
| **G5** 스크립트 `return` 의미 | `06-scripts.md` §6 | 암묵 main 의 return 으로 정의 |
| **G6** Partial struct 어노테이션 | `03-declarations.md` §3.4 | 각 선언 독립 적용, 합성 없음 |
| **G7** 진단 메시지 템플릿 | `03-declarations.md` §3.1 | positional-after-keyword 에러 박스 추가 |
