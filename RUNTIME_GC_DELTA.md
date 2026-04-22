# GC Delta — Model vs Live Runtime

This document tabulates the feature gap between the GC simulation model
(`examples/gc/`) and the live LLVM runtime (`internal/backend/runtime/osty_runtime.c`
+ `internal/llvmgen/generator.go`).

The working rule from [`RUNTIME_GC.md`](./RUNTIME_GC.md) applies: the live runtime
and LLVM lowering are the authoritative path. This delta is a planning aid, not
a spec. New implementation effort should land in the LLVM lowering/runtime path;
`examples/gc` is an invariant lab and semantic prototype.

## Sources

- Model: [`examples/gc/DESIGN.md`](./examples/gc/DESIGN.md),
  [`examples/gc/lib.osty`](./examples/gc/lib.osty) (6592 lines),
  [`examples/gc/lib_test.osty`](./examples/gc/lib_test.osty)
- Live runtime:
  [`internal/backend/runtime/osty_runtime.c`](./internal/backend/runtime/osty_runtime.c)
  (1938 lines)
- LLVM emitter:
  [`internal/llvmgen/generator.go`](./internal/llvmgen/generator.go) (safepoint /
  root / barrier call-site emission)
- Tests: [`internal/backend/llvm_runtime_gc_test.go`](./internal/backend/llvm_runtime_gc_test.go),
  [`internal/llvmgen/gc_integration_test.go`](./internal/llvmgen/gc_integration_test.go)

## 범례

- **상태**: ✅ 있음 · 🟡 부분/스텁(호출은 있으나 실동작 없음) · ❌ 없음
- **우선순위**:
  - **P1** — 근시 블로커. 다른 P2/P3 기능의 전제조건이거나, lowering은 이미
    호출을 emit하는데 런타임 쪽이 no-op이라 약속과 구현이 어긋나 있음.
  - **P2** — 중기. 독립 랜딩 가능, 기능 병렬화 여지.
  - **P3** — 장기 / aspirational. 큰 재작업 또는 스펙 개정을 수반.
  - **N/A** — 언어 스펙상 하지 않기로 한 것 (v0.5 baseline 기준).

## 1. 할당 · 힙 모양

| #   | 기능                                         | 시뮬 | 실  | 델타                                                 | P  | 참조                                                 |
| --- | -------------------------------------------- | ---- | --- | ---------------------------------------------------- | -- | ---------------------------------------------------- |
| 1.1 | ~~오브젝트 헤더~~                            | ✅   | ✅  | age + generation 추가 (Phase B1). pin/forwarding은 Phase D에서 | ✅ B1 | `osty_runtime.c:osty_gc_header`              |
| 1.2 | 지역별 힙 (Eden/Survivor/Old/Humongous/Pinned) | ✅ | 🟡 | YOUNG/OLD 논리 구분 + young alloc / old evacuation bump region 분리 landed. old evac region은 empty-block recycle까지 포함, Survivor/TLAB/pinned 전용 region은 아직 없음 | P3 | `osty_runtime.c:osty_gc_link` (generation 분기)      |
| 1.3 | Bump allocator                               | ✅   | 🟡  | young small-object bump block + major compaction old bump clone path landed. old evac region은 empty-block recycle 가능, young small-object는 minimal per-thread TLAB current block까지 포함 | P3 | `lib.osty:127-147` / `osty_runtime.c:osty_gc_bump_take_from_region`                                   |
| 1.4 | Free list (tenured / humongous)              | ✅   | 🟡  | sweep-dead header reuse + size-class bin landed. humongous는 direct alloc/free 경로로 분리 | P3 | `lib.osty:122-147` / `osty_runtime.c:osty_gc_free_list_take`                                   |
| 1.5 | Size class / large object threshold          | ✅   | 🟡 | 16-byte size-class bin + humongous threshold landed. bump/region 전략은 아직 동일 | P3 | `lib.osty:709-764` / `osty_runtime.c:osty_gc_size_class_index_for_total_size`                                   |
| 1.6 | TLAB (스레드별 할당 버퍼)                    | ✅   | 🟡  | young small-object bump current block만 per-thread TLS로 분리. Survivor/old/pinned region 전용 TLAB는 아직 없음 | P3 | `lib.osty:111-120` / `osty_runtime.c:osty_gc_tlab_current`                                   |
| 1.7 | Stable object identity (movable와 분리)      | ✅   | ✅  | monotonic `stable_id` + reverse index + forwarding-aware debug/load path landed | ✅ D1 | `lib.osty:94-108` / `osty_runtime.c:osty_gc_header` |

