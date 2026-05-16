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
>
> **Phase F 진행**: F1의 `println(struct)` 경로는 IR lowering이 print-family 인자를 `.toString()` method call로 자동 감싼 뒤 MIR payload가 `String` print operand만 LIR Proto로 넘기는 형태로 닫혔다. `source_println_struct_to_string` parity fixture와 backend MIR-shape regression이 GAP-PRINT-001 재발을 막는다.

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
| GAP-TYP-001 | lir_proto.osty:2719,2740 | ✅ 2026-05-17 trace 추가 (`lirTypeLowerTraceMessage`) | `unsupported param type \`T\`` + primitive/ref/option-result/layout-count root-cause hint |
| GAP-TYP-002 | lir_proto.osty:2993 | ✅ 2026-05-17 trace 추가 | `unsupported local type \`T\`` + 동일 trace |
| GAP-TYP-003 | lir_proto.osty:3105 | ✅ 2026-05-17 trace 추가 | `unsupported assign destination type \`T\`` + 동일 trace |
| GAP-TYP-004 | lir_proto.osty | ✅ Phase A에서 명시 진단 | `lirLowerMirType_module` 폴스루가 `unsupported module type`으로 보고됨 |

#### 2.2.3 Instruction-level fallthrough

| ID | 코드 위치 | 메시지 | 시나리오 |
|---|---|---|---|
| GAP-INSTR-001 | lir_proto.osty:3151 | `unsupported MIR instruction kind \`mirInstrKindName(instr.kind)\`` — kind 이름 이미 surface 됨 (trace 불필요) | 새 MirInstr* 추가 시 catch-all |
| GAP-INSTR-002 | lir_proto.osty:3297 | `unsupported MIR callee kind \`mirCalleeKindName(...)\`` — 동일 (kind 이름 이미 surface) | 미정의 callee variant |
| GAP-INSTR-003 | lir_proto.osty:3340 | ✅ 2026-05-17 trace 추가 (`lirTypeLowerTraceMessage`) | `cross-module call arg type \`T\` is not implemented` + root-cause hint |
| GAP-INSTR-004 | lir_proto.osty:3526 | ✅ 2026-05-17 trace 추가 | `indirect call arg type \`T\` is not implemented` + 동일 trace |
| GAP-INSTR-005 | lir_proto.osty:3203 | ✅ 2026-05-17 rootType.llvm 포함 | `<context> requires aggregate root, got \`<llvm-type>\`` |
| GAP-INSTR-006 | lir_proto.osty:3238,3243 | ✅ 2026-05-17 current type + projection kind 포함 | `projection on non-aggregate type \`<llvm>\` (projection kind: ...)` / `unsupported projection kind \`<kind>\` on type \`<llvm>\`` |

#### 2.2.4 Container receiver type extraction

| ID | 코드 위치 | 메시지 | 시나리오 |
|---|---|---|---|
| GAP-RECV-001 | lir_proto.osty:4775,4869,4986,5040,5818,6782,6843 | ✅ 2026-05-17 trace 추가 (`lirReceiverParseTraceMessage`) | List/Map/Set/Channel 타입 이름 파싱 실패 + container prefix 매치 trace |
| GAP-RECV-002 | lir_proto.osty:4781,4786 | ✅ 2026-05-17 trace 추가 (`lirTypeLowerTraceMessage`) | `<label> element type \`T\` has no LIR runtime lane (or MIR layout)` + root-cause hint |
| GAP-RECV-003 | lir_proto.osty:4802 | ✅ 2026-05-17 trace 추가 (`lirReceiverParseTraceMessage`) | Map<K, V>의 V 추출 실패 + container prefix 매치 trace |
| GAP-RECV-004 | lir_proto.osty:4809 | ✅ 2026-05-17 trace 추가 | `<label> value type \`T\` has no LIR runtime lane or MIR layout` + 동일 trace |
| GAP-RECV-005 | 3204 | `list.push composite element type \`T\` has no MIR layout` | bytes-v1 fallback도 layout 없으면 실패 |

