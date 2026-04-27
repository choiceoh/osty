# ONB 설계 — Osty Native Backend

> **Status: Paper design (2026-04-27).** 33개 설계 결정 통합. 구현 전 단계.
> 본 문서는 결정 lock-in이며, 후속 의제(예: aarch64 LIR opcode 카탈로그,
> cross-validation harness, simple inliner 도입 검토)는 별도 문서로 분기한다.

## 한 줄 요약

ONB는 **LLVM과 ABI·의미 100% 호환되는 단순 dev 백엔드**. Non-SSA MIR을
입력으로 받아 aarch64 LIR을 거쳐 lld로 darwin/linux binary를 emit. Full
DWARF 디버그, 옵티마이저는 4-pass minimal, vectorize·SIMD·incremental GC는
LLVM에 **영구 위임**한다.

---

## 1. 위상 / KPI

### 1.1 위상 (#1 = F)

`osty run`, `osty test`, watch loop → ONB (빠른 빌드).
`osty build --release` → LLVM (코드 품질).

두 백엔드는 **영구 공존**한다. ONB는 release 책임을 영원히 지지 않는다.
이건 #33 D로 강화된 정책.

### 1.2 KPI (#2 = D + E + G)

ONB 설계의 모든 후속 결정의 tie-breaker는 다음 셋의 균형:

- **D 속도+단순성** — 옵티마이저/RA/ISel 모두 단순한 쪽으로. SLOC ≤ LLVM
  백엔드의 절반.
- **E LLVM과의 의미 동등성** — 관찰 가능한 동작(overflow/NaN/정렬/어노테이션
  의미·panic 동작)이 LLVM과 일치. cross-validation harness가 first-class.
- **G 디버그 경험** — 빠른 빌드 + 정확한 backtrace + step debugging + 변수
  inspect.

### 1.3 첫 타겟 (#3 = A+C)

darwin/aarch64 + linux/aarch64. **aarch64 ISel·encoder·LIR·RA는 100% 공유**,
OS layer(syscall ABI·object 포맷·linker glue)만 분기. OS당 추가 비용
~500–800줄.

x86_64은 의제 외. 결정 #1 F로 release 책임이 LLVM에 영구 귀속되므로 x86_64
ONB가 영영 필요 없을 수 있음.

---

## 2. IR 계층

### 2.1 입력 IR (#4 = A)

기존 `internal/mir`를 그대로 입력으로 사용. **추가 IR 신설 없음**.

자동 해소:
- #5 SSA — 도입 안 함 (MIR이 non-SSA)
- #6 typed/untyped — typed 그대로
- #7 표현 방식 — arena+kind 그대로

이유: F 모델에서 ONB는 깊은 옵티마이저 책임이 없어 SSA 동기 약함. MIR을
LLVM 백엔드와 공유하면 cross-validation 자연.

### 2.2 GC root 표현 (#8 = A)

기존 shadow stack ABI 그대로:
- 함수 진입 시 `osty.gc.root_bind_v1(slot)` 호출 emit
- return 전 `osty.gc.root_release_v1(slot)` LIFO emit
- 런타임이 thread-local frame chain으로 root 추적

자동 해소: **#20 stack map writer 불필요**. ONB는 stack map section을 emit
하지 않는다.

---

## 3. 옵티마이저

### 3.1 Pass 집합 (#9 = C)

```
const fold → copy prop → jump threading → DCE → DCE
```

