# Osty Independent GC Design

이 문서는 `examples/gc` 패키지의 설계 기준이다. 목표는 Osty 언어로
작성된 독립 GC 코어를 만드는 것이다. 현재 Go transpiler 위에서 실행되지만,
구조는 미래의 native Osty 런타임이 그대로 가져갈 수 있는 형태를 지향한다.

## 큰 그림

Osty의 언어 스펙은 "GC가 있다"는 observable contract만 고정한다. 어떤 알고리즘을
쓰는지, 언제 멈추는지, 어떤 tuning knob을 제공하는지는 implementation detail이다.
따라서 이 설계의 핵심은 "지금 당장 Go 위에서 도는 예제"가 아니라, 나중에 Osty
native runtime이 가져야 할 책임 경계를 먼저 세우는 것이다.

GC runtime은 크게 네 가지 계약을 제공해야 한다.

1. Allocation contract: 객체를 만들고, 실패하면 명확한 failure path를 제공한다.
2. Reachability contract: stack, global, closure, thread-local root에서 닿는 객체는 살아남는다.
3. Movement contract: compaction으로 객체 위치가 바뀌어도 language-level reference는 깨지지 않는다.
4. Scheduling contract: collector가 언제, 얼마나, 어떤 pause budget으로 실행되는지 runtime이 제어한다.

현재 `examples/gc` 구현은 이 네 계약을 직접 OS 메모리로 수행하지 않고, Osty data
structure로 시뮬레이션한다. 하지만 API와 invariant는 실제 runtime으로 이동 가능한
형태로 둔다. 다시 말해 `Map<Int, GcObject>`는 미래의 arena/page allocator로,
`refs: List<Int>`는 compiler-generated pointer map으로, `roots: Set<Int>`는 stack
map과 global table로 바뀔 수 있다.

## 설계 철학

- Correctness first: leak보다 worse인 use-after-free, lost root, missed barrier를 먼저 막는다.
- Explicit metadata: 처음에는 metadata를 객체 안에 명확히 둔다. 나중에 성능을 위해 side table로 뺄 수 있다.
- Stable handle boundary: public API는 object ID를 본다. 내부 address는 움직일 수 있다.
- Barriers are visible: write barrier는 `link`에 숨어 있지만 개념적으로 public mutation path의 일부다.
- Deterministic tests: 현재 구현은 object ID 순서 compaction처럼 재현 가능한 동작을 선호한다.
- Telemetry by default: collector는 "무엇을 회수했는가"뿐 아니라 "왜 실행됐고 얼마나 이동했는가"를 말해야 한다.
- Runtime migration path: 모든 자료구조는 나중에 더 낮은 수준의 allocator, card table, stack map으로 대체 가능해야 한다.

## 최종 목표 아키텍처

처음부터 목표를 높게 잡는다. 최종 GC는 단순 mark-sweep이 아니라,
**precise, generational, regional, mostly-concurrent, incremental, compacting
collector**를 목표로 한다.

구체적으로는 다음 성질을 갖는다.

- Precise: stack, object field, closure capture, collection storage에서 pointer slot을 정확히 안다.
- Generational: young object는 nursery에서 빠르게 할당하고 minor collection으로 자주 수집한다.
- Regional: heap을 fixed-size region들로 나눠 evacuation, compaction, large object, pinned object 정책을 분리한다.
- Mostly-concurrent: root snapshot과 짧은 final synchronization만 stop-the-world로 두고 mark/evacuation work는 가능한 한 mutator와 병행한다.
- Incremental: pause budget을 넘기지 않도록 mark, remap, sweep work를 작은 step으로 나눈다.
- Compacting: fragmentation을 방치하지 않고 region evacuation으로 live object를 모은다.
- Moving-safe: object movement 후에도 language-level reference와 `std.ref.same` identity가 유지된다.
- Barrier-backed: compiler가 모든 heap mutation에 barrier를 삽입해 collector invariant를 유지한다.
- Profile-aware: latency-first, throughput-first, memory-tight profile이 collector policy를 바꿀 수 있다.

현재 `examples/gc` 구현은 이 최종 아키텍처의 작은 executable model이다. 즉,
prototype이 목표를 결정하는 것이 아니라 목표 아키텍처가 prototype의 모양을
결정한다.

## 되돌리기 어려운 결정

나중에 판갈이를 피하려면 초기에 다음 결정을 고정해야 한다.

### 1. Conservative GC는 쓰지 않는다

Osty는 statically typed language다. 따라서 GC는 precise 해야 한다. Conservative
stack scan은 처음에는 쉬워 보여도 moving compaction, pointer compression, FFI
pinning, `std.ref.same` semantics와 계속 충돌한다.

결정:

- Compiler는 safepoint별 stack map을 생성한다.
- Type descriptor는 object layout의 pointer map을 제공한다.
- Unknown word를 pointer처럼 취급하는 fallback은 production path에 넣지 않는다.

### 2. Object identity와 physical address를 분리한다

Moving collector를 제대로 하려면 "같은 객체인가"와 "지금 어디에 있는가"를 분리해야 한다.

결정:

- Language-level identity는 object header identity 또는 stable object ID로 정의한다.
- Physical address는 evacuation/compaction 중 바뀔 수 있다.
- `std.ref.same(a, b)`는 physical address 비교가 아니라 logical identity 비교다.

현재 prototype은 stable `id`와 movable `address`로 이 결정을 모델링한다.

### 3. Heap은 처음부터 region 기반으로 간다

단일 contiguous heap은 구현은 쉽지만, compaction과 large/pinned object 정책이
커질수록 발목을 잡는다.

결정:

- Heap은 region들의 집합이다.
- Region은 young, survivor, old, humongous, pinned, free 상태를 가진다.
- Collection은 whole-heap sweep보다 region selection과 evacuation을 중심으로 설계한다.
- Fragmentation은 object-level metric뿐 아니라 region-level metric으로 추적한다.

### 4. Allocation fast path는 thread-local이다

멀티스레드 runtime을 나중에 붙일 생각이라면 allocation fast path가 global lock에
의존하면 안 된다.

결정:

- 각 thread/task worker는 TLAB(thread-local allocation buffer)을 가진다.
- TLAB refill이 allocation slow path이며 GC trigger와 연결된다.
- Large object와 pinned object는 TLAB을 우회한다.

### 5. Barrier ABI를 compiler-runtime 계약으로 고정한다

Write barrier를 나중에 "필요한 곳에 적당히 추가"하면 반드시 빠지는 경로가 생긴다.

결정:

- 모든 heap pointer store는 compiler lowering 단계에서 barrier ABI를 호출한다.
- Field store, array/list store, map insert/update, closure environment update,
  runtime frame update가 모두 barrier 대상이다.
- Barrier ABI는 collector algorithm이 바뀌어도 compiler surface를 크게 바꾸지 않도록
  안정적인 함수/inline hook 형태로 둔다.

### 6. Safepoint와 stack map은 IR 단계에서 설계한다

GC는 backend 뒷단에서만 붙일 수 없다. 어떤 local이 live pointer인지 알려면 IR이
그 정보를 보존해야 한다.

결정:

- IR는 safepoint 후보를 명시한다.
- 각 safepoint는 live pointer slot metadata를 가진다.
- Optimizer가 local을 제거하거나 이동해도 stack map이 같이 갱신되어야 한다.

## 목표

- Osty 코드만으로 관리 힙과 객체 그래프를 모델링한다.
- reference cycle을 반드시 회수한다.
- nursery와 tenured generation을 분리해 짧게 사는 객체를 싸게 수집한다.
- old-to-young reference를 remembered set으로 추적한다.
- incremental tri-color mark를 제공하고 write barrier로 black-to-white 누락을 막는다.
- major collection 후 compaction으로 hole을 줄인다.
- pinned object를 지원해 FFI, IO buffer, stack map 등 움직이면 안 되는 객체를 표현한다.
- OOM은 `Result` API로 안전하게 보고하고, 편의 API는 `unwrap` 기반으로 단순하게 둔다.
- 모든 collection은 telemetry를 남겨 테스트와 튜닝이 가능해야 한다.

## 비목표

- 실제 OS 메모리 allocator를 대체하지 않는다.
- Go runtime GC를 끄거나 우회하지 않는다.
- thread-safe concurrent collector까지 구현하지 않는다.
- finalizer, weak reference, destructor는 제공하지 않는다. Osty v0.4 memory spec과 맞춘다.

## Production Runtime Shape

이 섹션은 최종 runtime이 가져야 할 구체적인 내부 모양이다. 현재 prototype은 이 모양을
단순화해서 Osty data structure로 표현한다.

### Object Header

모든 heap object는 header를 가진다.

필수 field:

- type descriptor pointer
- logical identity 또는 identity hash seed
- size class 또는 object size
- mark state
- generation / age bits
- forwarding state
- pinned bit
- remembered/card dirty hint

Header는 작아야 한다. 자주 읽는 bit는 header에 두고, 드문 metadata는 side table로
분리한다. 예를 들어 profiling allocation site, debug owner, extended pin reason은
side table이 더 적합하다.

### Type Descriptor

Type descriptor는 GC가 object body를 scan하는 방법을 알려준다.

필수 정보:

- object kind
- total size 또는 dynamic size function
- pointer bitmap 또는 pointer offset list
- array element trace descriptor
- enum variant payload descriptor
- finalizer 없음
- optional debug type name

Generic type은 monomorphized layout이면 descriptor도 concrete하게 만든다. Erased
generic layout이면 descriptor가 runtime type argument descriptor를 가진다.

### Region Table

Heap은 region table을 가진다.

Region metadata:

- base address
- size
- used bytes
- live bytes after last mark
- region kind: free, eden, survivor, old, humongous, pinned
- remembered/card bitmap
- evacuation candidate score
- allocation epoch

Region 설계의 장점:

- Minor collection은 eden/survivor region만 대상으로 삼을 수 있다.
- Old compaction은 fragmentation이 높은 region만 고를 수 있다.
- Pinned object가 있는 region은 evacuation에서 제외하거나 부분 evacuation 정책을 쓸 수 있다.
- Large object는 humongous region으로 분리해 copying cost를 피할 수 있다.

### Reference Representation

최종 runtime은 두 representation 중 하나를 선택해야 한다.

1. Direct pointer + forwarding/load barrier
2. Compressed handle/object ID + handle table

초기 production target은 direct pointer를 기본으로 둔다. 이유는 일반 object access의
비용을 낮추기 위해서다. 다만 prototype은 stable `Int` ID를 사용한다. 이 차이를
흡수하기 위해 public design은 "identity와 address 분리"를 고정하고, concrete
representation은 backend 결정으로 둔다.

Direct pointer 방식을 택하면 moving 중 다음 중 하나가 필요하다.

- Stop-the-world evacuation 후 모든 root/object slot을 갱신한다.
- Concurrent evacuation을 위해 load/read barrier를 둔다.

초기 native runtime은 stop-the-world evacuation으로 시작하고, 최종 목표는 selected
old-region concurrent evacuation까지 열어둔다.

### Collection Pipeline

최종 major cycle은 다음 pipeline을 가진다.

1. Initial safepoint: thread roots를 안정화한다.
2. Root snapshot: stack/global/thread-local/FFI roots를 수집한다.
3. Concurrent or incremental mark: grey work queue를 처리한다.
4. Remark safepoint: barrier buffer와 dirty cards를 drain한다.
5. Region selection: evacuation candidate를 고른다.
6. Evacuation/relocation: selected region의 live object를 새 region으로 옮긴다.
7. Remap: roots와 object fields를 forwarding destination으로 갱신한다.
8. Reclaim: empty/free region을 free list로 돌린다.
9. Telemetry publish: stats와 pause profile을 기록한다.