## 2. 루트

| #   | 기능                                | 시뮬 | 실  | 델타                                      | P  | 참조                              |
| --- | ----------------------------------- | ---- | --- | ----------------------------------------- | -- | --------------------------------- |
| 2.1 | Stack root (safepoint 기반)         | ✅   | ✅  | —                                         | —  | `osty_runtime.c:1614-1623`        |
| 2.2 | Manual root bind/release            | ✅   | ✅  | 시뮬은 list, 실은 refcount                | —  | `osty_runtime.c:1592-1612`        |
| 2.3 | ~~Global / static root 테이블~~     | ✅   | ✅  | slot-address 등록, safepoint mark에서 순회 — PR #400 후속 | ✅ done | `osty_runtime.c:osty_gc_global_root_register_v1` |
| 2.4 | Closure capture root                | ✅   | 🟡 | capture struct은 일반 alloc, slot descriptor 없음 | P2 | `lib.osty:618-622`          |
| 2.5 | Async frame root                    | ✅   | ❌  | async 구현 전까지 대기                    | P3 | `lib.osty:624-631`                |
| 2.6 | Derived-base root pair              | ✅   | ❌  | inner pointer 대응                        | P3 | `lib.osty:582-600`                |
| 2.7 | ~~Incremental shade-on-add~~        | ✅   | ✅  | `pre_write_v1` greys old value during MARK_INCREMENTAL (Phase C5) | ✅ C5 | `osty_runtime.c:osty_gc_pre_write_v1` |

## 3. 쓰기 / 읽기 배리어

| #   | 기능                                              | 시뮬 | 실  | 델타                                       | P      | 참조                                |
| --- | ------------------------------------------------- | ---- | --- | ------------------------------------------ | ------ | ----------------------------------- |
| 3.1 | ~~`pre_write_v1` (SATB snapshot)~~                | ✅   | ✅  | `osty_gc_satb_log` — pre-write시 old_value 적재, 컬렉션 완료 후 clear. concurrent mark 구현 시 소비처 연결 | ✅ done | `osty_runtime.c:osty_gc_satb_log*`  |
| 3.2 | ~~`post_write_v1` (generational / incremental)~~  | ✅   | ✅  | `osty_gc_remembered_edges` — dedup된 (owner, value) 로그. 세대 구현 시 region 필터로 소비 | ✅ done | `osty_runtime.c:osty_gc_remembered_edges*` |
| 3.3 | `load_v1` (relocation-ready)                      | ✅   | ✅ | forwarding table을 따라 stale payload를 canonicalize | ✅ D2     | `osty_runtime.c:osty_gc_load_v1`          |
| 3.4 | `mark_slot_v1` (aggregate trace)                  | ✅   | ✅  | —                                          | —      | `osty_runtime.c:1588-1590`          |
| 3.5 | 카드 테이블 (dirty card bitmap)                   | ✅   | 🟡 | Phase B는 per-edge log로 우회. card bitmap은 write-heavy 워크로드에서 Phase D 고려 | P3     | `osty_runtime.c:osty_gc_remembered_edges`  |
| 3.6 | ~~Remembered set (old→young 정확 집합)~~          | ✅   | ✅  | post_write log → minor GC compact (Phase B4). dedup + per-cycle rebuild | ✅ B4     | `osty_runtime.c:osty_gc_remembered_edges_compact_after_minor` |
| 3.7 | ~~필드 / 원소별 store site 배리어 hookup~~        | ✅   | ✅  | lowering의 기존 call들이 이제 실 log 생성. 동일 emit 경로, 의미론만 채워짐 | ✅ done | `osty_runtime.c:osty_gc_pre_write_v1` / `post_write_v1` |

§3.1 / §3.2 / §3.7은 lowering은 그대로 두고 런타임 쪽에 실 log 구조를 붙여
완료됐다 (SATB log + dedup된 remembered edge set). 현재 STW mark-sweep에선
소비자가 없어 passive recording이지만, §5 (세대 minor GC)와 §4.3 (incremental
/ concurrent marking) 랜딩 시 즉시 입력으로 사용된다.

## 4. 마킹

