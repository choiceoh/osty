# LLVM self-host — cross-pkg link symbol mangling wall measurement

> **상태**: measurement (cmd/osty-native-checker production-path build 의 다음 wall 정확화).
> **선행**: PR #1908 (void → aggregate cascade), PR #1934 (`?`-prefix synthesis),
> PR #1936 (Option/Result MIR return-type recovery), PR #1937 (List/Set MIR recovery).
> **소유**: backend / cross_pkg.

## 1. 30초 요약

`OSTY_STAGE0_FALLBACK=1 .bin/osty build --backend llvm cmd/osty-native-checker/` 의 link 단계가 여전히
실패:

```
ld.lld: error: undefined symbol: toolchain.frontInvalidTypeRepr
```

원인은 consumer/dep 의 **심볼 mangling drift** — 같은 함수에 대해 두 사이트가 다른 LLVM
심볼을 emit. dep 라이브러리 compile 자체가 가능해져도 link 가 불가능한 본질적 wall.

## 2. 재현

```sh
go build -o .bin/osty ./cmd/osty
OSTY_STAGE0_FALLBACK=1 .bin/osty build --backend llvm cmd/osty-native-checker/
```

stage0 fallback 으로 consumer 의 IR 생성은 통과. clang link 단계에서 fail.

## 3. 측정한 mangling drift

생성된 `cmd/osty-native-checker/.osty/out/debug/llvm/main.ll`:

```llvm
declare void @toolchain.frontInvalidTypeRepr()
...
define void @main() {
  call void @toolchain.frontInvalidTypeRepr()
}
```

consumer 가 호출하는 심볼: `@toolchain.frontInvalidTypeRepr` (dot-form, package-qualified).

소스: `internal/mir/lower.go::qualifiedSymbol`:

```go
case use.IsRuntimeFFI && use.RuntimePath != "":
    return use.RuntimePath + "." + name
case use.IsGoFFI && use.GoPath != "":
    return use.GoPath + "." + name
...
if use.RawPath != "" {
    return rewriteStdlibSymbolToRuntime(use.RawPath + "." + name)
}
```

`use toolchain as tc` → `RawPath = "toolchain"`, `Alias = "tc"` → consumer emit `toolchain.frontInvalidTypeRepr`.

dep `toolchain/` 를 단독 build 했을 때 emit 되는 심볼: `@frontInvalidTypeRepr` (bare,
qualifier 없음). 소스: `internal/backend/stage0/emit.go:540`:

```go
fmt.Fprintf(&b, "define %s @%s(", retLLVM, fn.Name)
```

stage0 가 dep 의 `pub fn frontInvalidTypeRepr` 을 그대로 `@frontInvalidTypeRepr` 로 emit.
consumer 가 부르는 `@toolchain.frontInvalidTypeRepr` 와 mismatch.

## 4. 보조 wall: void return-type leak

