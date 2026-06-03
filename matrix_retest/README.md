# matrix_retest/

`STDLIB_MATRIX.md` §2.1.A.2 (2026-05-23 audit) 재현용 e2e 케이스 모음.

## 구조

- `<module>/<module>_test.osty` — 1-call 형태의 LLVM E2E 테스트
- `<module>/osty.toml` — `edition = "0.3"` (기존 `examples/*_e2e/`와 동일)
- `AUDIT_LOG_2026-05-23.txt` — 본 audit 실행 시 캡처된 raw 결과

## 실행

루트에서 환경 세팅 후:

```sh
export OSTY_NATIVE_CHECKER_BIN="$PWD/.osty/bin/osty-native-checker"
export OSTY_SELF_BIN="$PWD/toolchain/.osty/out/debug/llvm/osty-self"
# OSTY_STDLIB_BODY_LOWER defaults ON (unset or non-0); only set =0 to bisect.

for d in matrix_retest/*/; do
  [ -f "$d/osty.toml" ] || continue
  echo "=== $d ==="
  (cd "$d" && ../../.bin/osty test)
done
```

## 케이스 분류

| 디렉토리 | 매트릭스 §2.1.A 분류 | 2026-05-23 결과 |
|---|---|---|
| `option_map/` | partial | FAIL (aggregate enum payload) |
| `result_map/` | partial | FAIL (aggregate enum payload) |
| `iter_map/` | FAIL | ✅ **PASS** (PR #1981) |
| `iter_fold/` | (List.fold 직접 검증) | ✅ PASS (PR #1981) |
| `iter_find/` | (PR #1983/#1987 직접 검증) | ✅ PASS |
| `iter_scan/` | (PR #1987 직접 검증) | ✅ PASS |
| `iter_groupby/` | (PR #1986/#1987 직접 검증) | ✅ PASS |
| `json_parse/` | FAIL (body lower) | FAIL (link: `undefined symbol osty_std_json__parse`) |
| `url_parse/` | FAIL (body lower) | FAIL (`MIR coverage incomplete: for-in over Map`) |
| `encoding_hex/` | FAIL | FAIL (lower+link 통과, 런타임 `exit -1`) |
| `crypto_sha256/` | partial | FAIL (lower+link 통과, 런타임 `exit -1`) |
| `http_serve/` | untested | ✅ PASS (import-only) |
| `deneb_ports/` | (§2.3 Surface-rich) | ✅ PASS (6개 동시 import + LLVM 빌드) |

다음 audit 시 동일 디렉토리에서 재실행하면 회귀/진전을 직접 비교할 수 있다. 새 케이스를 추가할 때는 동일 패턴 (`osty.toml` + `<name>_test.osty`)을 유지하고 매트릭스 표에 새 row 추가.
