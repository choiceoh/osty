# Osty v0.6 — 문법 / 의미론 리뷰

- **Scope**: `LANG_SPEC_v0.6/` 22 chapter + `OSTY_GRAMMAR_v0.6.md` (delta) + `OSTY_GRAMMAR_v0.5.md` (baseline) + `00-revision.md` + `SPEC_GAPS.md` + `CHANGELOG_v0.6.md` + `BREAKING_v0.6.md` + `MIGRATING_v0.5_to_v0.6.md` + `ABRIDGED.md` + `CLAUDE.md` 부록 C.
- **Method**: 7 영역 (lexical/grammar, type system, declarations+expressions, capabilities+IFC, concurrency+memory+errors, modules/scripts/FFI/tooling/testing, cross-doc consistency) 을 병렬 리뷰 에이전트로 분담. 각 에이전트의 발견을 본 문서에 통합.
- **Status**: 리뷰만. 어떤 spec 파일도 본 PR 에서는 변경하지 않는다. Blocker / minor / nit 분류는 발견자의 의견. action 은 follow-up PR 로 추적.

> **결론 요약**: v0.6 spec 본문은 의도와 디자인이 일관 되지만, *측정 / 카운팅 / 절차 기술* 의 표면에서 cross-document drift 가 있고, *concurrency 메모리 모델* / *capability 의 nominal-vs-structural 분리* / *sealed/error_contract/golden 의 외부-관찰 가능 boundary* / *parameter annotation grammar 와 site matrix 의 권한 차이* 가 부분 미명세. 18 blocker / 41 minor / 11 nit (총 70 발견).

---

## 0. 우선순위 — Blocker 18 건

(번호는 본 문서 §1–§7 에서 재사용)

| # | 영역 | 1-line 요약 |
|---|---|---|
| 1.1 | lexical | reserved keyword 카운트 (17 vs 18 vs 19) 가 3 곳에서 disagree |
| 1.4 | lexical | `spec` keyword promotion — closure body / if-arm / match-arm 어디까지 "함수 본문 첫 statement" 인지 미정 |
| 1.11 | lexical | `LABEL` (`'IDENT`) vs char literal `'X'` lexer disambiguation 룰 v0.6 chapter 에 부재 |
| 2.1 | type | `Int → Float64` lossless widening 주장이 사실 아님 (Int=64-bit, Float64 mantissa=53-bit) |
| 2.4 | type | flow tag 가 type identity 인지 아닌지 두 곳이 contradict (§2.12.2 vs §2.12.6) |
| 2.6 | type | Float `Ordered` totality 와 `Equal` NaN reflexivity 가 super-trait 계약 깨짐 |
| 3.3 | decl | `#[sealed_construct(IDENT)]` 의 named method 가 없거나 wrong-shape 일 때 진단 미정 |
| 3.4 | decl | `#[error_contract]` 의 caller ⊇ callee subset 정의 + diagnostic 코드 부재 |
| 4.1 | cap | capability satisfaction 이 nominal 인지 structural 인지 미정 (`MyClock: Clock` 등가성) |
| 4.5 | cap | `--legacy-globals` desugar 의 instance lifetime 미정 (per-call / module-singleton / process-singleton) |
| 4.11 | ifc | sink list extensibility — `E0903` 정의 vs §21.10.4 의 user 정의 sink 허용 contradict |
| 5.2 | err | `#[error_contract(any)]` 의 호출 boundary 동작 미명세 |
| 5.4 | conc | defer × cancel × panic 복합 interaction matrix 미정 (panic-during-defer, multi-defer LIFO + cancel 도착 등) |
| 5.5 | conc | `Handle<T>` non-escape (G13) — closure 가 capture 만 하고 미escape 일 때 룰 syntactic vs flow-sensitive 미정 |
| 5.6 | conc | channel close race — 두 close 동시 호출, blocked recv wakeup 시점 미정 |
| 5.9 | mem | §9 메모리 모델 (happens-before, atomic, data race) 전혀 미명세 |
| 5.11 | io | §16.1 `fs.create(dst)` 가 Writer 반환 (예시) vs §20.9.4 `Fs.create` 가 `Result<(), FsError>` (factory) — 두 normative chapter disagree |
| 6.1 | mod | `pub use` × `#[stability]` 상속 / override 룰 미정 |
| 6.2 | mod | `pub use` shadowing 룰 (자기 패키지 동명 심볼 vs 다른 `pub use`) 미정 |
| 6.3 | mod | `#[cfg(...)]` 가 §5 prose 에서 referenced 인데 v0.6 본문 어디에도 spec 없음 (G29 v0.5 결정만 존재) |
| 6.4 | scripts | script 의 ambient list 에서 빠진 capability 사용 시 진단 미정 |
| 6.8 | ffi | `#[trusted_declassify]` typing 룰 (어떤 tag 가 strip 되는지) 본문 부재 |
| 6.9 | tool | `#[golden]` `text` mode 의 byte-canonicalization (LF/CRLF, BOM, trailing newline) 미정 — CI flake 위험 |
| 6.11 | tool | `#[example(output = ...)]` 의 actual ↔ expected equality (`Equal` / 구조 deep / `ToString` round-trip) 미정 |
| 6.12 | tool | `#[fixture(name = "...")]` namespace (package vs workspace) 미정 |
| 6.14 | tool | `osty publish` diff — parameter rename 과 default value change 의 분류 missing |
| 6.16 | test | §11 parallel-by-default test isolation contract (filesystem / env / cwd / globals) 부재 |
| 7.1 | xdoc | §13.x 챕터 번호 drift — `osty context` 가 §13.4 (chapter) vs §13.6 (모든 다른 doc) |

---

## 1. Lexical / Grammar (EBNF) 영역