Minor cycle은 더 짧다.

1. Eden allocation pressure trigger
2. Root and remembered/card scan
3. Young mark/copy
4. Survivor aging or promotion
5. Eden reset

### Barrier Set

최종 collector는 barrier set을 명시적으로 가진다.

- Pre-write barrier: SATB 정책을 선택할 때 old value를 mark queue에 보존한다.
- Post-write barrier: generational remembered/card dirtying을 수행한다.
- Load/read barrier: concurrent relocation을 선택할 때 forwarding을 따라간다.
- Allocation barrier: newly allocated object의 color/generation을 현재 phase에 맞춘다.

현재 prototype은 `link` 안에 post-write barrier와 insertion barrier를 넣었다. 고급
runtime에서는 barrier set을 collector policy별로 교체 가능한 ABI로 만든다.

### Threading Model

최종 runtime은 mutator thread와 collector worker를 분리할 수 있어야 한다.

- Mutator thread는 TLAB에서 allocate한다.
- Collector worker는 mark queue와 evacuation work queue를 처리한다.
- Safepoint coordinator는 thread를 짧게 멈춰 root snapshot과 remark를 수행한다.
- Barrier buffers는 per-thread로 두고 remark에서 flush한다.

처음 native backend는 single-threaded stop-the-world로 시작할 수 있지만, API는
multi-threaded collector로 확장 가능한 형태여야 한다.

### Policy Layer

Mechanism과 policy를 분리한다.

Mechanism:

- mark
- sweep
- evacuate
- remap
- remembered/card scan
- root scan

Policy:

- 언제 minor를 실행할지
- 어떤 old region을 compact할지
- promotion threshold를 얼마로 둘지
- incremental budget을 얼마로 둘지
- pinned/humongous region을 언제 회수할지

이 분리는 중요하다. Mechanism이 깨지면 correctness bug지만, policy는 profile과
workload에 따라 바뀔 수 있다.

## Phase 1-4 Foundation Decisions

Phase 1-4는 구현 편의를 위한 TODO가 아니라, 나중에 전체 runtime을 갈아엎지 않기
위한 foundation이다. 이 구간에서 결정하는 내용은 compiler IR, generated code,
runtime ABI, profiler output까지 번진다.

### Phase 1 Detail: GC RFC 고정

Phase 1의 산출물은 "Osty GC는 어떤 계열의 collector인가"를 한 문장으로 말할 수
있게 만드는 것이다.

고정할 collector family:

- precise, not conservative
- moving-capable, not address-stable-only
- generational, with nursery as the default allocation target
- regional, with collection and evacuation candidate selection at region granularity
- incremental by construction
- mostly-concurrent as a target, even if the first backend is stop-the-world
- barrier-backed, with compiler lowering participating in GC correctness

명시적으로 금지되는 후퇴:

- "일단 conservative stack scan으로 시작하자"
- "object address를 identity로 영구 고정하자"
- "single contiguous heap으로 충분할 때까지 버티자"
- "write barrier는 나중에 필요한 store에만 붙이자"
- "safepoint metadata는 backend가 알아서 복원하자"

Phase 1 완료 조건:

- GC family가 문서에 명확히 고정되어 있다.
- language-visible behavior와 runtime implementation detail의 경계가 적혀 있다.
- compiler, runtime, stdlib, tooling owner가 각자의 책임을 분리할 수 있다.
- prototype이 최종 설계를 제한하지 않고, 최종 설계가 prototype을 설명한다.

### Phase 2 Detail: Object Header 설계

Object header는 모든 heap object의 최소 공통 ABI다. 너무 가볍게 잡으면 나중에
moving collection, profiling, pinning, type-directed scan을 붙일 때 계속 깨진다.

초기 logical header는 두 machine word 이상을 전제로 한다.

```text
word 0: type descriptor pointer
word 1: packed metadata
```

`word 1`의 logical fields:

- mark epoch or mark color
- generation bits
- age bits
- pinned bit
- remembered/card hint bit
- forwarding state bit
- size class or small object size
- identity hash state

큰 객체나 debug/profile metadata는 header에 직접 넣지 않는다. 다음 정보는 side
table 후보로 둔다.

- allocation site
- extended object size
- pin reason
- owner thread/region debug info
- profiling counters
- forwarding target for evacuation

Forwarding 정책:

- 초기 backend는 forwarding side table을 기본으로 한다.
- Header에는 forwarding state bit만 둔다.
- Evacuation 중 forwarding target은 region-local forwarding table에서 찾는다.
- 미래 concurrent relocation에서는 load barrier가 forwarding target을 따라간다.

이 선택은 type descriptor pointer를 header에서 안정적으로 유지한다. 객체 body를
forwarding pointer로 덮어쓰는 방식은 간단하지만, descriptor-based scan과 debug
tooling을 복잡하게 만든다.

Phase 2 완료 조건:

- Header가 scan, mark, pin, generation, forwarding 여부를 표현한다.
- Side table로 빠질 metadata와 header에 남을 metadata가 구분되어 있다.
- Header layout이 object kind와 무관하게 공통 ABI로 쓸 수 있다.
- Prototype의 `GcObject` field들이 native header/side table 중 어디로 갈지 매핑되어 있다.

Prototype mapping:

- `kind` -> type descriptor
- `size` -> size class or side table exact size
- `address` -> region base + object offset
- `generation`, `age`, `color`, `marked`, `pinned`, `large` -> packed metadata
- `region` -> region table membership cache
- `remembered` -> card/remembered hint
- `allocatedCycle` -> debug/profiling side table

### Phase 3 Detail: Type Descriptor 설계

Type descriptor는 precise GC의 중심이다. Collector는 source-level type checker가 아니라
runtime layout만 본다. 따라서 descriptor는 "어디가 pointer slot인가"를 빠르게 말해야 한다.

Descriptor 공통 field:

- runtime kind: record, enum, tuple, closure, array, bytes, string, runtime frame
- object size or dynamic size function
- pointer map encoding
- optional element descriptor
- optional enum variant table
- optional closure environment table
- debug type name

Pointer map encoding 기본 정책:

- 작은 고정 크기 object는 inline bitmap을 쓴다.
- 큰 고정 크기 object는 pointer offset list를 쓴다.
- Array/list는 element descriptor와 length로 scan한다.
- Pointer-free object는 `noPointers` fast path를 가진다.
- Closure는 capture slot descriptor를 가진다.
- Enum은 active variant tag로 payload descriptor를 고른다.

Generic policy:

- Monomorphized generic layout은 concrete descriptor를 생성한다.
- Erased layout이 필요한 경우 descriptor가 runtime type argument descriptor를 보유한다.
- `List<T>`, `Map<K, V>`, `Set<T>`는 element/key/value pointer-bearing 여부를 descriptor에 반영한다.

Descriptor가 책임지지 않는 것:

- liveness: stack map/safepoint가 책임진다.
- ownership: language type system이 책임진다.
- cleanup: Osty는 finalizer가 없으므로 descriptor가 destructor를 들지 않는다.

Phase 3 완료 조건:

- 모든 heap-allocated Osty value category가 descriptor를 가진다.
- Pointer-bearing 여부를 runtime이 source AST 없이 판단할 수 있다.
- Descriptor만으로 object scan loop를 작성할 수 있다.
- Descriptor format이 monomorphized generics와 runtime-erased generics를 모두 설명한다.

Prototype mapping:

- `ObjectKind` -> descriptor runtime kind
- `refs: List<Int>` -> descriptor-guided pointer slots
- `GcBytes` -> `noPointers`
- `GcClosure` -> closure capture descriptor
- `GcRuntimeFrame` -> stack/runtime frame descriptor

### Phase 4 Detail: Region Heap 설계

Region heap은 처음에는 무거워 보여도, moving/compacting collector를 고급으로 가져가려면
초기부터 맞는 선택이다. Region은 allocation, collection, evacuation, telemetry의
공통 단위다.

Region 종류:

- `free`: allocation 가능
- `eden`: fresh young allocation
- `survivor`: minor collection에서 살아남은 young object
- `old`: promoted or long-lived object
- `humongous`: region 하나 이상을 차지하는 large object
- `pinned`: moving 불가 object가 있어 evacuation에서 제외되는 region

Region metadata:

- base address
- region size
- used bytes
- live bytes after mark
- remembered/card bitmap
- object start bitmap or line table
- evacuation candidate score
- allocation epoch
- owner thread or shared state

Region state transitions:

```text
free -> eden -> survivor -> old
free -> humongous -> free
free -> pinned -> old/free
old -> evacuation-source -> free
old -> old
```

Collection policy hooks:

- Minor GC scans eden/survivor plus remembered cards from old.
- Major GC marks all regions but evacuates only selected candidates.
- Humongous regions are reclaimed when unreachable but usually not copied.
- Pinned regions are either skipped or partially compacted around pinned objects.

Phase 4 완료 조건:

- Heap 전체를 한 덩어리로 보는 code path가 core design에 남아 있지 않다.
- Minor, major, compaction, humongous, pinned policy가 region metadata로 설명된다.
- Fragmentation metric이 object-level이 아니라 region-level로도 정의된다.
- Prototype의 logical address model이 region base + offset model로 확장 가능하다.

Prototype mapping:

- `address` -> synthetic region offset
- `fragmentedBytes()` -> future region fragmentation metric의 object-level approximation
- `pinned` -> pinned object or pinned region policy seed
- `large` -> humongous object policy seed
- `region` -> eden/survivor/old/humongous/pinned membership
- `generation` -> eden/survivor/old region membership

## Phase 9 Compiler Contract Decisions

Phase 9의 핵심은 "collector가 멈출 수 있는 지점"과 "그 지점에서 live pointer가
어디 있는지"를 IR에서 잃지 않는 것이다. 이 정보가 사라지면 나중에 backend가 아무리
좋아도 precise moving GC를 만들 수 없다.

Safepoint kind:

- `call`: Osty function call, runtime call, FFI wrapper call
- `allocation`: allocation slow path, TLAB refill, heap-limit check
- `loopBackedge`: 긴 loop에서 incremental GC work를 나눠 실행할 수 있는 지점
- `asyncYield`: task suspension, await/yield, scheduler handoff

Safepoint metadata:

- stable safepoint id
- source IR node id
- backend instruction offset 또는 pseudo offset
- `mayCollect`: collector work가 실행될 수 있는지
- `mayAllocate`: allocation slow path나 allocating call인지
- `maySuspend`: async frame으로 보존될 수 있는지
- `requiresStackMap`: live pointer root map이 반드시 필요한지
- `liveRoots`: 해당 safepoint에서 live인 pointer slot 목록

Live root slot metadata:

- frame/register/spill slot index
- slot kind: local, temporary, register, spill, closure, derived
- debug name
- nullable 여부
- derived root인 경우 base root slot

Derived root는 slice data pointer, interior array pointer, field address처럼 object 내부를
가리키는 값이다. Moving GC에서는 derived pointer만으로 object를 remap할 수 없으므로,
base object root가 같은 safepoint에서 live여야 한다.

Validator rules:

- Safepoint id는 function 안에서 unique해야 한다.
- 모든 safepoint는 `mayCollect=true`이고 `requiresStackMap=true`여야 한다.
- Allocation safepoint는 `mayAllocate=true`여야 한다.
- Async yield safepoint는 `maySuspend=true`여야 한다.
- Root slot index는 frame slot 범위 안에 있어야 한다.
- 한 safepoint 안에서 같은 root slot이 두 번 나오면 안 된다.
- Derived root는 자기 자신이 아닌 live base root를 가져야 한다.