| #   | 기능                                        | 시뮬 | 실  | 델타                                       | P  | 참조                          |
| --- | ------------------------------------------- | ---- | --- | ------------------------------------------ | -- | ----------------------------- |
| 4.1 | ~~트라이-컬러 (White / Grey / Black)~~      | ✅   | ✅  | `osty_gc_header.color` 명시 필드 + `marked` legacy alias (Phase C1) | ✅ C1 | `osty_runtime.c:osty_gc_mark_header` |
| 4.2 | ~~Work queue (스택-안전)~~                  | ✅   | ✅  | Phase A3에서 completed (explicit mark_stack) | ✅ A3 | `osty_runtime.c:osty_gc_mark_stack` |
| 4.3 | ~~Incremental marking (budget step)~~       | ✅   | ✅  | state machine (IDLE/MARK_INCR/SWEEPING) + `_step(budget)` API (Phase C2) | ✅ C2 | `osty_runtime.c:osty_gc_collect_incremental_*` |
| 4.4 | Mostly-concurrent mark + final remark       | ✅   | ❌  | 단일 스레드 incremental만; 실제 concurrent는 스레드 추가 시 | P3 | `lib.osty:252-262`            |
| 4.5 | 타입 descriptor 기반 ref 투영               | ✅   | ✅  | —                                          | —  | `osty_runtime.c:trace callbacks` |

## 5. 세대

| #   | 기능                                  | 시뮬 | 실  | 델타 | P  |
| --- | ------------------------------------- | ---- | --- | ---- | -- |
| 5.1 | ~~Nursery / Eden~~                    | ✅   | 🟡  | 논리 구분만 (물리적 Eden/Survivor 분리는 Phase D), YOUNG 할당 경로는 완성 | ✅ B2 |
| 5.2 | Survivor space + budget               | ✅   | ❌  | age 기반 in-place 승격으로 대체 — from/to 분리는 compaction 시 | P3 |
| 5.3 | ~~Tenured (old)~~                     | ✅   | ✅  | OLD bucket + 카운터 + in-place 승격 | ✅ B2/B3 |
| 5.4 | ~~승격 정책 (age 비트, 임계)~~         | ✅   | ✅  | `OSTY_GC_PROMOTE_AGE_DEFAULT=3`, env override, age는 u8 | ✅ B3 |
| 5.5 | ~~Minor GC 트리거 (nurseryLimit)~~    | ✅   | ✅  | `OSTY_GC_NURSERY_BYTES` + dispatcher 2-tier 선택 | ✅ B3/B5 |

B3/B4는 A의 write barrier log 위에 그대로 올려졌다 — old→young edge는
post_write log가 소유하고, minor GC가 일관되게 소비한다.

## 6. 스윕 · 재활용

| #   | 기능                                   | 시뮬 | 실  | 델타                               | P  | 참조                          |
| --- | -------------------------------------- | ---- | --- | ---------------------------------- | -- | ----------------------------- |
| 6.1 | Mark-sweep 전체 heap                   | ✅   | ✅  | —                                  | —  | `osty_runtime.c:389-400`      |
| 6.2 | Compaction / forwarding table          | ✅   | ✅  | major GC 후 STW forwarding/remap + repeated compaction alias retention landed | ✅ D4 | `lib.osty:215-222` / `osty_runtime.c:osty_gc_compact_major_with_stack_roots`            |
| 6.3 | Evacuation (region 이전, pinned skip)  | ✅   | 🟡  | list/set/string/closure-env + typed channel + map payload (composite-inline value 포함) evacuate | P3 | `lib.osty:224-230` / `osty_runtime.c:osty_gc_header_is_movable`            |
| 6.4 | Destructor callback                    | ✅   | ✅  | —                                  | —  | `osty_runtime.c:393-395`      |
| 6.5 | 프래그멘테이션 계측                    | ✅   | ❌  | 진단용                             | P3 | `lib.osty:71-92`              |

## 7. 사이클 수집

| #   | 기능                                   | 시뮬 | 실  | 델타                   | P   |
| --- | -------------------------------------- | ---- | --- | ---------------------- | --- |
| 7.1 | Mark-sweep의 암묵 사이클 해소          | ✅   | ✅  | —                      | —   |
| 7.2 | Weak ref / trial deletion              | ❌   | ❌  | v0.5 스펙 없음         | N/A |

## 8. 핀 · 파이널라이즈

