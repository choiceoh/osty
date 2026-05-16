# E0553 — `pub use` re-export visibility design

- **Status**: design draft (2026-05-16). No code change yet.
- **Scope**: workspace-level visibility check for `pub use X` where `X` is private in its origin package.
- **Authority**: `LANG_SPEC_v0.5/05-modules-and-packages.md` §5.2 (Import) + §5.3 (Visibility); `OSTY_GRAMMAR_v0.5.md` G28 (scoped import) + G30 (`pub use`).

## TL;DR

E0553 진단은 **wording 까지 다 만들어져 있지만 emit 사이트가 0** 이다 (`internal/diag/codes.go:946` 코드 정의 + `internal/selfhost/resolve_adapter.go:82` 헬퍼 + `internal/selfhost/resolve_adapter_test.go:1353` 단위 테스트만 있고 production code path 에서 호출이 한 번도 없다). 이 문서는 emit 사이트를 어디에 어떻게 박을지 + 그 선행으로 무엇이 필요한지 정리한다.

두 가지 path:
- **Path A — Minimum bypass**: 30–50 LOC. 단일-홉 `pub use pkg.X` 만 검증, scoped import / 체인 무시.
- **Path B — Proper graph**: 100–150 Osty + 50 Go LOC. 전이적(transitive) 체인 + scoped re-export 모두 커버, selfhost canonical.

Path A 를 먼저 착륙시키고 Path B 로 incremental 확장 권장.

## 1. 현재 상태 (verified 2026-05-16)

### 1.1 데이터 모델

| 위치 | 내용 |
|---|---|
| `internal/ast/ast.go` `UseDecl.IsPub bool` | parser 가 `pub use` 토큰을 보고 set (v0.5 G30). |
| `internal/resolve/scope.go:113` `Symbol.Pub bool` | top-level 심볼의 가시성. selfhost resolve 결과의 `ResolvedSymbol.Public` 에서 채워짐 (`resolve_bridge.go:835`). |
| `internal/resolve/package_graph.go:365,452,483` `IsPub` | use-edge 단위 재노출 의도. `packageGraphImportFromAST()` 가 `IsPub: use.IsPub` 로 추출. |
| `internal/resolve/workspace.go:500-518` `packageUseGraphEdge{pos, pub}` | workspace 단위 pub edge 수집. |
| `toolchain/resolve.osty:156-175` `SelfUseEdge` / `SelfPackageUses` / `SelfWorkspaceUses` | selfhost canonical use-graph. 현재 cycle detection (E0552) 에서만 사용. |
| `toolchain/resolve.osty:65-82` `SelfSymbol{public: Bool}` | selfhost 결과의 심볼 export 플래그. |

### 1.2 알고리즘

- **Pub flag 채움**: selfhost resolver 가 `SelfSymbol.public` 을 채워서 결과로 돌려준다. Go bridge 가 `Symbol.Pub` 에 복제.
- **Cycle 검출 (E0552)**: `toolchain/resolve.osty:198-241` `selfDetectImportCycles` 가 `e.pub == true` 엣지만 따라 DFS 색칠. Go bridge (`workspace.go:548-580` `reexportCycleClosers` / `pubPathExists`) 도 동일.
- **Cross-package member lookup (E0507/E0508)**: `toolchain/resolve.osty:242-285` `selfLookupPackageMember` 가 status 0/1/2 = OK/Private/Missing 반환. `pub use` 체인은 **여기서 follow 하지 않음** — 즉 `A pub use B.X` 에서 `A.X` 를 lookup 해도 `B.X` 까지 전이적으로 따라가지 않는다.
- **Scoped import (G28)**: parser 단계에서 `use std.fs::{open}` 을 `IsScoped=true, ScopedBase="std.fs", ScopedMember="open"` 로 토큰화. `internal/selfhost/import_surface_arena.go:119-124` `arenaUseDeclIsScoped` 가 플래그 추출. **하지만 resolve 단계에서 symbol-aware filtering 이 없어서** `std.fs.open` 을 "패키지 std.fs.open not found" 로 떨어뜨림 (매트릭스 진술 확인).

### 1.3 갭 (왜 E0553 이 emit 되지 않는가)

E0553 은 "`pub use` 한 source symbol 이 origin 에서 `pub` 이 아니면 reject" 인데, 다음 4 가지가 빠져 있다:

1. **scoped import 의 symbol-aware 해석**: 위 1.2 의 마지막 항목. `pub use std.fs::{open}` 이 resolver 단계에서 `open` 을 `std.fs` 의 export 멤버로 처리하지 못해서, E0553 검사를 시작할 단서 자체가 없다.
2. **재노출 체인의 transitive visibility 추적**: `A pub use B.X` 가 있을 때 `B` 의 export scope 에서 `X` 의 `Pub` 플래그를 확인하는 단계가 어디에도 없다.
3. **Workspace pub-symbol graph 의 export list**: `SelfPackageUses` 는 use **edges** 만 가지고 있다. 패키지가 export 하는 심볼 리스트는 별도. 재노출 검증은 두 정보를 같이 보아야 한다.
4. **Emit 사이트 자체**: 위 3 가지가 있어도, `pub use` 마다 검증을 도는 loop 가 어디에도 없다.