Phase 10은 이 contract를 실제 bitmap/stack-map binary format으로 낮추는 단계다.
Phase 9는 format을 고정하지 않고, IR과 optimizer가 보존해야 할 semantic contract만
고정한다.

Prototype mapping:

- `GcSafepointKind` -> IR safepoint classification
- `GcLiveRootSlot` -> future stack-map live pointer entry
- `GcSafepoint` -> function-local safepoint record
- `GcSafepointPlan` -> backend가 stack map을 만들기 전 semantic input
- `validateSafepointPlan()` -> lowering/optimizer contract checker

## Phase 10 Stack Map Format Decisions

Phase 10은 Phase 9 contract를 실제 runtime이 빠르게 읽을 수 있는 stack-map shape로
낮춘다. 아직 platform ABI나 binary section format을 확정하지는 않지만, backend가
반드시 만들 수 있어야 하는 정보와 validator rule은 실행 가능한 모델로 고정한다.

Frame layout entry:

- frame slot index
- frame slot kind: local, temporary, register spill, closure capture, tuple field, defer,
  question, return
- debug name
- byte offset
- pointer 여부
- derived slot인 경우 base slot
- defer, `?`, early return path에서 보존되어야 하는지

Stack map entry:

- safepoint id
- instruction offset
- live pointer count
- inline bitmap slot count
- bitmap words
- overflow pointer slot list
- derived-root to base-root map

Encoding policy:

- 작은 frame과 hot slot은 bitmap으로 인코딩한다.
- bitmap이 덮지 않는 큰 slot 번호는 overflow slot list로 둔다.
- derived root는 bitmap/list에 live pointer로 표시하면서 별도 base mapping도 가진다.
- pointer-free frame slot은 stack map에 live root로 등장할 수 없다.
- frame slot kind와 root slot kind가 맞지 않으면 stack map build를 거부한다.

Control-flow preservation:

- `defer` path에서 필요한 root는 defer-running safepoint마다 live여야 한다.
- `?` early-exit path에서 필요한 root는 question-exit safepoint마다 live여야 한다.
- explicit early return path에서 필요한 root는 return safepoint마다 live여야 한다.

Prototype mapping:

- `GcFrameSlot` -> backend frame-layout table entry
- `GcStackMapEntry` -> per-safepoint encoded pointer map
- `bitmapWords` -> inline pointer bitmap
- `overflowSlots` -> large/sparse frame fallback list
- `GcDerivedRootMap` -> derived pointer remapping input
- `buildGcStackMap()` -> semantic stack map builder/validator

## Phase 11 Global and Static Root Decisions

Phase 11은 stack frame 밖에 있는 root를 다룬다. Top-level value, package global,
static runtime object, compile-time constant backing object는 stack map에 나타나지
않지만, collector root graph에는 반드시 들어가야 한다.

Global root entry:

- module name
- global name
- kind: top-level `let`, constant, package global, static runtime root
- init state: declared, initializing, ready, poisoned
- object id, 없으면 `-1`

Initialization policy:

- `declared`: symbol은 table에 있으나 아직 heap object가 없다.
- `initializing`: module initializer가 실행 중이며 partial object도 root로 취급한다.
- `ready`: initialization이 끝났고 object id가 반드시 live object를 가리킨다.
- `poisoned`: initialization failure나 teardown state이며 object root가 없어야 한다.
- 같은 module/name entry는 중복 선언하지 않는다. Poisoned entry는 remove 후 다시
  install해야 하며, 바로 object를 재bind할 수 없다.

이 정책은 module initialization 중 collection이 떠도 partially initialized aggregate가
사라지지 않게 만든다. 반대로 poisoned global은 object id를 비워 이후 collection에서
회수될 수 있게 한다.

Cross-package policy:

- Global root table은 module-qualified key를 쓴다.
- Global object가 다른 package object를 참조하면 일반 object edge로 trace된다.
- 따라서 root table은 entry point만 module-qualified로 관리하고, cross-package
  reachability는 object graph가 책임진다.

Incremental policy:

- Marking 중 global root가 새 object로 bind되면 즉시 shade한다.
- 이 규칙은 stack root 추가와 동일한 insertion-root barrier다.

Prototype mapping:

- `GcGlobalRootKind` -> global/static root category
- `GcGlobalInitState` -> module initializer lifecycle
- `GcGlobalRoot` -> module-qualified root table entry
- `globalRoots: Map<String, GcGlobalRoot>` -> future runtime global root table
- `installGlobalRoot`, `beginGlobalInit`, `setGlobalRootObject`, `finishGlobalInit`,
  `poisonGlobalRoot` -> compiler/runtime initialization hooks

## Phase 12 Closure and Async Frame Root Decisions

Phase 12는 stack/global 밖에서 생기는 long-lived execution state를 다룬다. Closure
environment와 suspended async frame은 둘 다 "현재 native stack에는 없지만 runtime이
보관 중인 pointer slot"이므로, descriptor와 root snapshot을 별도로 모델링한다.

Closure environment policy:

- Closure object는 `GcClosure` kind여야 한다.
- Capture descriptor는 slot, name, capture kind, object id를 가진다.
- Scalar capture는 object id를 가질 수 없다.
- Value/boxed/mutable-box capture는 live object id를 가져야 한다.
- Closure object의 `refs`는 capture descriptor에서 재작성된다.
- Boxed mutable capture는 box object를 capture root로 삼고, box 내부 payload는 일반
  object edge로 trace한다.

Async frame policy:

- Suspended task frame은 `GcRuntimeFrame` object와 live root snapshot을 가진다.
- Running frame은 active stack scanner가 책임지므로 async side table에서는 roots를 비운다.
- Completed/cancelled frame은 roots를 비워야 하며, 다시 suspended frame으로 되돌릴 수 없다.
- Marking 중 suspended frame이 새로 등록되면 frame object와 snapshot roots를 즉시 shade한다.

Prototype mapping:

- `GcClosureCaptureKind` -> scalar/value/boxed/mutable-box capture category
- `GcClosureCapture` -> closure environment slot descriptor
- `GcClosureEnvironment` -> closure object side-table descriptor
- `GcAsyncFrameState` -> task frame lifecycle
- `GcAsyncFrameRoot` -> suspended task root snapshot
- `closureEnvironments` and `asyncFrames` -> future runtime side tables

## Phase 13 TLAB Allocation Fast Path Decisions

Phase 13은 common allocation을 global heap cursor에서 분리한다. Native backend에서는
대부분의 small young allocation이 thread-local bump pointer 몇 줄로 끝나야 하며,
collector나 global heap metadata를 만지는 경로는 refill slow path로 밀어낸다.

TLAB policy:

- 각 mutator owner는 `GcTlab`을 가진다.
- Small non-large allocation은 `tryAllocateThread(owner, kind, size)`로 TLAB fast path를 탄다.
- TLAB에 남은 공간이 충분하면 object address는 TLAB cursor에서 받고, global
  `nextAddress`는 움직이지 않는다.
- TLAB refill은 slow path다. Refill은 nursery pressure와 heap limit을 확인하고,
  필요하면 collection을 실행한다.
- Refill 전 남은 tail bytes는 waste로 회계 처리한다.
- Collection 시작, including incremental major start, 은 active TLAB을 retire하고
  남은 bytes를 waste로 넘긴다.
- Large object, disabled TLAB, TLAB보다 큰 object는 global allocation path로 우회한다.

Prototype mapping:

- `GcConfig.tlabSize` -> default per-owner TLAB refill size
- `GcTlab` -> owner-local bump pointer state
- `tryAllocateThread` / `allocateThread` -> compiler-emitted allocation fast path
- `refillTlab` -> runtime allocation slow path and GC trigger boundary
- `tlabWasteBytes`, `tlabRefillCount`, `tlabFastAllocationCount` -> allocation telemetry seed

## 현재 구현된 범위

현재 패키지는 다음을 구현한다.

- `GcHeap`: 독립 heap instance
- `GcObject`: object metadata와 outgoing reference list
- `tryAllocate` / `allocate`: allocation pressure에 따라 minor, major collection을 시도
- `largeObjectThreshold`: nursery를 우회하는 humongous allocation policy seed
- `addRoot` / `removeRoot`: explicit root management
- `link` / `unlink`: object graph mutation과 write barrier
- `minorCollect`: nursery-only tracing collection
- `majorCollect`: full tracing collection과 cycle reclamation
- `startIncrementalMajor` / `stepIncremental` / `finishIncremental`: incremental major marking
- `compactLive`: stable ID 기반 logical compaction
- `pin` / `unpin`: compaction exclusion과 pinned region metadata
- `regionOf` / `isLarge`: object의 region/humongous 상태 introspection
- `validateHeap`: debug-time invariant checker
- `GcStats`: trigger, allocation debt, logical pause units, fragmentation, survival telemetry
- `lastCollectionStats`: pressure-triggered collection telemetry lookup
- deterministic graph stress tests: seed 기반 graph mutation과 reference reachability 비교
- `GcSafepointPlan`: IR safepoint와 live pointer root contract model
- `validateSafepointPlan`: backend stack map 입력의 semantic validator
- `GcFrameSlot`: backend frame layout model
- `GcStackMap`: bitmap + overflow-list stack-map prototype
- `buildGcStackMap`: stack map builder와 frame/root preservation validator
- `GcGlobalRoot`: module-qualified global/static root table entry
- global root lifecycle hooks: declare, initialize, publish, poison, remove
- `GcClosureEnvironment`: closure capture descriptor와 capture-ref rewrite
- `GcAsyncFrameRoot`: suspended async/task frame root snapshot
- `GcTlab`: thread-local bump allocation buffer와 refill/waste accounting

현재 구현은 "runtime algorithm prototype"이다. 아직 실제 compiler-emitted stack map,
real allocator, host FFI pointer pinning, thread safepoint와 연결되어 있지는 않다.

## Architecture Layers

### Layer 1: Object Model

Object layer는 "무엇이 heap object인가"를 정의한다. 현재는 `ObjectKind`로 record,
array, closure, bytes, runtime frame을 구분한다.

실제 runtime에서는 kind별로 trace strategy가 달라진다.

- Record: compiler가 field pointer bitmap을 제공한다.
- Array: element type이 pointer-bearing인지에 따라 scan 여부가 달라진다.
- Closure: captured environment slots를 scan한다.
- Bytes: pointer-free object이므로 scan하지 않는다.
- Runtime frame: stack root snapshot이나 coroutine frame을 나타낸다.

현재 `refs: List<Int>`는 모든 pointer-bearing field를 한곳에 모은 단순 모델이다.
native runtime에서는 object header와 type descriptor가 이 역할을 나눠 가진다.

### Layer 2: Allocation

Allocation layer는 nursery allocation fast path와 slow path를 나눈다.

- Fast path: TLAB에 충분한 공간이 있으면 owner-local bump allocation한다.
- Refill path: TLAB tail waste를 회계 처리하고 새 buffer를 예약한다.
- Slow path: refill 중 nursery pressure가 넘으면 minor collection을 실행한다.
- Full slow path: heap limit까지 넘으면 major collection을 실행한다.
- Large object path: threshold 이상 object는 nursery를 우회해 humongous region에 둔다.
- Failure path: collection 이후에도 공간이 부족하면 OOM으로 실패한다.