#### 2.2.5 Print intrinsic

| ID | 코드 위치 | 메시지 | 시나리오 |
|---|---|---|---|
| GAP-PRINT-001 | 2533 | `print argument type \`T\` is not implemented` | ✅ F1에서 `println(struct)` → `.toString()` MIR payload shape로 폐쇄 |

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

### Phase 0 — Bootstrap chain unblock (모든 phase 선행 필수)

**상태 (2026-05-11)**: 모든 Phase C–F closeout 작업은 `osty-self` 바이너리가 있어야 verification 가능. 현재 상태:
- 디폴트 registry (`github.com/choiceoh/osty/releases/.../osty-self-snapshots`) — 자산 0건 (404 확인).
- `OSTY_STAGE0_FALLBACK=1` — P21–P23 머지됨에도 `install-self`가 **1340 function declines** 로 차단 (`OSTY_STAGE0_LIST_ALL_DECLINES=1` 확인).
- 캐시/사전빌드 osty-self 없음.

**0-A — Stage0 P24+ unlock 진행** (master plan v2 — `docs/osty_self_b2_1_audit.md §4.3`)
- 다음 차단 클러스터를 P-phase로 분할. `tyToRepr` / `frontTypeReprToString` / `useDeclTailAfter` / `selfCheck*` / `checkLookup*` / `checkSubst*` 등이 decline top 그룹.
- 각 P-phase = independent PR, emit.go에 ~300–500 LOC + matcher + 회귀 테스트.
- 추정: 10–15 PR, 2–4 주. 부트스트랩 가능까지의 누적.
- 회귀: `TestStage0ToolchainAudit` cover % 모니터 + `OSTY_STAGE0_FALLBACK=1 install-self` E2E.

**0-B — Registry publish 활성화** (병렬 옵션)
- `.github/workflows/build-osty-self.yml` 가 stage0로 osty-self 빌드 → release upload. `docs/operations/first-publish-playbook.md` 참조.
- 의존성: 0-A 가 충분히 진행되어 stage0가 install-self를 완주 가능. 즉 **0-A의 부분 결과**.
- 회귀: fresh-clone CI matrix에서 registry 경로로 osty-self 페치 + 검증.

**Phase C–F closeout 차단**: 0-A가 install-self 완주 가능하게 만들거나, 0-B 가 publish 후 OSTY_SELF_REGISTRY_URL이 정상 작동할 때까지 Phase E3/E4/C4/G1/G2/F4 검증 불가. lir_proto.osty의 E3/E4 코드는 이미 머지 ([lir_proto.osty:2645](toolchain/lir_proto.osty:2645)) — verification만 차단.

### Phase A — 기반 인프라 강화 (선행)

**A1 — Type lowering invalid path 진단 강화** (GAP-TYP-001/002/003/004) ✅ **완료 2026-05-17**
- `lirLowerMirType_module` 폴스루를 silent invalid → 명시적 `lirLowerError`로 격상. (이전 phase)
- `lirTypeLowerTraceMessage(mir, name) -> String` helper 추가. 4 fallthrough 사이트 (param x2 / local / assign-dest) 가 같은 trace 메시지 emit. (2026-05-17)
- 결과 메시지 예: `unsupported param type \`Foo\` (primitive miss; ref-builtin miss; option/result miss; 0 interface layout(s); 5 struct layout(s); 0 tuple layout(s); 3 enum layout(s))` — root cause 즉시 가시화.
- 변경: lir_proto.osty +35 LOC (helper 추가) / -1 LOC (trace inline 제거) — 합계 ~+30 LOC.

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
- 진행: Go MIR lowering 회귀도 같은 shape를 `whole` alias store + `.addr.city`/`.addr.zip`/`.score` projected reads로 잠근다. LIR Proto catalog sentinel은 current-generator fixture set에 이 source parity fixture가 계속 포함되는지도 확인한다.
- 회귀: `TestRunCoversNestedStructBindingPattern` (cmd/osty-native-llvmgen).
- 의존성: A1 진단 가시화 권장.

