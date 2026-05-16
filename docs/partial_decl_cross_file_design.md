# Partial struct/enum cross-file stitching design

- **Status**: design draft (2026-05-16). No code change yet.
- **Scope**: workspace-level enforcement of R19 (partial declaration invariants) across multiple files within one package.
- **Authority**: `LANG_SPEC_v0.5/03-declarations.md` §3.4; `OSTY_GRAMMAR_v0.5.md` R19.

## TL;DR

현재 selfhost 의 `srHandlePartialType` (`toolchain/resolve.osty`) 가 R19 의 4 invariants 를 정상 검증한다. 그런데 그 **검증이 cross-file 에서 어떻게 작동하는지의 모델이 명시적이지 않다** — native_adapter 가 패키지의 여러 파일을 `[]api.PackageResolveFile{...}` 리스트로 selfhost 에 넘기고, selfhost 내부의 `partials` accumulator 가 그 리스트를 처리하는 동안 cross-file 상태를 공유하기 때문에 우연히 동작한다. 만약 누가 selfhost 를 file-level resolution 으로 refactor 하면 R19 (iv) (cross-file method dup) 가 **silently false negative** 가 된다.

해결: selfhost 가 file-level resolution 으로 가더라도 R19 가 유지되도록 **명시적인 post-walk cross-file pass 를 추가**. `srValidatePartialsCrossFile(result.partials)` 같은 함수가 single source of truth. Go side `merge.go::checkPartialMethodNames` 는 1c.5 deletion chain 에서 제거됨 (현재 fallback helper).

## 1. 현재 상태 (verified 2026-05-16)

### 1.1 selfhost 알고리즘 — `srHandlePartialType`

위치: `toolchain/resolve.osty:1494-1655` (~162 LOC, agent 조사 기준 line refs).

데이터 구조 `SelfPartialDecl` (`toolchain/resolve.osty:31-46`):

```osty
struct SelfPartialDecl {
    firstNode, firstStart, firstEnd,  // 첫 partial 의 위치 (canonical)
    firstPub: Bool,                   // R19(i)
    firstGenerics: List<String>,      // R19(ii) — 타입 파라미터 이름 리스트
    methodNames, methodStarts, methodEnds, methodNodes,  // R19(iv) — 누적된 메서드들
    hasFields: Bool,                  // R19(iii) — fields 가 declared 됐는지
    firstFieldsStart, firstFieldsEnd, // fields 가 있는 첫 위치 (있다면)
}
```

R19 4 invariants 검증 사이트 (`toolchain/resolve.osty:1574-1605`):
1. **(i) Pub 일치**: `isPub != prev.firstPub` → E0501 + "matching `pub` modifiers" hint.
2. **(ii) Generics 일치**: `!srGenericsEqual(prev.firstGenerics, newGenerics)` → E0501 + "same type-parameter list".
3. **(iii) Fields-in-one**: `prev.hasFields && newHasFields` → E0501 + "exactly one partial declaration".
4. **(iv) Method 이름 유일**: `srStringListIndex(methodNames, member.text) >= 0` → E0501 + "methods must have unique names".

### 1.2 native_adapter 의 multi-file 입력

`internal/resolve/native_adapter.go:251-293` `nativeResolveInput`:

```go
input := api.PackageResolveInput{
    Files: make([]api.PackageResolveFile, 0, len(pkg.Files)),
    ...
}
base := 0
for _, pf := range pkg.Files {
    src, _ := nativeResolveSourceForFile(pf)
    input.Files = append(input.Files, api.PackageResolveFile{
        Source:       append([]byte(nil), src...),  // per-file bytes
        Base:         base,                          // virtual offset
        Name:         filepath.Base(pf.Path),
        Path:         pf.Path,
        SourceFileID: ...,
    })
    base += len(src) + 1   // monotonic virtual offset
}
```