## 2. LANG_SPEC 권위 텍스트

`LANG_SPEC_v0.5/05-modules-and-packages.md`:

> **§5.2 Import** — `pub use <path>` re-exports the imported symbol from the current package; re-export cycles are E0552.
>
> **§5.3 Visibility** — Declarations are package-private by default. `pub` exports the declaration. Enum variants inherit the enum's visibility. Interface methods are visible wherever the interface is.

**합성된 규칙 (E0553 의 근거)**: `pub use X` 는 importing 패키지의 public scope 에 `X` 를 노출하겠다는 선언이다. **source symbol `X` 는 origin 패키지에서 `pub` 이어야 한다** — private 심볼을 re-export 하는 것은 가시성 모델에 모순이다.

## 3. 두 가지 구현 path

### 3.1 Path A — Minimum bypass (30–50 LOC, Go-only)

**아이디어**: 기존 `PackageExportSurface()` (`internal/resolve/import_surface.go`) + `PackageCheckImport` arena 결과를 재활용해 단일-홉 `pub use pkg.X` 만 검증한다. Selfhost 변경 없음.

**구현 사이트**: `internal/resolve/workspace.go::detectCycles` 의 cycle pass 직후, 또는 `ResolveAll` 의 per-package native resolve bridge 직후.

```go
// pseudo-code
for path, pr := range results {
    pkg := w.Packages[path]
    for _, file := range pkg.Files {
        for _, use := range file.Uses {
            if !use.IsPub { continue }
            if use.IsScoped {
                continue  // Path A 는 scoped import 무시 (G28 미해결)
            }
            target, member, ok := splitTrailingMember(use.Path)
            if !ok { continue }
            targetPkg := w.Packages[target]
            if targetPkg == nil { continue }
            if !targetPkg.HasPubSymbol(member) {
                emit(E0553, ReexportPrivateDiagnostic(target, member), use.Pos)
            }
        }
    }
}
```

**커버**:
- ✅ `pub use std.io.println` 에서 `println` 이 `std.io` 의 private 면 잡음
- ❌ Scoped: `pub use std.fs::{open}` (G28 미해결로 무시)
- ❌ Transitive: `A pub use B.X, B pub use C.X` 에서 `C.X` 가 private 이면 못 잡음 (`B` 의 export scope 에는 `X` 가 들어가 있지만 `B` 의 자체 선언이 아니라 재노출인 걸 따라가지 않음)

**위험**:
- Silent divergence: selfhost 가 export 정책을 바꾸면 Go-side 검증이 stale 해진다 (priv/POD/noalloc 3-gate 통합 때의 패턴과 동일한 함정).
- 단일-홉만 보호 — 가짜 안전감.

**가치**: emit 사이트를 production 에 박는다는 것 자체로 negative 코퍼스 회귀 잠금 가능. Path B 가 들어오기 전까지의 stop-gap 으로 의미 있다.

### 3.2 Path B — Proper graph (100–150 Osty + 50 Go LOC, selfhost canonical)

**아이디어**: `SelfPackageUses` 를 export-list 까지 들고 다니도록 확장하고, `selfDetectImportCycles` 옆에 동급의 `selfValidateReexportVisibility` 패스를 추가한다. selfhost 가 single source of truth.

**Data model 확장**:

```osty
// toolchain/resolve.osty 추가
pub struct SelfExportSymbol {
    pub name: String,
    pub kind: String,   // "fn" | "struct" | "enum" | "interface" | "type" | "variant" | "let"
    pub pub: Bool,
}

pub struct SelfPackageUses {
    pub path: String,
    pub uses: List<SelfUseEdge>,
    pub exports: List<SelfExportSymbol>,  // NEW
}
```

Go bridge 측 변경: `internal/resolve/resolve_bridge.go` 에서 native resolve 결과 후 `pkg.PkgScope.syms` 를 위 shape 로 squash 해서 SelfPackageUses 에 attach.

**Algorithm**:

```osty
// toolchain/resolve.osty 추가
pub fn selfValidateReexportVisibility(input: SelfWorkspaceUses) -> List<SelfReexportDiag> {
    let mut diags: List<SelfReexportDiag> = []
    let pkgIdx = buildPackageIndex(input)  // name -> SelfPackageUses

    for pkg in input.packages {
        for edge in pkg.uses {
            if !edge.isPub { continue }
            if edge.isScoped {
                // scoped: edge.scopedMembers 각각 검증
                for m in edge.scopedMembers {
                    validateMember(pkgIdx, edge.target, m, edge.pos, &diags)
                }
                continue
            }
            // bare `pub use a.b.c` — c 가 target package 의 export 인지
            let (basePath, member) = splitTrailing(edge.target)
            validateMember(pkgIdx, basePath, member, edge.pos, &diags)
        }
    }
    diags
}

fn validateMember(idx, target: String, member: String, pos, out diags) {
    match idx.get(target) {
        None -> {},  // E0500 territory, not E0553
        Some(targetPkg) -> {
            match findExport(targetPkg.exports, member) {
                None -> {},  // E0508 territory
                Some(sym) if !sym.pub -> diags.push(makeE0553(target, member, pos)),
                Some(_) -> {},
            }
        },
    }
}
```