| #   | 기능                                   | 시뮬 | 실  | 델타                                   | P   |
| --- | -------------------------------------- | ---- | --- | -------------------------------------- | --- |
| 8.1 | Pin bit (evacuation 제외)              | ✅   | ✅  | header `pin_count` + pinned counters landed                | ✅ D3  |
| 8.2 | pin / unpin API                        | ✅   | ✅  | `osty.gc.pin_v1` / `osty.gc.unpin_v1` export                                      | ✅ D3  |
| 8.3 | LLVM pinned handle ref (FFI)           | ✅   | 🟡 | `root_bind`로 의미는 동치, API 격차   | P2  |
| 8.4 | 유저 파이널라이저                      | ❌   | ❌  | 설계상 없음                            | N/A |
| 8.5 | 런타임 내부 destroy 콜백               | ✅   | ✅  | —                                      | —   |

## 9. 텔레메트리 · 트리거

| #   | 기능                                                  | 시뮬 | 실  | 델타                                 | P  | 참조                          |
| --- | ----------------------------------------------------- | ---- | --- | ------------------------------------ | -- | ----------------------------- |
| 9.1 | ~~Allocation pressure threshold~~                     | ✅   | ✅  | 2-tier: nursery (`OSTY_GC_NURSERY_BYTES`) + heap (`OSTY_GC_THRESHOLD_BYTES`) | ✅ B5 | `osty_runtime.c:osty_gc_note_allocation` |
| 9.2 | ~~Mutator assist (allocation debt)~~                  | ✅   | ✅  | Phase C3 depth: alloc당 `bytes/N` units drain, `OSTY_GC_ASSIST_BYTES_PER_UNIT` 튜닝 | ✅ C3 | `osty_runtime.c:osty_gc_allocate_managed` |
| 9.3 | ~~`GcStats` 구조체 (cycle / promoted / compacted / …)~~ | ✅ | ✅  | Phase A2 + B6: timing / index / minor / major / promoted / young / old 집합 | ✅ A2/B6 | `osty_runtime.c:osty_gc_debug_stats` |
| 9.4 | Collection trace 6종 (Minor / Full / Evac / Remap / Incr / Concurrent) | ✅ | 🟡 | Minor + Full 구분 + timing per tier 완료 (B6); Evac/Remap은 Phase D, Incr/Concurrent은 Phase C | P3 | `osty_runtime.c:osty_gc_debug_minor_count` |
| 9.5 | ~~`validateHeap()` 헬퍼~~                             | ✅   | ✅  | Phase A1 + B 세대 일관성 invariant (코드 -12..-14)  | ✅ A1/B | `osty_runtime.c:osty_gc_debug_validate_heap` |

## 10. Safepoint · 동시성

| #    | 기능                                                           | 시뮬 | 실  | 델타                                    | P  | 참조                          |
| ---- | -------------------------------------------------------------- | ---- | --- | --------------------------------------- | -- | ----------------------------- |
| 10.1 | Safepoint 종류 (call / alloc / loop-backedge / async-yield)    | ✅   | 🟡 | 단일 kind — id만 무시                   | P2 | `lib.osty:519-525`            |
| 10.2 | 스택맵 (라이브 bitmap + overflow)                              | ✅   | 🟡 | LLVM이 매번 slot 포인터 배열 emit       | P2 | `lib.osty:650-667`            |
| 10.3 | STW 베이스라인                                                 | ✅   | ✅  | —                                       | —  | —                             |
| 10.4 | 멀티스레드 (thread-local state)                                | ✅   | ❌  | 단일 스레드                             | P3 | `lib.osty:276-286`            |
| 10.5 | Stress 모드 (every-safepoint GC)                               | ❌   | ✅  | 실만 가진 장점 — 시뮬 역이식 검토       | —  | `osty_runtime.c:410-424`      |

## P1 진행 상태

- ✅ **§2.3 Global / static root 테이블** — slot-address 기반 등록, safepoint
  mark와 합류. 테스트 `TestBundledRuntimeGlobalRootKeepsSlotPayloadAlive`.
- ✅ **§3.1 / §3.2 / §3.7 실 write barrier** — `osty_gc_satb_log` +
  `osty_gc_remembered_edges`로 edge 실 기록. 컬렉션 완료 시 clear. 테스트
  `TestBundledRuntimeWriteBarriersLogEdges`. 현재 STW에선 passive recording,
  §5 (세대) / §4.3 (incremental) 때 소비.

P1 완료.

## Phase A 진행 상태 (관측·안정화 기반)

A1–A3는 런타임 내부 구조 보강, A4는 Phase 4 (closure capture) 핸드오프,
A5–A6는 safepoint ABI 주변 분류·가드.