**C4 — `Result<Struct, _>` composite payload widening** (GAP-TYP-004 잔여)
- C1이 Option aggregate를 `{i64 tag, %Foo payload}`로 넓힌 동일 인프라를 Result로 확장.
- Ok-side composite (`Result<Struct, _>`)와 Err-side composite (`Result<_, Struct>`) 양쪽을 한 PR. LIR Proto가 `%Result.Foo.Bar = { i64, %Foo, %Bar }` 직접 payload로 emit.
- `?` 전파, `Ok(struct)` / `Err(struct)` aggregate, `unwrap()` / `unwrap_err()` composite return path 포함.
- Source/Go tag convention `Ok=0` / `Err=1`은 C2에서 이미 정렬됨 — C4는 payload type widening만.
- 회귀: 1 IR fixture per side + `TestNativeOwnedModuleEntryResultStructPayloadBatch` (신규).
- 의존성: C1.

### Phase D — Generic method monomorphization symbol selection (Tier A `(c1)`)

**D1 — Generic method turbofish symbol resolution**
- MIR receiver의 `obj.method::<T>(...)` 호출에서 monomorphized mangled name 선택.
- 진행: IR `internal/ir.Monomorphize`가 generic owner의 method-local specialization을
  `_ZTS...__method_Z...` call target으로 남기고, MIR lowering이 signature table을 통해
  owner-qualified callee symbol을 유지하는 경로를 잠금.
- 회귀: `TestLowerGenericOwnerMethodTurbofishUsesMonomorphizedSymbol`,
  `TestLLVMBackendBinaryRunsGenericOwnerMethodTurbofish` (osty-self artifact 필요).
- 의존성: 없음 (independent).

### Phase E — Interface dispatch (Tier A `(c2)`–`(c4)`)

**E1 — Interface type lowering + boxing layout 정의** (GAP-IFACE-001, 002)
- `lirLowerMirType_module`에 interface case 추가 → `%osty.iface = type { ptr, ptr }` 자동 emit.
- HIR/MIR 단계에서 boxing site 식별 (let site `let s: Iface = concrete`).
- 진행: PR #1522에서 self-host MIR layout table과 LIR Proto가 interface 타입을 `%osty.iface`
  fat-pointer layout으로 낮추는 E1 토대를 열었다. 후속 회귀락은 Go MIR lowering/JSON bridge가
  method slot과 `(impl, iface)` vtable symbol을 보존하는지 고정한다. Boxing site materialization은 E3에 남김.
- 회귀: 1 IR snapshot fixture.

**E2 — Vtable + per-method shim emission** (GAP-IFACE-004, 005)
- (impl, iface) discovery (HIR resolved Symbol + structural method match).
- 각 쌍마다 `@osty.vtable.<impl>__<iface>` 글로벌 + `@osty.shim.<impl>__<iface>__<method>` ABI 변환.
- 진행: PR #1534에서 LIR Proto가 MIR interface impl layout을 읽어 vtable constant와
  per-method shim 함수를 self-host 경로에서 emit하도록 잠갔다.
- 회귀: vtable/shim IR fixture.
- 의존성: E1.

**E3 — Boxing at let / return / call-arg / assign sites** (GAP-IFACE-002 확장)
- 4개 site 모두에서 concrete → `%osty.iface` 변환 (`insertvalue ×2`).
- 진행: concrete 값이 interface target으로 저장/전달될 때 `osty.gc.alloc_v1` box +
  `%osty.iface { data, vtable }` 값을 만드는 LIR Proto coercion path를 추가.
- 회귀: assign/return + call-arg IR fixtures.
- 의존성: E1, E2.

