# LLVM Backend Gap Plan

> **목적**: PR #1405 (2026-05-05) 이후 새 architecture (`internal/llvmgen` → `osty-self lir-proto-lower` 서브프로세스)에서 LLVM 백엔드의 실제 coverage 빈 칸을 문서화하고, phase별 PR 분할안을 제시한다. 이 문서는 audit 결과이며 상세 lowering 설계는 각 phase PR에서 별도로.
>
> **상태**: 2026-05-07 audit. 현재 실패 테스트 9건 + 빌드 실패 6건 (Security framework link). `LLVM_MIGRATION_PLAN.md`의 Tier A 항목 (Map.update / optional aggregate / generic turbofish / interface dispatch / nested binding pattern) 이 모두 같은 root에 매달려 있음을 발견.
>
> **Phase A 진행**: module-context type lowering 폴스루는 명시적 `unsupported module type` 진단으로 바뀌었고, MIR `uses/imports`는 resolved trace-only metadata로 낮아져 더 이상 LIR Proto 모듈 전체를 decline시키지 않는다.
>
> **Phase B 진행**: B1의 첫 조각으로 `MirIntrinsicMapNew`가 destination `Map<K, V>` 타입에서 key/value ABI kind와 `value_size`를 계산해 `osty_rt_map_new(i64, i64, i64, ptr)`를 호출하도록 복구했다. 로컬 실행 회귀는 `osty-self` 캐시 부재로 아직 end-to-end binary까지는 닫지 못했고, LIR Proto 패리티 fixture가 ABI shape를 고정한다.
>
> **Phase B 추가 진행**: B2/B3의 넓은 조각으로 canonical `Map.update(k, |n| (n ?? 0) + delta)`를 Go MIR lowerer와 Osty self-host MIR lowerer 양쪽에서 `MirIntrinsicMapIncr`로 직접 낮추도록 고정했다. 또 LIR Proto receiver 분석이 `List<Pair>` element와 `Map<String, Pair>` value의 MIR layout을 composite lane으로 보존하게 바꿔, `map.set`/`map.getOr` bytes-v1 fixture가 receiver 단계에서 끊기지 않도록 했다.
>
> **Phase C 진행**: C1의 widened `Option<Struct>` / `Result<Struct, _>` typedef 위에 C2 첫 조각을 얹어, LIR Proto가 `Option<Struct>` None/Some, `Some(struct)` aggregate, `Option<Struct>.unwrap()` / `unwrapOr()`, `map.get` / `list.first` / `list.last` / `list.pop`의 composite Some payload를 i64 boxing 없이 직접 `%Struct` payload slot으로 emit한다. 이 과정에서 self-host MIR/LIR Proto의 Option/Result 태그도 source/Go convention(`Some`/`Ok`=0, `None`/`Err`=1)으로 맞췄다.

## 1. 새 architecture 요약

```
osty build foo.osty
  → Go binary `osty`
  → backend.LLVMBackend.Emit
  → generateLLVMIR
    → tryNativeOwnedMIRPayloadLLVMIRText (native-owned route)
       └→ nativellvmgen.TryMIR
          └→ subprocess: osty-native-llvmgen (Go shim, 248 LOC)
              └→ nativelirproto.Run → subprocess: osty-native-lirproto (Go shim, 184 LOC)
                  └→ subprocess: `osty-self lir-proto-lower`
                      └→ toolchain/llvmgen.osty + toolchain/lir_proto.osty
    → emitLLVMFallback (mir-direct route, declined로 fallthrough)
       └→ 같은 tryNativeOwnedMIRPayloadLLVMIRText 재호출
    → ErrUnsupported / RenderSkeleton (final fallback)
  → if !ok: stage0 fallback (Stage0FallbackEnabled() && IsOstySelfMissing()만)
```

