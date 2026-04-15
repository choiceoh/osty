# Osty 스펙·Grammar 갭 트래킹

`LANG_SPEC_v0.3/` + `OSTY_GRAMMAR_v0.3.md` 기준.

**v0.3 시점 open gap: 없음.** v0.2 의 모든 open gap (G4, G8–G12) 이
v0.3 에서 해결됐고, v0.2 전수 감사에서 드러난 91건의 추가 모호성도
모두 결정됐다. 새 open gap 이 발견되면 본 문서에 G13 부터 추가.

스펙은 v0.2 부터 폴더 구조이다. §X (X = 1..18) 는 `LANG_SPEC_v0.3/NN-*.md`
파일. §10 의 서브섹션은 `LANG_SPEC_v0.3/10-standard-library/NN-*.md`.

---

## Open Gaps

(없음)

---

## Resolved in v0.3

| 갭 | 해결 위치 | 비고 |
|---|---|---|
| **G4** 클로저 파라미터 패턴 | `04-expressions.md` §4.7 | `LetPattern` 확장, irrefutable 제한. 파서 미구현 명시. |
| **G8** 채널 close semantics | `08-concurrency.md` §8.5 | 누구든 close, 두 번째 abort. drain 후 `None`. |
| **G9** `Builder<T>` phantom | `03-declarations.md` §3.4 | 완전 추상화. 에러 메시지에 누락 필드 명시. |
| **G10** Char / surrogate | `02-type-system.md` §2.1, `10-standard-library/05-standard-numeric-methods.md` §10.5 | surrogate 불허, `Char.fromInt` 안전 변환. |
| **G11** Generic 컴파일 모델 | `02-type-system.md` §2.7.3 | Monomorphization. Interface 값은 fat pointer vtable. |
| **G12** Cancellation 전파 | `08-concurrency.md` §8.4 | Task-group 자동 전파. `Cancelled(cause: Error)`. stdlib 전체 cancel-aware. |

v0.3 는 추가로 v0.2 전수 감사에서 나온 91건의 세부 모호성을 모두
결정. 주요 항목은 `LANG_SPEC_v0.3/18-change-history.md` §18.1 참조.

---

## Resolved in v0.2

| 갭 | 해결 위치 | 비고 |
|---|---|---|
| **G1** 컬렉션 `Equal`/`Hashable` 자동 구현 조건 | `02-type-system.md` §2.6.5 | Built-in instances 표 + 컬렉션 derivation 규칙 |
| **G2** Duration toString, log 표현 | `10-standard-library/20-time-extensions.md`, `10-standard-library/10-logging.md` | `Duration.toString()` 추가, `LogValue::Duration` 명시 |
| **G3** `LogValue` 합 타입 정의 | `10-standard-library/10-logging.md` §10.10 | Concrete enum + `ToLogValue` trait + 자동 promotion |
| **G5** 스크립트 `return` 의미 | `06-scripts.md` §6 | 암묵 main 의 return 으로 정의 |
| **G6** Partial struct 어노테이션 | `03-declarations.md` §3.4 | 각 선언 독립 적용, 합성 없음 |
| **G7** 진단 메시지 템플릿 | `03-declarations.md` §3.1 | positional-after-keyword 에러 박스 추가 |