**중요한 정정** (agent B 보고서의 부정확 한 부분): 파일들은 **단일 바이트 버퍼로 concat 되지 않는다**. 각각이 `PackageResolveFile` 엔트리로 separate `Source []byte` 를 가지고 리스트에 들어간다. `Base` 는 diagnostic 위치가 충돌하지 않도록 부여한 **virtual offset** 일 뿐 (selfhost 가 위치를 globalize 해 위치 충돌을 피하기 위함).

### 1.3 cross-file 가 현재 우연히 작동하는 이유

selfhost `ResolvePackageStructured` 가 `input.Files` 리스트를 처리할 때, **`partials` accumulator 를 file 간에 reset 하지 않는다** (단일 `ResolvePackageStructured` 호출 내에서 공유). 그래서:

- 파일 1 의 `struct Foo { pub fn log(...) }` 가 `partials[Foo].methodNames = ["log"]` 를 만든다.
- 파일 2 의 `struct Foo { pub fn log(...) }` 가 `srHandlePartialType` 에 들어올 때, `srStringListIndex(methodNames, "log") >= 0` 가 true 라서 E0501 발화.

즉 **packageResolveStructured 의 한 invocation 이 패키지 전체의 partial 상태를 들고 있다**. native_adapter 가 다행히 패키지 단위로 호출하므로 이 invariant 가 유지된다.

### 1.4 이게 fragile 한 이유

**우연한 invariant**. 다음 refactor 가 들어오면 R19(iv) 가 silently 깨진다:
- `ResolvePackageStructured` 가 file-level 로 쪼개져 호출됨 (예: incremental LSP, 캐시된 per-file resolve)
- selfhost 가 partial state 를 per-file 로 reset 하도록 변경
- workspace 가 file 단위 cache 를 도입해 같은 파일을 따로 resolve