Go MIR emitter 미러는 PR #1405에서 제거됐다 (`internal/llvmgen` 112K LOC). 모든 MIR→LLVM IR 변환은 `osty-self` 바이너리 (= `toolchain/*.osty` 컴파일 결과) 경유.

**핵심 의미**: LLVM 백엔드 lowering 추가 = `toolchain/lir_proto.osty` 또는 `toolchain/mir_lower.osty` 또는 `toolchain/mir_generator.osty` Osty 코드 수정 후 osty-self 재빌드.

## 2. 현재 lir_proto.osty coverage (확인됨)

**파일 크기**: 6054 LOC, 306 함수, 141 unsupported decline 사이트.

### 2.1 ✅ 작동하는 영역

| 영역 | 함수 | 비고 |
|---|---|---|
| Primitive 타입 lowering | `lirLowerPrimitiveTypeName` (1232) | Int*/UInt*/Bool/Char/Byte/Float*/String/Bytes/RawPtr/Unit/Never |
| Reference builtin generic | `lirIsRefBuiltinTypeName` (1298) | List/Map/Set/Channel/Handle/Box/TaskGroup/Select → `ptr` |
| Option/Result aggregate | `lirLowerMirType_module` (1728) | `{i64, i64}` 3필드 변형 (Result는 별도 트랙) |
| Struct layouts | `mir.layouts.structs` lookup | MIR이 layout으로 보내는 struct는 named LLVM type으로 |
| 모든 MIR intrinsic dispatch | `lirLowerMirIntrinsic` (2369) | 141개 `MirIntrinsic*` 종 모두 case 분기 존재 |
| Map intrinsic 본체 | `lirLowerMirMapInsert/Get/Contains/Remove/...` (3444+) | 스칼라/String key+value 조합으로 runtime call |
| List intrinsic 본체 | `lirLowerMirListPush/...` (3166+) | typed lane + bytes-v1 composite fallback |
| String runtime calls | `lirLowerMirStringRuntimeCall` (2863) | 모든 String intrinsic |
| Direct/cross-module/indirect call | `lirLowerMirCall*` (2180/2229/2278) | scalar/string/ptr arg type만 |
| Globals + init ctor | `lirEmitMirGlobals` / `lirBuildInitGlobalsCtor` (1755) | `@__osty_init_globals` + `@llvm.global_ctors` |

### 2.2 ❌ 확인된 decline / 미구현 영역

#### 2.2.1 Module-level

| ID | 코드 위치 | 메시지 | 영향 |
|---|---|---|---|
| GAP-MOD-001 | lir_proto.osty | ✅ Phase A에서 trace-only lowering | resolved `use` edge가 native-owned 경로를 막지 않음 |

#### 2.2.2 Type-level

| ID | 코드 위치 | 메시지 | 시나리오 |
|---|---|---|---|
| GAP-TYP-001 | 1855 | `unsupported param type \`T\`` | Interface 타입, Closure type, Tuple, 미등록 generic struct |
| GAP-TYP-002 | 1974 | `unsupported local type \`T\`` | 위와 동일 + 임시 closure local |
| GAP-TYP-003 | 2081 | `unsupported assign destination type \`T\`` | 위와 같은 사유 |
| GAP-TYP-004 | lir_proto.osty | ✅ Phase A에서 명시 진단 | `lirLowerMirType_module` 폴스루가 `unsupported module type`으로 보고됨 |

#### 2.2.3 Instruction-level fallthrough

| ID | 코드 위치 | 메시지 | 시나리오 |
|---|---|---|---|
| GAP-INSTR-001 | 2070 | `unsupported MIR instruction kind` | 새 MirInstr* 추가 시 catch-all |
| GAP-INSTR-002 | 2186 | `unsupported MIR callee kind` | 미정의 callee variant |
| GAP-INSTR-003 | 2245 | `cross-module call arg type \`T\` is not implemented` | scalar/string 외 인자 |
| GAP-INSTR-004 | 2300 | `indirect call arg type \`T\` is not implemented` | 위와 동일 indirect |
| GAP-INSTR-005 | 2110 | `<context> requires aggregate root` | projection이 비-aggregate 대상 |
| GAP-INSTR-006 | 2134, 2139 | `projection on non-aggregate type` / `unsupported projection` | nested struct binding pattern 등 |

