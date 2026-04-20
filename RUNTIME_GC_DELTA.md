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
| 1.1 | 오브젝트 헤더                                | ✅   | ✅  | 시뮬엔 age/pin/forwarding/remembered bit 추가        | P2 | `lib.osty:94-108` vs `osty_runtime.c:15-26`          |
| 1.2 | 지역별 힙 (Eden/Survivor/Old/Humongous/Pinned) | ✅ | ❌ | 단일 heap                                            | P2 | `lib.osty:27-33`                                     |
| 1.3 | Bump allocator                               | ✅   | ❌  | `calloc()` 직접                                      | P3 | `lib.osty:127-147`                                   |
| 1.4 | Free list (tenured / humongous)              | ✅   | ❌  | `free()` → OS                                        | P3 | `lib.osty:122-147`                                   |
| 1.5 | Size class / large object threshold          | ✅   | 🟡 | `object_kind`는 있으나 할당 전략 동일                | P3 | `lib.osty:709-764`                                   |
| 1.6 | TLAB (스레드별 할당 버퍼)                    | ✅   | ❌  | 전부                                                 | P3 | `lib.osty:111-120`                                   |
| 1.7 | Stable object identity (movable와 분리)      | ✅   | ❌  | address가 identity — compaction 전제                 | P3 | `lib.osty:94-108`                                    |

## 2. 루트

| #   | 기능                                | 시뮬 | 실  | 델타                                      | P  | 참조                              |
| --- | ----------------------------------- | ---- | --- | ----------------------------------------- | -- | --------------------------------- |
| 2.1 | Stack root (safepoint 기반)         | ✅   | ✅  | —                                         | —  | `osty_runtime.c:1614-1623`        |
| 2.2 | Manual root bind/release            | ✅   | ✅  | 시뮬은 list, 실은 refcount                | —  | `osty_runtime.c:1592-1612`        |
| 2.3 | ~~Global / static root 테이블~~     | ✅   | ✅  | slot-address 등록, safepoint mark에서 순회 — PR #400 후속 | ✅ done | `osty_runtime.c:osty_gc_global_root_register_v1` |
| 2.4 | Closure capture root                | ✅   | 🟡 | capture struct은 일반 alloc, slot descriptor 없음 | P2 | `lib.osty:618-622`          |
| 2.5 | Async frame root                    | ✅   | ❌  | async 구현 전까지 대기                    | P3 | `lib.osty:624-631`                |
| 2.6 | Derived-base root pair              | ✅   | ❌  | inner pointer 대응                        | P3 | `lib.osty:582-600`                |
| 2.7 | Incremental shade-on-add            | ✅   | ❌  | concurrent marking 전제                   | P3 | `lib.osty:1011-1019`              |

## 3. 쓰기 / 읽기 배리어

| #   | 기능                                              | 시뮬 | 실  | 델타                                       | P      | 참조                                |
| --- | ------------------------------------------------- | ---- | --- | ------------------------------------------ | ------ | ----------------------------------- |
| 3.1 | **`pre_write_v1` (SATB snapshot)**                | ✅   | 🟡 | 카운터 + collection 플래그만               | **P1** | `osty_runtime.c:1541-1557`          |
| 3.2 | **`post_write_v1` (generational / incremental)**  | ✅   | 🟡 | 카운터만, edge 기록 없음                   | **P1** | `osty_runtime.c:1559-1578`          |
| 3.3 | `load_v1` (relocation-ready)                      | ✅   | 🟡 | identity return                            | P3     | `osty_runtime.c:1580-1586`          |
| 3.4 | `mark_slot_v1` (aggregate trace)                  | ✅   | ✅  | —                                          | —      | `osty_runtime.c:1588-1590`          |
| 3.5 | 카드 테이블 (dirty card bitmap)                   | ✅   | ❌  | generational 전제                          | P2     | `lib.osty:183-189`                  |
| 3.6 | Remembered set (old→young 정확 집합)              | ✅   | ❌  | generational 전제                          | P2     | `lib.osty:689, 1093-1114`           |
| 3.7 | **필드 / 원소별 store site 배리어 hookup**        | ✅   | 🟡 | lowering은 call emit, 런타임이 no-op       | **P1** | `lib.osty:485-517` · `generator.go` |