`declare void` 가 emit 되는 이유 — `let _typeRepr = tc.frontInvalidTypeRepr()` 의
binding 이 leading-`_` (unused). PR-G1 (#1902) 의 cross-pkg dispatch arm + PR #1908
의 cascade fix 가 결과를 `void` 로 받는다 (consumer 의 checker env 가 dep 의 진짜
return type `FrontTypeRepr` 모름). 본 wall 은 link 가 풀린 이후의 secondary
correctness 이슈.

## 5. 진짜 fix 의 path

세 후보, 각각 trade-off:

### Path α — dep library mode 가 package-qualified 심볼 emit

`cmd/osty-native-llvmgen/main.go::stripMainForLibraryMode` 가 main 만 strip. 확장해서
LibraryMode 일 때 exported `fn.Name` 을 `<package>.<name>` 로 rename. 패키지 이름은
manifest `[package] name` 또는 request 에 명시.

- **장점**: link 단일 unblock. Consumer-side 무수정.
- **단점**:
  - manifest plumbing 필요 (현재 PackageInput 에 package name 없음)
  - dep 자체를 standalone 으로 빌드할 때 다른 link 컨벤션 (자기 자신을 main 으로 빌드)
  - dep 내부 cross-fn call (recurse, helper) 도 qualified 형태로 emit 해야 함

### Path β — consumer 가 unqualified 심볼 emit (cross-pkg free-fn 경우)

`qualifiedSymbol` 의 `RawPath != ""` 분기를 narrow — cross-pkg 가 use-alias method
dispatch arm 으로 resolve 된 free-fn 인 경우 qualifier 생략. 단 MIR 가 그
context 정보 (elab dispatch arm 결정) 를 carry 안 함 → 추가 plumbing.

- **장점**: dep 측 변경 zero
- **단점**: stdlib/FFI 의 dot-form 도 같은 분기 — namespace collision 위험

### Path γ — symbol intermediate translation

builder 의 link 단계에서 `objcopy --redefine-sym` 같은 도구로 dep 객체의 심볼을
package-qualified 로 rename. consumer 와 dep 양쪽 변경 zero, 단 빌드 toolchain
복잡도 증가.

- **장점**: 컴파일러 변경 zero
- **단점**: objcopy 의존성, debug info 손상 위험

## 6. 권장 — Path α (library-mode qualification)

자기-제한적 mechanism, 단일 PR 가능 추정. 단계:

1. `internal/nativellvmgen.PackageInput` 에 `PackageName string` 필드 추가
2. `cmd/osty/cross_pkg_deps.go` 가 manifest 에서 package name 추출해 LibraryMode
   request 에 채움
3. `cmd/osty-native-llvmgen/main.go::stripMainForLibraryMode` 가 `entry.MIR.Functions`
   를 walk 해서 main 제외 / pub `fn.Name` 을 `pkg.fn` 로 rename
4. dep 내부 cross-fn call 의 symbol 도 rename 추적 (CallInstr.Callee.Symbol 매핑)

추정 ~80-100 LOC + 테스트. PR #1908 패턴 매치 (single dispatch site + 동등 변경).

## 7. 보조 wall 의 trajectory

§4 의 void return-type leak 은 link 가 풀린 후의 별개 작업:

- consumer 가 dep 의 real return type 인 `FrontTypeRepr` 을 받아 처리해야 정상 program
- 현재 stage0 dispatch arm 의 fallback (`<error>` → void) 이 silent → semantically
  incomplete 한 program 생성
- PR-G3+ trajectory 의 cross-pkg signature 완전 전파 (UseDecl 에 sig 캐리, MIR 의
  `useDeclFnType` 확장 등) 가 진짜 해결책

## 8. 이 doc 의 산출물

- `docs/llvm-selfhost-plan-cross-pkg-link-measurement.md` (이 doc) — wall trigger 정확화
  + Path α/β/γ 비교 + Path α 권장
- 코드 변경 zero

다음 fresh session 의 PR3-G (또는 추정 이름 PR-G3/G4) 의 시작점.

## 9. 관련 PR 카탈로그 (\<-2 weeks)

| PR | scope |
|---|---|
| [#1902](https://github.com/choiceoh/osty/pull/1902) | PR-G1 scaffold: `backend.Request.ExtraObjects` 인프라 |
| [#1905](https://github.com/choiceoh/osty/pull/1905) | PR-G2: `OSTY_CROSS_PKG_LINK=1` opt-in 의 dep library compile path |
| [#1908](https://github.com/choiceoh/osty/pull/1908) | void → aggregate zero-init cascade fix (`lirLowerMirAssign`) |
| [#1934](https://github.com/choiceoh/osty/pull/1934) | `?`-prefix synthesis from `ParamDefaults` (cross-pkg fn arity) |
| [#1936](https://github.com/choiceoh/osty/pull/1936) | Option/Result MIR return-type recovery |
| [#1937](https://github.com/choiceoh/osty/pull/1937) | List/Set MIR return-type recovery |
| (이 doc) | cross-pkg link symbol mangling wall trigger measurement |

## 10. Cross-pkg interface support (2026-05)

Separate from free-fn link/mangling (§1–§9), cross-package **interface**
values need nominal-type lowering, boxing, and (eventually) vtable injection
when the interface type lives in another package (for example `Error` from
`std.error` consumed by `std.keychain`).

### Shipped steps

| Step | PR | What landed | Code anchor |
|---|---|---|---|
| 1 | [#2004](https://github.com/choiceoh/osty/pull/2004) | Cross-pkg nominal types lower to opaque `ptr` in LIR Proto when no local layout exists (PascalCase heuristic avoids silent `int` → `ptr`) | `toolchain/lir_proto.osty` `lirLowerMirType` / `lirLowerMirType_module` |
| 2 (partial) | [#2004](https://github.com/choiceoh/osty/pull/2004) | Cross-pkg call arg/return types no longer decline with `cross-module call arg type T is not implemented` when the nominal is external | same |
| 3 | [#2007](https://github.com/choiceoh/osty/pull/2007) | `Aggregate → Ptr` assign coercion boxes struct values on the GC heap for cross-pkg interface slots (pattern-match / `Err(_)` subset) | `toolchain/lir_proto.osty` assign arm ~3953 |
| 3.5a | [#2009](https://github.com/choiceoh/osty/pull/2009) | Interface methods registered in MIR signature table so call sites declare correct return/param shapes (`Error__message` → `String`, not `i64`) | `internal/mir/lower.go` InterfaceDecl case |
| 3.5b | [#2010](https://github.com/choiceoh/osty/pull/2010) | `BuiltinTypeOwningModule("Error")` + `expandInterfaceMethodReach` inject concrete impl bodies (`BasicError.message`, etc.) | `internal/backend/stdlib_inject.go` |

Regression guard: `TestLLVMBackendBinaryCrossPkgInterfaceBoxingErrConstruct`
(`internal/backend/llvm_crosspkg_iface_box_test.go`) — `Err(error.new(...))`
through match arm.

### Remaining gap — cross-pkg vtable injection

Boxing (step 3) is a **prerequisite**, not full interface dispatch. Calling a
method on a cross-pkg interface value (for example `err.message()` on `Error`)
still needs vtable wiring across package boundaries. Until that lands:

- `Err(_)` / discriminant-only pattern arms work (boxed ptr is enough).
- Virtual dispatch on cross-pkg interface receivers fails at LIR or link.

`TestLLVMBackendStdKeychainApiKeyWrapperLowers`
(`internal/backend/llvm_keychain_test.go`) documents the vtable gap and pins
`OSTY_STDLIB_BODY_LOWER=0` for symbol-level coverage until vtable injection lands.

### Operational note

These paths assume **default-on** stdlib body injection (`OSTY_STDLIB_BODY_LOWER`
unset). Bisect with `=0` only when isolating injection-specific failures — see
`ARCHITECTURE.md` §Query / `OSTY_STDLIB_BODY_LOWER`.