### 1.1 Reserved keyword 카운트 disagree (blocker)
- `01-lexical-structure.md §1.2 / §1.10.1`: 18 reserved
- `OSTY_GRAMMAR_v0.6.md §"Grammar 규모 변화"`: v0.5=18 → v0.6=**19** (+`while`)
- `OSTY_GRAMMAR_v0.5.md §R7`: v0.5 reserved=17
- → 세 곳에서 baseline (17 or 18) 자체가 다름.
- **Action**: 카운트 1 군데로 동기화.

### 1.2 `#[requires]` 가 v0.5-reuse 와 v0.6-new 두 카테고리에 동시 등장 (minor)
- `01 §1.10.3`: "v0.5 의 11 fixed annotation 위에 v0.6 은 20 신규" 로 카운트하지만 `#[requires]` 가 v0.5 reuse 로 묘사됨.
- `OSTY_GRAMMAR_v0.5.md §R26`: `#[requires]` 미수록.
- `00-revision.md §5`: 20 신규에 `#[requires]` 포함.
- **Action**: `#[requires]` 를 v0.6-new 로 통일.

### 1.3 Tuple 요소 annotation 룰 vs `ParamDecl` 확장 (minor)
- `OSTY_GRAMMAR_v0.6.md §R29`: "Tuple 요소에는 annotation **불허**".
- 그러나 `ParamDecl ::= Annotation* Pattern : Annotation* Type` 의 `Pattern` 이 `(a, b): (Int, Int)` 형태인 경우 안쪽 element 위치는 syntactic 으로 reachable. closure parameter (`ClosureParam ::= LetPattern`) 도 동일.
- **Action**: nested `LetPattern` 안에서 `Annotation*` 명시 금지 production 추가, 또는 R29 의 blanket 룰 완화.

### 1.4 `spec` keyword 의 promotion 컨텍스트 미정 (blocker)
- `OSTY_GRAMMAR_v0.6.md §R28`: "함수 본문 첫 statement 위치" — 그러나 `Block` production 이 `FnDecl`/`MethodDecl` body 와 closure body / `if`-arm / `match`-arm 에 공유됨. 어디까지 "함수 본문" 인지 grammar 가 침묵.
- **Action**: R28 을 `FnDecl`/`MethodDecl` body 로 한정. closure / if-arm / match-arm 의 첫 statement 에서는 `spec` 이 식별자.

### 1.5 Spec block clause 분리자 — ASI vs 메서드 체이닝 (minor)
- `LineEnd ::= NEWLINE | (다음 SpecClause 시작 token 직전 — ASI 적용)`.
- `example: foo()\n.bar()\nexample: ...` 에서 `.bar()` 가 이전 clause 의 method chain 인지 다음 clause 의 시작인지 모호 (§1.8 leading-`.` 룰이 우선).
- **Action**: clause-start position (line-start 의 `example` / `law` / `invariant` / `forall`) 가 무조건 이전 clause 를 종료한다고 명시.

### 1.6 Spec contextual keyword scoping — clause-start position only (minor)
- `example: foo(example)` 같은 식에서 `example` 식별자 사용이 막혔는지 모호. 명시적인 "clause-start position 이외에는 식별자" 룰 부재.
- **Action**: lookahead 룰을 § R28 / §1.10.5 에 추가.

### 1.7 `SpecClause+` 하한 / 빈 spec / nested spec 미정 (minor)
- 빈 `spec { }` ? trailing comma ? nested `spec { spec { } }` ? — production silent.
- **Action**: empty spec 거부 + nested 거부 명시.

### 1.8 `ForallClause` 의 multi-binding generator 형태 (nit, Phase 5 deferred)
- `'forall' Ident (',' Ident)* 'in' Expr ':' Expr` — 다중 ident 가 tuple-`Gen<(A,B)>` 인지 두 generator 인지 미정.
- **Action**: Phase 5 land 시 함께 결정. 지금은 SPEC_GAPS 신규 entry.

### 1.9 `SealedAnno` 인자 required 여부 (nit)
- `'#[sealed_construct' '(' IDENT ')' ']'` — required positional. 그러나 default ("이름 미지정 시 `parse` 사용") 가능성을 spec 이 거부 / 허용 어느 쪽도 명시 안 함.
- **Action**: argument required 명시 (현재 EBNF 와 일치).

### 1.10 `as?` 의 whitespace 룰 v0.6 chapter 에 부재 (minor)
- `OSTY_GRAMMAR_v0.5.md`: "`as?` single token, whitespace 없이"
- `OSTY_GRAMMAR_v0.6.md` "새 lexer 토큰 0 개" + `01-lexical-structure.md §1.7` 모두 `as?` 룰 재서술 안 함.
- **Action**: §1.7 PUNCT 표 또는 §1.10 에 cross-reference.

### 1.11 `LABEL` (`'IDENT`) vs `CHAR_LIT` (`'X'`) lexer disambiguation 부재 (blocker)
- v0.5 `LABEL ::= "'" IDENT` + `CHAR_LIT ::= "'" CharBody "'"`. lexer 가 첫 `'` 다음에 closing `'` (char) vs IDENT (label) 를 어떻게 결정하는지 룰 없음. context-free 룰만으로는 결정 불가능.
- **Action**: `'X'` (한 글자 + closing `'`) 만 char, `'name` (IDENT 시작 + 무 closing `'`) 가 LABEL — 명시적 lookahead 룰 작성.

### 1.12 ASI suppression 표에서 `as?` 누락 (minor)
- `expr as? \n Type` 시 newline 이 statement 종료. `as?` 는 우측 type 을 요구하므로 항상 suppress 해야 함.
- **Action**: §1.8 의 "preceding token" 표에 `as?` 추가.

### 1.13 mid-file `#!` 처리 미정 (nit)
- §1.1: shebang 은 byte 0 에서만. `#` 다음 `[` 가 아니면 lex 에러로 가능 — 그러나 진단 코드 미할당.
- **Action**: `E000?` 코드 신규 할당 또는 `E0001` 일반화.