- ✅ **§9.5 A1 validateHeap 오라클** — `osty_gc_debug_validate_heap()`.
  doubly-linked 리스트 무결성 / live_count·live_bytes 일치 / root_count
  음수 없음 / stale mark 없음 / SATB·remembered edge payload가 힙 안에
  존재 / 글로벌 slot non-NULL / 누적 카운터 음수 없음. 위반 시 stable
  negative 코드 반환. 테스트 `TestBundledRuntimeValidateHeapInvariants`.
- ✅ **§9.3 A2 GcStats 집계** — `osty_gc_stats` 구조체 + 스칼라 accessor +
  `osty_gc_debug_stats_dump`. 누적 `allocated_bytes_total`,
  `swept_count_total`, `swept_bytes_total`, `mark_stack_max_depth` 추가.
  테스트 `TestBundledRuntimeStatsSnapshot`.
- ✅ **§4.2 A3 Mark work queue** — 재귀 trace 제거, explicit
  `osty_gc_mark_stack` + `osty_gc_mark_drain`. 깊은 그래프(100k 체인)도
  C 스택 안전. Sweep이 marked를 clear해 "outside collection == no marks"
  invariant 성립. 테스트 `TestBundledRuntimeMarkWorkQueueDeepGraph`.
- ✅ **§2.4 A4 Closure env kind 예약** — `OSTY_GC_KIND_CLOSURE_ENV = 1029`.
  Phase 1 closure env는 dedicated allocator `osty.rt.closure_env_alloc_v1`
  경유로 생성되고, heap dump와 `osty_gc_stats`에서 일반
  `osty.gc.alloc_v1` 객체와 분리 추적된다. Phase 4 capture 랜딩 시 이
  kind 태그로 분기하여 per-capture trace를 등록한다. 호출 지점:
  `internal/llvmgen/fn_value.go:emitFnValueEnv`.
- ✅ **§10.1 A5 Safepoint kind taxonomy** — safepoint id high byte에 kind
  인코딩 (`UNSPECIFIED/ENTRY/CALL/LOOP/ALLOC/YIELD`), 저 56비트에 per-module
  serial. 런타임이 kind별 카운터 집계 + `osty_gc_debug_safepoint_count_by_kind`.
  legacy + MIR 양쪽 emitter 15+ 호출 지점 전부 분류 (entry / call / loop).
  테스트 `TestBundledRuntimeSafepointKindCounters` +
  `TestGenerateFromMIREmitGCLoopSafepoint` 외 회귀.
- ✅ **§10.2 A6 Stackmap overflow guard** — `OSTY_GC_SAFEPOINT_MAX_ROOTS = 65536`
  상한. 런타임이 safepoint 폴마다 `root_slot_count` 확인, 초과 시 kind
  포함한 메시지로 abort. `osty_gc_debug_safepoint_max_roots_seen`로 peak
  관측. LLVM-side alloca 자체는 그대로 — 이는 crash early / crash loud
  backstop. 테스트 `TestBundledRuntimeSafepointRootSlotHighWaterMark`.

### Phase A 심화 보강 (2차 패스)

초기 Phase A 랜딩에서 얕았던 부분들이 아래와 같이 정밀화되었다:

- **A1 심화** — `osty_gc_debug_unsafe_*` corruption injector 11종. fork 기반
  하네스가 invariant별 네거티브 케이스를 실행하고, 각 에러 코드(−1…−11)가
  정확히 매칭되는지 확인. `TestBundledRuntimeValidateHeapNegativeInvariants`.
- **A2 심화** — `clock_gettime(CLOCK_MONOTONIC)` 기반 collection 타이밍.
  `nanos_total` / `nanos_last` / `nanos_max`를 `osty_gc_stats`에 추가, 누적·
  최악 사이클·마지막 사이클 관측. `TestBundledRuntimeCollectionTimingRecorded`.
- **A3 심화** — `osty_gc_find_header`의 O(n) 선형 스캔을 open-addressing +
  linear probing 해시 테이블로 교체 (SplitMix64 믹싱, load factor 0.75
  유지, 초기 cap 128, tombstone 기반 삭제). deep-mark 테스트 28s → <1s.
  capacity / count / tombstones / find_ops 관측.
  `TestBundledRuntimeFindHeaderHashIndex`.