**E4 — Interface method-call indirect dispatch** (GAP-IFACE-003)
- `iface_value.method()` → extract data + vtable, GEP slot, indirect call (shim 경유).
- 회귀: end-to-end binary test `TestLLVMBackendBinaryRunsInterfaceBoxingDispatch` 통과.
- 의존성: E1, E2, E3.

### Phase F — Cleanup / multi-file gate

**F1 — Print struct/enum lowering** (GAP-PRINT-001) — 완료
- `println(some_struct)` → ToString protocol 호출 lower.
- 회귀: `TestLLVMBackendEmitPrintlnStructAutoToString`가 native MIR payload의 `toString` call + `String` print operand를 직접 확인하고, `source_println_struct_to_string` LIR Proto parity fixture가 runtime print ABI shape를 잠근다.

**F2 — Multi-file package emit gate 측정**
- `TestLLVMBackendBinary*MultiFile*`의 declined 비율 확인.
- 의존성: A2 (uses lowering), B/C/D/E 모두.

**F3 — Security framework link fix** (별개 카테고리)
- `clangPlatformRuntimeLinkArgs` 수정 — 현재 darwin에 `Security`, `CoreFoundation` 추가 있지만 keychain object link 시 누락.
- 회귀: `TestBundledRuntimeMap*` × 6.

**F4 — LLVM015 fall-through audit & regression lock** (cleanup)
- 닫힌 historical 패턴 (`buf.clear()`, `List/Map/Set.isEmpty`, `String.bytes` / `String.chars`, `List.pop` discard, `!a.m()` precedence) 별 IR snapshot 1건씩 회귀 락만 추가. 새 로직 없음.
- LIR Proto에 새로 노출되는 fall-through는 GAP-INSTR-002/003/004로 추적 (LLVM015는 legacy Go-side bootstrap 진단 코드, 새 native 경로는 LIR Proto 진단 사용).
- 의존성: 모든 phase 후. 위험 낮음, 병렬 가능.

### Phase G — Closure escape (heap-alloc fallback)

LIR Proto 현 stage limit ([lir_proto.osty:6339](toolchain/lir_proto.osty)): closure env는 stack alloca, escape 시 깨짐. 현재 실패 테스트는 없지만 higher-order Map/List helper / Handle 반환 패턴의 future blocker. 별도 phase로 격리.

**G1 — Escape detection in MIR**
- 새 MIR pass가 closure aggregate 사이트별로 escape 분류: return-from-fn / capture-into-escaping-env / store-into-heap-rooted-struct.
- `MirAggClosure`에 `escapes: Bool` 플래그 추가. 비탈출은 stack alloca 유지 (perf 영향 0).
- 회귀: MIR snapshot fixture (escape 분류 결과 lock).
- 의존성: 없음 (independent).

**G2 — Heap-alloc lowering in LIR Proto**
- `lirLowerMirClosureAggregate`가 `escapes == true` 시 `osty.gc.alloc_v1(size)` 호출 후 GC-managed slot에 채워넣음. 비탈출은 기존 alloca 경로.
- 회귀: 1 IR fixture (heap-allocated env shape) + 1 E2E binary (returned closure를 caller가 invoke).
- 의존성: G1.

**참고**: annotation-based opt-in (`#[boxed_closure]` 등) 도입 안 함 — Osty 우선 원칙상 메타 신설 전에 escape analysis로 충분한지 먼저 확인.

## 5. 의존성 그래프

```
Phase 0-A (stage0 P24+) ──┐
                          ├──→ (osty-self 빌드 가능) ──→ 모든 Phase C–F closeout verification
Phase 0-B (registry pub) ─┘

A1 (진단) ─┬─→ B1 ─→ B2 ─→ B3
           ├─→ C1 ─→ C2 ─→ C3
           │         └──→ C4 (Result composite)
           └─→ D1
A2 (uses) ─→ F2 (multi-file)
E1 ─→ E2 ─→ E3 ─→ E4 (closes: TestLLVMBackendBinaryRunsInterfaceBoxingDispatch)
G1 ─→ G2 (closure escape; 독립)
F3 (별개): TestBundledRuntimeMap* link
F4 (cleanup): 모든 phase 후
```