#### 2.2.4 Container receiver type extraction

| ID | 코드 위치 | 메시지 | 시나리오 |
|---|---|---|---|
| GAP-RECV-001 | 3105 | `<label> could not extract element type from receiver \`T\`` | List/Map/Set 타입 이름 파싱 실패 |
| GAP-RECV-002 | 3110 | `<label> element type \`T\` has no LIR runtime lane` | 복합 element (struct in List 등) — `bytes-v1` fallback 필요 |
| GAP-RECV-003 | 3117 | `<label> could not extract value type from receiver \`T\`` | Map<K, V>의 V 추출 실패 |
| GAP-RECV-004 | 3122 | `<label> value type \`T\` has no LIR runtime lane` | 복합 V (struct/enum in Map<K, V>) |
| GAP-RECV-005 | 3204 | `list.push composite element type \`T\` has no MIR layout` | bytes-v1 fallback도 layout 없으면 실패 |

#### 2.2.5 Print intrinsic

| ID | 코드 위치 | 메시지 | 시나리오 |
|---|---|---|---|
| GAP-PRINT-001 | 2533 | `print argument type \`T\` is not implemented` | struct/enum print (ToString lowering 미구현) |

#### 2.2.6 Interface (full-blank)

| ID | 코드 위치 | 메시지 | 시나리오 |
|---|---|---|---|
| GAP-IFACE-001 | (없음 — 타입 lowering에서 silent invalid) | (silent) | `interface Sized` 선언 자체 인식 안 됨 |
| GAP-IFACE-002 | (없음) | (silent) | `let s: Sized = v` boxing site 인식 안 됨 |
| GAP-IFACE-003 | (없음) | (silent) | `s.size()` 메서드 호출 dispatch 안 됨 |
| GAP-IFACE-004 | (없음) | (silent) | `@osty.vtable.<impl>__<iface>` 글로벌 emission 안 됨 |
| GAP-IFACE-005 | (없음) | (silent) | `@osty.shim.<impl>__<iface>__<method>` ABI 변환 shim 안 됨 |

> **참고**: `toolchain/mir_generator.osty` (21939 LOC)에 **primitive 헬퍼만** 존재 — `mirInsertValueIfaceVtableText`, `mirOstyVTableName` 등 IR 텍스트 빌더 다수. Orchestration (boxing site detection, vtable discovery, dispatch lowering)은 이 새 architecture에서 0%.

## 3. 현재 실패 테스트 → 갭 매핑

| 테스트 | 카테고리 | 매핑 갭 | 추정 root cause |
|---|---|---|---|
| `TestLLVMBackendBinaryRunsInterfaceBoxingDispatch` | (c) | GAP-IFACE-001..005 | Interface 자체 미지원 |
| `TestLLVMBackendBinaryRunsMapLiteralInMain` | (a) | GAP-RECV-001 또는 -003 | `Map<String, Int>` 타입 이름 파싱 또는 receiver 분석 실패 |
| `TestLLVMBackendBinaryRunsMapUpdateCanonicalPattern` | (a) | GAP-RECV-* + closure body lowering | `Map.update`의 `\|n: Int?\| (n ?? 0) + 1` closure가 분리된 fn으로 lower될 때 Optional aggregate 인자 + indirect call |
| `TestLLVMBackendBinaryKeepsMapKeysSortedAliveUnderGC` | (a) | GAP-RECV-* / GAP-INSTR-006 | (확인 필요) |
| `TestBundledRuntimeMap*` × 6 | 별개 | (link error) | clang link 시 Security framework 미연결 — `framework Security` 누락 |
| `TestRunCoversNestedStructBindingPattern` (cmd/osty-native-llvmgen) | (d) | GAP-INSTR-006 | nested `extractvalue` + `name @ pattern` |
| 추정: 다양한 Optional struct payload 테스트 | (b) | GAP-TYP-004 + GAP-INSTR-005,006 | `let x: Foo? = ...; x?.field` |

