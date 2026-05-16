# LLVM self-host plan — PR1c blockers report (option a/b/c attempt)

> **상태**: 측정 (이 PR).
> **선행**: [docs/llvm-selfhost-plan.md](llvm-selfhost-plan.md), [docs/llvm-selfhost-plan-pr1b-readiness.md](llvm-selfhost-plan-pr1b-readiness.md).
> **소유**: backend / toolchain.

## 목적

PR1b ([#1821](https://github.com/choiceoh/osty/pull/1821)) 머지 후 PR1c (진짜 stdin 처리) 의 3개 옵션 attempt 결과 기록. 모두 단일 세션 작업 무리로 판명 — 다음 세션 시작점 후속 분기 정리.

## Attempt 1 — Option (a): stage0 emit 에 readLine intrinsic cover 추가

**전제**: `mir.IntrinsicKind` enum 에 `IntrinsicReadLine` 케이스 부재 (`IntrinsicPrint*` / `IntrinsicEprint*` / `IntrinsicAbort` / `IntrinsicChan*` / `IntrinsicSpawn` / `IntrinsicTaskGroup` / ... 만). `internal/backend/stage0/emit.go:1040` 의 `IsExternal` 거부 path 통과 시 즉시 unsupported.

**시도 경로 분석**:
1. `internal/mir/mir.go` 에 `IntrinsicReadLine` enum 추가 — MIR ABI 변경 (모든 MIR consumer 영향)
2. `toolchain/mir_generator.osty` 에 readLine call dispatch 를 IntrinsicReadLine 로 lowering — `mirRtIOReadAllSymbol` 같은 dead helper 옆 wire
3. `toolchain/lir_proto.osty` 에 IntrinsicReadLine emit — runtime call `osty_rt_io_read_line`
4. `internal/backend/stage0/emit.go` 의 1025 라인 `IntrinsicPrint*` 분기 옆 IntrinsicReadLine cover — `declareRuntimePrototype` + `call ptr @osty_rt_io_read_line()`
5. `internal/stdlib/modules/io.osty:517` 의 `pub fn readLine()` 가 새 intrinsic 으로 매핑되도록 prelude 수정

**복잡도**: 5 파일 동시 + MIR ABI 변경 + stage0 의 `classifyXxxCallLine` family (line 2418 ~ 6276 의 ~10 함수) 가 새 intrinsic 인지하도록 update. 100–200 LOC + 모든 MIR consumer 회귀 검증.

**판정**: 단일 PR 가능하나 **단일 세션 무리**. 별도 PR (PR1c-1) 로 분리 + fresh context.

## Attempt 2 — Option (b): `osty-self` source bootstrap

**시도**:
```sh
$ OSTY_STAGE0_FALLBACK=1 OSTY_INSTALL_SELF_ALLOW_SOURCE_BOOTSTRAP=1 .bin/osty install-self
```

**결과**:
```
osty build: llvm backend: code generation is not implemented yet:
  LLVM000 unsupported-source: stage0 fallback declined before native MIR
    payload marshaling; reasons: osty-self not found; ...
    stage0 fallback declined: stage0: MIR shape outside bootstrap subset:
    function "tomlBasicString" does not match any stage0 pattern
```

**의미**: source bootstrap 도 stage0 fallback 을 사용. `tomlBasicString` 함수가 stage0 의 16 decline 함수 중 하나로 추정 (audit 99.8% 의 잔여 0.2%). 즉 source bootstrap = stage0 100% cover 가 선행 조건.

**stage0 audit decline top shapes** (2026-05-16, 본 plan §3.1 측정):

| shape | 카운트 | 해석 |
|---|---|---|
| `blocks=103 params=1 ret=Result instrs=282 feats=call,agg,fr,switch` | 1 | mega 함수 (recursive parser/elab) |
| `blocks=48 params=1 ret=Result instrs=103 feats=call,agg,fr,switch` | 1 | 큰 stmt elab |
| `blocks=43 params=0 ret=Int instrs=84 feats=call,intr,agg,fr,switch` | 1 | 0-param Int 큰 함수 |
| `blocks=43 params=1 ret=String instrs=72 feats=call,intr,agg,fr` | 1 | 큰 string builder |
| `blocks=34 params=1 ret=String instrs=48 feats=call,intr,agg,fr,switch` | 1 | mid string builder |
| `blocks=25 params=0 ret=String instrs=40–42 feats=call,intr,fr` | 2 | 0-param string |
| `blocks=20 params=1 ret=Result instrs=48 feats=...switch` | 1 | mid Result |
| `blocks=19 params=3 ret=String instrs=35 feats=...switch` | 1 | 3-param |
| `blocks=13 params=1–2 ret=String instrs=23–27 feats=...switch` | 3 | mid string + switch |
| `blocks=11 params=1 ret=String instrs=31 feats=call,intr,agg,fr` | 1 | 작은 string |
| `blocks=7 params=0 ret=Bool instrs=18 feats=...switch` | 1 | small Bool |
| `blocks=5 params=1 ret=MirLowerer instrs=30 feats=call,intr,agg,fr` | 1 | aggregate ret |
| `blocks=1 params=0 ret=String instrs=3 feats=intr` | 1 | trivial 1-block (의외) |

**판정**: 16 decline 의 대부분이 large complex 함수 (blocks 11–103). stage0 가 각 패턴을 cover 하려면 `matchXxxPattern` 함수 십수 개 추가. **수 일–수 주 작업**. PR1c 의 직접 path 아님.

## Attempt 3 — Option (c): daemon-식 self-host (stdin 없이)

**판정**: self-host 의도 부정. 본 plan 의 §2 비-목표 "behavior parity" 와 충돌. **거부**.

## 본 PR (PR1c blockers report) 결론

세 옵션 모두 단일 세션 단위 작업 아님:

| 옵션 | 차단 | 작업량 | 다음 단계 |
|---|---|---|---|
| (a) stage0 readLine cover | mir.IntrinsicReadLine 신설 + 5 파일 dispatch + classifyXxx update | 100–200 LOC, MIR ABI 변경 | 별도 PR1c-1 (fresh context) |
| (b) source bootstrap | stage0 16 decline 함수의 individual cover 필요 (`tomlBasicString` 등) | 수 일~수 주 | stage0 trajectory (별도 owner / P25+) |
| (c) daemon | self-host 의도 부정 | n/a | 거부 |

## 권장 다음 단계 (다음 세션 시작점)

**경로 A — PR1c-1: mir.IntrinsicReadLine 신설 (가장 가성비)**

가장 작은 dependency:
1. `internal/mir/mir.go` 에 `IntrinsicReadLine IntrinsicKind = iota...` 라인 추가 + `String()` switch 옆 case
2. `toolchain/mir_generator.osty` 에 `readLine()` call 을 IntrinsicReadLine 로 인지하는 dispatch arm 추가 (mirIsStdIoOutputMethod 옆)
3. `toolchain/lir_proto.osty` 의 io_write emit site (line 4023) 옆에 io_read_line emit
4. `internal/backend/stage0/emit.go` 1025 라인 `case mir.IntrinsicPrint, ...` 옆 case + emit 함수 (`emitReadLineIntrinsic` ~30 LOC)
5. `internal/stdlib/modules/io.osty:517` 의 prelude registration 확인 (이미 readLine 등록)

추정: 5 파일, ~120 LOC. fresh context 한 세션 가능.

**경로 B — stage0 trajectory wait**

본 plan §9 의 sibling angle 인 stage0 trajectory 가 16 decline 을 깎으면 (예: P25/P26 시점), (b) source bootstrap 가능 → osty-self 캐시 빌드 → production path 만으로 PR1c 진행. 단 외부 owner / 별도 timeline.

## 본 plan §10.1 위험 갱신

- R2 (stage0 coverage 의 PR3 시점 충분도): spike 2 의 99.8% audit 측정과 본 PR 의 16 decline 함수 명세를 합쳐 **위험 재상승**. PR3 (L1 byte parity, 실제 checker 호출) 시 16 decline 의 일부가 transitively 도달할 가능성. install-self 의 `tomlBasicString` decline 이 직접 증거 — toml 자체는 checker 의 transitive dep 아니나 같은 shape 다른 함수가 reach 할 가능성.
- N9 (신규): mir.IntrinsicReadLine 추가 — PR1c-1 의 핵심 작업 항목. ~120 LOC, 5 파일.

## 본 plan §10.2 open question 추가

- Q12: `mir.IntrinsicReadLine` 추가 시 기존 `toolchain/mir_generator.osty::mirStdIoMethodIsReadLine` helper (line 15534) 가 어떻게 dispatch arm 으로 wire 되어야 하는가? 현재 caller 0개라 dead.
- Q13: stage0 의 `classifyXxxCallLine` family (line 2418 ~ 6276) 가 새 intrinsic 을 자동으로 cover 하는가, 별도 case 추가 필요한가?

각 question 은 PR1c-1 의 첫 시간 (~15 분 grep) 으로 답 가능.

## 다음 세션 시작 명령

```sh
# 1. fresh main
git pull origin main

# 2. 본 doc 의 §"권장 다음 단계 경로 A" 따라 PR1c-1 시작
git checkout -b pr1c-1-mir-intrinsic-read-line origin/main

# 3. Q12/Q13 grep (~15분)
grep -n "mirStdIoMethodIsReadLine\|mirIsStdIoOutputMethod" toolchain/mir_generator.osty
grep -n "classifyVoidCallLine\|classifyProjectedCallLine" internal/backend/stage0/emit.go

# 4. mir.IntrinsicReadLine 추가 + 5 파일 wire
# 5. test: 같은 PR1b stub 의 main.osty 를 io.readLine() 호출 형태로 되돌리고 build + smoke
```