- **A4 심화** — Phase 1 태그 swap을 넘어 `osty.rt.closure_env_alloc_v1`
  전용 allocator + `osty_rt_closure_env_trace` 콜백을 런타임에 구현.
  capture slot array를 직접 순회하는 self-describing 레이아웃
  (`{ ptr fn, i64 capture_count, ptr captures[] }`). llvmgen
  `emitFnValueEnv`가 dedicated 엔트리로 전환 — 현재는 capture_count=0이지만
  Phase 4가 값만 바꿔끼우면 된다. 3개 캡처 보유한 env로 trace 경로 검증:
  `TestBundledRuntimeClosureEnvTracesCaptures`.
- **A5 심화** — E2E 테스트 `TestGenerateFromASTSafepointKindMixCallAndLoop`:
  legacy 에미터가 lower한 IR을 정규식으로 파싱해 kind 분포를 복원,
  CALL + LOOP 존재와 UNSPECIFIED 부재를 검증. MIR 측 ENTRY + LOOP는
  기존 `TestGenerateFromMIREmitGCLoopSafepoint`가 literal serial id로 커버.
- **A6 심화** — `safepointRootChunkSize` (default 4096) 기반 프레임 분할.
  visible root 수가 청크를 넘으면 단일 poll이 여러 `safepoint_v1` 호출로
  쪼개져, 각 `alloca ptr, i64 N`이 bounded 크기 유지. 테스트에서 청크를
  3으로 낮추고 7개의 managed List를 가진 프레임을 lower하여 3/3/1 분할을
  확인: `TestGenerateFromASTSafepointSplitsLargeFrames`. 런타임 abort
  path는 fork 하네스로 exit code + stderr 메시지 검증:
  `TestBundledRuntimeSafepointOverflowAborts`.

Phase A의 열린 items는 없다. Phase B (세대 GC) 진입 준비 완료 — 세대 구현
단계에서 §9.3 누적 카운터 + 타이밍 + §10.1 kind 분류를 즉시 활용 가능하고,
§4.2 work queue는 concurrent marker가 재사용하며, §2.4 closure trace는
Phase 4 capture 랜딩 시 capture_count만 채우면 된다.

## Phase B 진행 상태 (세대 GC)

Phase A의 passive recording (SATB / remembered edge)이 이제 minor GC의 실제
입력으로 사용된다. 모든 할당은 YOUNG으로 시작, 지정된 age에 도달한 survivor는
OLD로 **in-place 승격** (주소 보존, compaction은 Phase D에서).

- ✅ **§1.1 B1 header 확장** — `osty_gc_header`에 `generation` / `age` 추가.
  validate_heap이 세대별 카운트·바이트 일치까지 검증 (-12, -13, -14 invariant).
- ✅ **§5.1-5.3 B2 nursery/tenured 구분** — 단일 heap 유지, 링크/언링크 경로에서
  per-generation 카운터 (`young_count` / `young_bytes` / `old_count` /
  `old_bytes`) 갱신. 할당은 무조건 YOUNG.
- ✅ **§5.4-5.5 B3 minor GC + 승격** — `osty_gc_collect_minor_with_stack_roots`
  가 YOUNG만 스캔. mark_header가 `osty_gc_minor_in_progress` 플래그로 OLD 헤더를
  enqueue에서 제외. survivor는 age++ → `promote_age` 도달시 `promote_header`로
  OLD로 이전. `OSTY_GC_PROMOTE_AGE` env로 튜닝.
- ✅ **§3.5-3.6 B4 remembered set 소비** — minor 단계 4에서 (owner=OLD) edge의
  value를 mark root로 seed. minor 완료 후
  `osty_gc_remembered_edges_compact_after_minor`가 (OLD,YOUNG) 페어만 남겨 재작성.
- ✅ **§9.1 B5 pressure tier** — 2-tier. `OSTY_GC_NURSERY_BYTES` (기본 8192B) /
  `OSTY_GC_THRESHOLD_BYTES`. safepoint dispatcher: heap 한계 초과 → major, 그 외 →
  minor. `osty_gc_debug_collect`는 Phase A 호환을 위해 강제 major 유지; 신규
  `_collect_minor` / `_collect_major`로 명시 tier 지정.
- ✅ **§9.4 B6 minor/full 카운터** — `minor_count` / `major_count` / `*_nanos_total`
  / `promoted_*_total` / `allocated_since_minor`를 `osty_gc_stats`에 추가.
