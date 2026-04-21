# RUNTIME_SCHEDULER.md — Osty 런타임 스케줄러 아키텍처 & 단계 로드맵

> **Status (2026-04):** Phase 1B/2 하이브리드 진행 중. pthread 기반 task spawn/join + mutex/cond 기반 채널이 공개 런타임에 들어왔다. `parallel` / `race` / `collectAll` 헬퍼는 더 이상 abort 스텁이 아니며 pthread 위에서 동작한다 (폴링 기반 race tie-break, 공유 카운터 기반 bounded-concurrency parallel, group-scoped collectAll). `thread.select`의 send arm도 타입별 value-register surface (`osty_rt_select_send_{i64,i1,f64,ptr,bytes_v1}`) 로 완성되어 공개 런타임에 `osty_sched_unimplemented` 경로는 더 이상 남아 있지 않다. 이 문서는 `LANG_SPEC_v0.5/08-concurrency.md` §8.0과 `LANG_SPEC_v0.5/19-runtime-primitives.md` §19.1 "GC × scheduler interaction" 절에 대응하는 **구현 레퍼런스**다. 스펙은 관찰 가능한 계약만 고정하고, 이 문서는 공개 LLVM 백엔드의 참조 런타임이 어떤 단계로 그 계약을 만족하는지 기술한다.

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
- `osty_rt_select` (recv / send / timeout / default 전부 지원): 내부 builder 할당 → body에 `(env, s)` 로 호출 → body가 arm 등록 → polling 루프에서 recv/send 시도, default는 즉시 발화, timeout은 deadline 도달 시 발화. 500μs backoff로 busy-spin 회피.
- `osty_rt_select_send_{i64,i1,f64,ptr,bytes_v1}`: 타입별 진입점. MIR은 channel element type으로 suffix를 고른 뒤 `(ptr s, ptr ch, <elemLLVM> value, ptr arm_env)` (scalar) 또는 `(ptr s, ptr ch, ptr src, i64 size, ptr arm_env)` (bytes) 형태로 call한다. 값은 scalar면 channel ring-buffer slot에 int64 비트로 packing하고, 복합형이면 GC-managed 버퍼로 복사한 뒤 그 포인터를 slot에 넣는다 — `osty_rt_thread_chan_send_*`와 같은 라인. Enqueue가 성공하면 루프는 arm closure(`() -> ()`)를 plain 호출하고 select에서 리턴 (`case ch <- v: arm()`의 도덕적 등가물). 닫힌 채널로 send하면 abort (blocking send와 동일한 loud-fail).
- `osty_rt_task_collect_all(body_env)`: fresh group에서 body를 돌려 `List<Handle<T>>`를 받고, 각 handle을 join해 `Ok(result)` 로 감싸 `List<Result<T, Error>>`를 반환. body가 group에만 attach하고 list에는 surface 안 한 stray child는 group teardown에서 pthread_join으로 회수.
- `osty_rt_task_race(body_env)`: fresh group에서 body를 돌려 handle 리스트를 받고, handle의 `done` flag를 500μs cadence로 폴링. 첫 완료된 handle을 winner로 삼고 group cancel flag를 release-store → sibling은 협력적 cancel point (channel op, sleep, explicit check, join) 에서 관찰. 반환은 `{i64 disc, i64 payload}` enum 레이아웃 (Ok=1, payload=winner result bits). 모든 sibling은 teardown에서 join.
- `osty_rt_parallel(items, concurrency, f)`: `items`의 `elem_size`가 8바이트 이하인 `List<T>`를 대상으로 bounded-concurrency map. `osty_rt_task_spawn`으로 `min(concurrency, n)` detached worker를 띄우고, 각 worker가 `__atomic_fetch_add`로 공유 카운터에서 인덱스를 가져와 `f(env, item)`을 호출, 결과를 pre-sized 출력 list의 해당 slot에 `Ok(result)` 로 set. 출력 list element size는 16 (enum layout), trace_elem은 NULL — 이는 Phase 1B/2가 전반적으로 가정하는 "task 결과는 8바이트 스칼라 폭" 계약과 일치한다.
- `osty_rt_thread_yield`: `sched_yield()` 호출.
- `osty_rt_thread_sleep(ns)`: 기존 `nanosleep` 경로 유지.
- GC 상호작용: 모든 `osty_gc_allocate_managed` 호출과 write barrier (`pre_write_v1`, `post_write_v1`, `load_v1`, `root_bind_v1`, `root_release_v1`)는 recursive mutex `osty_gc_lock`으로 직렬화. 자동 collection은 `osty_concurrent_workers > 0`인 동안 **보류**된다 (현재 스레드만의 stack root로는 sibling 스레드의 root를 커버할 수 없음). 모든 worker가 join된 뒤 재개. Concurrent collection은 Phase 3.