총 4개 pass + 마지막 DCE 한 번 더 (#10 = B). 단일 통과, fixpoint 반복 없음.

- **const fold** — overflow/NaN 동작은 LLVM과 정확히 동일하게 (E 보장)
- **copy prop** — `?` 전파/closure 캡처/메서드 self가 만드는 임시 copy 제거.
  Osty 코드의 RA 부담 자릿수로 감소
- **jump threading** — match 식의 nested branch 정리에 결정적
- **DCE** — Osty 특유의 unreachable code 다발 (`?` 전파 후 path, defer
  cleanup) 제거

### 3.2 보유 옵션 (Phase 1.5 트리거 시 추가)

- **simple inliner** — 측정상 함수 호출이 hot path임이 확인되면 cost-driven
  인라이너 도입. 처음에 짜지 않음
- **RA tuning** — Linear scan spill이 hot path에 다발하면 graph coloring 검토
- **safepoint inlining** — runtime call이 hot이면 inline fast path

---

## 4. Code generation

### 4.1 Vectorizer (#11 = A)

**자체 vectorizer 없음.** `#[vectorize]` 어노테이션은 ONB에서 no-op.
release LLVM에서만 의미.

자동 해소:
- #12 scalable/predicate — 무의미
- #13 reduction/horizontal/gather — 무의미

### 4.2 ISel (#14 = D)

Phase 1.0: **Linear lowerer** — MIR instr 1:1 → aarch64 LIR sequence.

Phase 1.1: **패턴 매처 핵심 4개** 추가:
- `MADD Xd, Xn, Xm, Xa` — `Add(Mul(a, b), c)`
- `MSUB Xd, Xn, Xm, Xa` — `Sub(c, Mul(a, b))`
- `LDR Xd, [Xn, #imm]` — 즉시 offset load
- `ADD Xd, Xn, Xm, LSL #N` — shift-fold

추가 패턴은 측정 후 결정. 처음에 30개 짜지 않는다.

### 4.3 LIR 추상 (#15 = B)

**aarch64-only LIR** — opcode가 곧 aarch64 mnemonic (MOV/ADD/LDR/B/BL 등).
Generic abstraction 없음. 인코더가 곧 mnemonic → 4-byte 표 lookup.

x86_64 추가 시점에 LIR을 generic화하는 리팩터(약 +500줄). 미리 짜지 않음.

### 4.4 Register Allocation (#16 = A, #17 = A)

- **Poletto Linear scan** — 1999년 표준 알고리즘. 약 600줄.
  Live interval 기반, weight = loop_depth × use_count.
- **callee-saved에 GC pointer 금지** — RA가 GC pointer를 x19–x28에 배정
  안 함. caller-saved(x0–x18) 또는 stack slot only.
  Function call을 가로지르는 GC pointer는 spill (root_bind 한 번 더 emit).

#### 4.4.1 Risk note — spill thrash

#17 A는 단순성과 LLVM 백엔드와의 정책 정합(LLVM도 GC pointer는 alloca/stack
slot home)을 우선한 결정이지만, ONB의 4-pass opt + Poletto linear scan은
LLVM의 mem2reg + GVN/LICM + deep RA 대비 **hot 사용 시 register hoisting이
약함**. AArch64 caller-saved 중 실용 GC pointer 후보는 x9–x15(7개) +
필요 시 x0–x8 spill이라, hot 함수에 GC pointer 5+개 동시 live + 함수 호출
다수면 stack spill 다발 가능. 매 spill은 stack store + `root_bind_v1`
호출, unspill은 stack load — 누적 시 dev 빌드 실행 속도가 LLVM 대비
1.5–3× 느려질 risk.

KPI #2의 D(속도) 충돌 가능 영역. **결정 변경 없이 측정 기반 자동 진화
path로 트래킹**:

- **Phase 1.0 종착 후 측정 우선순위 1**: 대표 hot path(셀프호스트 컴파일러
  자체 + 사용자 numeric 코드) register pressure 분포 + dev 빌드 실행 시간
  vs LLVM 비교
- **트리거 게이트**: ONB가 LLVM 대비 ≥3× 느리고 그 원인의 ≥40%가 GC
  pointer spill (perf 분석으로 root_bind/release call 비중 측정)이면
  **#17 A → B promote** — callee-saved에 GC pointer 허용 + entry/exit에
  `root_bind/release` LIFO emit. RA 변경 없음, emitter prologue/epilogue만
  ~300줄 추가.
- 트리거 미도달 시 #17 A 영구 유지.

---

## 5. ABI

### 5.1 Calling convention (#18 = A)

System ABI strict — linux는 **AAPCS64**, darwin은 **Apple ARM64** (variadic
변형). Result/Option은 small struct 규칙 (≤16B는 x0/x1 pair, 그 이상은 x8
indirect return).

LLVM 백엔드와 100% 동일. `use go` FFI·runtime call 모두 wrapper 없이 직접.

### 5.2 동시성 상태 (#19 = A)

cancel state는 **thread-local로 묵시 관리**:
- `static OSTY_RT_TLS osty_rt_task_group_impl *osty_sched_current_group`
- 함수 시그니처에 cancel context 인자 **없음**
- safepoint poll 또는 명시 `checkCancelled()` 호출 시점에 TLS 한 줄 atomic
  load