## 4. 갭 카테고리별 phase plan

각 phase = 한 PR 단위. 의존성 순서대로.

### Phase A — 기반 인프라 강화 (선행)

**A1 — Type lowering invalid path 진단 강화** (GAP-TYP-004)
- `lirLowerMirType_module` 폴스루를 silent invalid → 명시적 `lirLowerError`로 격상.
- 현재 발생하는 "unsupported param type"의 root cause를 trace로 노출 가능하게.
- 예상 변경: lir_proto.osty 한 함수, ~30 LOC.
- 회귀: 새 진단 스냅샷 1건.

**A2 — MIR uses/imports lowering** (GAP-MOD-001)
- 다중 파일 패키지가 native-owned 경로 진입 가능하게.
- multi-file integration 가시화 — 후속 phase 검증을 위해.
- 변경: lir_proto.osty `lirLowerMirModule`에 uses 처리 분기, ~100 LOC.
- 회귀: existing multi-file tests에서 declined가 emit으로 바뀌는지.

### Phase B — Map/List composite element & receiver 강건화 (Tier A `(a)`)

**B1 — `Map<String, Int>` 기본 insert/len/containsKey 회귀 회복** (GAP-RECV-001..004)
- 가설: receiver type string parsing이 정확히 어디서 실패하는지 진단 후 fix.
- 진행: `map_new`은 receiver arg가 없는 MIR shape이므로 receiver parser가 아니라 destination `Map<String, Int>` 타입에서 ABI tuple `(key=string, value=i64, size=8, trace=null)`을 합성해야 했다. LIR Proto가 4-arg runtime constructor를 내도록 고정.
- 회귀 대상: `TestLLVMBackendBinaryRunsMapLiteralInMain` (4초 test).
- 의존성: A1 권장 (진단 가시화).

**B2 — `Map.update` closure body lowering** (a)
- `counts.update(k, |n: Int?| (n ?? 0) + 1)` 패턴.
- 닫혀야 할 sub-기능: closure as Map.update 인자, Optional 인자, `??` coalesce in closure body.
- 진행: canonical counter shape `|n: Int?| (n ?? 0) + delta`는 일반 closure lowering을 우회해 `map_incr(map, key, delta)`로 직접 lowered. Go MIR와 `toolchain/mir_lower.osty` self-host mirror 모두 적용.
- 회귀: `TestLLVMBackendBinaryRunsMapUpdateCanonicalPattern`.
- 의존성: B1 + Phase D의 일부 (Optional 인자).

**B3 — composite element type 폴스루 보강** (GAP-RECV-002, 005)
- `List<Struct>`, `Map<K, Struct>` element ABI bytes-v1 path 검증.
- 진행: `lirLowerMirContainerReceiver`가 List element와 Map value에 대해 typed runtime lane이 없을 때 `lirLowerMirType` layout (`%Pair` 등)을 보존하도록 완화. Map key / Set element는 key ABI라 composite unsupported 상태를 유지.
- 회귀 lock: `map_set_struct_bytes_v1`, 기존 `map_get_or_struct_bytes_v1`, `list_remove_at_struct_bytes_v1` fixture가 같은 receiver path를 탄다.
- 회귀: `TestLLVMBackendBinaryKeepsMapKeysSortedAliveUnderGC` 와 비슷한 패턴.

### Phase C — Optional aggregate & projection (Tier A `(b)` + `(d)`)