**제약.**
- `race`: tie-break은 폴링 기반 (500μs cadence) — 진짜 per-handle 완료 세마포어는 Phase 1B fibers 에서 제공. 결과 payload는 8바이트 폭으로 제한 (handle_join ABI와 동일 제약).
- `select` send arm: 현재는 `send_bytes_v1` 루트도 8바이트 정렬된 스칼라와 동일 폴링 cadence를 공유. 진짜 send-park (버퍼 full일 때 fiber 대기)는 Phase 1B fibers 에서 제공. Phase 1B/2 polling 루프에서는 채널이 계속 full이면 timeout/default arm이 없는 한 무한 대기 가능.
- `parallel`: 입력 `List<T>`의 `elem_size` > 8바이트면 abort. 출력 `List<Result<R, Error>>`의 payload는 비-traced slot이라 `R`이 포인터 타입이면 호출자가 결과 list를 루트로 잡고 있는 동안만 안전 (list는 GC 루트로 연결, payload는 정적으로 추적되지 않음 — 현재 handle_join의 `int64_t` 결과 슬롯이 갖는 제약과 동일).
- GC는 worker-live 동안 일시 보류 → long-running concurrent workload에서 OOM 가능. Phase 3 concurrent collector가 해소.
- Thread-per-task 모델 — 큰 task 수에서 TLS/stack 오버헤드. Chase-Lev deque + work-stealing 기반 worker pool은 Phase 2+ 후속 작업.

**수용 테스트.** `internal/backend/llvm_runtime_sched_test.go`:
- `TestBundledRuntimeScheduler`: taskGroup + spawn/join + cancel 전파 + 4-way parallel spawn wall-clock 검증.
- `TestBundledRuntimeSchedulerChannels`: 프로듀서/컨슈머 cross-thread, close-then-drain, `is_closed`.
- `TestBundledRuntimeSchedulerTaskGroupAutoReap`: body가 forgetful하게 자식을 남겨두고 리턴해도 group teardown이 pthread_join으로 회수함을 확인.
- `TestBundledRuntimeSchedulerChannelStress`: 4 producer × 4 consumer × 250 items/producer = 1000 items 전송을 capacity-16 채널 위에서 돌려 produced/consumed 합 일치.
- `TestBundledRuntimeSchedulerSelect`: recv-ready, timeout-only, default-present 3가지 시나리오에서 올바른 arm이 선택되는지 확인.
- `TestBundledRuntimeSchedulerCollectAll`: 4개 handle 전부 join되고 각 slot이 `{disc=1, payload=capture}` 레이아웃으로 출력 list에 들어가는지 검증.
- `TestBundledRuntimeSchedulerRace`: 10ms winner + 2초짜리 loser × 2 설정에서 winner 결과가 반환되고, sibling이 cancel 협조해서 전체 wall-clock이 500ms 미만임을 고정.
- `TestBundledRuntimeSchedulerParallel`: 10 items × concurrency 4 × `f(x)=x*x` 매핑의 각 slot 검증 + 빈 입력 엣지 케이스.
- `TestBundledRuntimeSchedulerSelectSend`: `osty_rt_select_send_i64`로 capacity-1 채널에 send 후 arm closure가 발화했는지, 채널에 값이 enqueue되었는지, recv arm을 가진 두 번째 select가 그 값을 드레인하는지 검증 (arm이 두 번 호출되는 것까지 고정).

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

// select (Phase 1B부터 functional — send/recv/timeout/default 모두)
void osty_rt_select(void *s);
void osty_rt_select_recv(void *s, void *ch, void *arm);
void osty_rt_select_send_i64(void *s, void *ch, int64_t value, void *arm);
void osty_rt_select_send_i1(void *s, void *ch, bool value, void *arm);
void osty_rt_select_send_f64(void *s, void *ch, double value, void *arm);
void osty_rt_select_send_ptr(void *s, void *ch, void *value, void *arm);
void osty_rt_select_send_bytes_v1(void *s, void *ch, const void *src,
                                  int64_t sz, void *arm);