ABI 변경 0. LLVM 백엔드와 정확히 동일.

### 5.3 EH / unwind table (#34 = A)

unwind table emit **안 함**. Osty는 panic = `abort()`라 stack unwind 의미상
불필요. defer는 normal-path inline cleanup으로 emit.

backtrace는 frame pointer chain만으로 충분:
- darwin/Apple ARM64 — frame pointer 강제 (ABI mandate)
- linux/aarch64 — default가 frame pointer 보존

lldb/gdb backtrace 자연 동작. `.eh_frame` section emit 0줄.

### 5.4 Atomic ops baseline (#35 = B)

**ARMv8.1+ LSE (FEAT_LSE)** 사용:
- `CAS Xs, Xt, [Xn]` — compare-and-swap 단일 명령 (vs LDXR/STXR loop)
- `SWP Xs, Xt, [Xn]` — atomic exchange
- `LDADD/LDCLR/LDEOR Xs, Xt, [Xn]` — atomic RMW

baseline 가정: Apple Silicon(ARMv8.5+), modern aarch64 서버(Graviton 2+,
Ampere Altra, modern Cortex-A). RPi4 등 ARMv8.0 only 머신은 dev 백엔드
시나리오 외 — F 모델에서 release LLVM이 cover.

### 5.5 TLS access (#36 = A)

OS별 표준 그대로:

| OS | 모델 | aarch64 코드 | linker reloc |
|---|---|---|---|
| **darwin** | Thread-local variable | `bl _tlv_get_addr` thunk + 결과는 register | dyld lazy resolve |
| **linux** | ELF Initial-Exec (IE) | `mrs x0, tpidr_el0; ldr x0, [x0, #offset]` | `R_AARCH64_TLSIE_ADR_GOTTPREL_PAGE21` 등 |

`osty_sched_current_group` 같은 thread-local 변수 access는 모두 OS 표준
경로. wrapper 없음.

### 5.6 PIC / PIE (#37 = B)

**PIE binary, mixed addressing**:
- **Internal symbol** (같은 모듈 내) — PC-relative direct
  (`ADRP + ADD` for text, `ADRP + LDR` for data)
- **External symbol** (libc, runtime, FFI) — GOT 경유
  (`ADRP + LDR (GOT entry)`)

darwin Mach-O는 PIC 강제 (ABI mandate). linux PIE는 modern 표준 (ASLR).
aarch64 PC-relative addressing이 `ADRP + ADD` 한 쌍으로 ±4GB 범위 cover —
PIC overhead가 x86_64보다 작아 dev 백엔드 KPI에 영향 미미.

### 5.7 FP ABI 변종 (#38 = A)

OS별 자연 분기. emitter glue에서 ~50줄로 처리:

| 차이점 | linux AAPCS64 | darwin Apple ARM64 |
|---|---|---|
| HFA (≤4 fp field) | v0–v3 register pass | 동일 |
| **Variadic FP** | v register 사용 | **모두 stack** |
| Empty struct | 0 byte | 1 byte |
| Stack alignment | 16B | 16B |

`use go` FFI · libc 호출(특히 `printf` 등 variadic) 시 OS별 ABI를 정확히
따라야 immediate crash 회피. emitter는 `Target` flag로 분기.

---

## 6. GC 통합

### 6.1 Safepoint poll (#21 = E)

`osty.gc.safepoint_v1(id, root_set)` runtime call emit. LLVM과 동일 ABI +
**동일 placement**:
- Entry: 함수 진입 1회
- Call: function call site
- Loop: back-edge (단 v0.6 A5.2 기본 ON 정책상 스킵, `#[no_vectorize]`만
  유지)
- Alloc: heap 할당 직후
- Yield: 명시 yield 지점

ID 인코딩은 LLVM 백엔드의 `safepointKind` 표 (Unspecified/Entry/Call/Loop/
Alloc/Yield) 그대로 사용.

### 6.2 Write barrier (#22 = A)

Emit 안 함. LLVM 백엔드도 emit 안 하므로 의미 동등 자동.

미래 incremental/generational GC 도입은 runtime + LLVM 백엔드 + ONB 동시
변경 의제 — 별도 트래킹.

---

## 7. 런타임 (#23 = A)

`internal/backend/runtime/osty_runtime.c` (15,667줄) **그대로 유지**. ONB는
같은 .o를 링크.

