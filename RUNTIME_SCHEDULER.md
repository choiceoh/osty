# RUNTIME_SCHEDULER.md — Osty 런타임 스케줄러 아키텍처 & 단계 로드맵

> **Status (2026-04):** Phase 1A 진행 중. 이 문서는 `LANG_SPEC_v0.5/08-concurrency.md` §8.0과 `LANG_SPEC_v0.5/19-runtime-primitives.md` §19.1 "GC × scheduler interaction" 절에 대응하는 **구현 레퍼런스**다. 스펙은 관찰 가능한 계약만 고정하고, 이 문서는 공개 LLVM 백엔드의 참조 런타임이 어떤 단계로 그 계약을 만족하는지 기술한다.

## 배경

스펙 §8은 구조적 동시성 (`taskGroup`, `spawn`, `Handle.join`, 채널, `thread.select`, 취소)을 정의하지만 **스케줄러 형상**을 숨겨 왔다. v0.4부터 스펙 §8.0은 다음을 명시한다:

- 스케줄러는 **M:N**이다. 프로그램은 OS 스레드 정체성에 의존하지 못한다.
- 양보점 목록은 고정됨 (channel ops, `join`, `yield`, `sleep`, `select` blocking arms, GC safepoint, cancel check).
- 선점은 **v0.4 범위 밖**. cooperative만 요구된다.
- 병렬 실행은 **허용되나 보장되지 않는다**. 1-worker 구현도 스펙 준수.

이 문서는 이 계약을 만족하는 실제 런타임을 **단계별로** 어떻게 쌓는지 규정한다. 각 단계는 (a) 무엇을 하는가, (b) 스펙 대비 어떤 제약을 남기는가, (c) 어떤 테스트로 검증되는가를 적는다.

## 설계 축

1. **ABI 안정성.** `osty_rt_task_*`, `osty_rt_thread_chan_*`, `osty_rt_select_*`, `osty_rt_cancel_*` 심볼 집합은 단계가 바뀌어도 고정된다. Phase 1A의 sequential 구현이 수출하는 심볼 목록과 시그니처는 Phase 2의 work-stealing 구현이 수출하는 것과 동일하다. 백엔드는 재컴파일 없이 새 런타임과 링크된다.
2. **GC 협력.** 모든 단계는 §19 STW GC와 공존한다. 단계가 올라갈수록 collector-scheduler 상호작용이 복잡해진다 (Phase 1A: 자명, Phase 1B: 양보점에서 root registration, Phase 3: concurrent).
3. **단조성.** 높은 단계는 낮은 단계보다 프로그램이 관찰 가능한 방식으로 **더 빠를 뿐** 의미는 같다. 예외는 `race` tie-break과 scheduler 내부 순서 — 이 둘은 스펙이 이미 비결정적으로 선언.
4. **구조적 불변식.** Handle/Group non-escape (E0743), 취소 전파, 자식 join-on-parent-return, defer×cancel(§8.4)는 모든 단계에서 성립.

## 단계 로드맵

### Phase 1A — Sequential ABI filler (현재)

**목표.** 백엔드가 lower하는 concurrency 심볼 집합을 **모두 제공**해서 LLVM 경로 프로그램이 링크/실행되게 하기. 실제 M:N은 없음 (worker 수 N=1, green task 수 M=1).

**의미론.**
- `osty_rt_task_group(body)`: `body(NULL)`을 호출하는 thread에서 즉시 실행. 완료되면 결과 반환.
- `osty_rt_task_spawn(body)` / `osty_rt_task_group_spawn(group, body)`: 호출 시점에 `body`를 즉시 실행, 결과를 Handle에 저장. `spawn`은 호출자를 블로킹한다 (진짜 병렬 없음).
- `osty_rt_task_handle_join(h)`: 이미 저장된 결과 반환. 블로킹 없음.
- `osty_rt_task_group_cancel(g)`: 그룹 플래그 set. 이후 자식 호출에서 `is_cancelled()`가 true.
- `osty_rt_cancel_is_cancelled()` / `..._group_is_cancelled(g)`: 플래그 read.
- `osty_rt_thread_yield()`: no-op.
- `osty_rt_thread_sleep(ns)`: calling thread blocking sleep.

**제약.**
- **채널/select은 Phase 1A에서 트랩**: `osty_rt_thread_chan_*`, `osty_rt_select_*`는 abort-with-message stub. 채널 의존 프로그램은 런타임 에러. 이유: 채널은 블록/wake 필요 → 진짜 fiber 또는 다수 OS 스레드 필요 → Phase 1B의 핵심.
- **`collectAll` / `race`**: stub (abort). Phase 1A 스코프 밖.
- 병렬 실행 없음 → spec §8.0이 허용 ("병렬은 보장되지 않는다").
- GC 상호작용: 자명. 모든 task가 호출자 스택에서 동기적으로 실행되므로 parked-root 문제 없음. `safepoint_v1`이 평소대로 동작.

