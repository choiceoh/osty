# RUNTIME_SCHEDULER.md — Osty 런타임 스케줄러 아키텍처 & 단계 로드맵

> **Status (2026-04):** Phase 1B/2 하이브리드 진행 중. pthread 기반 task spawn/join + mutex/cond 기반 채널이 공개 런타임에 들어왔다. Select/parallel/race/collectAll은 여전히 abort 스텁. 이 문서는 `LANG_SPEC_v0.5/08-concurrency.md` §8.0과 `LANG_SPEC_v0.5/19-runtime-primitives.md` §19.1 "GC × scheduler interaction" 절에 대응하는 **구현 레퍼런스**다. 스펙은 관찰 가능한 계약만 고정하고, 이 문서는 공개 LLVM 백엔드의 참조 런타임이 어떤 단계로 그 계약을 만족하는지 기술한다.

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

### Phase 1A — Sequential ABI filler (완료, 대체됨)

Phase 1A는 `osty_rt_task_*` / `osty_rt_thread_*` 심볼 집합을 먼저 링크 가능한 형태로 노출하는 데 초점이 있었다. 모든 task body를 호출자 스레드에서 동기 실행하고, 채널/select/helper는 abort 스텁이었다. 현재 Phase 1B/2 pthread 경로에 의해 대체된다 — ABI는 불변이며, Phase 1A 수용 테스트는 Phase 1B 경로 위에서도 그대로 통과한다.

### Phase 1B / Phase 2 — pthread-backed (현재)

**목표.** 실제 OS 스레드 기반 병렬 실행. 채널 block/wake 의미론 정확. 관찰 가능한 M:N 병렬성 제공.

**구현.**
- `osty_rt_task_spawn` / `osty_rt_task_group_spawn`: `pthread_create`로 새 OS 스레드에서 body 실행. 스레드 진입 시 `osty_concurrent_workers` 증가. `pthread_cleanup_push`로 등록된 cleanup handler가 정상/비정상 종료 공히 카운터 감소와 `done` flag release store를 보장 (pthread_cancel / pthread_exit / body abort 경로에서도 누수 없음).
- `osty_rt_task_handle_join`: `pthread_join`으로 스레드 완료 대기. handle의 result는 acquire-load로 읽음.
- `osty_rt_task_group(body)`: 호출 스레드에서 body 실행하고 TLS에 group을 바인딩. 자식 스레드는 `h->group`을 통해 트램펄린에서 TLS를 상속받음. body 리턴 시점에 `osty_rt_task_group_reap`가 group의 child 리스트를 훑고 unjoined 스레드를 pthread_join으로 회수 (spec §8.1 structured lifetime).
- Cancellation: `cancelled` flag는 atomic (release store / acquire load). group flag set은 subsequent `is_cancelled` 호출에서 관찰 가능.
- 채널: ring buffer + `pthread_mutex_t mu` + `pthread_cond_t not_full` + `pthread_cond_t not_empty`. Capacity 0은 1로 clamp (진짜 rendezvous는 Phase 1B fiber 영역, 미구현 표기).
- `osty_rt_select` (recv / timeout / default 지원): 내부 builder 할당 → body에 `(env, s)` 로 호출 → body가 arm 등록 → polling 루프에서 recv 시도, default는 즉시 발화, timeout은 deadline 도달 시 발화. 500μs backoff로 busy-spin 회피.
- `osty_rt_select_send`: 아직 MIR이 value-register surface를 emit하지 않아 보류 (`osty_sched_unimplemented`). 4-arg 시그니처로 전환될 때 완성.
- `osty_rt_thread_yield`: `sched_yield()` 호출.
- `osty_rt_thread_sleep(ns)`: 기존 `nanosleep` 경로 유지.
- GC 상호작용: 모든 `osty_gc_allocate_managed` 호출과 write barrier (`pre_write_v1`, `post_write_v1`, `load_v1`, `root_bind_v1`, `root_release_v1`)는 recursive mutex `osty_gc_lock`으로 직렬화. 자동 collection은 `osty_concurrent_workers > 0`인 동안 **보류**된다 (현재 스레드만의 stack root로는 sibling 스레드의 root를 커버할 수 없음). 모든 worker가 join된 뒤 재개. Concurrent collection은 Phase 3.

**제약.**
- `osty_rt_select_send`, `osty_rt_task_race`, `osty_rt_task_collect_all`, `osty_rt_parallel`: 여전히 abort 스텁. 이들의 등록/실행 surface는 MIR이 아직 emit하지 않는 register allocation (send의 경우 값 슬롯, parallel의 경우 list element size / trace fn) 을 필요로 함. 도달 시 `osty_sched_unimplemented("...")` 진단 후 abort.
- GC는 worker-live 동안 일시 보류 → long-running concurrent workload에서 OOM 가능. Phase 3 concurrent collector가 해소.
- Thread-per-task 모델 — 큰 task 수에서 TLS/stack 오버헤드. Chase-Lev deque + work-stealing 기반 worker pool은 Phase 2+ 후속 작업.

**수용 테스트.** `internal/backend/llvm_runtime_sched_test.go`:
- `TestBundledRuntimeScheduler`: taskGroup + spawn/join + cancel 전파 + 4-way parallel spawn wall-clock 검증.
- `TestBundledRuntimeSchedulerChannels`: 프로듀서/컨슈머 cross-thread, close-then-drain, `is_closed`.
- `TestBundledRuntimeSchedulerTaskGroupAutoReap`: body가 forgetful하게 자식을 남겨두고 리턴해도 group teardown이 pthread_join으로 회수함을 확인.
- `TestBundledRuntimeSchedulerChannelStress`: 4 producer × 4 consumer × 250 items/producer = 1000 items 전송을 capacity-16 채널 위에서 돌려 produced/consumed 합 일치.
- `TestBundledRuntimeSchedulerSelect`: recv-ready, timeout-only, default-present 3가지 시나리오에서 올바른 arm이 선택되는지 확인.
- `TestBundledRuntimeSchedulerSelectSendStubAborts`: 미구현된 send arm이 loud-fail 함을 고정.

### Phase 1B — Single-worker cooperative fibers (대안 경로, 미채택)

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
| §19.1 STW GC | ✓ | ✓ + parked-root union | **paused while workers live** | concurrent |
| §19.10 safepoint contract | unchanged | extended (park-as-safepoint) | gated on `osty_concurrent_workers` | extended (preempt_check) |

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
- 2026-04-20: pthread 기반 spawn/join + mutex/cond 채널 런타임 착수. task_group/spawn/handle_join/cancel이 실제 병렬로 동작. `osty_gc_lock` 도입, 자동 collection은 `osty_concurrent_workers > 0`인 동안 보류.
- 2026-04-20 (follow-up): write barrier / root bind/release 전부 `osty_gc_lock`으로 감쌈; pthread cleanup handler로 worker counter 누수 불가능; `task_group`에 auto-reap 도입; `thread.select`의 recv/timeout/default arm 구현. send arm만 보류.
