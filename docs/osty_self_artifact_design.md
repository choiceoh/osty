# `osty-self` artifact cache 설계

> **상태**: 제안 + 첫 단계 구현 (이 PR의 `internal/toolchain/selfhostcache`).
> **연관**: `docs/osty_self_bootstrap_design.md` (stage0 — emergency-only fallback).
> **소유**: backend / toolchain / CI.

## 1. 문제 정의

PR #1405 가 in-process Go MIR emitter (`internal/llvmgen`) 를 제거한 뒤,
모든 production MIR → LLVM 변환은 **`osty-self lir-proto-lower`**
서브프로세스를 통해야 한다. `osty-self` 는 `toolchain/*.osty` 를
`osty build` 로 컴파일한 산출물이고, 그 `osty build` 자체가 또
`osty-self` 를 필요로 한다 — 닭-달걀 부트스트랩 의존.

기존 옵션 두 가지:
- **stage0 fallback** (`docs/osty_self_bootstrap_design.md` P0~P15) — Go
  쪽에 minimal MIR→LLVM emitter 를 두고 `osty-self` 부재 시 사용. 본격
  toolchain 컴파일까진 cover 하지 못 함. 사실상 `internal/llvmgen` 의
  소형 재창조에 가까워서 #1405 의 정신에 어긋남.
- **외부 사전-빌드 osty-self** — 어딘가에서 빌드된 바이너리를 가져와
  쓰는 방식. 영구 가치가 가장 높은 접근.

본 문서는 두 번째 옵션을 단계적으로 구현하는 설계다.

## 2. 큰 그림

```
                 ┌─────────────────────────────────────────────┐
                 │  ResolveBinary(projectRoot)                 │
                 │                                             │
   ┌────────┐    │  1. $OSTY_SELF_BIN env override             │
   │ caller │───▶│  2. toolchain/.osty/out/{debug,release}/   │
   └────────┘    │     llvm/osty-self  (in-tree dev build)     │
                 │  3. .osty/cache/self-host/<sha-triple>/    │
                 │     osty-self  ← content-addressed cache    │
                 │  4. (future) network fetch from release     │
                 │  5. ErrNotCached → stage0 emergency or fail │
                 └─────────────────────────────────────────────┘
```

캐시 entry 의 키는 **`(toolchain SHA-256, host triple)`** 의 페어.
`toolchain/*.osty` 가 변경되면 SHA 가 바뀌어 자동으로 무효화되고,
다른 호스트에서 빌드된 바이너리는 triple 부분에서 매치되지 않는다.

## 3. 단계별 로드맵

| Phase | 범위 | 측정 |
|---|---|---|
| **A1** | `internal/toolchain/selfhostcache` 패키지 — `Key`, `ComputeKey`, `ResolveBinary`, `Install`, `CachePath`. 디스크에 이미 있는 캐시 entry 만 lookup. 네트워크 0. | 13 단위 테스트 통과 — **구현 완료** |
| **A2** | `cmd/osty-native-lirproto` 가 `selfhostcache.ResolveBinary` 사용. `LocateProjectRoot` 헬퍼 추가. ErrNotCached → "osty-self not found" 메시지로 변환해서 IsOstySelfMissing 호환 유지. | 기존 lirproto 테스트 통과 + 캐시 lookup 통합 — **구현 완료** |
| **A3** | `osty install-self` 서브커맨드 — toolchain 빌드 + 캐시 적재 한 번에. `just bootstrap` 레시피가 build-all + install-self 호출. | TestInstallSelfUsageMessage / TestBuildOstySelf — **구현 완료** |
| **A4** | 네트워크 fetcher — `<base>/<sha>-<triple>.json` manifest + binary 다운로드, SHA-256 검증 필수. `OSTY_SELF_REGISTRY_URL` / `OSTY_SELF_REGISTRY_OFFLINE` env var 게이트. `ResolveBinaryWithFetch` 가 캐시 miss 시 호출. | 17 tests (httptest server 기반) — **구현 완료** |
| **A5** | `cmd/osty-native-lirproto` 가 `ResolveBinaryWithFetch` + `EnvFetcher()` 호출. `osty manifest-self` 서브커맨드 — 빌드된 binary → manifest JSON. `.github/workflows/build-osty-self.yml` 6개 triple matrix scaffold (manual dispatch). | manifest round-trip 4 tests + 기존 lirproto/selfhostcache 회귀 — **구현 완료** |
| **A6** | Manifest signing — ed25519 detached sig (`<key>.json.sig`). `OSTY_SELF_TRUSTED_KEY` env var로 verify. `osty sign-self` / `osty sign-self genkey` 서브커맨드. CI workflow 가 `OSTY_SELF_SIGNING_KEY` secret 있을 때만 서명. | 14 unit tests (signing.go) + 4 CLI round-trip tests — **구현 완료** |
| **A7** (이 PR) | `verify-self-rebuild --reuse-stage1` 가 selfhostcache 인식. `osty cache-self [--check\|--key\|--triple]` 서브커맨드 — 컨텐트-어드레스드 캐시 path/key 조회. cache hit 시 stage1 빌드 skip; fresh 빌드는 자동 promote. `--no-selfhostcache` 로 legacy mtime 캐시 fallback. | 7 cache-self CLI 단위 테스트 — **구현 완료** |

## 4. Key 구성