lowering에서는 struct 필드, tuple/enum payload, receiver 필드, array/list/map/set
원소, runtime frame, async frame 모든 store site에서 배리어를 부르도록 이미
emit한다 (3.7). 런타임이 실동작을 붙이지 않아 3.1-3.2는 사실상 기장 역할만
한다 — 이 격차가 P1의 본질.

## 4. 마킹

| #   | 기능                                        | 시뮬 | 실  | 델타                                       | P  | 참조                          |
| --- | ------------------------------------------- | ---- | --- | ------------------------------------------ | -- | ----------------------------- |
| 4.1 | 트라이-컬러 (White / Grey / Black)          | ✅   | 🟡 | binary `marked` bit + 재귀                 | P2 | `osty_runtime.c:343-368`      |
| 4.2 | Work queue (스택-안전)                      | ✅   | ❌  | 재귀 — 깊은 그래프에서 stack overflow 위험  | P2 | `osty_runtime.c:219-334`      |
| 4.3 | Incremental marking (budget step)           | ✅   | ❌  | STW 단일 pass                              | P3 | `lib.osty:1143-1159`          |
| 4.4 | Mostly-concurrent mark + final remark       | ✅   | ❌  | —                                          | P3 | `lib.osty:252-262`            |
| 4.5 | 타입 descriptor 기반 ref 투영               | ✅   | ✅  | —                                          | —  | `osty_runtime.c:219-334`      |

## 5. 세대

| #   | 기능                                  | 시뮬 | 실  | 델타 | P  |
| --- | ------------------------------------- | ---- | --- | ---- | -- |
| 5.1 | Nursery / Eden                        | ✅   | ❌  | 전부 | P2 |
| 5.2 | Survivor space + budget               | ✅   | ❌  | 전부 | P2 |
| 5.3 | Tenured (old)                         | ✅   | ❌  | 전부 | P2 |
| 5.4 | 승격 정책 (age 비트, 임계)            | ✅   | ❌  | 전부 | P2 |
| 5.5 | Minor GC 트리거 (nurseryLimit)        | ✅   | ❌  | 전부 | P2 |

세대 도입은 §3.1 – §3.2 (실 write barrier)가 먼저. barrier 없이 세대 올리면
old→young edge를 놓친다.

## 6. 스윕 · 재활용

| #   | 기능                                   | 시뮬 | 실  | 델타                               | P  | 참조                          |
| --- | -------------------------------------- | ---- | --- | ---------------------------------- | -- | ----------------------------- |
| 6.1 | Mark-sweep 전체 heap                   | ✅   | ✅  | —                                  | —  | `osty_runtime.c:389-400`      |
| 6.2 | Compaction / forwarding table          | ✅   | ❌  | stable ID (§1.7) 전제              | P3 | `lib.osty:215-222`            |
| 6.3 | Evacuation (region 이전, pinned skip)  | ✅   | ❌  | —                                  | P3 | `lib.osty:224-230`            |
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
| 8.1 | Pin bit (evacuation 제외)              | ✅   | ❌  | compaction 도입 시 필요                | P3  |
| 8.2 | pin / unpin API                        | ✅   | ❌  | —                                      | P3  |
| 8.3 | LLVM pinned handle ref (FFI)           | ✅   | 🟡 | `root_bind`로 의미는 동치, API 격차   | P2  |
| 8.4 | 유저 파이널라이저                      | ❌   | ❌  | 설계상 없음                            | N/A |
| 8.5 | 런타임 내부 destroy 콜백               | ✅   | ✅  | —                                      | —   |

