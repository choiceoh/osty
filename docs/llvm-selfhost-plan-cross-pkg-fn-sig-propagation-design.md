# LLVM self-host — cross-pkg fn signature propagation through IR (Task B design)

> **상태**: design (architectural change — not yet implemented).
> **선행**: PR #1934 (`?`-prefix synthesis), PR #1936 (Option/Result MIR
> recovery), PR #1937 (List/Set MIR recovery + library-mode symbol
> qualification + cross-pkg link wall measurement). 본 doc 는 cross-pkg
> trajectory 의 다음 단계.
> **소유**: ir / mir / check.

## 1. 30초 요약

`use toolchain as tc; tc.frontInvalidTypeRepr()` 호출이 MIR/LLVM 단계에서
return type 을 잃는 leak (`declare void @toolchain.frontInvalidTypeRepr()`,
즉 dep 의 실제 `-> FrontTypeRepr` 시그니처 대신 void). PR #1937 의
library-mode qualification 이 link 측 mangling drift 는 해소했지만, 호출
사이트의 return type 은 여전히 leak 가능. UseDecl 의 cross-pkg fn 시그니처
plumbing 이 미완성.

## 2. 현재 위치의 leak path

`internal/mir/lower.go::useDeclFnType` (line 4055) 는 `*ir.UseDecl.GoBody`
만 walk:

```go
func useDeclFnType(use *ir.UseDecl, name string) *ir.FnType {
    if use == nil || name == "" { return nil }
    for _, d := range use.GoBody {
        fn, ok := d.(*ir.FnDecl)
        if !ok || fn == nil || fn.Name != name { continue }
        ...
    }
    return nil
}
```

`GoBody` 는 inline FFI `use X { fn name(...) }` 만 채움. 일반 cross-pkg
`use toolchain as tc` 는 GoBody 가 비어있어 useDeclFnType nil 반환. 결과:
`internal/mir/lower.go::resolveQualifiedCall` 의 fallback path 도 무용.

```go
func (bs *bodyState) resolveQualifiedCall(...) ([]Operand, Callee) {
    callType := t
    if sig := useDeclFnType(use, name); sig != nil {
        if isPoisonType(callType) || irHasPoisonedTypeArg(callType) {
            callType = sig  // <- 일반 cross-pkg는 도달 못함 (sig == nil)
        }
    }
    return out, &FnRef{Symbol: qualifiedSymbol(use, name), Type: callType}
}
```

즉 cross-pkg call 의 consumer-side `t` (IR type) 가 poison 일 때 recovery
경로가 없음. stage0 emit 은 poison 을 void 로 lower → 우리가 본
`declare void @<pkg>.<fn>()`.

## 3. 진짜 fix path — UseDecl.Imports 캐리

**Step 1: IR shape**

`internal/ir/ir.go::UseDecl` 에 cross-pkg sig field 추가:

```go
type UseDecl struct {
    ...
    GoBody  []Decl   // 기존 — inline FFI body
    Imports []*FnDecl // 신규 — 일반 cross-pkg import surface
    SpanV   Span
}
```

`Imports` 가 set 되면 consumer 의 checker 가 import surface 에서 봤던 dep
의 pub fn 의 시그니처 (param 이름/타입, return type) 가 캐리.

**Step 2: ir.Lower 파라미터 확장**

`internal/ir/lower.go::Lower` 는 현재 `(*ast.File, *resolve.Result,
*check.Result)`. cross-pkg import surface 데이터가 추가로 필요. 두 옵션:

a) `check.Result` 에 새 필드 추가 — `ImportSurfaces []api.PackageCheckImport`
   - 장점: 기존 시그니처 변화 없음 (Result 만 확장)
   - 단점: Result 는 검사 출력이므로 surface 데이터를 끼워넣는 게 의미적 mismatch

b) `Lower` 에 새 파라미터 `imports []api.PackageCheckImport`
   - 장점: 의미 명확
   - 단점: 호출 사이트 다수 갱신 (5-6 곳)