**C1 — Optional struct payload type lowering** (GAP-TYP-004)
- `Foo?` (Foo가 struct)의 MIR layout + LIR aggregate → `{i64 tag, %Foo payload}`.
- 진행: C1 typedef는 merged. C2 첫 조각에서 widened payload construction/unwrap도 같은 layout을 실제 값 경로에 연결.
- 회귀: 새 single-shape smoke (1 fixture).

**C2 — `?.field` chain lowering** (GAP-INSTR-005, 006)
- `x?.field` projection이 None branch → null phi, Some branch → extractvalue.
- 진행: LIR Proto widened Option helpers가 `%Option.Foo = { i64, %Foo }`의 payload type을 읽어 None/Some/unwrap/unwrapOr 및 composite-returning map/list option intrinsics를 direct `%Foo` payload로 낮춘다. 또 self-host `mir_lower`의 coalesce/optional-field/`?` rebuild 태그 상수를 Go MIR lowerer와 맞춰 `Some`/`Ok`=0, `None`/`Err`=1로 통일했다. `source_some_struct`, `source_optional_field_chain`, `map_get_struct_bytes_v1`, `nullary_none_option_struct`, `agg_enum_variant_some_struct`, `option_unwrap_struct`, `option_unwrap_or_struct`, `list_first_struct_bytes_v1`, `list_last_struct_bytes_v1`, `list_pop_struct_bytes_v1` fixture needles를 widened-direct shape와 source tag convention으로 갱신/추가.
- 회귀: `TestNativeOwnedModuleEntryOptionalFieldBatch` 류 (이전 internal/llvmgen 테스트 재구축).
- 의존성: C1.

**C3 — Nested struct binding pattern** (GAP-INSTR-006) — Tier A (d)
- `match user { User { addr: Address { city } } -> ... }` recursive `extractvalue` + `name @ pattern` alias.
- 진행: `source_nested_struct_binding` source parity fixture가 `let whole @ User { addr: Address { city, zip }, score } = u`를 통해 whole-scrutinee alias + nested field extraction을 고정한다.
- 회귀: `TestRunCoversNestedStructBindingPattern` (cmd/osty-native-llvmgen).
- 의존성: A1 진단 가시화 권장.

### Phase D — Generic method monomorphization symbol selection (Tier A `(c1)`)

**D1 — Generic method turbofish symbol resolution**
- MIR receiver의 `obj.method::<T>(...)` 호출에서 monomorphized mangled name 선택.
- 현재: monomorphization은 IR 단계에서 작동 (`internal/ir.Monomorphize`) — MIR 단계에서 그 결과 심볼을 callee로 raw text 보내는 plumbing이 빠져 있을 가능성.
- 회귀: 1 fixture (generic struct + generic method 호출).
- 의존성: 없음 (independent).

### Phase E — Interface dispatch (Tier A `(c2)`–`(c4)`)

**E1 — Interface type lowering + boxing layout 정의** (GAP-IFACE-001, 002)
- `lirLowerMirType_module`에 interface case 추가 → `%osty.iface = type { ptr, ptr }` 자동 emit.
- HIR/MIR 단계에서 boxing site 식별 (let site `let s: Iface = concrete`).
- 회귀: 1 IR snapshot fixture.

**E2 — Vtable + per-method shim emission** (GAP-IFACE-004, 005)
- (impl, iface) discovery (HIR resolved Symbol + structural method match).
- 각 쌍마다 `@osty.vtable.<impl>__<iface>` 글로벌 + `@osty.shim.<impl>__<iface>__<method>` ABI 변환.
- 회귀: 1 IR snapshot fixture (vtable layout).
- 의존성: E1.

**E3 — Boxing at let / return / call-arg / assign sites** (GAP-IFACE-002 확장)
- 4개 site 모두에서 concrete → `%osty.iface` 변환 (`insertvalue ×2`).
- 회귀: 4 IR snapshot fixtures (각 site 1개씩).
- 의존성: E1, E2.