매트릭스 (`SELFHOST_PORT_MATRIX.md` Resolver 우선순위 #2) 도 같은 우려를 명시:
> "현재는 single-file (native_adapter 가 `ResolvePackageStructured` 로 다중 파일을 synthetic 단일 네임스페이스로 합쳐 통과). True cross-file stitching 은 workspace 모델 등장 이후."

매트릭스 표현의 "synthetic 단일 네임스페이스" 는 **selfhost 내부의 in-memory state sharing** 을 가리킨다 (Go-side source concat 이 아님 — 위 1.2 정정).

### 1.5 Go-side fallback (현재 dead code 후보)

`internal/resolve/merge.go::checkPartialMethodNames` 가 historic resolver 잔여로 존재. `internal/resolve/resolve.go` body-walk 가 제거된 이후엔 호출 site 가 없을 가능성 — Resolver 매트릭스 1c.5 deletion chain 에서 `merge.go` 삭제 대상으로 명시. **이 문서가 다루는 cross-file stitching 작업과 별개로 진행 가능한 cleanup.**

## 2. LANG_SPEC 권위 텍스트

`LANG_SPEC_v0.5/03-declarations.md` §3.4:

> **Partial declarations** — A struct or enum may be declared across multiple files **within the same package**. Each subsequent partial must agree on visibility (`pub`) and on the type-parameter list. Fields (or variants, for enums) must be declared in exactly one of the partials; the others contribute only methods. Methods declared in any partial share the type's namespace, and duplicate method names across partials are an error.

`OSTY_GRAMMAR_v0.5.md` R19 — 위 4 invariants 의 정식 grammar 결정 로그.

## 3. cross-file 함정 시나리오

각 invariant 의 cross-file 사례. 현재 모두 **우연히** 정상 캐치되지만 (1.3), file-level resolve 가 들어오면 (iv) 가 먼저 깨진다.

### 3.1 R19(i) Pub mismatch
```osty
// file1.osty
pub struct Config { host: String }

// file2.osty
struct Config { port: Int }       // missing `pub` — E0501
```
- 현재: ✅ 캐치. `firstPub != newIsPub`.
- File-level reset 후: ✗ false negative (`firstPub` 가 file2 의 첫 등장이 됨).

### 3.2 R19(ii) Generics mismatch
```osty
// file1.osty
struct Box<T> { value: T }

// file2.osty
struct Box<U> {                  // 타입 파라미터 이름 불일치 — E0501
    pub fn unwrap(self) -> U { self.value }
}
```
- 현재: ✅ 캐치. `srGenericsEqual([T], [U]) == false`.
- File-level reset 후: ✗ false negative.

### 3.3 R19(iii) Fields-in-one violation
```osty
// file1.osty
struct User { id: Int }

// file2.osty
struct User { name: String }      // 둘 다 fields — E0501
```
- 현재: ✅ 캐치. `prev.hasFields && newHasFields`.
- File-level reset 후: ✗ false negative.

### 3.4 R19(iv) Method duplicate across files
```osty
// file1.osty
struct Logger {
    pub fn log(self, msg: String) { ... }
}

// file2.osty
struct Logger {
    pub fn log(self, level: Int, msg: String) { ... }   // 같은 이름 — E0501
}
```
- 현재: ✅ 캐치. `methodNames` 가 누적된다.
- File-level reset 후: ✗ **가장 잡기 어려움**. 첫 file 의 `log` 는 commit 됐고, 두 번째 file 시작 시 `methodNames=[]` 으로 reset 되어 dup detection 불가능.

## 4. 설계 옵션

### 4.1 Option A — Workspace-pass (Go side re-validation)

`internal/resolve/workspace.go` 에 `StitchPartialDeclarations(workspace, packageResults) []Diagnostic` 추가. Per-package resolve 후 별도 패스가 모든 `result.partials` 를 모아 cross-file R19 재검증.

**Pros**:
- Selfhost 변경 없음 (frozen generated.go 와의 sync 부담 0).
- 명시적 phase boundary.

**Cons**:
- R19 알고리즘이 selfhost + Go 양쪽 중복 — silent divergence 위험 (priv/POD 3-gate 때 학습한 패턴).
- ~300 LOC Go 추가.

**Osty-friendliness**: ⭐ (single source of truth 위반).

### 4.2 Option B — Selfhost post-walk pass (recommended)

`toolchain/resolve.osty` 의 file walk 종료 후 `srValidatePartialsCrossFile(partials)` 호출. 현재 `partials` accumulator 가 이미 모든 cross-file 데이터를 가지고 있으므로 file walk 가 file-level 로 쪼개져도 이 pass 가 invariant 를 보존한다.

```osty
// toolchain/resolve.osty 추가 (대략)
fn srValidatePartialsCrossFile(partials: List<SelfPartialDecl>) {
    for p in partials {
        // 현재 srHandlePartialType 의 누적 검증을 post-walk 에서 한 번 더
        // 명시적으로 — 특히 (iv) method dup 가 file 경계 넘는지 확인.
        // 추가로 SelfPartialDecl 의 methodFiles: List<String> 필드 사용해서
        // 같은 method 가 다른 file 에 등장하는지 확인 (현재 srHandlePartialType
        // 이 in-walk 으로 잡지만, post-walk 명시화로 file-level resolve 미래
        // refactor 에 robust).
    }
}
```

**보강 데이터**: `SelfPartialDecl` 에 `methodFiles: List<String>` 추가 (각 method 가 어느 file 에서 왔는지). 이게 있으면 같은 method 가 다른 file 에 있는지 명시적으로 알 수 있다 (현재는 `methodNames` 만 들고 있어서 같은 file 인지 다른 file 인지 모름).

**Pros**:
- Selfhost canonical, single source of truth.
- File-level resolve refactor 에 robust.
- Algorithm 중복 없음.

**Cons**:
- Generated.go (frozen seed) 가 새 struct field 를 보지 못함 → bootstrap go-side check 가 stale. 하지만 1c.5 진행으로 production 경로는 osty native checker 만 사용하므로 영향 minimal — frozen seed 는 tests 와 fallback 에만 쓰임.
- `SelfPartialDecl` 시그너처 변경이라 selfhost 측 회귀 가능성.

**Osty-friendliness**: ⭐⭐⭐.

### 4.3 Option C — Go orchestration of per-file resolve

Go side 가 file 들을 fields-bearing vs methods-only 로 batch 해서 순서대로 resolve, partial state 를 Go side 가 carry.

**Pros**: 현재 architecture 변경 최소.
**Cons**: orchestration logic 이 Go 측에 — selfhost-unfriendly. R19 자체는 selfhost 가 하더라도 batch 정책은 Go 가 결정. ⭐.

## 5. 권장 path

**Option B**. 구체 phases:

### Phase B.1 — methodFiles 추가 (~30 LOC Osty)
`SelfPartialDecl` 에 `methodFiles: List<String>` 필드 추가. `srHandlePartialType` 에서 `methodNames` push 할 때 같이 push. 검증 사이트는 그대로 `methodNames` indexOf 사용. 후방 호환.

### Phase B.2 — post-walk 검증 함수 추가 (~50 LOC Osty)
`srValidatePartialsCrossFile(partials) -> List<Diag>` 추가. 현재 walk-time 검증과 동일한 결과를 post-walk 으로 재확인 — 통과해야 함 (이중 검증). 통과 후 walk-time 검증을 weakening (개별 partial 등록만 하고 invariant 는 post-walk 에서 잡도록) 도 옵션. 단계적 migration.

### Phase B.3 — file-level resolve 시 안전성 회귀 fixture (~3 negative 코퍼스)
`testdata/spec/negative/reject.osty` 에 R19(iv) cross-file dup 케이스 명시. 향후 누가 walk-time 검증을 떼더라도 post-walk 가 이걸 잡아야 함. Phase B.2 의 lock.

### Phase B.4 — `internal/resolve/merge.go::checkPartialMethodNames` 제거 (~30 LOC Go)
1c.5 deletion chain 의 일부. 별도 PR 로 가능 (이 design 과 독립).

## 6. 의존 / 선행

- **`SelfPartialDecl` shape 변경**: generated.go (frozen seed) 와 sync 깨질 위험. 영향 범위 확인 필요 — 매트릭스가 generated.go 를 frozen 으로 못박았고 production native checker 는 별도 빌드. 단, bootstrap Go-side adapter (`internal/selfhost/resolve_adapter.go`) 가 SelfPartialDecl 을 직접 deserialize 하면 영향 — 확인.
- **다른 partial 검증 추가 가능성**: 매트릭스가 cross-file partial method dup 만 명시했지만, future spec 가 추가 invariant 를 정의할 수 있음. 이 design 의 phase 분할이 incremental 확장 가능.

## 7. Open questions

- `srHandlePartialType` 의 walk-time 검증과 새 post-walk 검증을 **둘 다 유지하면** 진단이 중복 발화할 위험. Phase B.2 에서 walk-time 검증의 일부를 dedup 시키거나 walk-time 검증을 약화시키는 정책 결정 필요.
- partial 가 generic 인 경우 (`struct Box<T>` partial), `firstGenerics` 비교의 정확한 시맨틱스 — 이름이 다른 T vs U 는 reject, 그러면 type parameter 가 0 개일 때와 N 개일 때의 corner 케이스는? Spec 의 §3.4 가 "agree on the type-parameter list" 까지만 말함 — list 의 길이 vs 이름 둘 다 인지 spec 확인 필요.

## 8. 참조

- `toolchain/resolve.osty:25-46` (SelfPartialDecl), `:1494-1655` (srHandlePartialType)
- `internal/resolve/native_adapter.go:251-293` (nativeResolveInput)
- `internal/selfhost/api/package.go` (PackageResolveFile = PackageCheckFile alias)
- `internal/resolve/merge.go` (Go-side checkPartialMethodNames fallback, 1c.5 deletion 대상)
- `LANG_SPEC_v0.5/03-declarations.md` §3.4
- `OSTY_GRAMMAR_v0.5.md` R19 decision log
- `SELFHOST_PORT_MATRIX.md` Resolver 우선순위 #2