**수용 테스트.**
- `taskGroup(|g| { let h = g.spawn(|| 42); h.join() })` → 42 반환.
- `taskGroup`이 자식 `Err(e)` 전파 (§8.2).
- `taskGroup` 탈출 시 `Handle` 사용은 E0743 (컴파일 타임, 런타임 미관련).
- `thread.sleep(10.ms)` 지연 관찰.
- 런타임 심볼 빠짐 없음 (링크 에러 0).

### Phase 1B — Single-worker cooperative fibers

**목표.** 채널과 select가 동작. 한 OS 스레드 안에서 `ucontext`(POSIX) 기반 cooperative green fiber 다수. 관찰 가능한 M:N 병렬성은 여전히 없지만 **블록/wake 의미론은 정확**.

**추가되는 것.**
- Fiber struct: stack (min 64KB, guarded), saved ucontext, state (runnable/parked/dead), live-root snapshot (for GC).
- Global FIFO ready queue.
- Scheduler loop: pop runnable → switch → switch 돌아오면 다음.
- Channel 진짜 blocking: sender가 버퍼 꽉 차면 fiber park, receiver가 recv 시 unpark.
- `select`: arm별 ready 검사 → 없으면 park on all arms → 첫 wake로 resume.
- `join`: target fiber dead까지 park.
- `yield`: ready queue에 reinsert 후 switch.
- GC 확장: park 시 fiber가 자기 live-root 배열을 per-fiber slot에 복사. 수집 시 walker는 현재 fiber의 safepoint root 배열 + 모든 parked fiber의 stored root 배열을 union으로 본다.

**제약.**
- OS 스레드 1개. 실제 CPU 병렬 없음 (멀티코어 활용 0%).
- FFI blocking 호출은 전체 fiber 풀을 정지시킴. 알려진 한계.

**수용 테스트.**
- Producer/consumer with `thread.chan::<Int>(4)` 동작.
- `taskGroup`에서 10K spawn → 완료 (fiber 비용 검증).
- `thread.select` 4-arm (recv/send/timeout/default) 동작.
- `Handle.join`이 자식 완료 전까지 부모 대기.
- GC 테스트: `taskGroup(|g| { g.spawn(|| allocLoop()); allocLoop() })` — 할당-양보-할당 사이클에서 collector가 parked-fiber root를 놓치지 않음.

### Phase 2 — Multi-worker M:N

**목표.** 실제 CPU 병렬. 관찰 가능한 모델 동일.

**추가되는 것.**
- N 개의 OS worker 스레드 (default `min(CPUs, 8)`, env `OSTY_SCHED_WORKERS`).
- Global ready queue → per-worker deque + work stealing (Chase-Lev 혹은 단순 lock queue로 시작).
- Channel send/recv lock은 per-channel mutex; select는 two-phase lock (arms 정렬 → 순차 lock).
- GC STW: collection 시작 worker가 `stop_requested = 1` flag set → 모든 worker가 다음 safepoint에서 park → collector 돈 뒤 release.
- cooperative preemption: worker가 safepoint 체크 주기 ≤ N ms (tunable).

**제약.**
- 여전히 non-preemptive. 긴 compute 루프는 다른 task의 진행을 막음.
- concurrent GC 없음. STW pause는 모든 할당 스레드 멈춤.

**수용 테스트.**
- `parallel(items, 8, f)`이 실제로 8-way 병렬 (wall-clock ≈ N_items / 8 × per-item).
- Stress: 1M spawn across 8 workers 안정.
- GC 레이스 없음 (TSan clean).
- `race()`의 비결정 tie-break이 observable (stable within run, varies across runs).

### Phase 3 — Preemption & concurrent GC

**목표.** 긴 compute 루프도 양보. GC pause 최소화. 프로덕션-grade.

**추가되는 것.**
- 컴파일러: loop backedge 마다 `osty.sched.preempt_check_v1` emit.
- 런타임: preempt_check는 `stop_requested` 검사 → set이면 safepoint로 위임.
- 추가: 시그널 기반 async preemption (Go 1.14+ 스타일 SIGURG).
- GC: concurrent tri-color marking + write barriers (§19.8 pre/post_write는 이미 존재). STW pause는 root scan + stack flush에만.

**제약.** 없음 (target state).

**수용 테스트.**
- 무한 compute 루프에서도 `thread.select` timeout arm 발화.
- GC pause < 1ms on 100MB heap.
- SIGURG inject 후 크래시 없음.