### 1.14 R-rule 인덱스 vs 본문 cross-reference 일관성 (minor)
- `OSTY_GRAMMAR_v0.6.md` 본문 EBNF subsection header 가 G37 / G40 등 *gap* 번호로 되어 있어, R29 가 실제로 governing 인 production 도 R29 라는 걸 외부 reader 가 추적하기 어려움.
- **Action**: subsection header 에 "(R29)" 보조 표시.

### 1.15 `E0405` 두 번 정의 (nit)
- v0.5 grammar: "Conditional compilation cfg" 룰 변경에서 이미 사용.
- v0.6 grammar: "annotation site invalid" 으로 재할당.
- **Action**: 한 의미만 유지하고 다른 의미는 신규 코드 (`E0406` 등) 로 분리.

### 1.16 `00-revision §7.6` 매트릭스 vs `ParamDecl` `Annotation*` 권한 (minor)
- 매트릭스: parameter 위치 ✓ 는 `#[taint]` / `#[requires]` 만.
- 그러나 grammar 는 *모든* annotation 을 syntactic 으로 허용.
- **Action**: grammar = syntactic surface, matrix = checker enforcement (E0405) 라는 위계 명시.

### 1.17 매트릭스가 v0.5 R26 annotation 을 제외함 (minor)
- §7.6 가 "v0.6 신규" annotation 만 enumerate. `01 §1.10.3` 은 매트릭스가 *모든* 어노테이션의 권위라고 주장 — 두 곳 disagree.
- **Action**: 매트릭스 확장 또는 §1.10.3 wording 변경.

### 1.18 `#[fixture]` 의 zero-arg / explicit-return-type 룰 (nit)
- `OSTY_GRAMMAR §G42`: "함수에만 적용 가능, 인자 0 개, 반환 type 명시 필수".
- 매트릭스 / EBNF 어디에도 enforcement 룰 미수록 — 체커 책임으로 떨어짐.
- **Action**: `E04xx` 코드 할당 + 매트릭스에 explicit 룰 표기.

---

## 2. Type System (§2, §2a, §14, §15, §17)

### 2.1 `Int → Float64` 가 lossless widening 이라는 주장 (blocker)
- `Int` = 64-bit signed (§2.1). `Float64` = IEEE-754 double (mantissa 53-bit). |x| > 2^53 인 Int 는 정밀도 손실.
- v0.6 §2.2 lossless lattice 주장과 모순.
- **Action**: lattice 에서 `Int → Float64` 제거 (`.toFloat()` 강제) 또는 "float widening 은 precision-tolerant" 카르브아웃 명시.

### 2.2 Top-level `let x = 5` defaulting 순서 미정 (minor)
- `02a §2a.6` rule 3: "enclosing statement boundary" 에서 default. 그러나 *나중* 사용 사이트 (`f(x: Float64)`) 가 retroactive pin 가능한지 §2a.11 가 부분 답.
- **Action**: 한 줄 룰 추가 — "RHS hint 가 statement 안에 있으면 hint 우선, 없으면 statement 경계에서 default 즉시 적용".

### 2.3 Synth ↔ check 충돌 시 진단 emitter 미정 (minor)
- `§2a.3` mode-switch 사이트마다 unifier 가 emit 하는지 caller 가 emit 하는지 침묵.
- **Action**: 각 mode-switch 별로 진단 코드 + emitter 명시.

### 2.4 Flow tag 가 type identity 인지 (blocker)
- `§2.12.2`: tag 는 *value-level*, type identity 영향 없음.
- `§2.12.6`: `id<String@{user_input}>` 와 `id<String@{}>` 가 distinct monomorphization.
- 두 룰 contradict.
- **Action**: 한 모델로 결정. (권장: tag erasure at monomorphization, output tag 는 `T-Combine` 으로 호출 사이트마다 계산. §4.9 와 일관).

### 2.5 함수 type / `Result<T, E>` variance 미명세 (minor)
- §2.7.3 invariance 룰이 collection 만 cover. 함수 type 의 argument / result variance, `Result` 의 T/E variance 침묵.
- **Action**: 한 단락 추가 — function type invariant in arg+result, `Result` invariant in T+E.

### 2.6 Float `Ordered` totality vs `Equal` NaN reflexivity (blocker)
- §2.6.5: Float Ordered 는 IEEE-754 *total* ordering (NaN > all).
- §2.9: `NaN.eq(NaN) == false`.
- → `lt(NaN, NaN) = false`, `eq(NaN, NaN) = false` → `le` default `lt || eq = false`. NaN 이 자기 자신 ≤ 도 아님 → super-trait 계약 (Ordered: Equal) 위반.
- **Action**: Float 의 `Ordered` 는 `eq(NaN, NaN) = true` 로 redefine (primitive `==` 와 분리), 또는 Float 를 `Ordered` 에서 빼고 `std.float.totalOrder` 분리.

### 2.7 cyclic struct 의 auto-derived `toString` / `Equal` 발산 (minor)
- 자기 참조 그래프에서 무한 재귀.
- **Action**: cycle detection + depth bound 명시 또는 "auto-derive 는 cycle 에서 UB / abort" 명시.

### 2.8 Iteration protocol 의 `Iterator<Option<U>>` ambiguity (minor)
- `Some(None)` 가 element 인지 end-of-iter 인지 desugar 가 침묵.
- **Action**: 룰 명시 — `None` 만이 terminator. value 는 항상 `Some(...)` (element 가 자체 Option 이어도 `Some(None)`).