## 9. 텔레메트리 · 트리거

| #   | 기능                                                  | 시뮬 | 실  | 델타                                 | P  | 참조                          |
| --- | ----------------------------------------------------- | ---- | --- | ------------------------------------ | -- | ----------------------------- |
| 9.1 | Allocation pressure threshold                         | ✅   | ✅  | 실은 단일 tier (`OSTY_GC_THRESHOLD_BYTES`) | P2 | `osty_runtime.c:84-88`   |
| 9.2 | Mutator assist (allocation debt)                      | ✅   | ❌  | incremental 전제                     | P3 | `lib.osty:242-250`            |
| 9.3 | `GcStats` 구조체 (cycle / promoted / compacted / …)   | ✅   | 🟡 | debug counter 수 개                  | P2 | `lib.osty:71-92`              |
| 9.4 | Collection trace 6종 (Minor / Full / Evac / Remap / Incr / Concurrent) | ✅ | ❌ | 진단 커버리지       | P3 | `lib.osty:191-262`            |
| 9.5 | `validateHeap()` 헬퍼                                 | ✅   | ❌  | 테스트 오라클로 유용                 | P2 | `lib.osty:264-294`            |

## 10. Safepoint · 동시성

| #    | 기능                                                           | 시뮬 | 실  | 델타                                    | P  | 참조                          |
| ---- | -------------------------------------------------------------- | ---- | --- | --------------------------------------- | -- | ----------------------------- |
| 10.1 | Safepoint 종류 (call / alloc / loop-backedge / async-yield)    | ✅   | 🟡 | 단일 kind — id만 무시                   | P2 | `lib.osty:519-525`            |
| 10.2 | 스택맵 (라이브 bitmap + overflow)                              | ✅   | 🟡 | LLVM이 매번 slot 포인터 배열 emit       | P2 | `lib.osty:650-667`            |
| 10.3 | STW 베이스라인                                                 | ✅   | ✅  | —                                       | —  | —                             |
| 10.4 | 멀티스레드 (thread-local state)                                | ✅   | ❌  | 단일 스레드                             | P3 | `lib.osty:276-286`            |
| 10.5 | Stress 모드 (every-safepoint GC)                               | ❌   | ✅  | 실만 가진 장점 — 시뮬 역이식 검토       | —  | `osty_runtime.c:410-424`      |

## 권장 시작점 (P1 요약)

1. **실 write barrier (§3.1, §3.2, §3.7)**. lowering은 이미 모든 store site에서
   pre/post-write를 부르고 있는데 런타임이 카운터만 증가시킨다. SATB log 또는
   remembered-set buffer 중 하나를 선택해 실제 edge를 기록하기 시작 — 이게
   §5 (세대)와 §4.3 (incremental)의 공통 전제.
2. **Global / static root 테이블 (§2.3)**. stdlib / prelude의 전역 값이 늘어나면
   바로 막힌다. 헤더 구조 + 등록 API + safepoint의 루트 스캔 순회에 합류.

위 두 개가 끝나면 §5 (세대) → §4.3 (incremental) → §6.2-6.3 (compaction, stable
ID §1.7 포함) 순으로 열린다.

## 유지 규칙

- 이 문서는 **계획 보조 자료**이지 스펙이 아니다. 실제 델타 해소는 해당 P1/P2
  항목 랜딩 시 그 커밋에서 표를 같이 갱신.
- [`RUNTIME_GC.md`](./RUNTIME_GC.md) working rule 우선 — 구현은 LLVM lowering +
  런타임 + backend test 경로에 먼저 들어간다. `examples/gc`는 해당 기능이
  명확해진 뒤에 clarify / archive 용으로만 업데이트.
- §3.7 (store site 배리어 hookup)처럼 "lowering은 약속, 런타임은 no-op" 패턴이
  또 발견되면 이 문서에 P1으로 추가.