void osty_rt_select_timeout(void *s, int64_t ns, void *arm);
void osty_rt_select_default(void *s, void *arm);

// helpers (현재 pthread 위에서 동작, Phase 2+ work-stealing 도입 시에도 ABI 불변)
struct osty_rt_result_enum_v1 { int64_t disc; int64_t payload; };
struct osty_rt_result_enum_v1 osty_rt_task_race(void *body);        // Result<T, Error>
void *osty_rt_task_collect_all(void *body);                          // List<Result<T, Error>>
void *osty_rt_parallel(void *items, int64_t concurrency, void *f);   // List<Result<R, Error>>
```

**계약.** 백엔드는 이 시그니처에 맞춰 lower한다 ([mir_generator.go:1749](internal/llvmgen/mir_generator.go:1749) 이하). 런타임이 단계 이전되어도 **프로그램 재컴파일 불필요**.

## 스펙 참조 매트릭스

| 스펙 조항 | Phase 1A | Phase 1B | Phase 2 | Phase 3 |
|---|---|---|---|---|
| §8.0 M:N + no thread identity | trivially (N=M=1) | trivially (N=1) | ✓ | ✓ |
| §8.1 structured lifetime | ✓ | ✓ | ✓ | ✓ |
| §8.2 failure propagation | ✓ | ✓ | ✓ | ✓ |
| §8.3 `parallel` bounded | sequential stub | concurrent on 1 worker | actual N-way (pthread, 현재) | ✓ |
| §8.3 `race` nondeterministic tie | N/A (no concurrency) | N/A | observable (폴링 기반, 현재) | observable (per-handle park) |
| §8.3 `collectAll` | sequential stub | sequential stub | ✓ (현재) | ✓ |
| §8.4 cancel with cause | ✓ | ✓ | ✓ | ✓ |
| §8 channels blocking | **abort stub** | ✓ | ✓ | ✓ |
| §8 `thread.select` (recv/timeout/default) | **abort stub** | ✓ | ✓ | ✓ |
| §8 `thread.select` send arm | **abort stub** | ✓ (폴링, 현재) | ✓ (per-channel lock) | ✓ (fiber park) |
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
- 2026-04-21: `osty_rt_task_collect_all` / `osty_rt_task_race` / `osty_rt_parallel` abort 스텁 제거. race는 handle `done` flag 폴링 (500μs cadence) + group cancel 브로드캐스트, collectAll은 fresh group 스코프에서 handle list를 돌며 `Ok(result)` 로 감싸 반환, parallel은 detached worker × `__atomic_fetch_add` 인덱스 분배로 pre-sized 출력 slot에 결과를 쓴다. MIR: `IntrinsicRace`는 `{i64, i64}` enum 레이아웃 반환으로 전환 (기존 `ptr` 반환 제거). 남은 abort 스텁은 `osty_rt_select_send` 하나.
- 2026-04-21 (follow-up): `osty_rt_select_send` abort 제거. `osty_rt_select_send_{i64,i1,f64,ptr,bytes_v1}` 5종 진입점을 타입별로 추가하고, select 폴링 루프에 send-arm try-enqueue 경로를 합류시켰다. 스칼라는 channel ring-buffer의 int64 slot에 bits packing, 복합형은 `osty_rt_thread_chan_send_bytes_v1`와 동일하게 GC-managed 버퍼 복사 후 포인터 slot 저장. Enqueue 성공 시 arm closure를 `() -> ()`로 plain 호출한다. MIR (`internal/llvmgen/mir_generator.go emitSelectSend`)은 channel element 타입으로 suffix를 골라 4-arg scalar / 5-arg bytes 형태로 lower. Stdlib `thread.osty`의 `Select.send`도 `(ch, value, f)` 3-인자 surface로 정정. `TestBundledRuntimeSchedulerSelectSendStubAborts`는 `TestBundledRuntimeSchedulerSelectSend` (end-to-end harness) 로 대체. 공개 런타임에서 `osty_sched_unimplemented` 호출은 이제 0개.