**E4 — Interface method-call indirect dispatch** (GAP-IFACE-003)
- `iface_value.method()` → extract data + vtable, GEP slot, indirect call (shim 경유).
- 회귀: end-to-end binary test `TestLLVMBackendBinaryRunsInterfaceBoxingDispatch` 통과.
- 의존성: E1, E2, E3.

### Phase F — Cleanup / multi-file gate

**F1 — Print struct/enum lowering** (GAP-PRINT-001)
- `println(some_struct)` → ToString protocol 호출 lower.
- 회귀: 1 fixture.

**F2 — Multi-file package emit gate 측정**
- `TestLLVMBackendBinary*MultiFile*`의 declined 비율 확인.
- 의존성: A2 (uses lowering), B/C/D/E 모두.

**F3 — Security framework link fix** (별개 카테고리)
- `clangPlatformRuntimeLinkArgs` 수정 — 현재 darwin에 `Security`, `CoreFoundation` 추가 있지만 keychain object link 시 누락.
- 회귀: `TestBundledRuntimeMap*` × 6.

## 5. 의존성 그래프

```
A1 (진단) ─┬─→ B1 ─→ B2 ─→ B3
           ├─→ C1 ─→ C2 ─→ C3
           └─→ D1
A2 (uses) ─→ F2 (multi-file)
E1 ─→ E2 ─→ E3 ─→ E4 (closure: TestLLVMBackendBinaryRunsInterfaceBoxingDispatch)
F3 (별개): TestBundledRuntimeMap* link
```

병렬화 가능: A1 후 B*/C*/D*/E* 동시 진행 가능. F3는 처음부터 독립.

## 6. PR 사이즈 추정

| Phase | PR | 추정 LOC | 예상 review 부담 |
|---|---|---|---|
| A | 2 PR | 100 + 150 | 작음 |
| B | 3 PR | 200 + 300 + 200 | 중간 |
| C | 3 PR | 200 + 250 + 300 | 중간 |
| D | 1 PR | 250 | 중간 |
| E | 4 PR | 200 + 300 + 350 + 400 | 큼 (특히 E4) |
| F | 3 PR | 150 + audit + 50 | 작음 |
| **총** | **16 PR** | **~3100 LOC** | — |

추정 작업 시간: 한 사람 기준 2-3주. 병렬화 시 1.5주.

## 7. 결정 사항

1. **Osty 우선 원칙 (CLAUDE.md)** — 모든 lowering 신규 코드는 `toolchain/lir_proto.osty` 또는 `toolchain/mir_generator.osty`에. Go 측 (`internal/backend/*.go`)은 capability matrix / dispatcher 변경만.

2. **Stage0 fallback 확장 안 함** — 기본 디자인이 "production builds never silently route through bootstrap emitter". interface/Map.update 같은 일반 패턴을 stage0에 넣지 않는다.

3. **`internal/llvmgen` 재도입 금지** — PR #1405 결정 유지. 모든 lowering은 native subprocess 경로 통해.

4. **테스트 분리 원칙** — 각 phase PR은 IR snapshot 1건 + (가능 시) end-to-end binary 1건. snapshot은 lowering 모양 lock, binary는 의미 lock.

5. **`-short`로 가려진 reds 무관** — 모든 phase는 `-short` 빼고 측정.

## 8. 추후 audit 추가 항목

- (TODO) cmd/osty-native-llvmgen 측 nested binding 회귀 (Tier A (d)) 정확한 코드 위치
- (TODO) `MIRDirect` 라우트가 항상 `tryNativeOwnedMIRPayloadLLVMIRText`로 fallback하는 현재 로직 재검토 — 이중 호출 비효율
- (TODO) 진짜 Tier B (pkgmgr 셀프-컴파일) 갭은 별도 audit. 현재 본 문서는 Tier A 범위.

---

이 문서를 phase별 PR 시작 시 reference로 사용. 새 갭 발견 시 `## 2.2` 또는 `## 4`에 row 추가.