- ✅ **B7 통합 테스트** — 4건:
  - `TestBundledRuntimeMinorCollectSweepsYoungOnly` — minor 한 번으로 YOUNG
    garbage 청소 + survivor age++, major 카운터 0 유지.
  - `TestBundledRuntimePromotesAfterAgeThreshold` — `PROMOTE_AGE=2`에서 2회 minor
    후 YOUNG→OLD 승격, 주소 불변, `promoted_count_total=1`.
  - `TestBundledRuntimeMinorUsesRememberedSetForOldToYoung` — OLD owner에 YOUNG
    child을 post_write로 설치 후 minor에서 child 생존. 추가 minor에서 child 승격
    시 rem set이 0으로 compact.
  - `TestBundledRuntimeNurseryLimitTriggersMinor` — nursery=1KB, heap=1MB 환경에서
    단일 safepoint가 minor만 발동, major=0.

### Phase B 경계 / 후속

- OLD-heavy 워크로드는 결국 major에 의존 — Phase C (incremental) + Phase D
  (compaction) 필요. remembered set 소비 경로가 고정된 덕에 C/D 랜딩 시 minor
  경로는 수정 없음.
- 다음 예정: Phase C (incremental marking). SATB log가 처음으로 소비처를 얻는
  단계. A3 mark work queue 위에 budget step을 얹는 형태.

## Phase C 진행 상태 (incremental marking)

Phase A SATB log가 passive recording에서 **live consumer**로 승격. 마킹
페이즈가 여러 step 호출로 쪼개져 STW 대체 가능.

- ✅ **§4.1 C1 tri-color 명시** — `osty_gc_header.color` (WHITE/GREY/BLACK)
  + `marked` legacy alias. mark_header가 GREY로 push, drain이 BLACK으로
  마무리. sweep이 survivor를 WHITE로 리셋.
- ✅ **§4.3 C2 incremental mark** — 상태 머신 3단계 (IDLE → MARK_INCREMENTAL
  → SWEEPING → IDLE). 3개 entry point:
  `osty_gc_collect_incremental_start_with_stack_roots` (seed roots),
  `osty_gc_collect_incremental_step(budget)` (drain N greys, returns
  has_more), `osty_gc_collect_incremental_finish` (final drain + sweep).
- ✅ **§2.7 C5 SATB consume** — `pre_write_v1`이 MARK_INCREMENTAL 상태일 때
  old_value를 GREY로 칠한다. snapshot-at-the-beginning 보장: mark 시작
  시점에 live했던 포인터는 mark 완료 시점까지 생존. `satb_barrier_greyed_total`
  관측.
- ✅ **Validate 확장** — 3개 새 invariant: `-17 INVALID_COLOR`,
  `-18 COLOR_MARKED_MISMATCH`, `-19 NONWHITE_OUTSIDE_MARK`. `mark_stack_count`
  검사는 IDLE 상태에서만 (MARK_INCREMENTAL은 live grey set 보유).
- ✅ **테스트 3건**:
  - `TestBundledRuntimeIncrementalMarkStepByStep` — 상태 머신 + 단일 step
    으로 단순 sweep.
  - `TestBundledRuntimeIncrementalBudgetDrainsLongChain` — 51 work units
    을 budget=10으로 6 step에 완주.
  - `TestBundledRuntimeIncrementalSATBBarrierGreysOldValue` — mark 시작 후
    child을 list에서 제거(overwrite)했을 때 SATB가 GREY로 칠해서 생존.
    `satb_barrier_greyed_total = 1`.

### Phase C 깊이 패스 (2차 랜딩)

초기 Phase C가 얕았던 부분들을 메꿨다:

- ✅ **C1 깊이** — `-17 INVALID_COLOR`, `-18 COLOR_MARKED_MISMATCH`,
  `-19 NONWHITE_OUTSIDE_MARK` 각각에 대한 corruption injector + fork 하네스
  네거티브 테스트 (`TestBundledRuntimeValidateHeapNegativeInvariantsPhaseC`).
- ✅ **C2 깊이 — STW guard** — `osty_gc_collect_major_with_stack_roots`와
  `osty_gc_collect_minor_with_stack_roots`가 MARK_INCREMENTAL/SWEEPING
  상태에서 호출되면 즉시 abort. mark stack 실수 stomp 방지.
- ✅ **C3 §9.2 mutator assist** — `osty_gc_allocate_managed`가
  MARK_INCREMENTAL 동안 `payload_size / bytes_per_unit` units 만큼 grey
  queue 드레인. `OSTY_GC_ASSIST_BYTES_PER_UNIT` 환경 변수 (기본 128).
  `mutator_assist_work_total` / `mutator_assist_calls_total` 관측.