### 2.9 Capability 가 structural interface — nominal 보호 부재 (minor)
- §14.1 "구조적 인터페이스" 룰에 따라 모르는 user struct `FakeFs { fn read(...) ... }` 가 자동으로 `Fs` capability 로 사용 가능. nominal tag 부재.
- **Action**: §20.x 에서 capability satisfaction 의 grading rule (§20.6) 이 nominal `std.capability.Clock` 심볼에만 attach 한다는 점 명시. (issue 4.1 와 페어).

### 2.10 `E0765` numeric narrowing — both-sides-literal 룰 (minor)
- `let x: Int8 = 100 + 50` — 두 literal 각각 Int8 표현 가능, 합은 overflow. constant-fold 후 fit-check 인지 default 후 narrowing 인지 미정.
- **Action**: §2.2 단락 추가 — "RHS 가 constant expression 이고 hint 가 narrower type 이면 fold 후 fit-check; fold 결과가 hint type 에 안 들어가면 E0765".

### 2.11 `Equal` × `Hashable` 일관성 invariant 미명세 (nit)
- 두 trait 별도 구현 시 `eq(a,b) ⇒ hash(a) == hash(b)` 보장 의무 어디에도 없음.
- **Action**: §2.6.5 에 한 문장 — "user 정의 시 일관성은 프로그래머 책임. 위반은 Map/Set 에서 UB".

### 2.12 함수 값 coercion 시 tag erasure (minor)
- §2.12.5: "tag 는 함수 값에 *preserved*"
- G15: default/keyword metadata 는 erase.
- 두 룰의 교집합에서 "tag 는 보존, 메타데이터만 erase" 가 explicit 인지 모호.
- **Action**: 한 문장 — "function-value coercion erases default/keyword metadata but preserves parameter and result flow tags".

---

## 3. Declarations + Expressions (§3, §4)

### 3.1 `spec { }` 만 있는 함수 본문의 return type 미정 (minor)
- spec block 이 expression 아님. 함수 본문이 spec block 만이면 `Unit` ? `()` ? missing-return ?
- **Action**: "함수 본문이 spec block 만이고 declared return = `()` 이면 OK; 아니면 missing-return 진단".

### 3.2 `while` ≡ `for cond` — 라벨 / loop-vs-while 드리프트 (minor)
- §4.4.1 가 `loop {}` 와 `for cond {}` 만 distinguishe — `while` 명시 안 함. labeled `'l: while cond` 가 G24 라벨 룰을 따르는지 명시 안 됨.
- **Action**: §4.4 한 줄 — "`while` 은 `for cond` 와 동일한 lowering. 라벨 / break-value (불허) / continue 모두 동일".

### 3.3 `#[sealed_construct(IDENT)]` 의 named method missing-handling (blocker)
- IDENT 가 (a) 존재 안 함 / (b) wrong return shape / (c) private / (d) instance vs associated 일 때 진단 미정.
- spread `{ ..x }` 거부가 parse / resolve / check 어디에서 일어나는지 미정.
- **Action**: `E0423` 신규 — "sealed_construct named method must be associated function returning `Self` or `Self?` on the same type". spread 거부는 resolve 단계.

### 3.4 `#[error_contract]` propagation subset rule (blocker)
- 호출자 contract ⊇ 피호출자 contract — "include" 의미 (variant name 일치? enum 동일? 의미 동일?) 미정.
- 진단 코드 (`E0413` 등) 미할당.
- 이종 error enum 합성 시 "closed union" 형식만 허용된다는 룰 explicit 부재.
- **Action**: `(EnumName, VariantName)` 동등성으로 정의, `E0413` 할당, closed union 룰 §7.5 에 추가.

### 3.5 `#[ambient]` entry-point 정의가 §3.1 외부에만 존재 (minor)
- 권위 list (`fn main` / script / `#[test]` / `#[bench]`) 가 `00-revision.md` 에만 있음.
- 사용자 정의 capability name 의 ambient 등록 여부 침묵.
- **Action**: §3.1 에 entry-point 정의 mirror + "AmbientArg list 는 7 canonical 만, custom interface 는 ordinary parameter 로만 전달" 명시.

### 3.6 Defaulted parameter × parameter annotation × tag (minor)
- R29 가 `#[taint]` 등을 parameter 에 허용. default literal 자체가 source tag 를 inherit ? `#[requires]` 가 default 를 검증 ?
- **Action**: 한 문장 — "default literal 은 항상 `#[requires(_)]` 를 만족 (compiler-supplied), 빈 tag set 을 carry".

### 3.7 sealed type × pattern destructure (minor)
- `match e { Email { local, .. } -> ... }` 가 외부에서 합법한지 침묵.
- → 일반 field-visibility 룰 따른다 (private field 는 외부 패턴 불가) 가 default 답이지만 명시 안 됨.
- **Action**: §3.4.5 한 줄 명시.

### 3.8 `spec { law: invariant: }` v0 단계 typecheck 의미 (minor)
- 표 "v0: doc-only" vs `E0442` 존재 (boolean 으로 평가 안 됨).
- `Unit`-반환 함수에서 `result == X` 가 trivially `() == X` — type 안 맞으면 거부.
- **Action**: "v0 단계: parse + typecheck (실행 안 함). `result` 는 declared return type. Unit-반환 함수에 law/invariant 면 `W0442` lint".

### 3.9 `defer` × `if`/`match` arm expression scope (nit)
- arm 이 bare expression 일 때 defer 는 syntactic 으로 불가 (statement 만). block arm 이면 arm-block 끝에서 실행.
- expression-context 로 yield 되기 전 / 후 시점 명시 안 됨.
- **Action**: §4.12 한 문장 — "block arm 의 defer 는 block exit 시점, value yield 직전".

---

## 4. Capabilities + Information Flow (§20, §21)