**Phase C–F closeout 우산 (작업 순서)**:
1. **Phase 0 prerequisite** — stage0 P24+ unlock 또는 registry 활성화로 osty-self 확보. 미해결 시 이하 단계 verification 불가.
2. E3 → E4 (named E2E 테스트 1:1 매핑, E1/E2 컨텍스트 fresh) — lir_proto 코드는 이미 머지, 부트스트랩 후 검증만.
3. C4 (Result composite — Optional aggregate batch와 함께 검증)
4. G1 → G2 (closure escape, 디자인 불확실성 높음 → 위 둘 후)
5. F4 (audit-only)

D는 D1 완료로 종료. 새 갭 발견 시 그때 phase 추가.

병렬화 가능: Phase 0이 풀린 후 A1 → B*/C*/D*/E*/G* 동시 진행 가능. F3/F4는 독립. **0-A 와 0-B는 서로 병렬**.

## 6. PR 사이즈 추정

| Phase | PR | 추정 LOC | 예상 review 부담 |
|---|---|---|---|
| **0 (선행)** | **10–15 PR (stage0 P24+) + 1 PR (registry)** | **~3000–5000 (Go) + CI yaml** | **큼 — 가장 큰 영역** |
| A | 2 PR | 100 + 150 | 작음 |
| B | 3 PR | 200 + 300 + 200 | 중간 |
| C | 4 PR | 200 + 250 + 300 + 250 | 중간 |
| D | 1 PR | 250 (완료) | 중간 |
| E | 4 PR | 200 + 300 + 350 + 400 | 큼 (특히 E4) |
| F | 4 PR | 150 + audit + 50 + 100 | 작음 |
| G | 2 PR | 150 + 250 | 중간 |
| **총** | **30–35 PR** | **~6650–8650 LOC** | — |

추정 작업 시간 (Phase 0 포함): 한 사람 기준 4–6주. 병렬화 시 3–4주.
**Phase 0 단독**: 2–4주 (master plan v2 추정).

## 7. 결정 사항

1. **Osty 우선 원칙 (CLAUDE.md)** — 모든 lowering 신규 코드는 `toolchain/lir_proto.osty` 또는 `toolchain/mir_generator.osty`에. Go 측 (`internal/backend/*.go`)은 capability matrix / dispatcher 변경만.

2. **Stage0 fallback 확장 안 함** — 기본 디자인이 "production builds never silently route through bootstrap emitter". interface/Map.update 같은 일반 패턴을 stage0에 넣지 않는다.

3. **`internal/llvmgen` 재도입 금지** — PR #1405 결정 유지. 모든 lowering은 native subprocess 경로 통해.

4. **테스트 분리 원칙** — 각 phase PR은 IR snapshot 1건 + (가능 시) end-to-end binary 1건. snapshot은 lowering 모양 lock, binary는 의미 lock.

5. **`-short`로 가려진 reds 무관** — 모든 phase는 `-short` 빼고 측정.

## 8. 추후 audit 추가 항목

- cmd/osty-native-llvmgen 측 nested binding binary 회귀는 `osty-self` artifact가 있는 환경에서만 활성화한다. 로컬 기본 잠금은 Go MIR shape + LIR Proto source parity catalog로 유지.
- (TODO) `MIRDirect` 라우트가 항상 `tryNativeOwnedMIRPayloadLLVMIRText`로 fallback하는 현재 로직 재검토 — 이중 호출 비효율
- (TODO) 진짜 Tier B (pkgmgr 셀프-컴파일) 갭은 별도 audit. 현재 본 문서는 Tier A 범위.

---

이 문서를 phase별 PR 시작 시 reference로 사용. 새 갭 발견 시 `## 2.2` 또는 `## 4`에 row 추가.