Incremental marking 중에는 nested minor collection을 실행하지 않는다. Young mark
state와 major mark state가 같은 prototype metadata를 공유하기 때문이다. 실제 runtime은
phase별 mark bitmap을 분리하거나 safepoint에서 incremental cycle을 끝낸 뒤 nursery
collection을 실행해야 한다.

Phase 13 prototype은 small allocation의 common path를 `GcTlab`으로 분리한다. 다음
단계에서는 size class, page, arena, large object space를 도입해야 한다. Phase 7
prototype은 `largeObjectThreshold`와 `GcHumongousRegion`으로 이 정책의
API/invariant를 먼저 고정한다.

Phase 14 prototype은 nursery 내부를 eden과 survivor로 명시적으로 나눈다.
`nurseryLimit`는 young generation 전체 압력으로 남고, `survivorLimit`는 minor GC
to-space 예산이다. New allocation은 eden에서 시작한다. Minor GC에서 살아남은
young object는 age를 올린 뒤 survivor 예산이 허용하면 survivor로 이동하고, 예산을
넘거나 promotion threshold에 도달하면 old generation으로 fallback promote된다.
`survivorLimit == 0`은 survivor 공간을 비활성화하고 모든 survivor를 즉시 promote하는
정책이다.

Phase 15 prototype은 old generation placement를 `GcTenuredAllocator`로 분리한다.
Old allocation은 first-fit free list를 먼저 사용하고, 맞는 block이 없을 때만
tenured bump cursor로 heap span을 확장한다. Minor promotion은 nursery address를
그대로 tenured object로 바꾸지 않고 old backend에서 새 address를 얻은 뒤 source
block을 free list로 반환한다. Major compaction은 pinned/humongous object 때문에
남은 gap을 tenured free list로 공개한다.

Phase 16 prototype은 threshold 이상 object를 `GcHumongousAllocator`가 소유하는
별도 humongous space에 배치한다. Humongous allocation도 first-fit free list를 먼저
사용하고, 맞는 block이 없을 때 humongous cursor로 heap span을 확장한다. Major sweep은
unreachable large object를 humongous free list로 반환한다. Compaction은 live
humongous object와 humongous free block을 모두 reserved range로 취급해 movable old
object가 그 범위로 들어가지 않게 한다.

### Layer 3: Root Discovery

Root discovery는 collector의 정확성을 좌우한다. 현재는 `addRoot`로 explicit root를
넣는다. 실제 runtime은 다음 root source를 자동으로 제공해야 한다.

- active stack frames
- suspended async frames
- globals and constants containing references
- closure environments
- thread-local runtime state
- FFI handles and pinned host objects

정확한 GC를 하려면 compiler가 safepoint마다 stack map을 내보내야 한다. Stack slot이
integer인지 pointer인지 모르면 conservative GC가 되며, compaction과 충돌한다.
Phase 9 prototype은 `GcSafepointPlan`으로 이 contract를 executable model로 고정한다.
Phase 11 prototype은 stack map 밖의 globals/constants/static roots를
`GcGlobalRoot` table로 고정한다.
Phase 12 prototype은 closure capture side table과 suspended async frame root snapshot을
collector root graph에 연결한다.

### Layer 4: Barriers

Barrier layer는 mutator가 heap graph를 바꿀 때 collector invariant를 유지한다.

Phase 17 prototype은 barrier ABI v1을 executable model로 고정한다.

- `osty.gc.pre_write_v1(owner, old_value, slot_kind)`: SATB/old-value preservation을 위한 hook이다.
  현재 Dijkstra insertion barrier 정책에서는 required가 아니지만 compiler surface에는 남긴다.
- `osty.gc.post_write_v1(owner, new_value, slot_kind)`: generational remembered set과
  incremental insertion barrier를 수행하는 필수 hook이다.
- `osty.gc.load_v1(value)`: future concurrent relocation/read barrier를 위한 hook이다.
  현재 non-moving read path에서는 stable identity를 반환한다.

현재 `link`는 ABI v1의 post-write barrier를 수행한다.

- Generational barrier: tenured object가 nursery object를 가리키면 remembered set에 넣는다.
- Incremental barrier: marking 중 black object가 white object를 새로 가리키면 child를 shade한다.

`unlink`는 pointer removal 전에 pre-write hook을 호출한다. Closure capture rewrite도
pre/post barrier ABI를 통한다.

Phase 18 prototype은 struct field, tuple payload, enum payload, method receiver
mutation lowering을 `lower*Store` API로 모델링한다. 각 lowered store는 slot descriptor를
side table에 남기고, old value pre-write hook과 new value post-write hook을 같은 ABI로
실행한다. Side table과 object refs projection이 어긋나면 `validateHeap()`가 실패한다.

Phase 19 prototype은 같은 lowering contract를 std collection 내부 pointer slot까지
확장한다. Array/list element, map key/value, set key store는 모두 `GcBarrierArrayElement`
slot kind를 사용하고, lowered pointer slot side table을 통해 `GcObject.refs`와
remembered set에 반영된다. 같은 object를 여러 collection slot이 공유할 수 있으므로,
overwrite는 다른 lowered slot이 아직 old object를 가리키는지 확인한 뒤 refs edge를
제거한다.

실제 runtime에서는 모든 field store, array store, closure capture update, map entry
write가 barrier를 호출해야 한다. Compiler lowering에서 store instruction이 barrier
hook을 빠뜨리면 collector correctness가 깨진다.

### Layer 5: Collection Algorithms

Collection layer는 minor, major, incremental phase를 제공한다.

- Minor: young generation만 trace한다.
- Major: full heap을 trace하고 unreachable cycle을 회수한다.
- Incremental: mark work를 작은 step으로 나눠 pause를 줄인다.
- Compact: fragmentation을 줄이고 cache locality를 높인다.

현재 compaction은 logical address만 바꾼다. 실제 moving collector에서는 모든 root와
object field를 forwarding pointer를 통해 갱신해야 한다.

### Layer 6: Scheduler Integration

Scheduler integration은 collection work를 언제 실행할지 결정한다.

- Allocation pressure가 limit을 넘으면 collector를 깨운다.
- Safepoint에서 incremental step을 실행한다.
- Latency-sensitive profile에서는 작은 budget을 자주 쓴다.
- Batch profile에서는 더 큰 major collection을 허용한다.

현재 prototype은 `stepIncremental` 직접 호출에 더해 scheduler safepoint와 mutator assist를
모델링한다. Native runtime에서는 function prologue, loop backedge, allocation slow path,
async yield 같은 safepoint가 `schedulerSafepoint`로 내려가고, allocation debt가 threshold를
넘으면 `mutatorAssist`가 bounded mark step을 실행한다. Phase 9에서는 이 safepoint 종류를
`call`, `allocation`, `loopBackedge`, `asyncYield`로 분류하고, 각 지점이 live root metadata를
반드시 보존하도록 한다.

### Layer 7: Observability

Observability layer는 GC가 runtime black box가 되지 않도록 한다.

현재 `GcStats`는 다음 정보를 제공한다.

- collection kind and cycle
- trigger reason
- object count before and after
- bytes before and after
- reclaimed objects and bytes
- promoted objects
- compacted objects
- remembered set scan count
- mark step count
- logical pause units
- allocation bytes since the previous completed collection
- fragmentation before and after
- heap survival and promotion percentages

추가로 필요한 정보는 wall-clock pause time, mutator utilization, allocation rate,
card dirtying rate, pinned bytes, large object bytes다.

## 핵심 모델

### Object ID와 address

객체는 외부에 `Int` ID로 노출된다. ID는 안정적이다. Compaction은 객체의
논리 address만 바꾸고 ID는 바꾸지 않는다.

이 선택은 세 가지 장점이 있다.

- 외부 handle이 compaction 때문에 무효화되지 않는다.
- 테스트가 안정적인 ID를 기준으로 객체 생존 여부를 확인할 수 있다.
- 미래 backend에서는 ID를 handle table index, address를 실제 heap pointer로 치환할 수 있다.

### 객체 메타데이터

`GcObject`는 다음 정보를 가진다.

- `id`: 안정적인 object handle
- `kind`: record, array, closure, bytes, runtime frame
- `size`: allocator accounting에 쓰는 logical byte size
- `address`: compaction 대상 logical address
- `generation`: `0`은 nursery, `1`은 tenured
- `age`: minor collection 생존 횟수
- `color`: tri-color mark state
- `marked`: sweep fast path용 mark bit
- `pinned`: compaction 금지
- `large`: nursery 우회와 humongous region 배치 여부
- `region`: eden, survivor, old, humongous, pinned 중 현재 membership
- `refs`: outgoing object references
- `remembered`: old-to-young edge 보유 여부
- `allocatedCycle`: allocation이 일어난 collection cycle

### Heap 상태

`GcHeap`은 다음 상태를 소유한다.

- `objects: Map<Int, GcObject>`
- `roots: Set<Int>`
- `remembered: Set<Int>`
- `grey: List<Int>`
- `phase: TracePhase`
- `liveBytes`, `nextId`, `nextAddress`
- collection counters and config

이 구조는 전역 상태 없이 heap instance를 여러 개 만들 수 있게 한다. 테스트나
미래 isolate runtime에서 중요하다.

## Public API

### Construction

```osty
let mut heap = newGcHeap(productionGcConfig())
```

`productionGcConfig`는 보수적인 기본값이고, `testGcConfig`는 작은 limit와
작은 incremental budget으로 edge case를 쉽게 만든다.

### Allocation

```osty
let id = heap.allocate(GcRecord, 64)
let maybe = heap.tryAllocate(GcArray, 1024)
let old = heap.allocateTenured(GcClosure, 128)
```

`tryAllocate`가 기본 primitive다. 필요하면 minor collection, major collection을
순서대로 시도한 뒤에도 limit을 넘으면 `Err(String)`을 반환한다.

`allocate`는 편의 API다. `tryAllocate(...).unwrap()`과 같은 정책이다.
`tryAllocateTenured`와 `allocateTenured`는 compiler/runtime이 long-lived object를
바로 old generation에 배치해야 할 때 쓰는 명시적 old-space allocation path다.

### Roots

```osty
heap.addRoot(id)
heap.removeRoot(id)
```

Root는 collector의 시작점이다. Incremental marking 중 새 root가 추가되면
즉시 shade되어 snapshot invariant를 유지한다.

### Edges

```osty
heap.link(parent, child)
heap.unlink(parent, child)
heap.gcPreWriteBarrier(GcBarrierObjectField, parent, oldChild)
heap.gcPostWriteBarrier(GcBarrierObjectField, parent, child)
let loaded = heap.gcLoadBarrier(child)

let lowered = heap.lowerStructFieldStore(parent, 0, "next", child)
let tuple = heap.lowerTuplePayloadStore(parent, 1, "payload", child)
let receiver = heap.lowerReceiverFieldStore(parent, 2, "self.field", child)
let arraySlot = heap.lowerArrayElementStore(array, 0, child)
let listSlot = heap.lowerListElementStore(list, 0, child)
let mapKey = heap.lowerMapKeyStore(table, 10, key)
let mapValue = heap.lowerMapValueStore(table, 11, value)
let setKey = heap.lowerSetKeyStore(set, 4, key)
```

`link`는 ABI v1 post-write barrier를 수행한다.

- Generational barrier: old object가 young object를 가리키면 parent를 remembered set에 넣는다.
- Incremental barrier: marking 중 black object가 white object를 새로 가리키면 child를 grey로 shade한다.

`unlink`는 edge를 제거한 뒤 remembered set을 재구성한다. 구현은 단순하지만
정확성을 우선한다. Removal 전에는 ABI v1 pre-write barrier hook을 호출한다.