- ✅ **Auto-dispatcher 편입** — `OSTY_GC_INCREMENTAL=1` 시 safepoint가
  major 대신 incremental로 라우팅. `OSTY_GC_INCREMENTAL_BUDGET` (기본 64)
  units per safepoint. queue 드레인 시 자동으로 finish + IDLE 복귀.
- ✅ **C5 깊이 — SATB 시나리오 3종** — `TestBundledRuntimeSATBBarrierScenarios`:
  (a) MARK 이전 barrier = no-op, (b) 같은 slot 연속 overwrite시 각
  old_value 개별 grey, (c) finish 이후 barrier = no-op.
- ✅ **Incremental stress test** — `TestBundledRuntimeIncrementalStress`:
  15 cycle × 랜덤 budget + 랜덤 alloc + 배리어 write 조합, 각 quiescent
  point에서 validate_heap 체크, deterministic srand(7).

### Phase C 경계 / 후속

- **§4.4 mostly-concurrent** — 여전히 ❌. 지금은 단일 스레드 incremental만
  — mutator가 step 사이에만 활동. 진짜 concurrent는 GC 스레드를 띄우는
  작업으로 Phase D 근처.
- **Minor + incremental 상호작용** — 두 cycle이 동시에 돌 수 없도록 abort
  guard는 추가됐지만, 혼합 워크로드 (긴 OLD scan + 빠른 YOUNG churn)에서
  가장 좋은 선택 (incremental vs STW minor)을 결정하는 정책은 아직 없음.
  단순한 tier 분리만.
- **Go/Osty 경계는 축소됐지만 아직 남아 있음** — A5/A6/A4의 kind/ID
  상수, safepoint 빈 poll / rooted poll 템플릿, closure env alloc 템플릿,
  rooted safepoint chunk planning 정책, bare-fn thunk symbol/body template,
  legacy fn-value indirect-call IR template은 이제
  `toolchain/llvmgen.osty` + `support_snapshot.go`가 소유한다. 남은 Go 코드는
  legacy emitter의 상태 의존부(visible-root 주소 materialize, thunk cache
  소유, indirect-call callee shape 판별)라 correctness 위반이라기보다
  점진적 cleanup 대상이다.

## 다음 단계

Phase C 깊이 패스 또는 Phase D (compaction). Phase C 깊이로 가면:
§9.2 mutator assist → §4.4 concurrent thread → auto-dispatcher에 incremental
편입 → minor + incremental 혼합.

Phase D는 §1.7 stable ID → §6.2-6.3 forwarding + evacuation → §8.1-8.2 pin
API → §1.3-1.6 region heap (bump/freelist/TLAB/size class).

### Phase D 진행 상태

- ✅ **D1 stable logical identity** — 모든 live header에 monotonic
  `stable_id`를 부여하고 stable-id → header index를 추가.
- ✅ **D2 forwarding + evacuation (movable subset)** — major GC 후
  movable survivor를 clone/replace하고 stack/global/object slot을 remap.
  `load_v1`는 stale payload를 forwarding table로 canonicalize한다.
- ✅ **D4 forwarding history retention** — 반복 compaction 뒤에도 예전
  payload alias를 stable-id 기준으로 최신 header에 재결합한다.
- ✅ **D3 pin API** — `pin_count`와 `osty.gc.pin_v1` /
  `osty.gc.unpin_v1`를 추가해서 evacuation 제외 대상을 명시할 수 있다.
- 🟡 **남은 Phase D tail** — Survivor/from-to space /
  survivor-old-pinned 전용 TLAB depth / young compaction-safe recycle /
  pinned region-local recycle 분리는 아직 후속이다.

## 유지 규칙

- 이 문서는 **계획 보조 자료**이지 스펙이 아니다. 실제 델타 해소는 해당 P1/P2
  항목 랜딩 시 그 커밋에서 표를 같이 갱신.
- [`RUNTIME_GC.md`](./RUNTIME_GC.md) working rule 우선 — 구현은 LLVM lowering +
  런타임 + backend test 경로에 먼저 들어간다. `examples/gc`는 해당 기능이
  명확해진 뒤에 clarify / archive 용으로만 업데이트.
- §3.7 (store site 배리어 hookup)처럼 "lowering은 약속, 런타임은 no-op" 패턴이
  또 발견되면 이 문서에 P1으로 추가.