### 4.1 Capability satisfaction nominal vs structural (blocker)
- §20.2 / §20.5 가 `pub interface Clock` 정의 + structural interface 룰 (§14) 인용.
- §20.6 의 determinism grade 표가 7 canonical name 에 hardcoded → nominal.
- 두 view 충돌 — user `struct FakeClock { fn now() }` 이 `Clock` parameter 자리에 들어갈 수 있는지 미정.
- **Action**: §20.6 첫 단락 — "determinism grade 는 *canonical* `std.capability.Clock` 심볼에 nominal 하게 attach. user 타입은 structural 일치하더라도 default `non-deterministic` grade. `#[reproducible_capability]` marker 가 grade 를 promote".

### 4.2 `#[ambient]` non-transitive — example 가 명시되지 않음 (nit)
- 룰은 명확 (transitive 아님) — direct callee 의 explicit param 만 매칭. 그러나 reader 가 오해할 수 있음.
- **Action**: §20.3.2 에 "`main` 이 `#[ambient(clock)]`, `lib()` 호출, `lib()` 가 `inner(rng: Rng)` 호출 — `rng` 가 inject 안 됨" 예 추가.

### 4.3 `#[reproducible_capability]` obligation 방향 (minor)
- spec 이 *interface* 선언에 검증 룰 (E0783) 명시. 그러나 *implementer* 측 의무 (각 메서드가 자동으로 `#[reproducible(scope="target")]`) 가 전부 explicit 아님.
- **Action**: §20.5 에 한 문장.

### 4.4 `#[pure]` × `Console` 명시 (nit)
- §20.4: pure 가 모든 capability 거부 (`E0785`).
- §20.9.7: `Console` 이 reproducible(scope="run") 허용 — pure 와 다름.
- 두 룰 일관 — 그러나 reader 가 contradiction 으로 오해 가능.
- **Action**: §20.9.7 에 cross-reference.

### 4.5 `--legacy-globals` 의 `time.systemClock` 등 instance lifetime (blocker)
- desugar table 이 `time.now()` → `time.systemClock.now()` 만 보여 줌.
- per-call instantiation? module-level singleton? process singleton?
- 특히 `random.host` 의 reseeding 동작 / `FakeRng` test 에서의 sequence reproducibility 에 의미 차이.
- **Action**: §20.15 한 문장 — "각 host capability 는 process-wide singleton, lazy first-use resolution, identity stable across calls".

### 4.6 Tag propagation through `match` arms — `T-Match` rule 부재 (minor)
- §21.5.3 이 `T-Const` / `T-Forward` / `T-Combine` / `T-Source` / `T-Sanitize` / `T-Sink` / `T-Struct` / `T-Container` / `T-Result` / `T-Closure` / `T-Generic-Mono` 만 cover.
- `match tainted { _ -> 1 }` 의 결과 tag 가 explicit 아님 (implicit-flow 정책상 clean — 그러나 형식 룰 부재).
- **Action**: `T-Match` / `T-If` 추가 — "arm result tag = 각 arm 의 explicit data tag 의 union. discriminant tag 는 결과에 entry 안 함 (§21.6 참조)".

### 4.7 `Option<T>`-반환 sanitizer 의 failure path tag 의미 (minor)
- §21.5.3 `T-Sanitize` 가 `T -> U` (Option 미감안).
- `parse(s) -> Email?` 에서 `None` branch 는 Email 안 carry — 실제로 moot 이지만 룰은 lift 필요.
- **Action**: `T-Sanitize` 를 `Option<U>` / `Result<U,E>` 로 일반화. failure path 는 trust manufacture 안 함.

### 4.8 Implicit flow acknowledgement — 양호 (clean)
- §21.6 / §21.16 모두 1-bit covert channel 을 out-of-scope 로 명시. Jif 인용. `#[strict_flow]` 를 v0.7+ open item 으로 등록.

### 4.9 Generic propagation — tag variable / mono key 불명확 (minor)
- `T-Generic-Mono` 가 monomorphization 결정만 언급. tag 가 mono key 일부인지 미정 (§2.4 와 페어).
- **Action**: §21.5.3 한 문장 — "tag 는 mono key 에 entry 안 함. 한 generic 인스턴스 가 tag 다른 호출 사이트 공유; 호출 사이트마다 `T-Combine` 으로 결과 tag 계산".

### 4.10 `osty audit --trusted-declassify` — 양호 (clean)

### 4.11 Sink list extensibility — `E0903` vs §21.10.4 contradict (blocker)
- §21.8: "stdlib sink **catalog**" + `E0903` "`#[requires]` uses tag outside stdlib catalog" → user sink 금지 인상.
- §21.10.4: user-defined sinks 허용.
- **Action**: `E0903` 정의를 "stdlib sink 가 canonical trust-tag 표 밖 tag 사용" 로 narrow. user code 는 자기 trust-tag 자유 선언.

### 4.12 `#[taint_field]` placement scope (minor)
- grammar `TaintFieldAnno` 가 placement 룰 침묵. enum variant / tuple component / interface method 에서의 동작 미정.
- **Action**: `TaintFieldAnno` placement 를 `StructFieldDecl` 로만 한정. 다른 위치는 §21.5 에 explicit reject.

---

## 5. Concurrency + Memory + Errors + IO (§7, §8, §9, §16, §19)

### 5.1 §7.5 heading G44 (실은 G41) (minor)
- §7 intro: "§7.5 (G41)"
- §7.5 heading: "Error Contract (G44)"
- 모든 cross-doc 은 G41.
- **Action**: heading rename.

### 5.2 `#[error_contract(any)]` 의 호출 boundary semantics (blocker)
- "documentation-only form (no check)" 라고 했지만 caller 가 이 callee 를 `?`-propagate 할 때 superset rule 이 (vacuously satisfied? always satisfied? opaque?) 적용되는지 미정.
- **Action**: `(any)` 가 caller superset rule 을 disable 한다고 explicit. documentation-only at the call boundary.