**Transitive 처리 옵션**: 첫 버전은 단일-홉만. 체인은 `B pub use C.X` 가 `B` 의 export list 에 `X` 를 (pub 으로) 등록하므로, `A pub use B.X` 에서 `B.exports[X]` 를 검사할 때 자연히 OK. **단, `B` 의 export-list 가 자체 선언 vs 재노출을 구분해서 들고 있어야 함** — 재노출인 경우 origin 까지 transitive follow. 이 부분은 Phase B.2 로 미룰 수 있다.

**Scoped import (G28) 처리**: 이게 진짜 선행이다. `SelfUseEdge` 에 `scopedMembers: List<String>` 필드 추가 + parser 에서 채움 + resolve 단계 import-surface filter 가 `scopedMembers` 만 노출하도록 변경. 이 작업이 E0553 보다 무거운 면이 있다 — 별도 PR 시리즈 가치.

**커버**:
- ✅ bare `pub use pkg.X`
- ✅ scoped `pub use pkg::{X, Y}` (G28 작업 후)
- ✅ transitive chain (export-list 가 재노출까지 포함하면)
- ✅ cycle-detection 과 동일 layer 라 silent divergence 위험 ↓

**위험**:
- `SelfPackageUses.exports` 채우는 비용 — 모든 패키지의 top-level 심볼 squash 가 native resolve 마다 추가. small (수십 심볼) 이지만 측정 필요.
- Generated.go regen pipeline — `toolchain/resolve.osty` 의 새 struct 가 `internal/selfhost/generated.go` (frozen seed) 와 sync 안 됨. CLAUDE.md 가 명시한대로 generated.go 는 동결됐고 재생성 경로가 없다 — 이건 셀프호스팅 LLVM 빌드 경로로만 반영 가능. **이 점이 Path B 의 가장 큰 lock-in**: bootstrap go-side 가 신 struct 를 보지 못한다. Path A 가 이 제약을 우회.

## 4. 의존 / 선행 작업

| 작업 | 선행 |
|---|---|
| Path A (단일-홉 검증) | 없음. 바로 가능 |
| Scoped import G28 resolution | `SelfUseEdge.scopedMembers` 필드 + import surface filter — 별도 design |
| Path B (graph 확장) | Scoped G28 + generated.go regen path 부활 (또는 그냥 osty native checker 만으로 sync) |
| Transitive chain (export-list 이 재노출 포함) | Path B 의 B.2 sub-phase |

## 5. 권장 진행 순서

1. **Path A 착륙** (~30-50 LOC, 1 PR). 단일-홉 `pub use pkg.X` 만 검증. Scoped 는 silently skip. negative 코퍼스 fixture 1 개 추가.
2. **Scoped import G28 별도 design 문서** (이 문서 범위 밖). resolve 단계 import surface 가 scopedMembers 만 노출하도록 만드는 작업.
3. **Path B sub-phase B.1** — `SelfPackageUses.exports` 추가 + selfhost validator + Path A 의 Go 측 검증을 selfhost 호출로 교체.
4. **Path B sub-phase B.2** — transitive chain 처리 (export-list 가 origin 까지 추적).

## 6. Open questions

- `LANG_SPEC_v0.5` 가 transitive `pub use` 의 정확한 시맨틱스 (chain 깊이 제한? cycle 처리?) 를 명시하지 않는다. §5.2 의 cycle = E0552 외에 추가 규칙 필요할 수 있음 — SPEC_GAPS 진입 candidate.
- `pub use` 가 enum variant 만 노출 (`pub use Color::Red`) 하는 케이스의 의미 — variant 의 가시성은 enum 상속 (§5.3) 인데, variant 만 별도 노출 가능한지 spec 결정 필요.

## 7. 참조

- `internal/diag/codes.go:942-946`
- `internal/selfhost/resolve_adapter.go:78-92`
- `internal/selfhost/resolve_adapter_test.go:1353-1364`
- `toolchain/resolve.osty:156-175` (SelfUseEdge / SelfPackageUses / SelfWorkspaceUses)
- `toolchain/resolve.osty:198-241` (selfDetectImportCycles)
- `toolchain/resolve.osty:242-285` (selfLookupPackageMember)
- `internal/resolve/workspace.go:500-580` (workspace pub-edge collection + cycle closure)
- `internal/resolve/package_graph.go:365,452-487` (graph edge IsPub)
- `internal/resolve/import_surface.go:23-154` (PackageImportSurfaces + scoped use ref)
- `internal/selfhost/import_surface_arena.go:119-124` (arenaUseDeclIsScoped)
- `LANG_SPEC_v0.5/05-modules-and-packages.md` §5.2 / §5.3
- `OSTY_GRAMMAR_v0.5.md` G28 / G30
- `SELFHOST_PORT_MATRIX.md` Resolver 우선순위 #5