**권장: (a)** — `check.Result.ImportSurfaces` 추가. check 단계가 이미
import surface 를 `selfhostInstallImportSurfaces` 로 컨슈밍 중이라
같은 데이터를 Result 에 캐싱하는 것은 자연스러움. 추정 한 줄 추가.

**Step 3: lowerUseDecl 확장**

`internal/ir/lower.go::lowerUseDecl` 가 `l.chk.ImportSurfaces` 에서
alias 매칭 surface 를 찾고 그 surface 의 Functions 를 `*ir.FnDecl`
로 lower 해서 `out.Imports` 에 넣음. (각 함수의 receiver, param, return
type 을 `lowerType` 으로 변환.) 추정 ~40 LOC.

**Step 4: MIR useDeclFnType 확장**

```go
func useDeclFnType(use *ir.UseDecl, name string) *ir.FnType {
    if use == nil || name == "" { return nil }
    for _, d := range use.GoBody { ... }  // 기존
    for _, fn := range use.Imports {       // 신규
        if fn == nil || fn.Name != name { continue }
        // 같은 FnDecl → FnType 변환 로직 재사용
        return fnDeclToFnType(fn)
    }
    return nil
}
```

**Step 5: 테스트**

- `internal/mir/use_decl_fn_type_test.go` — UseDecl.Imports 가 있는 cross-pkg
  call 의 return type recovery 검증.
- `internal/check/cross_pkg_import_surface_test.go` — check.Result.ImportSurfaces
  가 PackageImportSurface 의 출력과 일치 검증.
- Integration: `selfhost.CheckPackageStructured` 의 기존 cross-pkg test 들에
  return type 검증 추가.

## 4. 추정 LOC + 위험

| 단계 | LOC | 위험 |
|---|---|---|
| Step 1 (UseDecl.Imports field) | ~5 | 낮음 (additive) |
| Step 2 (check.Result.ImportSurfaces field) | ~5 | 낮음 (additive) |
| Step 3 (lowerUseDecl populate) | ~40 | 중간 (type lowering recursion) |
| Step 4 (useDeclFnType consult) | ~10 | 낮음 |
| Step 5 (tests) | ~80-120 | — |
| **합** | **~150-200** | medium (multi-file, cross-package) |

이전 #1934/#1936/#1937 의 ~30-100 LOC 패턴보다 큼. 단일 fresh session 가능
하나 review cycle 길어질 가능성 — sub-PR 분할 권장.

## 5. 분할 권장 — 3 sub-PR sequencing

```
PR (B-1): UseDecl.Imports + check.Result.ImportSurfaces field 추가 (additive shape)
  ↓ no behavioral change, no test
PR (B-2): lowerUseDecl populate Imports (behavioral; only consumes new field)
  ↓ unit test for lowerUseDecl
PR (B-3): useDeclFnType + MIR recovery 활성화 + integration test
  ↓ end-to-end cross-pkg return-type leak 해소 검증
```

각 sub-PR ~50-70 LOC. review 가 짧고 self-contained.

## 6. 본 doc 의 산출물

- `docs/llvm-selfhost-plan-cross-pkg-fn-sig-propagation-design.md` (이 doc)
  — Task B 의 architectural design + sub-PR 분할 권장
- 코드 변경 zero

다음 fresh session 의 cross-pkg trajectory 의 다음 단계 시작점.

## 7. 관련 PR 카탈로그

| PR | scope |
|---|---|
| [#1902](https://github.com/choiceoh/osty/pull/1902) | PR-G1 scaffold |
| [#1905](https://github.com/choiceoh/osty/pull/1905) | PR-G2 cross-pkg dep library compile path (opt-in) |
| [#1908](https://github.com/choiceoh/osty/pull/1908) | void → aggregate zero-init cascade fix |
| [#1934](https://github.com/choiceoh/osty/pull/1934) | `?`-prefix synthesis from `ParamDefaults` |
| [#1936](https://github.com/choiceoh/osty/pull/1936) | Option/Result MIR return-type recovery |
| [#1937](https://github.com/choiceoh/osty/pull/1937) | List/Set MIR recovery + library-mode symbol qualification + 본 doc 자매 doc |
| (Task B; 이 doc) | UseDecl.Imports plumbing — cross-pkg fn sig propagation |