```go
type Key struct {
    ToolchainSHA string  // hex SHA-256
    Triple       string  // "linux-amd64", "darwin-arm64", ...
}
```

### 4.1 ToolchainSHA

다음 알고리즘:

1. `projectRoot/toolchain` 아래 모든 `*.osty` 파일을 walk.
2. `toolchain/.osty/` 아래는 **제외** (생성 산출물).
3. repo-relative path 로 정렬.
4. 각 파일에 대해 hash 에 다음을 입력:
   ```
   path:<rel-path-with-slashes>\n
   size:<byte-len>\n
   <file-bytes>
   <0x00 separator>
   ```
5. SHA-256 의 hex.

### 4.2 Triple

`runtime.GOOS + "-" + runtime.GOARCH`. 예: `linux-amd64`, `darwin-arm64`.

LLVM target triple 과는 다른 layer. cross-compile 빌드는 별도 정책 (현재
범위 외).

## 5. Lookup 우선순위 근거

1. **`OSTY_SELF_BIN` 환경변수** — 디버깅 / 비표준 위치 / 테스트용
   override 가 항상 이김.
2. **In-tree build path** — 활성 개발 워크트리에서 로컬 빌드한 바이너리는
   캐시보다 항상 신선. 캐시는 그 빌드가 끝난 후 별도로 promote 된다 (A2).
3. **Content-addressed cache** — 다른 워크트리 / 다른 머신에서 promote
   된 바이너리. SHA 가 다르면 stale 로 자동 거부.
4. **Network fetch** (A4+) — 캐시에도 없으면 release host 에서 다운로드.
5. **Stage0 emergency fallback** — 네트워크 disabled / offline 환경에서만.

## 6. 보안 / 무결성

A4/A6 단계에서 도입한 정책:
- 다운로드는 **HTTPS** 만 (production). http:// 는 local registry 테스트용.
- 응답 본문의 SHA-256 가 manifest 의 값과 일치하는지 검증 (A4).
- `OSTY_SELF_TRUSTED_KEY` 가 설정된 경우, `<key>.json.sig` 의 ed25519
  서명이 manifest 본문에 대해 verify 되어야 함 (A6). 서명 누락 시
  `ErrSignatureMissing` — 검증 disabled 상태로 silent downgrade 하지 않음.
- 검증 실패 시 캐시에 **저장하지 않고** 에러로 fall through.

A1~A3 (로컬-only) 까지는 보안 모델이 단순하다 — 사용자가 이미 제어하는
파일시스템.

## 7. Stage0 의 미래

`docs/osty_self_bootstrap_design.md` P0~P15 의 stage0 emitter 는 본 artifact
시스템이 작동하기 시작하는 시점부터 **emergency-only** 로 동결된다:

- 네트워크 차단 + 캐시 비어 있음 + in-tree 빌드 없음 일 때만 활성.
- `OSTY_STAGE0_FALLBACK=1` 환경변수가 여전히 게이트.
- 새 패턴 추가는 중단 (P16+ 미진행).

stage0 의 인프라 (P0 `IsOstySelfMissing`, P4 디스패처 wiring, P5 real-MIR
test infra) 는 본 artifact 시스템에서도 그대로 가치 보존된다 — fallback
체인의 마지막 단계로 유지되며, real-MIR 회귀 테스트는 stage0 든 osty-self
든 어느 쪽이 emit 했는지 무관하게 회귀 게이트로 작동.

## 8. 결정 필요 항목

1. **CI 빌드 호스트** — GitHub Actions 의 어떤 runner 가 어떤 triple 을
   담당? linux-amd64, linux-arm64 (self-hosted?), darwin-amd64,
   darwin-arm64, windows-amd64, windows-arm64.
2. **Release 정책** — main push 마다? 태그 push 시? 분기 빌드는?
3. **Storage 비용** — GitHub Release artifact 무료 한도 vs S3 / R2 호스팅.
4. **Toolchain 변경 빈도** — 너무 잦으면 release 폭주. 캐시 키 안정성을 위해
   toolchain 외 변경은 키에 들어가지 않게 4.1 알고리즘 유지.
5. **Reproducibility** — 같은 toolchain SHA 에서 host 가 같은 OS/CPU 라면
   바이너리가 같아야 함. LLVM 의 비결정성 (예: -fdebug-prefix-map) 통제 필요.

## Appendix A. A1 단계 (이 PR) 의 효과

- **사용자**: 아직 실제로 동작하지 않음 (`cmd/osty-native-lirproto` 는
  여전히 옛날 path 만 본다 — A2 에서 wiring).
- **개발자**: `selfhostcache.ResolveBinary` 가 호출 가능. `Install` 로
  바이너리 promote 가능. 단위 테스트 13개로 인프라 검증.
- **회귀 위험**: 0 — production path 는 변경되지 않음.

## Appendix B. A2 wiring 미리보기

```go
// cmd/osty-native-lirproto/main.go
func resolveOstySelfBin() (string, error) {
    root, err := projectRootHint()
    if err != nil {
        return "", err
    }
    bin, _, err := selfhostcache.ResolveBinary(root)
    if errors.Is(err, selfhostcache.ErrNotCached) {
        return "", err
    }
    return bin, err
}
```

`bin` path 의 stat / exec.LookPath 검사는 cache 패키지 내부에서 처리되므로
호출부는 단순화된다.
