# ONB 설계 — Osty Native Backend

> **Status: Phase 0 scaffold started (2026-04-30); dev-loop integration
> (Slice A1) landed (2026-05-01).** 33개 설계 결정 통합.
> `internal/onb` + `--backend onb` 등록까지 착수했고, `fn main() {}`의 MIR를
> ONB Program으로 낮춘 뒤 첫 aarch64 LIR (`mov w0, #0`; `ret`)까지 생성하는
> Phase 1.0 slice가 구현됐다. ONB assembly text artifact (`main.s`) 렌더링과
> 최소 Mach-O relocatable object (`main.o`) emission이 가능하다. Binary emit은
> ONB object를 host linker에 넘겨 실행 파일을 만들며, darwin/aarch64
> `fn main() {}` smoke는 exit 0까지 확인됐다. 다음 slice로
> `println("literal")` MIR intrinsic을 ONB LIR + assembly text까지 낮추며
> `_puts` 호출과 `__cstring` 섹션을 렌더링하고, Mach-O `PAGE21` / `PAGEOFF12`
> / `BR26` relocation을 포함한 object emission까지 확장했다. 이어서
> `println(123)` 같은 정수 리터럴 출력도 `_printf("%lld\n", value)` 경로로
> lowering/object/binary smoke까지 통과한다.
>
> **Slice A1 (dev-loop integration)** — 실제 개발자가 `osty run --backend onb`
> 를 켜둔 채 작업할 수 있도록 두 축이 추가됐다. (1) `internal/onb`가 미지원
> MIR shape를 만나면 `ErrUnsupportedShape` sentinel을 반환하고, `ONBBackend`
> 가 `EmitObject` / `EmitBinary` 모드에서 자동으로 LLVM 백엔드에 위임한다
> (`--emit asm`은 사용자가 ONB asm을 명시적으로 원했다고 보고 hard-fail
> 유지). 결과 binary는 `.osty/out/llvm/`에 떨어지며 `osty run`이 그대로
> 실행한다. (2) `OSTY_ONB_TIMING=1`이면 stderr에 한 줄 요약을 찍는다 —
> `onb: emit 312ms [native: aarch64-apple-darwin]` 또는
> `onb: emit 1.82s [fallback to llvm: instruction *mir.CallInstr is outside phase 1]`.
> Cross-validation 흐름은 `OSTY_ONB_STRICT=1`로 fallback을 끄면 raw 거부
> 에러를 받을 수 있다.
>
> **Slice A2 Week 1 (Int 산술 + locals, 2026-05-02)** — ONB native path의 첫
> 의미 있는 커버리지 확장. 패턴: `let mut n = 1; n = n + 5; println(n)`,
> `let x = 10; let y = 32; println(x + y)`, `let a = 10; let b = 3; println(a - b * 2)`
> 모두 native path로 통과 (각 50–150ms wall-clock, LLVM 대비 5–10×).
> 추가된 LIR opcode: `Load64Stack`, `MovRegReg`, `AddReg` / `SubReg` / `MulReg`.
> 모델: **stack-everything** — RA 없이 모든 read 대상 local에 고정 stack slot
> 할당, 모든 operand가 x9/x10 scratch register를 거침. 프레임 layout:
> `[sp+0..16] vararg slot (printf int용) → [sp+V..V+8N] locals → [sp+frameSize-16] FP/LR`.
> dead-store는 자동 elide (slot 할당이 read 기준). Linear scan RA 도입은
> 후속 의제로 유예.
>
> **Slice A2 Week 17 (`for x in list` native lowering, 2026-05-02)** —
> ONB가 `for x in list` 루프를 native lowering. 이전에는 List 인덱스
> read가 fallback 사유였는데, `LenRV` + `IndexProj` 두 vocab item을
> 추가해서 모든 element type (Int/Bool/Float64/String) 의 list가 통과.
>
> 작동:
>
> ```osty
> let mut v: List<Int> = []
> v.push(10); v.push(20); v.push(30)
> let mut sum = 0
> for x in v {
>     sum = sum + x
> }
> println(sum)                 // 60
>
> for i in 0..10 { sum = sum + i }    // Range는 카운터 루프로
>                                      // lowering — Week 1부터 통과
> ```
>
> Front end MIR 셰이프 (counter + bounded loop):
>
> ```
> _len = len _iter           // LenRV → osty_rt_list_len
> _idx = const 0
> bb1: cond = _idx < _len
> branch cond -> [bb2 (body), bb4 (exit)]
> bb2: _elem = use _iter[_idx]   // IndexProj → osty_rt_list_get_*
>      sum = sum + _elem
> bb3: _idx = _idx + 1
>      goto bb1
> ```
>
> 추가:
> - 새 runtime symbol 상수: `runtimeSymListGetI64` / `_I1` / `_F64` /
>   `_String`
> - `lowerLenAssign` (LenRV → osty_rt_list_len + capture x0)
> - `loadIndexedPlaceIntoIntReg` / `loadIndexedPlaceIntoFloatReg` —
>   IndexProj 감지 시 list_get_* 디스패치 (element type 기준)
> - `indexCallPrefix` helper — list pointer → x0, index → x1
> - `listGetSymbol(elem)` — Int/Bool/String/Float dispatch
> - `materialiseOperand` / `materialiseFloatOperand`이 IndexProj가
>   있으면 indexed-load 경로로 우회
> - `collectRValueLocals`이 LenRV.Place를 read로 표시
> - `collectOperandLocals`이 IndexProj.Index 안의 locals도 수집
>   (인덱스 local이 slot을 못 받는 회귀 방지)
>
> 검증:
> - E2E binary smoke 3개 (`TestONBBackendBinaryRunsForInLoopOnDarwinARM64`):
>   range_sum (0..10 sum=45), list_int_sum (10+20+30=60),
>   list_string_count (4 strings → 4)
>
> 한계 (이번 슬라이스에서 명시적으로 보류):
> - `list[i] = v` write — IndexProj as Dest 미구현
> - `list.pop()` — runtime symbol 다양 (Option<T> 반환), 미구현
> - struct/enum element type을 가진 List — push/get_bytes 경로 필요
> - `for (k, v) in map` — Map iterator 별도
> - `for x in iter()` — 사용자 정의 Iterable 프로토콜 미구현
>
> 다음 슬라이스 후보: struct field mutation (`p.x = 5`) → struct >16B
> sret-style passing.
>
> **Slice A2 Week 16 (List<T> 일반화 — Bool/Float64/String, 2026-05-02)** —
> ONB가 List<Int> 외의 element type에 대해 push/len을 native lowering.
> 런타임은 이미 `osty_rt_list_push_i64` 외에 `_i1`/`_f64`/`_string`을
> 노출하고 있어서 dispatch만 추가하면 됨.
>
> 작동:
>
> ```osty
> let mut bools: List<Bool> = []
> bools.push(true)             // osty_rt_list_push_i1
>
> let mut floats: List<Float64> = []
> floats.push(3.14)            // osty_rt_list_push_f64 — value in d0
>
> let mut strs: List<String> = []
> strs.push("hi")              // osty_rt_list_push_string
> ```
>
> 추가:
> - 새 runtime symbol 상수: `runtimeSymListPushI1`, `runtimeSymListPushF64`,
>   `runtimeSymListPushString`
> - `lowerListPush`이 `value.Type()`로 디스패치 — Int/Bool/String은
>   x1, Float은 d0 (AAPCS64 FP arg cursor)
>
> 검증:
> - E2E binary smoke 3개 (`TestONBBackendBinaryRunsListGenericsOnDarwinARM64`):
>   list_bool (3 push → len 3), list_float (2 push → len 2),
>   list_string (4 push → len 4)
>
> 한계 (이번 슬라이스에서 명시적으로 보류):
> - `list[i]` 인덱스 read/write — `osty_rt_list_get_*` / `_set_*`
>   디스패치 미구현
> - `list_pop()` — runtime symbol 다양함, 미구현
> - 비-empty list literal `[1, 2, 3]` — `AggregateRV(list)` non-empty
>   case 미구현 (현재 빈 리스트만)
> - `for x in list` — iterator protocol 미구현
> - struct/enum element type — `osty_rt_list_push_bytes` 경로 필요
>
> 다음 슬라이스 후보: for-in 루프 native lowering (List<T> + 인덱스 read
> 결합), 또는 struct field mutation.
>
> **Slice A2 Week 15 (Float64 + IEEE 산술 + AAPCS64 FP ABI, 2026-05-02)** —
> ONB가 처음으로 Float64를 native lowering. `let x: Float64 = 3.14` /
> `(a + b) * 3.0` / `fn double(x: Float64) -> Float64` 같은 코드가
> fallback 없이 통과.
>
> 작동:
>
> ```osty
> fn double(x: Float64) -> Float64 { x * 2.0 }       // d0 in / d0 out
> fn add(n: Int, f: Float64) -> Float64 { f + 1.0 }  // n in x0, f in d0
> fn main() {
>     let a: Float64 = 1.5
>     let b: Float64 = 2.5
>     let c = (a + b) * 3.0
>     println(c)                                      // %g format
>     println(double(3.5))
>     println(add(10, 2.5))
> }
> ```
>
> 모델: scalar-everything 모델 유지 + d-register 클래스 추가. d8/d9를
> FP scratch (mirroring x9/x10), d0-d7을 AAPCS64 FP-arg cursor로 사용.
> Float64 const는 `movz/movk x9 + fmov d, x9` 시퀀스로 materialise.
> Mixed Int+Float fn signature은 두 개의 독립 cursor (regCursor +
> fpCursor)로 처리 — `fn(n: Int, f: Float64)`는 n→x0, f→d0 (자주
> 잘못 매핑되는 x0/x1이 아님).
>
> 추가:
> - 새 LIR opcodes: `LoadFloat64Stack`, `StoreFloat64Stack`,
>   `FmovDFromX` / `FmovXFromD`, `FaddReg` / `FsubReg` / `FmulReg` /
>   `FdivReg`, d0–d9 register names
> - `isFloatABIType(t)` predicate (Float / Float64), `isABIScalarType`
>   가 이를 포함하도록 확장
> - `materialiseFloatOperand` — FloatConst 또는 Float local → d 레지스터
>   (FloatConst는 `floatConstInstrs` helper로 movz/movk + fmov 시퀀스)
> - `lowerFloatBinaryAssign` — d8/d9 페어로 fadd/fsub/fmul/fdiv
> - `lowerUseAssign` Float 분기 — Float local 복사는 d 레지스터 경유
> - `lowerPrintln`이 Float 인자에 `%g\n` + fmov x1, d9 + str x1, [sp]
>   (darwin 변동인자 ABI는 모든 vararg가 stack)
> - `paramShuffle` / `lowerCall` / `epilogue`가 fpCursor로 d0-d7 사용
> - 새 Mach-O encoders: `dRegisterNumber`, `encodeFPStack` (str/ldr d),
>   `encodeFmovDFromX` / `encodeFmovXFromD`, `encodeFPArith` (fadd 외)
> - 자동 surface: `DebugTypeFloat`은 dwarf.go에 이미 있었던 dead path
>   였는데 이번 슬라이스에서 첫 사용자 등장
>
> 검증:
> - 단위 테스트 2개 (`internal/onb/onb_test.go`) — 1.5 + 2.5 패턴이
>   FmovDFromX + FaddReg + StoreFloat64Stack + FmovXFromD를 모두 emit
>   하는지, Float local이 DWARF DebugLocal로 surface되는지
> - E2E binary smoke 4개 (`TestONBBackendBinaryRunsFloat64OnDarwinARM64`):
>   const_print (3.14), arith ((1.5+2.5)*3.0=12), fn_param_ret
>   (double(3.5)=7), mixed_int_float_args (add(10,2.5)=3.5 — Int/Float
>   cursor 분리 회귀 테스트)
>
> 한계 (이번 슬라이스에서 명시적으로 보류):
> - Float 비교 연산 (`a < b`, `a == b`) — fcmpe + cset / b.cond 미구현
> - Float32 — 모든 경로가 d 레지스터 (Float64) 가정
> - Int↔Float 변환 (`n.toFloat64()`, `f.toInt()`) — fcvtzs / scvtf 미구현
> - Float을 struct/enum payload로 — abiRegSlots는 1-reg로 분류하지만
>   field write/read 코드 경로는 d 레지스터를 인식 못 함
> - Float DWARF (`frame variable c` 시 값 표시) — DebugTypeFloat은
>   surface되나 lldb 통합 테스트는 별도 슬라이스
>
> 다음 슬라이스 후보: List<T> 일반화 (`List<Bool>` / `List<Float64>` /
>  `List<String>` 런타임 디스패치) → for-in 루프 native.
>
> **Slice A2 Week 14 (Enum lowering v1 — Color / Option / Result + `?`,
> 2026-05-02)** — ONB가 처음으로 enum을 native lowering. Option/Result가
> prelude라 거의 모든 의미 있는 Osty 코드가 fallback에서 풀려나옴.
>
> 작동 (canonical 예제):
>
> ```osty
> enum Color { Red, Green, Blue }                // no-payload enum
> fn pick() -> Color { Color.Green }
>
> fn first(n: Int) -> Int? {                      // Option<scalar>
>     if n > 0 { Some(n) } else { None }
> }
>
> enum MyError { Empty, BadInt }
> fn parseSign(n: Int) -> Result<Int, MyError> {  // Result + ? operator
>     if n == 0 { Err(MyError.Empty) } else { Ok(n) }
> }
> fn parseAndDouble(n: Int) -> Result<Int, MyError> {
>     let v = parseSign(n)?
>     Ok(v * 2)
> }
> ```
>
> 슬롯 레이아웃: `[disc 8B][payload N×8B]`, 16B로 캡 (1 disc reg +
> 1 payload reg, AAPCS64 small-struct ABI 재사용). no-payload enum은
> 8바이트, Option<scalar>·Result<scalar, scalar>·Result<scalar, no-payload-enum>
> 은 16바이트.
>
> 추가:
> - `lookupEnumLayout(t)` — user enum (`mod.Layouts.Enums[name]`) +
>   합성 레이아웃 (`OptionalType`, `NamedType{Option/Maybe/Result, Builtin}`)
> - `syntheticOptionLayout` (None=0/Some=1) + `syntheticResultLayout`
>   (Err=0/Ok=1) — 컨벤션은 `internal/llvmgen` + `mir/lower.go`와 동일
> - `enumSlotSize(layout)` / `enumPayloadOffset(fieldIdx)` /
>   `enumDiscriminantValue(layout, idx)` 헬퍼
> - `payloadAllScalar`이 `isABIWordType`로 일반화 — scalar OR
>   1-register-slot 타입 (no-payload enum 같은) 허용 → `Result<Int,
>   MyError>` 가능
> - `lowerAggregateAssign`이 `AggEnumVariant` 처리 — discriminant를
>   slot+0에, payload를 slot+8+i*8에 stamp
> - 새 rvalue lowering: `*mir.NullaryRV` (None tag write),
>   `*mir.DiscriminantRV` (slot+0 → dest slot)
> - `placeProjectionOffset`이 `*mir.VariantProj` 처리 — payload는
>   slot+8 부터 (FieldIdx=-1은 "전체 payload tuple" → slot+8 시작)
> - `collectRValueLocals`가 `AggregateRV.Fields` + `DiscriminantRV.Place`
>   를 스캔해 enum scrutinee local이 slot을 받도록 보장
> - `lowerUseAssignMulti` — multi-slot copy (`_q = use _result`처럼
>   `?` 연산자가 fallback 분기에서 전체 enum을 복사할 때 필요).
>   destination type이 2-reg일 때 자동 선택; 1-byte clipping 회피
> - 새 LIR opcode `Brk{Imm}` + Mach-O 인코딩 (`brk #1`) — match
>   exhaustiveness가 추가하는 `mir.UnreachableTerm`을 안전하게 trap
>
> 검증:
> - 단위 테스트 3개 (`internal/onb/onb_test.go`) — Color enum 태그
>   write, UnreachableTerm → Brk lowering, Option<Int> Some 분기의
>   tag+payload 8B 간격
> - E2E binary smoke 7개 (`TestONBBackendBinaryRunsEnumPatternsOnDarwinARM64`,
>   darwin/arm64): no-payload enum, Option Some, Option None, Result
>   Ok, Result Err, `?` Ok 전파, `?` Err 전파 — 모두 native path 통과
>
> 한계 (이번 슬라이스에서 명시적으로 보류):
> - struct payload를 가진 enum variant — payload 1-reg 캡 때문에
>   `Some(Point)` 같은 형태는 fallback (Point가 2-reg)
> - 2개 이상 payload field를 가진 variant — `Some((a, b))` 같은 튜플 페이로드
> - `match` arm에서 `_4@Some.0` 같은 nested projection 외 (struct
>   payload + field projection 조합)
> - enum DWARF DIE — `frame variable` 출력에는 여전히 안 잡힘
>   (B.5 abbrev infra는 이미 있음, lower→encoder 와이어링은 별도 슬라이스)
>
> 다음 슬라이스 후보: Float64 + IEEE 산술 (Week 15) → List<T>
> 일반화 → for-in 루프 native lowering.
>
> **Slice A2 Week 13 (AAPCS64 small-struct passing + DWARF struct
> wiring, 2026-05-02)** — ONB가 처음으로 struct를 함수 경계에서 사용 가능.
> `≤16B` 모든-스칼라 struct가 AAPCS64 small-struct ABI로 2-reg pair에 실려
> 전달되며, lldb의 `frame variable`이 struct 필드를 풀어서 보여준다.
>
> 작동 (canonical 예제):
>
> ```osty
> struct Point { x: Int, y: Int }
> fn make() -> Point { Point { x: 5, y: 6 } }
> fn px(p: Point) -> Int { p.x }
> fn main() {
>     let p = make()           // {x0, x1} → slot+0 / slot+8
>     println(px(p))           // slot+0 / slot+8 → {x0, x1}
> }
> ```
>
> 추가:
> - `abiRegSlots(t)` / `isABIPassableType(t)` — ABI 슬롯 카운트 (스칼라 1,
>   ≤16B all-scalar struct N=ceil(size/8), 그 외 0/false)
> - `paramShuffle`이 register-cursor 모델로 multi-reg struct param을 처리:
>   regs[i..i+N) → slot+0 / slot+8 / …
> - `lowerCall`이 struct 인자를 `materialiseStructOperand`로 슬롯에서 N개의
>   인자 레지스터에 적재; 반환 값이 struct이면 x0, x1을 dest+0 / dest+8에 캡처
> - `epilogue`가 struct 반환 시 `_return` 슬롯에서 x0/x1을 로드
> - `assignLocalSlots`가 struct 반환 함수에서 `$ret`를 강제로 read 셋에
>   추가 (스칼라/Unit는 기존대로 x0 직통)
> - `lowerFunction` ABI guard가 struct 파라미터 / 반환을 허용 (>16B 또는
>   composite 필드는 여전히 fallback)
> - DWARF: `DebugTypeStruct` enum 값과 `DebugLocal.{StructName, StructFields}`
>   추가; `macho.go`의 `collectStructTypeInputs`가 distinct struct 타입을
>   첫-등장 순서로 수집해 `dwarfStructTypeInput` 리스트로 `emitDwarfInfo`에
>   전달; 변수 DIE는 `StructTypeIndex`로 struct DIE를 가리킴
>
> 검증:
> - 단위 테스트 4개 (`TestLowerMIRStruct*`, `TestEmitObjectIncludesStructDIE*`)
>   — paramShuffle multi-reg, epilogue multi-reg, call site materialise +
>   capture, DebugLocal에 struct 필드 노출, `__debug_info`에 struct/member DIE
> - E2E binary smoke (`TestONBBackendBinaryRunsStructParamAndReturnOnDarwinARM64`)
>   — `make() -> Point` + `px(p)` / `py(p)` 호출 체인이 darwin/arm64에서
>   `5\n6\n` 출력
> - LLDB integration (`TestONBBackendLldbFrameVariableShowsStructOnDarwinARM64`)
>   — `frame variable`이 `(Point) p = (x = 7, y = 11)` 표시
>
> 한계 (이번 슬라이스에서 명시적으로 보류):
> - >16B struct (3+ scalar 필드) — indirect/sret-style passing 미구현
> - 비-스칼라 필드를 가진 struct (List/String 포함) — HFA / 복합 분류 필요
> - struct 필드 mutation (`p.x = 5`) — projection-as-write 미지원, 여전히
>   fallback
> - 중첩 struct
> - struct 생성을 인자로 직접 (`px(Point { x: 1, y: 2 })`) — Aggregate-as-arg
>   는 fallback
>
> 다음 슬라이스 후보: enum lowering v1 (struct payload 없는 단순 enum
> variant), 또는 list element 일반화 (List<Bool> / List<Float64>).
>
> **Slice A2 Week 12 (struct lowering v1 — literals + field reads, 2026-05-02)** —
> ONB가 처음으로 struct를 lower. `struct Point { x: Int, y: Int }` 같은
> 모든-스칼라 struct 한정. 작동:
> - `let p = Point { x: 3, y: 4 }` → 16바이트 슬롯 (필드별 8바이트) 할당,
>   각 필드를 slot+offset에 write
> - `p.x` → `Copy(local projs=[FieldProj{Index:0}])` → `Load64Stack` at
>   slot + `Index * 8`
> - `println(p.x)` 정상 동작
>
> 추가:
> - `lowerState.mod` 필드 — 모듈-범위 layout 조회용
> - `localTypeSize`: 가변 슬롯 크기 (Unit 0, scalar 8, struct N×8, builtin
>   pointer 기본 8)
> - `lookupStructLayout` / `fieldByteOffset` / `placeProjectionOffset`
>   helpers — Type → layout → 바이트 offset 변환
> - `lowerStructLiteralAssign`: `AggregateRV(struct)` 처리, 각 필드를
>   slot+i*8에 store
> - `loadPlaceIntoReg`가 projection을 받아 slot+offset에서 load
> - `assignLocalSlots`가 `localTypeSize` 사용 (이전 hardcoded 8)
>
> 한계 (이번 슬라이스에서 명시적으로 보류):
> - struct fn param / return (AAPCS64 small-struct passing 2-reg / indirect)
> - DWARF struct 변수 인스펙션 (`frame variable p`) — B.5 infra는 깔려
>   있으나 lower→encoder 와이어링은 별도 슬라이스
> - struct 필드 mutation (`p.x = 5`) — projection-as-write 미지원
> - 중첩 struct
>
> 다음 슬라이스 후보: AAPCS64 small-struct passing → struct를 fn 경계에서
> 사용 가능 → DWARF 변수 인스펙션과 묶어서 진행.
>
> **Slice A2 Week 11 (DWARF B.5 infra — struct/member DIEs, 2026-05-02)** —
> 인코더 인프라만 추가. ONB가 struct를 lower하지 않으므로 dead code지만,
> 향후 struct lowering 슬라이스가 들어올 때 DWARF 측은 더 이상 막히지 않음.
> 구체적으로:
> - `DW_TAG_structure_type` (abbrev 6, has children) + `DW_TAG_member`
>   (abbrev 7) 추가
> - `DW_AT_data_member_location` 속성 + `DW_FORM_udata` form
> - `dwarfStructTypeInput` / `dwarfStructMemberInput` 입력 타입
> - `emitDwarfInfo`가 `structs []dwarfStructTypeInput` 인자 추가 (callers
>   pass nil 으로 변동 없음)
> - 구조체 멤버가 base 타입을 참조하면 같은 CU의 base_type DIE를 공유
>   (`collectUsedTypeKinds`가 멤버까지 union)
> - `dwarfVariableInput.StructTypeIndex int` (–1 = base type 사용,
>   ≥0 = struct list 인덱스 — `variableTypeOffset` helper로 분기)
>
> 검증: 단위 테스트 3개 — abbrev 테이블에 struct/member 코드 존재,
> 인코더가 struct 입력을 받으면 DIE 바이트가 늘어남, variable이
> StructTypeIndex로 struct DIE를 가리킬 수 있음.
>
> 한계: dwarfdump round-trip은 macho.go가 struct를 받지 않아 ScaleHole.
> 실제 사용은 lowering 들어올 때 (struct AggregateRV + place projection
> + AAPCS64 small-struct passing).
>
> **Slice A2 Week 10 (DWARF B.4 — Bool/String var inspection, 2026-05-02)** —
> Phase B.3의 variable inspection을 Int 외 ABI-scalar 두 종류로 확장. Bool은
> `byte_size 1` + `DW_ATE_boolean` (lldb는 byte_size 8 + boolean을 거부하고
> "void"로 표시), String은 `DW_TAG_pointer_type` → `DW_TAG_base_type "char"`
> 체인 (lldb가 pointee를 C 문자열로 표시).
>
> 검증: `fn first(a: Int, b: Bool, c: String) -> Int { a }` 함수 진입 시점에
> `frame variable`이 다음을 표시:
> - `(long) a = 42`
> - `(bool) b = true`
> - `(char *) c = 0x... "hi"`
>
> 추가:
> - `DebugTypeBool` / `DebugTypeString` / `DebugTypeFloat` enum entries
>   (Float은 ONB lowering이 D-reg 미지원이라 dead code, future-ready)
> - `dwarfBaseTypeBool` / `Float` / `String` + `dwarfAbbrevPointerType`
> - `collectUsedTypeKinds`로 변수에서 참조된 타입만 emit (CU dead 타입 X)
> - `emitDwarfInfo`가 String일 때 char base_type + pointer_type 두 DIE
> - `mirTypeToDebugKind` helper로 MIR type → debug kind 단일 source
> - **Param 슬롯이 read 여부와 무관하게 항상 할당** — unused param도
>   `frame variable`에 표시되도록 (이전엔 unused면 슬롯 없어 invisible)
>
> 한계: Float은 ONB native가 아직 처리 못 함 (Float 인자/반환 → fallback to
> LLVM). struct/list/Map composite 타입은 후속 슬라이스.
>
> **Slice A2 Week 9 (DWARF B.3 — variable inspection, 2026-05-02)** —
> lldb `frame variable` 가 ONB 빌드 결과에서 named Int local의 현재 값을
> 보여줌. 추가:
> - `DW_TAG_base_type` DIE for Int (byte_size 8, encoding DW_ATE_signed)
> - `DW_TAG_variable` DIE per named Int local: `DW_AT_name` + `DW_AT_type`
>   (ref4 to base_type) + `DW_AT_location` (DW_OP_fbreg sleb128(slot))
> - Subprogram DIE에 `DW_AT_frame_base = DW_OP_breg31 0` (sp-relative)
> - Abbrev table 4 entries: CU, subprogram(children), variable, base_type
> - LIR Function에 `DebugLocals []DebugLocal` 필드 — 이름·slot·type 캐리
> - lower.go가 named Int local만 (`_return` / 익명 temp 제외) 추출
> - **Param shuffle instrs는 LineSpan zero**: shuffle PC가 source line으로
>   매핑되면 lldb가 prologue 끝/shuffle 시작 사이에 stop해서 uninitialised
>   슬롯을 읽는 문제 — line program이 shuffle 행을 skip하도록 수정
>
> 검증: `lldb -o "br set -f main.osty -l 1" ./app` → `frame variable`
> → `(long) a = 10`, `(long) b = 32`. 변수 이름·값 모두 정확.
>
> 한계: 현재는 Int만 (DW_ATE_signed). String/Bool/Float/List 등은
> DebugTypeKind 확장 + 대응 base_type/pointer_type DIE 추가가 필요.
>
> **Slice A2 Week 8 (DWARF B.2 — lldb integration, 2026-05-02)** — DWARF
> 파이프라인이 lldb 자동 인식까지 도달. `dsymutil`이 .o의 DWARF를 .dSYM으로
> 번들링하고, `lldb`가 `br set -f main.osty -l 4` 같은 source-level breakpoint
> 를 PC로 해상하며 source view까지 표시한다. 추가:
> - `DW_TAG_subprogram` DIE per user function (CU의 child DIE; CU는
>   `DW_CHILDREN_yes`로 변경) — dsymutil이 child 없는 CU를 빈 것으로 처리하던
>   문제 해결
> - `__debug_info` + `__debug_line` 섹션에 **non-extern UNSIGNED 릴로케이션**
>   (extern=false, symbolnum=__text section number) — clang의 패턴과 동일.
>   extern symbol-rel 릴로케이션은 dsymutil이 silently 거부함
> - `dwarfInfoEncoded.LowPCOffsets` / `dwarfLineEncoded.SetAddressOffset`로
>   인코더가 reloc 위치를 호출자에게 전달
> - Mach-O writer가 `__text` reloc 다음에 DWARF section reloc들을 배치
>   (highest-address-first 정렬)
> - `ltmp0` local symbol (당시엔 reloc target으로 의도했으나 결국 section-rel
>   reloc으로 갔으므로 unused; 향후 cleanup 가능)
>
> 검증: `lldb -o "br set -f main.osty -l 4" ./app` 실행 시
> `Breakpoint 1: where = app\`main + 56 at main.osty:4:9` + source 5라인 표시.
>
> **Slice A2 Week 7 (DWARF B.1 — CU DIE, 2026-05-02)** — `__debug_info` +
> `__debug_abbrev` + `__debug_str` 세 섹션이 추가되어 DWARF 인프라 완비.
> CU DIE는 `DW_TAG_compile_unit` 1개에 7개 attribute (producer, language=C99,
> name, comp_dir, low_pc, high_pc, stmt_list). DW_AT_stmt_list가 line program을
> 가리키므로 DWARF 파서가 컴파일 유닛 → 라인 프로그램 traversal 가능.
> `dwarfdump --debug-info / --debug-abbrev / --debug-str` 모두 zero-warning.
>
> 추가:
>   - DWARF tag/attr/form 상수 (DW_TAG_compile_unit, DW_AT_producer 등)
>   - `dwarfStringTable`: dedup string storage with stable byte offsets
>   - `emitDwarfAbbrev`: abbrev table 1개 entry (compile_unit + 7 attrs)
>   - `emitDwarfInfo`: CU header + DIE encoder
>   - Mach-O writer가 `__DWARF` 세그먼트에 4 sections (line/info/abbrev/str)
>   - 새 section name 상수 (`machoSectnameInfo` / `Abbrev` / `Str`)
>
> 한계 — lldb 자동 인식은 아직: dsymutil이 .o의 Mach-O Stab debug symbols
> (N_OSO/N_FUN/N_BNSYM) 을 walk해서 .dSYM 번들을 만드는데, 우리는 아직 Stabs를
> emit 안 함. dwarfdump는 DWARF 섹션을 직접 읽어 모두 검증되지만, lldb가
> `bt`에서 `main.osty:42` 표시하려면 Phase B.2 (Stabs)이 추가로 필요.
>
> **Slice A2 Week 6 (String concat + List<Int>, 2026-05-02)** — ONB native
> path가 처음으로 런타임 호출 경로를 사용. `osty_runtime.c` 가 link 단계에
> 같이 들어오며 다음 패턴이 native로 통과:
>
>   - `let name = "world"; println("hello, " + name)` (String concat with local)
>   - `fn greet(name: String) -> String { "hello, " + name }` (String param/return)
>   - `let mut v: List<Int> = []; v.push(10); println(v.len())` (list ops)
>   - `for i in 0..5 { v.push(i) }` (list build via loop)
>
> 추가:
>   - 런타임 심볼: `osty_rt_strings_Concat`, `osty_rt_list_new`,
>     `osty_rt_list_push_i64`, `osty_rt_list_len` — 모두 `BranchLink`로 호출
>   - `materialiseOperand`가 `StringConst` → `LoadCStringAddress` 경로 추가
>   - `lowerStringConcatAssign`: `BinaryAdd` + `T == TString` → 두 args를
>     x0/x1에 띄우고 runtime concat 호출, 결과 ptr를 dest slot에 저장
>   - `lowerAggregateAssign`: 빈 list literal → `osty_rt_list_new`
>   - `lowerListPush` / `lowerListLen`: 새 intrinsic dispatch
>   - `lowerPrintln`이 String local도 처리 (`puts(string ptr)` 경로)
>   - `LoadCStringAddress`가 임의 X register dst 지원 (이전엔 x0 only)
>   - `isABIScalarType` helper로 user fn param/return을 Int/Bool/String 모두 허용
>   - Backend가 매 빌드에 `EnsureRuntimeObject` 호출해서 runtime.o를 link 인자로 추가
>
> 부수 (직전 Week 5 cleanup): `_ = fnStart` / `_ = dwarfSegmentSections` 제거,
> `functionPrologueWords`를 lir.go로 이동 (single source of truth),
> Mach-O 세그먼트/섹션 이름 상수화.
>
> **Slice A2 Week 5 (DWARF .debug_line, 2026-05-02)** — ONB가 처음으로 디버그
> 메타데이터를 emit. `__debug_line` 섹션이 Mach-O `__DWARF` 세그먼트로 들어감.
> `dwarfdump --debug-line foo.o`가 정상 파싱하며 PC→source `<file>:<line>:<col>`
> 매핑이 모든 실행 경로에서 정확. 추가:
> - DWARF 4 line-program 인코더 (`internal/onb/dwarf.go`): leb128/uleb128 +
>   prologue + state machine. Special opcode는 안 쓰고 `set_address +
>   advance_pc + advance_line + copy + end_sequence` 시퀀스로 단순화
> - `Block.LineSpans` 필드 + `lowerBlock`이 각 LIR 명령마다 source position 부착.
>   Dead-store / 중복 행은 `appendLineRow`가 코얼레스
> - Mach-O writer가 `__DWARF` + `__LINKEDIT` 세그먼트 추가. 파일 레이아웃은
>   `text → cstring → debug_line → relocs → symtab → strtab` 순서로 segment
>   range overlap 없이 배치. ld64가 multi-segment .o를 받아들이려면 LINKEDIT가
>   필수 (이전엔 단일 __TEXT 세그먼트라 LINKEDIT 없어도 통과)
> - `Request.SourcePath` / `PackageName`이 `Program`까지 흐르면서 file table에
>   소스 경로가 채워짐
>
> 후속: `__debug_info` + `__debug_abbrev` + `__debug_str`이 들어와야 lldb가
> 자동 인식. dsymutil 통합도 추가 슬라이스로 분리.
>
> **Slice A2 Week 4 (loops + match, 2026-05-02)** — `while` / `for ... in 0..N`
> / 중첩 루프 / `match`(non-const scrutinee 포함)이 모두 native path로 통과.
> 핵심 발견: while/for/단순 match는 Week 3 multi-block + branch fixup
> 인프라로 이미 동작 — 별도 lowering 없이도 native. 추가된 것은
> SwitchIntTerm 한 가지 (non-const scrutinee가 있는 일반 match가 사용):
> - `BranchCond { Cond, Target }` LIR opcode + Mach-O fixup (`b.cond imm19`)
> - `lowerSwitchIntTerm`: scrutinee를 x9에 load, 각 case는 `mov x10, #imm;
>   cmp x9, x10; b.eq case_target`, 마지막에 `b default`
> - `collectTerminatorReads`가 `SwitchIntTerm.Scrutinee`도 walk
> 부수: `_ = fnStart` 미사용 변수 정리 (simplify 리뷰 피드백).
>
> **Slice A2 Week 3 (control flow, 2026-05-02)** — `if/else` + 6개 비교
> 연산자 (`==`, `!=`, `<`, `<=`, `>`, `>=`) 분기 코드가 native path로 통과.
> 패턴 `let x = K; if x > 0 { println(1) } else { println(0) }` 모두 OSTY_ONB_STRICT
> 으로 동작 (80–180ms). 추가:
> - `Cmp` / `Cset` opcode + 6개 `Cond` 상수 (CondEq/Ne/Lt/Le/Gt/Ge)
> - `Branch` (unconditional) + `BranchCondNotZero` (cbnz) — block index를
>   target으로 들고, 인코더가 fixup pass에서 PC-relative imm26 / imm19로 패치
> - Multi-block 함수 지원: `Block.OriginalIndex`로 MIR BlockID를 보존,
>   entry block first 정책으로 emit slot 0이 항상 entry
> - `BoolConst` materialisation (`mov #0` / `#1`)
> - `collectTerminatorReads`로 BranchTerm.Cond 같은 terminator-level 읽기를
>   slot allocation에 반영
>
> **Slice A2 Week 2 (사용자 함수 호출, 2026-05-02)** — 단일 모듈 내 helper
> 함수 + AAPCS64 호출 규약 추가. 패턴 `fn add(a: Int, b: Int) -> Int { a + b };
> fn main() { println(add(40, 2)) }`이 native path로 통과. 모든 Int 파라미터
> 지원 (최대 8개, x0..x7). 변경:
> - `LowerMIR`이 모든 함수를 순회 (main 외 helper 포함). main이 항상 첫 번째
> - 함수 진입 시 prologue가 AAPCS64 인자 register를 stack slot으로 shuffle
> - 비-main 함수의 `ReturnTerm`은 `_return` slot을 x0에 load 후 ret
> - `CallInstr` (FnRef 한정) lowering: 인자를 x0..x7에 배치 → `bl <symbol>`
>   → 결과 register x0를 dest slot에 저장
> - Mach-O encoder가 multi-function: 각 함수가 자기 symbol entry + text
>   offset, `bl _add` 같은 local fn 호출은 local symtab slot으로 reloc해서
>   `_add`가 defined와 undefined로 중복 등장하는 문제 방지
>
> 본 문서는 결정 lock-in이며, 후속 의제(예: aarch64 LIR opcode 카탈로그,
> cross-validation harness, simple inliner 도입 검토)는 별도 문서로 분기한다.