`gcLoadBarrier`는 Phase 17 현재 forwarding 없이 같은 ID를 돌려준다. ABI는 이미
분리해 두었으므로 Phase 22 이후 concurrent relocation을 도입해도 compiler call
surface를 바꾸지 않는다.

Phase 18 prototype은 compiler-lowered object store를 executable contract로 모델링한다.

- `lowerStructFieldStore`: user-defined struct field write를 object-field barrier로 낮춘다.
- `lowerTuplePayloadStore`: tuple-like payload overwrite를 같은 discipline으로 낮춘다.
- `lowerEnumPayloadStore`: enum payload replacement를 같은 discipline으로 낮춘다.
- `lowerReceiverFieldStore`: `self.field = value` 같은 receiver mutation path를 낮춘다.

Lowering 결과는 `GcLoweredStoreEvent`로 남는다. Event는 store kind, owner, slot,
old/new value, barrier slot kind, pre/post barrier 호출 여부를 기록한다.
`GcHeap`은 `GcLoweredPointerSlot` side table을 유지하고, 이 table의 pointer-bearing
slot projection이 `GcObject.refs`에 반영되는지를 `validateHeap()`에서 검증한다.
Overwrite는 old value에 pre-write hook을 호출하고, new value에 post-write hook을
호출한 뒤 remembered set을 재구성한다.

Phase 19 prototype은 collection store lowering을 같은 side table에 통합한다.

- `lowerArrayElementStore`: array element write를 array-element barrier로 낮춘다.
- `lowerListElementStore`: list/vector backing store update를 같은 discipline으로 낮춘다.
- `lowerMapKeyStore`: pointer-bearing map key insert/update를 lowered pointer slot으로 남긴다.
- `lowerMapValueStore`: map value insert/update를 같은 entry storage contract로 낮춘다.
- `lowerSetKeyStore`: set 내부 key slot store가 object graph에서 숨겨지지 않게 한다.

Map key/value와 set key는 collection backing storage의 pointer slot이다. Logical bucket
번호와 physical pointer slot 번호가 다를 수 있으므로 runtime lowering은 안정적인 slot
id를 넘겨야 한다. Prototype에서는 caller가 slot id를 제공하고, 이름은 `map.key[slot]`
같은 deterministic descriptor로 기록한다.

Phase 20 prototype은 compiler/runtime generated execution state mutation도 같은 lowered
pointer slot contract에 태운다.

- `lowerRuntimeFrameSlotStore`: runtime frame spill/local slot write를 `GcBarrierRuntimeFrame`
  barrier로 낮춘다.
- `lowerAsyncFrameRootStore`: suspended async frame root snapshot update를
  `GcBarrierAsyncFrameRoot` barrier로 낮춘다.

Async frame root lowering은 runtime frame object refs와 async frame snapshot roots를
함께 갱신한다. Incremental marking 중 black frame에 late root가 추가되면 post-write
barrier가 root를 shade한다. Overwrite는 old snapshot root에 pre-write hook을 호출하고,
같은 frame의 다른 lowered async root slot이 old object를 아직 가리키는지 확인한 뒤
snapshot root를 제거한다.

Phase 21 prototype은 remembered set의 exact object set을 native backend가 쓰는 card table
shape로 매핑한다.

- `GcCardTable`: fixed-size card, dirty card set, dirty/scan/clear counters를 가진다.
- Post-write barrier는 old-to-young edge를 발견하면 owner object의 card를 dirty로 만든다.
- `rebuildRemembered`는 exact remembered object set과 dirty card projection을 함께 재구성한다.
- `validateHeap`은 remembered object가 dirty card에 속하는지, stale dirty card가 없는지 검증한다.

Prototype에서는 store slot address가 아직 없으므로 object start address를 card index로 낮춘다.
Native backend에서는 field/element byte offset으로 더 세밀한 card를 dirty하게 된다. Public API는
`dirtyCardCount`, `cardIndexForObject`, `cardIsDirty`, card scan/clear counters를 노출해
compiler-lowered barrier와 minor collector가 같은 contract를 공유하는지 확인한다.

Phase 22 prototype은 minor collector backend를 root scan + card scan + young copy/promotion
pipeline으로 관측 가능하게 만든다.

- Minor collection은 dirty card에 걸린 remembered old object만 young child scan 대상으로 삼는다.
- `GcMinorCollectionTrace`는 manual/global/async root scan, remembered/card scan, young mark,
  survivor copy, promotion, eden reset, survivor swap을 기록한다.
- `GcStats.rememberedScanned`는 이제 card table scan으로 실제 방문한 remembered object 수를 뜻한다.
- Minor 후 dirty cards는 clear되고, 살아남은 old-to-young edge가 있으면 rebuilt projection으로
  다시 dirty 상태가 된다.

Phase 23 prototype은 full-heap major collector를 mark work queue와 type scan 관점에서
관측 가능하게 만든다.

- `GcFullMarkTrace`: root seed count, mark queue seed, scanned object 수, pointer slot scan 수,
  swept object 수를 기록한다.
- Object scan descriptor는 현재 `ObjectKind + refs` projection으로 모델링한다. Native backend에서는
  이 projection이 type descriptor bitmap/offset table로 내려간다.
- `majorCollect`는 full heap mark/sweep 후 이 trace를 남겨 full mark backend가 region table과
  연결됐는지 검증한다.

Phase 24 prototype은 compaction의 object move를 forwarding table로 남긴다.

- `GcForwardingEntry`: object id, old/new address, size, old/new region을 기록한다.
- `compactLive`는 pinned/humongous object를 evacuation에서 건너뛰고 movable live object만
  forwarding entry로 기록한다.
- `GcEvacuationTrace`는 selected object, forwarded object, skipped pinned/humongous object,
  copied bytes를 기록한다.

Phase 25 prototype은 stable object id model 위에서 remap verification을 수행한다.

- Prototype은 pointer value를 object id로 모델링하므로 실제 root/ref 값은 바꾸지 않는다.
  대신 roots, globals, async frame roots, object refs, lowered pointer slots가 forwarding table의
  moved object를 얼마나 참조했는지 `GcRemapTrace`로 기록한다.
- `verifyForwardingTable`은 forwarding entry가 live moved object의 current address와 일치하고
  pinned/humongous object를 잘못 forward하지 않았는지 검증한다.
- Native backend에서는 이 trace contract가 stack/global/object slot rewrite pass로 내려간다.

Phase 26 prototype은 incremental marking을 runtime scheduler가 호출할 수 있는 형태로
끌어올린다.

- `GcIncrementalSchedulerTrace`는 safepoint count, scheduled step, mutator assist step,
  started/completed cycle, allocation debt, last budget을 기록한다.
- `schedulerSafepoint(reason)`은 configured interval마다 idle heap에서 incremental major를
  시작하고, marking 중이면 budgeted step을 실행한다.
- `mutatorAssist(bytes)`는 allocation slow path의 debt를 누적하고 threshold를 넘으면 동일한
  incremental cycle을 시작한다.

Phase 27 prototype은 mostly-concurrent mark의 정확성 contract를 executable model로 둔다.

- `startMostlyConcurrentMark`는 root snapshot과 mutator epoch를 기록하고 mark phase를 연다.
- `stepMostlyConcurrentMark`는 concurrent worker slice처럼 별도 budget으로 grey queue를
  drain한다.
- `finishMostlyConcurrentMark`는 final remark에서 roots/global/async frame을 다시 shade하고,
  barrier shade delta를 기록해 late mutation이 살아남는지 검증한다.

Phase 28 prototype은 mechanism과 policy를 분리한다.

- `GcPolicyProfile`은 latency-first, throughput-first, memory-tight profile을 가진다.
- `gcConfigForProfile`은 nursery/survivor size, promotion age, incremental/concurrent budget,
  mutator assist threshold, evacuation threshold를 profile별로 조정한다.
- `shouldEvacuateRegion(livePercent)`는 future region selection policy의 public contract다.

Phase 29 prototype은 collector를 디버깅 가능한 runtime subsystem으로 노출한다.

- `GcDiagnosticsSnapshot`은 heap size, fragmentation, roots, remembered/card state, barrier
  counters, scheduler/concurrent trace, last collection stats, validation result를 한 번에 담는다.
- `GcHeapDump`는 deterministic object/edge/root list를 제공하고, `objectGraphDebug`와
  `heapObjectDebugLine`은 test failure나 profiler log에 바로 붙일 수 있는 textual graph output을
  만든다.
- `GcStressMode`는 minor/full/incremental/no-compaction/always-compact collection path를
  도구가 명시적으로 구동할 수 있게 해 재현성을 높인다.

Phase 30 prototype은 production hardening gate를 값으로 만든다.

- `GcRuntimeContract`는 OOM result, abort behavior, defer stack map, FFI pinning,
  stable identity, moving collector remap, `std.ref.same` contract를 명시한다.
- `GcBarrierAudit`은 lowered pointer slots와 remembered-set projection이 실제 object graph와
  맞는지 검사한다.
- `GcPerformanceThresholds`와 `GcHardeningReport`는 heap validity, barrier audit, pause/
  fragmentation/live-size thresholds를 한 번에 확인하는 release gate 역할을 한다.

Phase 31 prototype은 LLVM/native backend가 물리 pointer 대신 안정적인 managed handle을
GC 경계로 쓰는 contract를 executable model로 둔다.

- `GcLlvmHandleEntry`는 lowered allocation site, object id, release/pin/root flag, load/store
  accounting을 기록한다.
- `allocateLlvmHandle`과 `allocateLlvmHandleThread`는 backend-lowered allocation이 object id와
  handle을 동시에 얻는 경로다.
- `llvmHandleSame`은 compaction 뒤에도 logical identity가 physical address가 아니라 handle/object id
  기준으로 유지되는지 검증한다.

Phase 32 prototype은 LLVM stack/global/runtime root metadata를 heap marking과 연결하는 root
bridge를 모델링한다.

- `GcLlvmRootRefEntry`는 stack root, pinned root, external root 같은 compiler/runtime root slot을
  handle에 바인딩한다.
- `llvmRuntimeRoots`는 root bridge가 mark phase에 seed하는 object id set이며, minor/full/
  incremental/concurrent mark 모두에서 scan된다.
- Root ref release 뒤에는 runtime root set에서 제거되고, 다음 collection에서 stale handle load가
  감지된다.

Phase 33 prototype은 LLVM lowering에서 allocation/store/barrier event가 남는 contract를 고정한다.

- `GcLoweredAllocationTrace`는 backend target, allocation site, object kind/size, TLAB fast path 여부를
  기록한다.
- `lowerLlvmStore`는 handle 기반 store를 existing `lowerStructFieldStore`와 barrier ABI v1로 낮추고,
  old/new object identity와 post-write 호출 여부를 trace로 남긴다.
- 이 표면은 현재 `internal/llvmgen`의 `emitLet`, `emitAssign`, `emitStructLit`, `emitCall`에 실제
  helper call을 꽂을 때의 acceptance contract다.

### Barrier ABI v1

`gcBarrierAbiV1()`은 compiler/runtime 계약을 값으로 노출한다.

- Version: `1`
- Pre-write symbol: `osty.gc.pre_write_v1`
- Post-write symbol: `osty.gc.post_write_v1`
- Load symbol: `osty.gc.load_v1`
- Required today: post-write
- Reserved today: pre-write, load

Barrier slot kind는 object field, array element, closure capture, global root,
async frame root, runtime frame mutation을 구분한다. Prototype은 object graph edge,
closure capture rewrite, user object field store lowering, collection backing store
lowering, runtime frame root lowering을 이 ABI로 낮췄다.