### 5.3 자동 `B → A` error 변환 부재 명시 부재 (minor)
- spec 이 concrete → `Error` upcast 만 cover. concrete → 다른 concrete 자동 변환 없음 — 그러나 explicit 안 됨.
- **Action**: §7.4 에 한 문장.

### 5.4 defer × cancel × panic interaction matrix (blocker)
- 룰 요지: "panic skip defer / `?` run defer / cancel run defer (uninterruptibly)".
- 미정: multi-defer LIFO 순서 (CLAUDE.md 만 assert), panic-during-defer (남은 defer skip? 모두 실행?), defer 중 cancel 도착.
- **Action**: §4.12 / §8.4.3 에 interaction matrix subsection 추가.

### 5.5 `Handle<T>` non-escape (G13) 룰 syntactic vs flow-sensitive (blocker)
- "closure 가 outlive 가능" 의 operational 정의 부재.
- closure 가 capture 만 하고 unused 일 때, 또는 inline-only 호출 일 때 진단 룰 미정.
- **Action**: 룰을 "Handle 을 capture 하는 closure 가 name binding / return / store / pass-to-non-escape-only-typed-param 중 하나면 `E0743`. inline-invoke 만 허용" 로 pin.

### 5.6 channel close race + blocked recv wakeup (blocker)
- "두 번째 close abort" — race 시 한 쪽 winning 인지 양쪽 abort 인지 implementation-defined 인지 미정.
- close 시 blocked recv 가 prompt wakeup 인지 next send/yield 까지 지연인지 미정.
- **Action**: "exactly one close 성공, race losers abort. blocked recv 는 close 즉시 wake up, buffer drain 후 `None` 반환".

### 5.7 `select` fairness (minor)
- "scheduler 가 non-deterministically 선택" + "registration order 로 readiness eval" — 두 문장 contradictory 표면. uniform random 인지 implementation-defined 인지 미정.
- **Action**: "implementation-defined; reference runtime = uniform random over ready set; programs MUST NOT rely on either".

### 5.8 GC 와 finalizer (clean)
- §9.2: finalizer 없음. defer / finalizer 질문 자체가 moot. 양호.

### 5.9 §9 메모리 모델 부재 (blocker)
- §9 chapter 전체가 ~50 라인. happens-before, atomic, volatile, data-race semantics 어디에도 없음.
- §8.5 가 "send is atomic" 정도, §19.5 가 toolchain `cas = seq_cst`.
- 사용자 surface 의 `std.sync` atomics + `Mutex` 모두 ordering model 부재.
- **Action**: §9.3 신규 — DRF-SC 선언, sync edge 정의 (channel ops / `Handle.join` / `Mutex.lock-unlock` / atomic ops), data race 는 UB 또는 benign-race 정의.

### 5.10 §19.2 privileged-package gate 의 `pub use` transitivity (minor)
- 비-privileged 패키지가 `pub use std.runtime.raw.alloc` 한 경우 consumer 의 `E0770` 발생 시점 미정.
- **Action**: "비-privileged 의 `pub use privileged_symbol` 은 re-export 사이트에서 `E0770`, consumer 사이트 아님".

### 5.11 §16.1 `fs.create(dst)` Writer 반환 vs §20.9.4 `Fs.create` `()` 반환 (blocker)
- 두 normative chapter 의 동일 method 가 다른 signature.
- §20 가 capability protocol — streaming write 는 `Fs.createWriter` 따로.
- **Action**: §16.1 / §7.6 예제를 `fs.createWriter(dst)?` 로 정정.

### 5.12 §16 vs §20 capability-protocol 분리 (nit)
- `Fs` capability 는 *factory*, `Reader/Writer/Closer` 는 *protocol*. 양 chapter 이 implicit 으로 일관.
- **Action**: §16 intro 한 문장 — "Capability (§20) 는 protocol handle 의 factory; capability 자체는 Reader/Writer 가 아님".

### 5.13 defer 안 blocking call: safepoint vs cancel point (minor)
- §8.4.3 "uninterruptible" 와 §19.10 "blocking call 은 safepoint" 가 동시 성립. 그러나 명시 안 됨.
- **Action**: §8.4.3 에 cross-reference.

### 5.14 §8.7.4 capability-typed channel 강제 (nit)
- "거의 항상 실수" 라고 advisory 만. lint (`L0xxx`) 등록 여부 미정.
- **Action**: 명확히 — soundness 책임 host adapter, 권고 lint.

### 5.15 §7.6 cancel swallowing — 정적 검사 부재 (minor)
- `match e { Err(_) -> Ok(default) }` 가 `Cancelled` 도 흡수 — 정적 진단 없음.
- **Action**: "lint-only future work" 를 SPEC_GAPS 에 등록.

---

## 6. Modules / Scripts / FFI / Tooling / Testing (§5, §6, §11, §12, §13)

### 6.1 `pub use` × `#[stability]` (blocker)
- §5.6 "public surface" 인용만, stability 상속 / override 룰 침묵.
- **Action**: "re-export 의 effective stability = min(target.stability, package.default). `pub use` 라인 위 `#[stability]` 가 override 가능".

### 6.2 `pub use` shadowing (blocker)
- `pub use a.X` 와 local `pub fn X` 충돌, 또는 두 `pub use` 가 같은 이름 — 진단 미정.
- **Action**: `E0552`-adjacent shadowing 진단. `#[deprecated]` 한 쪽만 silent shadow.

### 6.3 `#[cfg(...)]` v0.6 본문 spec 부재 (blocker)
- §5 §0 언급만. v0.5 G29 결정에 grammar / pre-resolve 룰 / 허용 key 까지 다 있지만 v0.6 chapter 어디에도 transcribe 안 됨.
- **Action**: §5.7 신규 — "`#[cfg]` filter" + grammar + `os/target/arch/feature` whitelist + pre-resolve 보장.