## ABI 계약 (모든 단계 불변)

```c
// task group
void *osty_rt_task_group(void *body_env);      // returns result ptr
void *osty_rt_task_spawn(void *body_env);      // returns Handle*
void *osty_rt_task_group_spawn(void *group, void *body_env);
void *osty_rt_task_handle_join(void *handle);  // returns result ptr
void  osty_rt_task_group_cancel(void *group);
bool  osty_rt_task_group_is_cancelled(void *group);
bool  osty_rt_cancel_is_cancelled(void);

// thread utilities
void  osty_rt_thread_yield(void);
void  osty_rt_thread_sleep(int64_t nanos);

// channels (Phase 1B부터 functional, 1A에서는 abort stub)
void *osty_rt_thread_chan_make(int64_t capacity);
void  osty_rt_thread_chan_close(void *ch);
bool  osty_rt_thread_chan_is_closed(void *ch);
void  osty_rt_thread_chan_send_i64(void *ch, int64_t v);
void  osty_rt_thread_chan_send_i1(void *ch, bool v);
void  osty_rt_thread_chan_send_f64(void *ch, double v);
void  osty_rt_thread_chan_send_ptr(void *ch, void *v);
void  osty_rt_thread_chan_send_bytes_v1(void *ch, const void *src, int64_t sz);
struct recv_result { int64_t value_or_ptr; int64_t ok; };
struct recv_result osty_rt_thread_chan_recv_i64(void *ch);
// ... same for i1/f64/ptr/bytes_v1

// select (Phase 2부터 functional)
void osty_rt_select(void *s);
void osty_rt_select_recv(void *s, void *ch, void *arm);
void osty_rt_select_send(void *s, void *ch, void *arm);
void osty_rt_select_timeout(void *s, int64_t ns, void *arm);
void osty_rt_select_default(void *s, void *arm);

// helpers (Phase 2+)
void *osty_rt_task_race(void *body);
void *osty_rt_task_collect_all(void *body);
```

**계약.** 백엔드는 이 시그니처에 맞춰 lower한다 ([mir_generator.go:1749](internal/llvmgen/mir_generator.go:1749) 이하). 런타임이 단계 이전되어도 **프로그램 재컴파일 불필요**.

## 스펙 참조 매트릭스

| 스펙 조항 | Phase 1A | Phase 1B | Phase 2 | Phase 3 |
|---|---|---|---|---|
| §8.0 M:N + no thread identity | trivially (N=M=1) | trivially (N=1) | ✓ | ✓ |
| §8.1 structured lifetime | ✓ | ✓ | ✓ | ✓ |
| §8.2 failure propagation | ✓ | ✓ | ✓ | ✓ |
| §8.3 `parallel` bounded | sequential stub | concurrent on 1 worker | actual N-way | ✓ |
| §8.3 `race` nondeterministic tie | N/A (no concurrency) | N/A | observable | observable |
| §8.4 cancel with cause | ✓ | ✓ | ✓ | ✓ |
| §8 channels blocking | **abort stub** | ✓ | ✓ | ✓ |
| §8 `thread.select` | **abort stub** | ✓ | ✓ | ✓ |
| §19.1 STW GC | ✓ | ✓ + parked-root union | ✓ worker sync | concurrent |
| §19.10 safepoint contract | unchanged | extended (park-as-safepoint) | extended (stop_requested flag) | extended (preempt_check) |

## 파일 인벤토리

- `internal/backend/runtime/osty_runtime.c` — GC + collection intrinsics + (Phase 1A부터) task/thread/chan/select 심볼.
- `internal/llvmgen/mir_generator.go` — 심볼 lowering. 변경 시 이 문서 ABI 섹션 동기화.
- `internal/backend/llvm_runtime.go` — 런타임 소스 embed. 파일 분리 시 두 번째 embed 추가.
- `LANG_SPEC_v0.5/08-concurrency.md` §8.0 — 스케줄러 모델 고정.
- `LANG_SPEC_v0.5/19-runtime-primitives.md` §19.1 "GC × scheduler interaction" — 수집기와 스케줄러 상호작용.

## 비-목표

- Async/await 구문. Osty 문법은 이를 가지지 않으며, 스펙 §14 (excluded features)에 준함. 함수 coloring은 영구적으로 거부.
- Actor model. Erlang/BEAM-스타일 고립. 현재 스펙에 부합하지 않음.
- User-space scheduling hooks. 스케줄러 정책은 런타임 내부. `OSTY_SCHED_WORKERS` 외 노출되는 튜닝 없음 (Phase 2).

## 변경 이력

- 2026-04-19: 문서 신설, Phase 1A 착수.