자동 해소: **#24 scheduler 모델** — 1:1 OS thread (`pthread_create`).
Runtime unchanged.

런타임 자체의 Osty 재작성은 ONB 의제 외. 별도 점진 마이그레이션 트래킹.

---

## 8. Object / Linker

### 8.1 Object 포맷 (#25 = B)

- Phase 1.0: **Mach-O64** (darwin/aarch64). dev 머신 우선.
- Phase 1.1: **ELF64** (linux/aarch64).

각 포맷당 ~1k줄.

### 8.2 Dynamic linking (#26 = B)

System dynamic + 사용자 코드/runtime은 static.

- darwin: `LC_LOAD_DYLIB(libSystem.B.dylib)` 한 줄. dyld 강제.
- linux: `DT_NEEDED` 0개. musl로 fully static.

사용자가 manifest에 dynamic dependency 추가하는 시나리오는 Phase 2 의제.

### 8.3 Linker (#27 = C)

**lld 호출** — LLVM stack과 동일. ONB는 .o만 emit하고 lld에 위임.

자체 minimal linker는 보유 옵션. 측정상 link 시간이 dev loop 병목이면 검토.

---

## 9. 디버그 정보

### 9.1 DWARF 깊이 (#28 = E)

**Full DWARF 5** — rustc/clang 수준. SLOC ~2.8k.

- `.debug_line` — line/column → PC, state machine encoder
- `.debug_info subprogram` — 함수 단위 DIE, abbreviation table
- `.debug_loc` — 인자/지역변수 location list (DWARF expression)
- `.debug_info type` — `DW_TAG_structure_type/enumeration_type/array_type`
  Osty 타입 → DWARF 매핑
- lexical block, inline frame, macro info, advanced line opcode

### 9.2 자체 디버그 포맷 (#29 = D)

DWARF + **`__osty_sourcemap` section** — PC → AST node id 매핑.
- panic backtrace에 source 식 텍스트 표시
- `dbg(expr)` 식 출력에 정확한 source 위치
- AST-aware 도구가 PC를 AST node로 역추적

추가 ~200줄.

---

## 10. 어노테이션 매핑

### 10.1 vectorize (#30 = A, #31 = A)