### Collection

```osty
let minor = heap.minorCollect()
let major = heap.majorCollect()

heap.startIncrementalMajor()
let step = heap.stepIncremental()
let done = heap.finishIncremental()
```

모든 collection은 `GcStats`를 반환한다.

## Algorithms

### Minor collection

Minor collection은 nursery만 대상으로 한다.

1. Young object mark state를 초기화한다.
2. Young root를 grey로 shade한다.
3. Young global root와 suspended async frame root를 grey로 shade한다.
4. Old root, old global/async root, remembered old object의 young children을 shade한다.
5. Grey queue를 young-only로 drain한다.
6. Unmarked young object를 sweep한다.
7. Surviving young object는 age를 올린다.
8. Promotion threshold, pinning, survivor overflow를 기준으로 survivor 또는 old target을 선택한다.
9. Remembered set을 재구성한다.

이 방식은 old generation 전체를 trace하지 않으면서 old-to-young reference의
정확성을 보존한다.

### Nursery and survivor policy

Nursery는 두 공간으로 모델링한다.

- `eden`: 새 small object가 들어가는 공간이다. Age는 0이다.
- `survivor`: minor collection을 한 번 이상 살아남았지만 아직 promotion threshold에
  도달하지 않은 young object의 공간이다.
- `old`: promotion threshold 도달, pinning, survivor overflow fallback으로 이동한
  long-lived object의 공간이다.

Minor GC는 copying collector의 to-space 선택을 다음 순서로 모델링한다.

1. Marked young object의 age를 1 증가시킨다.
2. Pinned object는 old/pinned target으로 promote한다.
3. `age >= promotionAge`이면 old로 promote한다.
4. `survivorLimit <= 0`이면 survivor 공간을 쓰지 않고 old로 promote한다.
5. 현재 minor cycle의 survivor to-space 사용량에 object size를 더했을 때
   `survivorLimit`를 넘으면 old로 promote한다.
6. 위 조건에 걸리지 않으면 generation 0을 유지하고 `GcSurvivorRegion`에 배치한다.

이 정책은 survivor overflow가 생겨도 collection 실패로 이어지지 않는다. 대신
old generation으로 밀어내며, Phase 15의 tenured allocation backend가 이 promoted
object의 실제 old-space 배치를 책임진다.

### Tenured allocation backend

Tenured backend는 nursery/TLAB path와 별도인 old generation allocator다.

- `GcTenuredAllocator.cursor`: old-space bump fallback cursor
- `freeList`: compaction gap, promotion source block, reclaimed old block의 reusable block list
- `bumpAllocations`: free block이 없어 heap span을 확장한 횟수
- `reuseAllocations` / `reusedBytes`: free list 재사용 회계
- `directAllocations`: `allocateTenured`로 들어온 long-lived allocation 횟수
- `promotedAllocations`: minor promotion이 old backend를 사용한 횟수

정책은 의도적으로 단순한 first-fit split free list다. Segregated size class와
per-region free lists는 Phase 15 이후 실제 backend로 내릴 때 확장할 수 있다.
Prototype에서는 먼저 다음 contract를 고정한다.

1. Long-lived allocation은 nursery pressure를 증가시키지 않는다.
2. Movable young object promotion은 old backend에서 새 address를 받는다.
3. Promotion source slot은 reusable free block으로 반환된다.
4. Free block이 충분하면 old allocation은 heap span을 늘리지 않는다.
5. Major compaction 후 pinned/humongous gap은 free list와 `fragmentedBytes()`에 함께 드러난다.

### Humongous object space

Humongous space는 큰 string/bytes/array/object를 nursery evacuation cost에서 분리한다.
Prototype 기준 large object는 `largeObjectThreshold > 0 && size >= largeObjectThreshold`
이면 humongous object가 된다.

- `GcHumongousAllocator.cursor`: humongous bump fallback cursor
- `freeList`: unreachable humongous object가 반환한 reusable block list
- `bumpAllocations`: 새 humongous range를 heap span 끝에 예약한 횟수
- `reuseAllocations` / `reusedBytes`: humongous free block 재사용 회계
- `allocatedObjects` / `allocatedBytes`: humongous allocation throughput
- `reclaimedObjects` / `reclaimedBytes`: major sweep에서 회수한 humongous object 회계

정책은 다음처럼 고정한다.

1. Large allocation은 nursery와 TLAB을 우회한다.
2. Large `allocateTenured` 요청도 old free list가 아니라 humongous space로 라우팅한다.
3. Unreachable humongous object는 major sweep에서 dedicated humongous free block으로 반환된다.
4. Tenured allocation은 humongous free block을 소비하지 않는다.
5. Compaction은 live humongous object와 humongous free block의 address를 보존한다.

### Major collection

Major collection은 전체 힙을 대상으로 한다.

1. 모든 object mark state를 초기화한다.
2. Stack/manual root set, global root table, suspended async frame table에서 tri-color
   mark를 시작한다.
3. Grey queue가 빌 때까지 full graph를 trace한다.
4. Unmarked object를 sweep한다. Cycle도 여기서 회수된다.
5. Live object를 tenured로 정규화한다.
6. Movable object를 compact한다. Pinned/humongous object는 address를 유지한다.
7. Remembered set을 재구성한다.

Cycle collection은 별도 refcount cycle detector가 아니라 tracing으로 해결한다.
Root에서 닿지 않는 strongly connected component는 mark되지 않으므로 sweep된다.

### Incremental marking

Incremental major collection은 major mark phase를 step 단위로 나눈다.

1. `startIncrementalMajor`가 모든 mark state를 초기화하고 roots를 shade한다.
2. `stepIncremental`은 `incrementalBudget`만큼 grey object를 blacken한다.
3. Grey queue가 남아 있으면 phase는 `GcMarking`으로 유지된다.
4. Grey queue가 비면 sweep, compact, remembered rebuild를 수행하고 `GcIdle`로 돌아간다.

Write barrier는 Dijkstra-style insertion barrier다. Marking 중 black object가
white object를 새로 참조하면 child를 grey로 바꾼다. 따라서 black object가
white object를 숨기는 상황이 생기지 않는다.

Allocation slow path는 incremental marking 중 nested minor collection을 만들지 않는다.
이 제약은 Phase 8 stress에서 고정됐다. Mark bitmap을 세대별로 분리하기 전까지는
major marking과 young-only marking을 같은 heap에서 동시에 실행하지 않는다.

### Compaction

Compaction은 stable ID를 유지하면서 logical address만 재배치한다.

- Live movable object는 낮은 address부터 packed된다.
- Pinned object는 address가 유지된다.
- Pinned object 뒤의 cursor는 overlap을 피하도록 pinned end 이후로 이동한다.
- Humongous object도 기본적으로 움직이지 않으며, cursor는 humongous end 이후로 이동한다.
- `fragmentedBytes`는 heap span과 live bytes 차이로 계산한다.

현재 구현은 deterministic testability를 위해 object ID 순서로 compact한다. 실제
native backend에서는 allocation address order나 size class order를 선택할 수 있다.

## Invariants

다음 조건은 항상 유지되어야 한다.

- `liveBytes`는 `objects`에 남아 있는 object size 합과 같다.
- `roots`에는 live object ID만 남아야 한다.
- `globalRoots`의 ready entry는 live object ID를 가져야 한다.
- `globalRoots`의 poisoned entry는 object ID를 가지면 안 된다.
- `closureEnvironments`는 live closure object에 붙어야 하며, closure refs와 capture
  descriptor가 일치해야 한다.
- Suspended `asyncFrames`는 live runtime-frame object와 live snapshot roots를 가져야 한다.
- Running/completed/cancelled async frame entry는 roots를 보유하지 않아야 한다.
- Active `tlabs`는 cursor가 `[start, limit]` 안에 있어야 하며, collection start에서
  remaining bytes를 waste로 retire한다.
- `remembered`에는 old object만 들어간다.
- `remembered` object는 적어도 하나의 live young child를 가진다.
- Marking 중 black object는 white child를 직접 새로 숨길 수 없다.
- Major collection 뒤 movable live objects는 가능한 낮은 address로 이동한다.
- Non-pinned `large` object는 tenured이고 `GcHumongousRegion`에 속한다.
- `pinned` object는 `GcPinnedRegion`에 속하고 compaction에서 address가 유지된다.
- Non-large movable object의 region은 generation/age에서 결정된다.
- Non-large movable young object는 `age == 0`이면 eden, `age > 0`이면 survivor에 속한다.
- Survivor region byte 합은 `survivorLimit`를 넘지 않는다.
- Tenured free block은 live object address range와 겹치면 안 된다.
- Tenured free block끼리도 서로 겹치면 안 된다.
- Tenured allocator cursor는 `nextAddress`를 넘지 않는다.
- Humongous free block은 live object address range와 겹치면 안 된다.
- Humongous free block은 tenured free block과 겹치면 안 된다.
- Humongous free block끼리도 서로 겹치면 안 된다.
- Humongous allocator cursor는 `nextAddress`를 넘지 않는다.
- Barrier counters는 음수가 될 수 없다.
- Post-write barrier가 old-to-young edge를 만들면 remembered set state가 즉시 갱신된다.
- Marking 중 post-write barrier가 black-to-white edge를 만들면 new value가 grey로 shade된다.
- Load barrier는 current non-moving prototype에서 forwarding 없이 identity를 보존한다.
- `allocate`가 반환한 ID는 object가 sweep되기 전까지 안정적이다.

## Failure Policy

Osty language spec은 OOM을 recoverable error로 보지 않는다. 이 library는 두 층을 둔다.

- `tryAllocate`: `Result<Int, String>`으로 OOM을 보고한다.
- `allocate`: 편의 API이며 OOM이면 `unwrap` panic으로 실패한다.

미래 runtime integration에서는 `tryAllocate`의 error branch를 `std.process.abort`
또는 runtime trap으로 연결할 수 있다.

## Test Strategy

필수 테스트는 다음을 포함한다.