### 6.4 Script ambient mismatch 진단 부재 (blocker)
- 스크립트가 `#[ambient(clock, rng, env, fs)]` 만 declare, 본문에서 `net.get(...)` 호출 시 — 진단 미정.
- **Action**: "out-of-ambient 는 `E0501` (binding not in scope) + hint 'add net to #[ambient]'".

### 6.5 §6 의 `defer` 룰 (minor)
- "bare `defer` at top level 컴파일 에러 — wrap in `{ defer ... }`" — 그러나 synthesized main body 가 이미 block. 룰의 합리성 약함.
- **Action**: 룰 의도를 명확히 (no implicit block) 또는 wrap 요건 제거.

### 6.6 FFI String/Map 비대칭 (minor)
- §12.2: `String↔string`, `Map<K,V>↔map[K]V`.
- §12.8 runtime-ABI 표: `String=ptr`, `Map` omitted.
- **Action**: `Map` 의 `use c` / `runtime.cabi` 동작을 unsupported 또는 marshalling 룰 명시.

### 6.7 §12.5 goroutine × `taskGroup` (minor)
- "not integrated" — cancellation propagation, defer, leak / lint 침묵.
- **Action**: "goroutine 은 structured-concurrency tree 밖. cancellation 도달 안 함. spawn 시 lint `L0xxx` 권장".

### 6.8 `#[trusted_declassify]` typing rule 부재 (blocker)
- 어떤 tag 가 strip 되는지, return value 가 untainted 인지 명시 안 됨.
- **Action**: "wrapper return 에서 모든 `#[taint]` tag drop. `reason` 인자만 audit metadata 로 보관".

### 6.9 `#[golden]` `text` mode byte canonicalization (blocker)
- LF/CRLF, BOM, trailing newline 명시 부재. CI flake hazard.
- **Action**: "LF only, no BOM, mandatory trailing newline".

### 6.10 `#[golden]` `ast` mode + doc comment (minor)
- `///` 가 AST-attached. `#[purpose]` / `#[spec]` load-bearing. ast mode 가 doc comment 무시인지 포함인지 미정.
- **Action**: 명시.

### 6.11 `#[example(output = ...)]` equality (blocker)
- `Equal` ? structural deep ? `ToString` round-trip ? unspecified.
- `output = "Some(...)"` literal 의 `...` 가 placeholder pattern 인지 literal 인지 미정.
- **Action**: equality = `T: Equal` (assertEq 와 동일). `...` placeholder 거부 또는 매칭 의미 명시.

### 6.12 `#[fixture(name)]` namespace (blocker)
- package-scoped vs workspace-global 미정.
- duplicate name 진단 미정.
- **Action**: "package-scoped, duplicate `E04xx`. cross-package 는 `pkg.name` qualified".

### 6.13 `#[purpose]` / `#[spec]` markdown rendering (minor)
- markdown escape 룰 (`<`, `>`, `&`, backtick) 미정.
- `#[spec]` anchor lookup 의 root 미정 (`LANG_SPEC_v0.6/**.md` 만 vs 임의 reachable).
- **Action**: anchor root = `LANG_SPEC_v0.6/**.md` 만, 나머지는 `W0790` orphan.

### 6.14 `osty publish` diff — rename param + change default (blocker)
- "trailing default 추가 = COMPAT-ADD / non-trailing = BREAKING" 만 cover.
- "rename param of stable fn" / "change default value" 미정.
- G20 named call 에 따르면 rename = breaking.
- **Action**: 표에 두 row 추가.

### 6.15 `osty context` schema versioning vs `osty doc --json` (minor)
- 두 출력의 schema relationship 명시 안 됨.
- **Action**: "context v1 ⊆ `osty doc --json`" 또는 explicit split.

### 6.16 §11 parallel-by-default test isolation (blocker)
- CLAUDE.md / §11.6 가 default parallel — 그러나 isolation contract (filesystem / env / cwd / globals) 없음.
- **Action**: §11.6.1 — "tests share process state. capability fakes (§11.9) 또는 `--serial` 사용. 자동 sandbox 없음".

### 6.17 capability surface — re-export transitivity (minor)
- §5.6.3 가 capability 추가 / 변경을 breaking 으로 등록. 그러나 `pub use` chain 의 transitive analysis 가 §13.10.5 에서 interface 만 cover.
- **Action**: §13.10.5 확장.

### 6.18 §6.1 ambient default 에 `console` 누락 (minor)
- default = `(clock, rng, env, fs)` — `console` 없음. 그러나 첫 예제가 `println(...)` 사용.
- → prelude `println` 이 Console-free (ambient stdout) — 명시 부재.
- **Action**: "prelude `println` 은 Console-free; `console.println` 은 Console-method form".

---

## 7. Cross-Document Drift

### 7.1 `osty context` 가 §13.4 (chapter) vs §13.6 (모든 다른 doc) (blocker)
- `13-tooling.md`: §13.4 = `osty context`, §13.6 = `osty validate-spec`.
- `00-revision.md` 32, 88, 484, 785, 987 / `18-change-history.md` 25 / `CLAUDE.md` C.5/C.12 / `ABRIDGED.md` 267-270 모두 §13.6 = `osty context`.
- 13-tooling.md 가 renumber 됐고 나머지 doc 이 update 안 됨.
- **Action**: §13.x 번호 동기화 (13-tooling 변경 또는 다른 doc 일괄 update).

### 7.2 §7.5 heading G44 (실제 G41) (minor)
- (5.1 와 동일 finding)

### 7.3 R1–R30 vs R1–R29 (minor)
- `LANG_SPEC_v0.6/README.md:33`: "R1–R30 + EBNF"
- `00-revision.md:1805`: "R27–R30"
- `OSTY_GRAMMAR_v0.6.md:14, 20, 39, 241-313`: 단 R27 / R28 / R29 만 정의
- `CLAUDE.md:41`: "R1–R29"
- **Action**: README + 00-revision wording 을 R29 까지로 정정 (R30 신설 또는 wording 정정 둘 중 하나).