## 한 줄 요약

ONB는 **LLVM과 ABI·의미 100% 호환되는 단순 dev 백엔드**. Non-SSA MIR을
입력으로 받아 aarch64 LIR을 거쳐 lld로 darwin/linux binary를 emit. Full
DWARF 디버그, 옵티마이저는 4-pass minimal, vectorize·SIMD·incremental GC는
LLVM에 **영구 위임**한다.

---

## 0. 관련 문서와 경계

이 문서가 **LLVM 보완용 dev/debug 백엔드(ONB)** 의 주 설계 문서다.
실행 속도, 디버그 경험, `osty run` / `osty test` / watch loop의 기본 개발
경로, 그리고 "무엇을 하지 않을지"는 여기 결정이 우선한다.

[`MIR_EMITTER_PORT.md`](./MIR_EMITTER_PORT.md)는 별도 보조 트랙이다. 그 문서는
기존 LLVM emitter (`internal/llvmgen/mir_generator.go`)의 LLVM 텍스트 생성
의미를 `toolchain/mir_generator.osty` 쪽으로 옮기는 포팅 현황표이지,
ONB 구현 계획이 아니다. ONB는 LLVM IR을 더 self-host로 잘 생성하는 작업이
아니라, 같은 MIR 의미를 입력으로 받는 **별도 dev backend**다.

판단 규칙:
- 새 작업이 `osty run` / `osty test` / watch loop를 더 빠르고 디버그하기
  쉽게 만드는 별도 backend 구현이면 이 문서를 따른다.
- 새 작업이 기존 LLVM IR emitter의 문자열 builder, 지원성 검사, intrinsic
  lowering을 Osty 소유로 옮기는 일이면 `MIR_EMITTER_PORT.md`를 따른다.
- ONB와 LLVM emitter가 같은 의미를 다루는 경우 LLVM은 reference/release
  backend, ONB는 dev/debug backend로 두고 cross-validation으로 동등성을
  확인한다.

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