- Root에서 끊어진 cycle이 major collection에서 회수되는지
- Old-to-young edge가 remembered set을 통해 minor collection에서 보존되는지
- Minor survival 후 promotion이 이루어지는지
- Nursery cycle이 root 없으면 minor collection에서 회수되는지
- Incremental marking 중 black-to-white edge가 barrier로 보존되는지
- Major compaction이 hole을 닫고 fragmentation을 0으로 줄이는지
- Heap limit 초과 allocation이 full collection 이후에도 불가능하면 `Err`를 반환하는지
- Large object가 nursery pressure를 유발하지 않고 humongous region에 들어가는지
- Pinned object가 compaction hole을 남기고, unpin 후 다시 compact되는지
- Seed 기반 random graph가 major collection 뒤 reference reachability와 일치하는지
- Incremental marking 중 black-to-white insertion barrier가 stress graph에서도 보존되는지
- Safepoint plan이 call/allocation/loop/async yield별 live root contract를 보존하는지
- Duplicate root slot, missing stack map, dangling derived root를 validator가 거부하는지
- Stack map이 live pointer bitmap, overflow slot list, derived-root mapping을 생성하는지
- Stack map builder가 scalar slot root, missing frame slot, cleanup path root loss를 거부하는지
- Global/static root가 minor, major, incremental collection에서 object를 보존하는지
- Partially initialized global은 collection 중 살아남고 poisoned global은 회수되는지
- Cross-package global reference가 일반 graph edge를 통해 보존되는지
- Closure descriptor가 direct/boxed/mutable/scalar capture를 정확히 trace하는지
- Closure capture update가 stale capture object를 계속 살리지 않는지
- Suspended async frame이 resume 전까지만 frame/root snapshot을 보존하는지
- TLAB fast path가 refill 뒤 같은 owner-local buffer에서 global cursor 없이 allocate하는지
- TLAB refill이 tail waste를 기록하고 nursery pressure collection을 trigger하는지
- Collection이 active TLAB을 retire하고 large allocation이 TLAB을 우회하는지
- Minor collection 뒤 rooted eden object가 survivor space에서 aging되는지
- 두 번째 minor collection에서 aged survivor가 promotion threshold로 old에 올라가는지
- Survivor overflow가 collection 실패 대신 promotion fallback으로 처리되는지
- Explicit tenured allocation이 nursery를 우회하고 old backend 회계를 남기는지
- Minor promotion이 old backend address를 받고 source slot을 free list에 반환하는지
- Tenured allocation이 promotion/compaction에서 생긴 free block을 재사용하는지
- Dead humongous object가 major collection에서 dedicated humongous free list로 반환되는지
- Humongous allocation이 dedicated free block을 재사용하고 split remainder를 남기는지
- Tenured allocation이 humongous free block과 address space를 섞지 않는지
- Barrier ABI v1 symbol/version/required flags가 stable한지
- `link`/`unlink`/closure capture update가 pre/post barrier hook을 호출하는지
- Load barrier가 현재 stable ID를 반환하고 forwarding count를 만들지 않는지
- Struct field store lowering이 field slot side table과 refs projection을 함께 갱신하는지
- Tuple/enum payload overwrite가 old pointer pre-write barrier와 new pointer post-write barrier를 호출하는지
- Receiver field mutation lowering이 incremental marking 중 late edge를 shade하는지
- Array/list/map/set collection store lowering이 array-element barrier와 refs projection을 함께 갱신하는지
- Collection slot overwrite가 다른 slot의 공유 pointer edge를 실수로 제거하지 않는지
- Runtime frame slot store가 runtime-frame barrier와 remembered set projection을 함께 갱신하는지
- Async frame root overwrite가 old snapshot root를 제거하고 late incremental root를 shade하는지
- LLVM handle이 compaction 전후에도 identity를 유지하는지
- LLVM runtime root bridge가 mark root로 작동하고 release 뒤 stale handle load를 감지하는지
- LLVM lowered store가 handle identity와 write barrier trace를 함께 남기는지

## 해야 할 일

이 섹션은 "현재 예제 패키지"에서 "진짜 Osty runtime GC"로 가기 위한 작업 목록이다.
순서는 대략적인 의존성을 따른다.

### Phase 1: GC RFC 고정

- 최종 collector 목표를 precise, generational, regional, incremental, compacting으로 고정한다.
- Conservative GC를 production path에서 배제한다.
- Identity와 physical address 분리 원칙을 language/runtime contract로 적는다.
- Detailed baseline은 `Phase 1 Detail: GC RFC 고정`을 따른다.
- Acceptance criteria: 나중에 mark-sweep-only, conservative, non-moving design으로 후퇴하지 않도록 문서가 명확해야 한다.

### Phase 2: Object Header 설계

- Type descriptor pointer, mark state, generation bits, pinned bit, forwarding state를 header layout에 배치한다.
- Header와 side table에 들어갈 metadata를 분리한다.
- Alignment, size class, identity hash seed 정책을 정한다.
- Detailed baseline은 `Phase 2 Detail: Object Header 설계`를 따른다.
- Acceptance criteria: object header만 보고 scan, mark, move 가능 여부를 판단할 수 있어야 한다.

### Phase 3: Type Descriptor 설계

- Struct, enum, tuple, closure, array, string/bytes descriptor shape를 정의한다.
- Pointer bitmap과 pointer offset list 중 기본 representation을 정한다.
- Generic instantiation descriptor 생성 정책을 정한다.
- Detailed baseline은 `Phase 3 Detail: Type Descriptor 설계`를 따른다.
- Acceptance criteria: 모든 Osty value layout이 정확한 trace descriptor를 가진다.

### Phase 4: Region Heap 설계

- Region kind를 free, eden, survivor, old, humongous, pinned으로 정의한다.
- Region table metadata와 region state transition을 문서화한다.
- Region selection score를 live bytes, fragmentation, pin density 기준으로 설계한다.
- Detailed baseline은 `Phase 4 Detail: Region Heap 설계`를 따른다.
- Acceptance criteria: 단일 heap sweep 없이 region 단위 collection/evacuation을 설명할 수 있어야 한다.

### Phase 5: Prototype Invariant Checker

- Status: done for the executable prototype.
- `validateHeap()`를 추가한다.
- `liveBytes`, remembered set, roots, mark colors, generation invariants를 검사한다.
- 모든 collection 테스트 뒤 invariant check를 호출한다.
- Acceptance criteria: prototype 내부 상태가 깨지면 테스트가 즉시 실패한다.

### Phase 6: Prototype Telemetry 확장

- Status: done for the executable prototype.
- `GcStats`에 trigger reason, logical pause units, allocation bytes since last GC를 추가한다.
- Promotion rate, survival rate, fragmentation before/after를 기록한다.
- Golden telemetry tests를 추가한다.
- Acceptance criteria: collection이 왜 발생했고 어떤 비용/효과가 있었는지 stats로 설명된다.

### Phase 7: Large Object와 Pinned Prototype

- Status: done for the executable prototype.
- Nursery를 우회하는 large object path를 prototype에 추가한다.
- Pinned object가 compaction hole을 만드는 케이스를 테스트한다.
- Pinned region 개념을 prototype model에 반영한다.
- Acceptance criteria: movable object와 non-movable object 정책이 명확히 갈라진다.

### Phase 8: Random Graph Stress Prototype

- Status: done for the executable prototype.
- Random object graph 생성기를 만든다.
- Link/unlink/root add/remove/collection interleaving을 fuzz-like로 검증한다.
- Reference model과 GC 결과를 비교한다.
- Acceptance criteria: cycle, remembered edge, incremental barrier가 무작위 그래프에서도 깨지지 않는다.

### Phase 9: IR Safepoint Contract

- Status: done for the executable prototype.
- IR에 safepoint 후보를 표현하는 방식을 설계한다.
- Function call, allocation slow path, loop backedge, async yield를 safepoint로 분류한다.
- Safepoint별 live pointer slot metadata contract를 정의한다.
- Acceptance criteria: backend가 stack map을 만들 수 있는 정보가 IR에서 사라지지 않는다.

### Phase 10: Stack Map Format

- Status: done for the executable prototype.
- Stack frame layout과 live pointer bitmap format을 정의한다.
- Register spill, temporary, closure capture, tuple destructuring local을 다룬다.
- `defer`, `?`, early return path의 live root 보존 규칙을 정한다.
- Acceptance criteria: 어느 safepoint에서 major GC가 떠도 live local reference가 보존된다.

### Phase 11: Global and Static Roots

- Status: done for the executable prototype.
- Top-level `let`, constants, package globals의 root table을 설계한다.
- Module initialization 중 partially initialized global을 다루는 규칙을 정한다.
- Cross-package global references를 root graph에 포함한다.
- Acceptance criteria: global object가 collection timing에 따라 사라지지 않는다.

### Phase 12: Closure and Async Frame Roots

- Status: done for the executable prototype.
- Closure environment descriptor를 정의한다.
- Captured mutable binding과 boxed capture의 trace 규칙을 정한다.
- Suspended async/task frame root scan을 설계한다.
- Acceptance criteria: closure와 task suspension이 root liveness를 잃지 않는다.

### Phase 13: TLAB Allocation Fast Path

- Status: done for the executable prototype.
- Thread-local allocation buffer 구조를 설계한다.
- Bump pointer fast path와 refill slow path를 나눈다.
- TLAB waste accounting과 refill trigger를 정한다.
- Acceptance criteria: common allocation이 global lock 없이 실행될 수 있다.

### Phase 14: Nursery and Survivor Spaces

- Status: done for the executable prototype.
- Eden, survivor aging, promotion threshold를 설계한다.
- Minor GC에서 copy/promotion target region을 선택하는 정책을 정한다.
- Survivor overflow 시 promotion fallback을 정의한다.
- Acceptance criteria: short-lived object는 싸게 죽고, long-lived object는 안정적으로 old로 이동한다.

### Phase 15: Tenured Allocation Backend

- Status: done for the executable prototype.
- Old generation allocation policy를 정한다.
- Free list, segregated size class, region bump allocation 중 초기 방식을 선택한다.
- Fragmentation accounting을 region metadata에 연결한다.
- Acceptance criteria: promoted object와 long-lived allocation이 nursery 정책과 분리된다.

### Phase 16: Humongous Object Space

- Status: done for the executable prototype.
- Large string, bytes, large array threshold를 정의한다.
- Humongous region allocation/reclaim 정책을 만든다.
- Humongous object compaction 여부와 pinning interaction을 정한다.
- Acceptance criteria: 큰 객체가 nursery/evacuation cost를 망치지 않는다.

### Phase 17: Barrier ABI v1

- Status: done for the executable prototype.
- Runtime barrier function signatures를 고정한다.
- Post-write barrier, pre-write barrier, load barrier hook을 분리한다.
- Compiler가 호출할 stable ABI와 runtime이 inline할 fast path를 나눈다.
- Acceptance criteria: collector policy가 바뀌어도 compiler call surface는 안정적이다.

### Phase 18: Store Lowering Integration

- Status: done for the executable prototype.
- Struct field store가 barrier를 호출하게 한다.
- Tuple/enum payload update가 있다면 barrier 대상에 포함한다.
- Self/method receiver mutation path를 검토한다.
- Acceptance criteria: user-defined object field update에서 barrier 누락이 없다.

### Phase 19: Collection Store Barrier

- Status: done for the executable prototype.
- List/array element write barrier를 붙인다.
- Map key/value insert/update barrier를 붙인다.
- Set 내부 key store가 pointer-bearing type일 때의 규칙을 정한다.
- Acceptance criteria: std collection 내부 mutation이 GC graph를 숨기지 않는다.

### Phase 20: Closure and Runtime Frame Barrier

- Status: done for the executable prototype.
- Closure capture update barrier를 붙인다.
- Async frame, task handle, channel buffer, runtime frame mutation barrier를 설계한다.
- Compiler-generated temporary heap object update path를 점검한다.
- Acceptance criteria: compiler-generated heap mutation도 user field store와 같은 barrier discipline을 따른다.

### Phase 21: Card Table and Remembered Set

- Status: done for the executable prototype.
- Region/card size를 정한다.
- Card dirtying, card scan, card clearing 정책을 만든다.
- Prototype의 `Set<Int>` remembered set을 native card table 설계로 매핑한다.
- Acceptance criteria: minor GC가 old generation 전체 scan 없이 old-to-young edges를 찾는다.

### Phase 22: Minor Collector Backend

- Status: done for the executable prototype.
- Root scan + remembered/card scan + young copy/promotion pipeline을 구현한다.
- Eden reset과 survivor swap을 구현한다.
- Minor collection telemetry를 연결한다.
- Acceptance criteria: nursery pressure만으로 young collection이 정확히 실행된다.

### Phase 23: Full Mark Collector Backend

- Status: done for the executable prototype.
- Mark work queue를 구현한다.
- Type descriptor 기반 object scan을 구현한다.
- Full heap mark/sweep을 region table과 연결한다.
- Acceptance criteria: root-unreachable cycle과 old garbage가 회수된다.

### Phase 24: Evacuation and Forwarding