### 7.4 13 vs 14 결정 framing (minor)
- `00-revision.md:3`: "14 결정"
- `00-revision.md:11, 36, 50`: "13 + 1 ergonomics"
- `SPEC_GAPS.md:239`: "13 + 2 ergonomics" (그러나 G49 만 ergonomics)
- `CHANGELOG_v0.6.md:12`: 14
- **Action**: "13 hidden-dependency + 1 ergonomics = 14 결정" 으로 동기.

### 7.5 G48 description "신규 어노테이션 14 개" vs 카탈로그 +20 (minor)
- `SPEC_GAPS.md:262`: "신규 어노테이션 14 개 (G36-G47 합계)"
- `OSTY_GRAMMAR_v0.6.md:299` / `00-revision.md:1183` / `18-change-history.md:57`: +20.
- **Action**: SPEC_GAPS 의 G48 row "14" → "20".

### 7.6 Contextual keyword 카운트 baseline (nit)
- v0.5 baseline 이 10 (`OSTY_GRAMMAR_v0.6.md`) vs 11 (`01-lexical-structure.md`) 로 미세 disagree.
- `forall` 이 v1 인지 v0.6 baseline 인지 카운팅 모호.
- **Action**: 한 군데로 동기.

### 7.7 CHANGELOG 가 open SPEC_GAPS 미언급 (minor)
- `SPEC_GAPS.md` Open Gaps: `vectorize-hint` (iterator-protocol), `log-fields-sugar`, `stdlib-body-llvm-wall` (option/result combinator).
- `CHANGELOG_v0.6.md` 는 G36–G49 phase 만, "Known limitations" 섹션 없음.
- **Action**: CHANGELOG 에 "Known limitations" 섹션 추가, SPEC_GAPS Open 항목 link.

### 7.8 `forall` 가 lexical chapter 에 이미 등재 vs "v1 only" (nit)
- `01-lexical-structure.md:66` 가 `forall` 을 contextual 로 listing — 그러나 v0.6 baseline 에서는 reserved 안 됨.
- **Action**: `forall` row 에 명시적 "Phase 5 / v1 까지는 식별자" annotation.

### 7.9 CLAUDE.md anchor sample (clean)
- §3.4.5 (sealed_construct) / §7.5 (error_contract — wrong G-tag 별개) / §3.13 (spec block) / §20 / §21 모두 file 에 존재. 양호.

### 7.10 진단 코드 band declaration vs 사용 코드 (minor)
- `OSTY_GRAMMAR_v0.6.md:302`: "E0001-E0949 + E2149 + W0949".
- 사용 sample (E0410/E0420/E0440/E0450/E0780-E0796/E0900-E0903/E2100-E2102/W0790/W0795/W0902/W2100): 모두 in-band.
- 그러나 `E0789` (20-capabilities 에서 사용) 가 `00-revision.md §6` 의 E0780-E0788+E0790+E0795+E0796 enumeration 에서 missing.
- **Action**: §6 allocation 표에 E0789 추가.

### 7.11 Phase table (clean)
- CHANGELOG / 00-revision / CLAUDE.md 모두 Phase 0-5 + Pre-1.0 일관. 양호.

### 7.12 `while ≡ for cond` wording (clean)
- README / 00-revision / GRAMMAR / 01-lexical / ABRIDGED 모두 "same lowering" 일관. 양호.

---

## 8. 권장 follow-up 우선순위

**P0 (다음 minor release 전 필수)**
- 1.1, 1.4, 1.11, 2.1, 2.4, 2.6, 3.3, 3.4, 4.1, 4.5, 4.11, 5.2, 5.4, 5.5, 5.6, 5.9, 5.11, 6.1, 6.2, 6.3, 6.4, 6.8, 6.9, 6.11, 6.12, 6.14, 6.16, 7.1.

**P1 (현 baseline 안에서 doc fix 만으로 해결 가능)**
- 5.1, 5.3, 6.5–6.7, 6.10, 6.13, 6.15, 6.17, 6.18, 7.2–7.5, 7.7, 7.10, 그리고 §1 / §2 / §3 / §4 / §5 의 modal minor 항목 다수.

**P2 (nit, 시간 날 때)**
- 1.13, 1.15, 1.18, 2.11, 4.2, 4.4, 5.12, 5.14, 7.6, 7.8.

---

## Appendix — 리뷰 메소드

이 리뷰는 7 개 병렬 에이전트로 분담:

| Agent | 영역 | 지원 파일 |
|---|---|---|
| 1 | Lexical / EBNF | 01-lexical-structure.md, OSTY_GRAMMAR_v0.5/0.6.md, 04-expressions.md, 00-revision §5–§7 |
| 2 | Type system | 02-type-system.md, 02a-type-inference.md, 14-excluded-features.md, 15-iteration-protocol.md, 17-display-and-format-protocol.md |
| 3 | Declarations + expressions | 03-declarations.md, 04-expressions.md |
| 4 | Capabilities + IFC | 20-capabilities.md, 21-information-flow.md, 00-revision G36/G37, 10/46-capability-migration.md |
| 5 | Concurrency + memory + errors + IO | 07/08/09/16/19 chapters, RUNTIME_GC.md, RUNTIME_SCHEDULER.md |
| 6 | Modules / scripts / FFI / tooling / testing | 05/06/11/12/13 chapters |
| 7 | Cross-doc consistency | README, 00-revision, 18-change-history, ABRIDGED, OSTY_GRAMMAR_v0.6, SPEC_GAPS, CHANGELOG_v0.6, BREAKING/MIGRATING, CLAUDE.md |

각 에이전트는 read-only 로 작동했고, 본 PR 은 spec 본문을 변경하지 않는다.
