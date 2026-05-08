# B1 — Real toolchain build attempt

> **상태**: **ARCHIVED — superseded by `docs/osty_self_b2_1_audit.md`**.
> 이 문서는 P0–P15 시점의 측정 + UX 개선이다. P20 머지 + B2 4 PR
> 변환 이후의 정확한 stage0 coverage 는 11.3% (`b2_1_audit.md` §3)
> 임이 측정으로 확정됐고, 본 문서의 §3 "다음 작업" 옵션 (B2/B3/B4)
> 는 옵션 (C) registry 결정 (`osty_self_artifact_design.md` §8
> RESOLVED) 으로 흡수됐다. 본문은 측정 baseline 으로 보존하되 새
> 작업의 baseline 으로 인용하지 말 것 — 대신 `b2_1_audit.md` 를
> 참조.
>
> **연관**: `docs/osty_self_artifact_design.md` (A1–A9 — 인프라),
> `docs/osty_self_b2_1_audit.md` (active audit).
> **소유**: backend / toolchain.

## 1. 목표

A1–A9 가 만든 artifact cache 인프라를 실제 `toolchain/*.osty` (157 파일,
~50K 라인) 빌드에 적용해 봐서:

- 부트스트랩 흐름이 실제로 작동하는지
- 어떤 단계에서 막히는지
- A1–A9 가 풀어야 할 문제와 다른, 본질적 갭이 어디인지

## 2. 실측 결과

### 2.1 빈 캐시 + 무 osty-self 상태

신선한 클론 시뮬레이션:

```sh
rm -rf toolchain/.osty/out
.bin/osty install-self            # OSTY_STAGE0_FALLBACK 미설정
```

결과 (6:18 후):

```
Profile: debug  Target: host  Features: none
osty build: llvm backend: code generation is not implemented yet
  detail: LLVM000 unsupported-source: native LIR Proto subprocess
          declined MIR coverage for route mir-direct;
          hint: route through the Osty-owned MIR/LIR Proto backend
          path; backend-route: mir-direct
  artifact: /home/user/osty/toolchain/.osty/out/debug/llvm/main.ll
  runtime: /home/user/osty/toolchain/.osty/out/debug/llvm/runtime
osty install-self: build: osty build toolchain/: exit status 1
```

**핵심 관찰**:

1. **Front-end 는 통과**. 157 파일 + 6052-라인 `lir_proto.osty` 가 parse +
   resolve + check + lint 까지 클린 (72 packages, checker cache 생성됨).
2. **MIR → LLVM IR 단계에서 실패**. `osty-native-lirproto` 가 osty-self 를
   찾지 못해 `declined: true` 응답. 백엔드 디스패처는 stage0 fallback
   대상 (`IsOstySelfMissing`) 으로 인식했지만, `OSTY_STAGE0_FALLBACK` 미설정
   상태라 그냥 declines 처리.
3. **시간 비용**: front-end 만 6분. cache 인프라가 없으면 매 빌드마다 이
   비용 발생.

### 2.2 Stage0 fallback 활성화

```sh
OSTY_STAGE0_FALLBACK=1 .bin/osty install-self
```

실측 결과: **§2.1 과 동일한 LLVM000 사용자-가시 진단** (5:18 실행 후).
stage0 이 attempt 했더라도 lir_proto.osty 의 MIR 패턴 (158 개 `pub fn`)
중 stage0 P0–P15 가 cover 하지 않는 패턴에서 declines. P0–P15 의 커버리지는
trivial main / 산술 / if-else / while / for-in-range / struct field / list /
string concat / aggregate constructor 까지로, toolchain 의 closure /
interface dispatch / 복잡한 generics 는 미지원.

→ **fresh clone 에서 OSTY_STAGE0_FALLBACK=1 만으로 osty-self 부트스트랩이
완성되지 않는다**. P16+ (closure / interface / monomorph) 가 필요하나
A6 의 결정 (stage0 동결, emergency-only) 이 새 패턴 추가를 막아둠.

## 3. 결론: 부트스트랩 워크플로

A1–A9 인프라가 풀어야 할 케이스:

- **재빌드 / 워크트리 간 공유** → A1–A8 모두 작동. 캐시 hit 시 재빌드 skip.
- **Fresh clone (cache empty)** → 다음 셋 중 하나 필요:
  1. **`OSTY_SELF_REGISTRY_URL`** 설정 (CI 가 발행한 pre-built osty-self
     다운로드) — A4 인프라, A5 CI workflow 가 활성화되면 작동.
  2. **`OSTY_SELF_BIN`** 으로 기존 binary 직접 가리키기 — backup / 디버그.
  3. **`OSTY_STAGE0_FALLBACK=1`** — 현재 P0–P15 커버리지로는 toolchain
     전체를 빌드 못 함. 향후 P16+ 가 추가되거나, toolchain 이 단순화되어야
     완성.

## 4. UX 개선 (이 PR)

`osty install-self` 가 빌드 실패 시 위 워크플로 옵션 셋을 명시적으로 안내:

```
osty install-self: build: osty build toolchain/: exit status 1

hint: bootstrap from a fresh clone needs an osty-self source. Pick one:
  - point OSTY_SELF_REGISTRY_URL at a registry serving a pre-built osty-self,
  - point OSTY_SELF_BIN at an existing osty-self binary, or
  - retry with OSTY_STAGE0_FALLBACK=1 to use the emergency bootstrap emitter
    (subset coverage; see docs/osty_self_bootstrap_design.md).
```

`OSTY_STAGE0_FALLBACK` 가 이미 set 되어 있으면 이 hint 는 출력하지 않음
(redundant guidance 회피).

## 5. 다음 작업

A 시리즈가 만든 인프라는 cache hit / 워크트리 공유 / CI publish 시나리오
모두 커버한다. fresh-clone 부트스트랩의 마지막 1 마일은 다음 중 하나로
풀어야 한다:

- **B2** — `toolchain/lir_proto.osty` 단순화로 stage0 P0–P15 안에 들어오게.
- **B3** — stage0 retire 결정 (CI 가 매 main push 마다 publish 하면 fresh
  clone 도 항상 registry hit). A8 의 결정 항목 (CI 호스트 정책 / release
  cadence) 가 풀려야 시행 가능.
- **B4** — `internal/llvmabi` cleanup (제거된 Go MIR emitter 잔재 정리).
  부트스트랩과는 직교하나 코드 품질 개선.

이 PR 은 A 시리즈 인프라가 **실제로 어디에 닿는지** 와 **갭이 어디 남았는지**
를 명문화하고, fresh-clone 사용자가 올바른 경로를 빠르게 찾도록 install-self
의 진단 메시지를 개선한다.