- Status: done for the executable prototype.
- Forwarding pointer/table을 구현한다.
- Selected region live object를 evacuation target으로 복사한다.
- Object header forwarding state와 remap phase를 연결한다.
- Acceptance criteria: evacuated object의 모든 reference가 새 location으로 갱신된다.

### Phase 25: Root and Object Remapping

- Status: done for the executable prototype.
- Stack roots, globals, closure env, object fields를 forwarding destination으로 갱신한다.
- Remap 중 pinned/humongous object를 건너뛰는 규칙을 구현한다.
- Remap verification pass를 추가한다.
- Acceptance criteria: compaction 후 stale pointer가 남지 않는다.

### Phase 26: Incremental Marking Scheduler

- Status: done for the executable prototype.
- Incremental budget accounting을 구현한다.
- Allocation slow path와 loop/call safepoint에서 mark step을 실행한다.
- Mutator assist policy를 도입한다.
- Acceptance criteria: configured budget을 넘기지 않고 mark progress가 보장된다.

### Phase 27: Mostly-Concurrent Marking

- Status: done for the executable prototype.
- Concurrent marker worker와 mark queue synchronization을 설계한다.
- Barrier buffer flush와 remark safepoint를 구현한다.
- Memory ordering과 data race 규칙을 문서화한다.
- Acceptance criteria: concurrent mark가 mutator mutation과 race 없이 정확성을 유지한다.

### Phase 28: Policy and Profiles

- Status: done for the executable prototype.
- Latency-first, throughput-first, memory-tight profile을 정의한다.
- Nursery size, promotion age, region evacuation threshold, incremental budget을 profile별로 조정한다.
- Runtime config와 `osty run/build` profile config를 연결한다.
- Acceptance criteria: collector mechanism은 그대로 두고 policy만 바꿀 수 있다.

### Phase 29: Diagnostics, Stress, and Tooling

- Status: done for the executable prototype.
- GC stats snapshot을 runtime diagnostics에 노출한다.
- GC stress mode를 추가한다. 예: every allocation minor, frequent full GC, no compaction, always compact.
- Heap dump와 object graph debug output을 설계한다.
- Acceptance criteria: GC bug를 재현하고 telemetry로 원인을 좁힐 수 있다.

### Phase 30: Spec and Production Hardening

- Status: done for the executable prototype.
- OOM, `abort`, `defer`, FFI pinning, `std.ref.same`의 spec alignment를 완료한다.
- Long-running stress, randomized graph, generated-code barrier audit를 CI에 넣는다.
- Performance baseline과 regression thresholds를 만든다.
- Acceptance criteria: GC implementation detail이 language behavior로 새지 않고, production runtime으로 켤 수 있다.

### Phase 31: LLVM Managed Handle Table

- Status: done for the executable prototype.
- LLVM/native backend가 object address 대신 stable handle을 참조 경계로 쓰도록 모델링한다.
- Handle create/release/load/store/identity accounting과 stale load detection을 추가한다.
- Compaction 뒤에도 `llvmHandleSame`이 stable identity를 유지하는지 검증한다.
- Acceptance criteria: moving collector가 physical address를 바꿔도 backend-visible identity는 깨지지 않는다.

### Phase 32: LLVM Runtime Root Bridge

- Status: done for the executable prototype.
- Stack root, pinned root, external root를 `GcLlvmRootRefEntry`로 바인딩한다.
- Runtime root bridge를 minor/full/incremental/concurrent marking seed에 포함한다.
- Root ref release가 runtime root set을 비우고 이후 collection에서 객체가 회수되는지 검증한다.
- Acceptance criteria: LLVM stack/root metadata가 GC liveness와 정확히 연결된다.

### Phase 33: LLVM Lowered Allocation and Store Events

- Status: done for the executable prototype.
- Lowered allocation trace에 backend target, allocation site, kind/size, TLAB fast-path 여부를 기록한다.
- Handle 기반 lowered store를 write barrier ABI v1과 기존 lowered slot model로 연결한다.
- Old/new identity, post-write 호출 여부, handle store accounting을 테스트한다.
- Acceptance criteria: `internal/llvmgen`이 allocation/store helper를 호출할 때 검증할 executable contract가 있다.

### Phase 40: LLVM Pin and Runtime Root State Hardening

- Status: done for the executable prototype.
- Pinned root ref, handle pin bit, object pin state가 항상 같은 liveness transition을 보이도록
  `validateHeap()` invariant를 강화한다.
- Released root/root-ref 이후 runtime root set, live root-ref count, pinned handle/object count가
  함께 0으로 내려가는 회귀 테스트를 추가한다.
- Corrupted handle pin metadata가 살아 있는 pinned root ref와 어긋나면 validation이 즉시
  실패하도록 고정한다.
- Acceptance criteria: LLVM bridge pinning bug가 compaction/mark 이전에 heap validator에서 바로 드러난다.

### Phase 41: Barrier Failure Paths and Bridge Accounting Integrity

- Status: done for the executable prototype.
- `barrierAudit()`의 failure path를 lowered slot/owner ref mismatch와 remembered bookkeeping
  mismatch 테스트로 직접 잠근다.
- `hardeningReport()`가 heap invalid + barrier audit failure를 동시에 surface하는지 regression으로
  고정한다.
- LLVM bridge trace의 handle/root/runtime-root accounting이 실제 table/set state와 어긋나면
  `validateHeap()`가 즉시 실패하도록 한다.
- Acceptance criteria: audit/hardening drift와 bridge counter corruption이 런타임 연동 전에
  executable model에서 먼저 드러난다.
## Open Questions

- Generational policy를 two-generation으로 고정할지, nursery plus aging tenured buckets로 확장할지.
- Large object threshold를 byte size만으로 정할지, pointer density도 고려할지.
- Closure environment를 일반 record로 볼지, 별도 object kind로 최적화할지.
- Strings and bytes를 moving heap에 둘지, 별도 immutable/pointer-free arena에 둘지.
- Incremental barrier를 insertion barrier로 계속 갈지, SATB barrier로 바꿀지.
- Compaction을 항상 major collection에 붙일지, fragmentation threshold 기반으로 분리할지.
- Multi-threaded runtime에서 per-thread nursery를 둘지, shared nursery를 lock-free로 만들지.
- FFI pinned object가 많을 때 fallback policy를 어떻게 둘지.

## Milestones

### M1: Executable Prototype

현재 상태다. Osty code로 GC core가 동작하고, 주요 correctness test가 통과한다.
대략 Phase 1-8에 해당한다.

### M2: Verified Prototype

Heap invariant checker, random graph stress test, pinning/fragmentation tests를 추가한다.
대략 Phase 5-8을 production-grade prototype 품질로 끌어올리는 구간이다.

### M3: Metadata Prototype

IR 또는 generator layer에서 type descriptor와 pointer map을 실험적으로 생성한다.
대략 Phase 9-12에 해당한다.

### M4: Runtime Backend Spike

Toy native allocator 또는 LLVM-emitted runtime helper에서 실제 object header와 arena를 사용한다.
대략 Phase 13-17에 해당한다.

### M5: Compiler Integration

Generated code의 allocations, field stores, stack maps, safepoints가 GC runtime contract를 따른다.
대략 Phase 18-25에 해당한다.

### M6: Production Hardening

Stress mode, telemetry, profile knobs, FFI pinning, incremental scheduling을 갖춘다.
대략 Phase 26-30에 해당한다.

### M7: LLVM Backend Bridge

LLVM lowering이 stable handle, runtime root bridge, lowered allocation/store barrier event를
따르도록 연결한다. 대략 Phase 31-33에 해당한다.

### M8: LLVM Runtime ABI Surface

LLVM module render path가 GC runtime symbol을 직접 선언하고, self-hosted LLVM emitter가
allocation, root bind/release, post-write barrier call을 Osty-owned helper로 생성한다.
이 흐름은 LLVM smoke corpus 번호와 분리된 GC bridge phase이며, 대략 GC Phase 34-36에
해당한다.

- Phase 34는 `osty.gc.alloc_v1`, `osty.gc.post_write_v1`, `osty.gc.root_bind_v1`,
  `osty.gc.root_release_v1` 선언을 module-level ABI contract로 고정한다.
- Phase 35는 기존 scalar `llvmCall` 위에 void/runtime call wrapper를 얹어 backend lowering이
  GC helper call을 문자열 조립 없이 호출하게 만든다.
- Phase 36은 allocation site string, root bind/release, pointer-bearing store barrier가 같은
  smoke IR 안에서 함께 나타나는지 검증한다.
- Phase 37은 production `internal/llvmgen.Generate`에 `gcManaged` provenance를 추가해서
  raw global `ptr`와 future heap-managed `ptr`를 구분한다.
- Phase 38은 managed mutable `ptr` local만 stack/runtime-frame root slot으로 보고
  `osty.gc.root_bind_v1`/`osty.gc.root_release_v1` hook을 function return path에 배치할 수
  있게 한다.
- Phase 39는 managed `ptr` assignment에만 `osty.gc.post_write_v1` runtime-frame slot
  barrier를 배치하도록 guard를 둔다. 현재 raw string globals는 GC runtime에 opt-in하지 않는다.
- Phase 40은 pinned root ref, handle pin bit, object pin state의 symmetry를 heap validator와
  regression test로 잠가 LLVM bridge state transition을 더 엄격하게 만든다.
- Phase 41은 barrier audit failure path와 llvm bridge accounting mismatch를 hardening gate로
  묶어 negative-path coverage를 높인다.

## Future Native Runtime Path

이 Osty implementation을 실제 runtime으로 승격할 때의 순서는 다음과 같다.

1. `GcObject.refs`를 compiler-generated pointer bitmap 또는 stack map으로 대체한다.
2. `address`를 real heap pointer 또는 arena offset으로 대체한다.
3. `roots`를 stack roots, globals, thread locals, closure captures에서 자동 생성한다.
4. `remembered`를 card table 또는 page-level remembered bitmap으로 낮춘다.
5. `stepIncremental`을 scheduler safepoint와 연결한다.
6. `pinned`를 FFI buffer, syscall buffer, host interop object에 연결한다.
7. `GcStats`를 profiler event stream으로 노출한다.

## LLVM Backend Integration Path

현재 LLVM backend는 `internal/backend/llvm.go`가 `internal/llvmgen.Generate`를 호출하고,
`internal/llvmgen`이 AST를 직접 textual LLVM IR로 낮춘다. 아직 별도 root/stack-map layer가
없기 때문에 다음 순서로 연결한다.

1. `internal/llvmgen`의 module render path에 `osty.gc.alloc_v1`, `osty.gc.post_write_v1`,
   `osty.gc.root_bind_v1`, `osty.gc.root_release_v1` 선언을 추가한다.
2. `emitLet`, `emitStructLit`, `emitCall`의 allocation-like lowering이 `allocateLlvmHandle` contract와
   같은 allocation site metadata를 남기게 한다.
3. `emitAssign`과 `llvmStore` 주변에 pointer-bearing store classification을 넣고 `lowerLlvmStore`
   contract와 같은 post-write barrier 호출을 보장한다.
4. `alloca` 기반 mutable locals 중 pointer-bearing slot을 stack root로 등록하고, safepoint/return path에서
   root release를 생성한다.
5. `internal/backend/layout.go`의 LLVM `runtime/` artifact directory에 GC runtime object/library를
   배치하고, `clang` link path가 그 runtime을 함께 연결하게 한다.

이 설계의 핵심은 API와 invariants를 먼저 고정하고, storage backend만 교체하는 것이다.