ONB는 모든 vectorize hint를 **무시**:
- `#[vectorize]` → no-op
- `#[vectorize(scalable, predicate, width = N)]` → no-op
- `#[no_vectorize]` → no-op (vectorize 자체를 안 하므로 opt-out도 무의미.
  단 mid-loop safepoint 정책은 #21 E로 따라감)

Spec unchanged. release LLVM에서만 의미.

### 10.2 target_feature (#32 = A)

`#[target_feature(neon, sve, ...)]` → 무시. ONB는 baseline aarch64만 emit
(NEON register는 Float scalar용으로 사용, SVE 등은 X).

### 10.3 다른 어노테이션

LLVM 백엔드와 동일 처리:
- `#[inline]` / `#[inline(always)]` / `#[inline(never)]` — Phase 1.5에
  simple inliner 도입 시 의미 부여. 그 전까진 hint 보유만.
- `#[hot]` / `#[cold]` — `.text.hot` / `.text.unlikely` section 배치
  (linker가 자연 처리)
- `#[parallel]` — alias analysis 우회 hint. 4-pass opt에선 무의미.
  Phase 1.5에서 의미 부여 가능
- `#[unroll]` / `#[unroll(count = N)]` — 자체 unroller 없으므로 no-op
- `#[noalias]` / `#[noalias(p1, p2)]` — alias analysis hint. 4-pass opt
  에선 무의미. Phase 1.5에서 의미 부여 가능
- `#[pure]` — readnone hint. const fold/CSE 강화에 활용 가능 (Phase 1.5)
- `#[deprecated]` — lint only, codegen 무관

---

## 11. Validation / 셀프호스팅 (#33 = D)

LLVM 백엔드는 **영구 reference + release**. ONB는 영구 dev. 졸업 의제 없음.

cross-validation harness는 first-class:
- 같은 .osty 입력에 대해 ONB와 LLVM 출력의 관찰 가능 동작이 일치해야 함
- 테스트 코퍼스(`testdata/spec/positive`, `testdata/spec/negative`,
  `internal/backend/llvm_*_test.go`)를 양쪽에서 실행 후 diff
- diff 발생 시 ONB 버그로 우선 가정

ONB가 미래에 vectorize·optimizer를 강화해도 LLVM이 reference로 살아 있어
회귀 검증 자동.

---

## 12. 단계별 마일스톤

| Phase | 종착점 | 작업 |
|---|---|---|
| **1.0** | Hello world darwin/aarch64 | MIR consumer + 4-pass opt + linear lowerer + aarch64 LIR + Poletto RA + Mach-O writer + DWARF .debug_line + lld 호출 |
| **1.1** | Step debugging + linux 지원 | 패턴 매처 핵심 4개 + DWARF subprogram/var loc/type + ELF writer (linux/aarch64) |
| **1.2** | 셀프호스트 dev loop | sourcemap section + 셀프호스트 컴파일 시간 측정 + cross-validation harness 자동화 |
| **1.5** (조건부) | dev 빌드 실행 속도 강화 | 측정 후 simple inliner / RA tuning / safepoint inlining 등 ad-hoc 추가 |

---

## 13. SLOC 추정

| 컴포넌트 | Osty | Go (host) |
|---|---|---|
| 4-pass optimizer | ~1.2k | 0 |
| Linear lowerer + 패턴 매처 | ~1.5k | 0 |
| aarch64 LIR + encoder | ~1.2k | 0 |
| Poletto RA | ~600 | 0 |
| Mach-O writer | ~1k | ~300 (lld glue) |
| ELF writer | ~1k | ~300 |
| DWARF (line+info+loc+type) | ~2.8k | 0 |
| `__osty_sourcemap` section | ~200 | 0 |
| Driver / orchestration | ~500 | ~500 |
| **Total** | **~10.0k Osty** | **~1.1k Go** |

비교: 현재 `internal/llvmgen` ~25k Go LOC. ONB는 절반 이하.

---

## 14. 코드 구조 (예정)

```
internal/
  onb/                   # Go-side host glue
    artifacts.go         # 출력 파일 layout
    driver.go            # backend dispatcher 진입점
    lld_invoke.go        # lld 호출
    objwriter_macho.go   # Mach-O writer host glue
    objwriter_elf.go
    runtime_link.go      # osty_runtime.o 위치/링크

toolchain/
  onb/
    onb_driver.osty      # 파이프라인 orchestration
    opt_const_fold.osty
    opt_dce.osty
    opt_copy_prop.osty
    opt_jump_thread.osty
    isel_aarch64.osty    # linear + 패턴
    lir_aarch64.osty
    encode_aarch64.osty
    regalloc.osty        # Poletto linear scan
    abi_aapcs64.osty
    abi_apple_arm64.osty
    macho.osty           # Mach-O writer 본체
    elf.osty             # ELF writer 본체
    dwarf.osty           # DWARF 5 emit
    sourcemap.osty       # __osty_sourcemap section
```

`internal/backend/runtime/osty_runtime.c`는 unchanged.

---

## 15. 미해결 / 유예 항목

다음은 ONB 결정에서 의식적으로 유예된 의제:

1. **simple inliner** (#9 Phase 1.5) — 측정 후 결정.
2. **RA quality 강화** (#16 graph coloring) — Phase 1.5 측정 후.
3. **자체 linker** (#27 보류) — link 시간이 dev loop 병목이면.
4. **자체 vectorizer / SIMD intrinsic** (#11 D 옵션) — Osty가 SIMD를
   first-class로 surface하는 spec 결정과 결합되어야. ONB 의제 외.
5. **incremental / generational GC + write barrier** (#22) — runtime +
   LLVM 백엔드 + ONB 동시 변경. 별도 의제.
6. **x86_64 추가** — 결정 #1이 D가 아니라 F이므로 영영 미실현 가능.
7. **런타임 Osty 재작성** (#23) — 셀프호스팅 진화의 자연 종착점이지만 ONB
   의제 외.

---

## 16. 다음 의제 (별도 문서로 분기 예정)

- aarch64 LIR opcode 카탈로그 (mnemonic + operand kind + RA constraint
  표)
- DWARF emit 세부 (Osty 타입 시스템 → DW_TAG 매핑 표)
- cross-validation harness 설계 (diff 정책, golden snapshot 운영)
- Phase 1.0 implementation plan (작업 분할, 의존성 DAG, 테스트 게이트)
- v0.6 spec 부록에 ONB hint 정책 명시 (`#[vectorize]` 백엔드별 정책 등)
