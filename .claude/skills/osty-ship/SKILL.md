---
name: osty-ship
description: Osty 프로젝트 전용 ship 워크플로우. `just prepush` 게이트 통과 후 commit/push/draft PR 생성 + auto-merge 활성화. Stop 훅으로 자동 호출되거나 수동 `/osty-ship`으로 실행. 변경사항 없으면 조용히 skip.
---

# /osty-ship — Osty 프로젝트 ship 워크플로우

Osty 레포 컨벤션(`CLAUDE.md` §커밋 메시지, §테스트 규칙)에 맞춘 ship 자동화.
사용자가 "매번 치기 귀찮다"고 한 플로우 — 기본적으로 autonomous 진행.

## 중단 조건 (사전 체크)

다음 중 하나라도 해당하면 **즉시 종료** (에러 아님, 그냥 아무것도 안 함):

1. `git status --porcelain` 결과 비어있음 AND `git log @{u}..HEAD` 비어있음 → 보낼 게 없음
2. 현재 브랜치가 `main` 또는 `master` → feature branch 에서만 ship
3. diff에 `.env`, `*.key`, `*.pem`, `credentials*`, `*secret*` 포함 → 시크릿 차단
4. 변경사항이 `internal/selfhost/generated.go` 뿐 → 이 파일은 동결된 시드 산출물(부트스트랩 트랜스파일러 제거됨)이므로 직접 수정은 보류하고 의도 확인

## 1단계: 게이트

```
just prepush
```

- fmt-check + vet + repair-check + ci 전부 통과해야 함
- 실패 시: 출력을 사용자에게 보여주고 **중단**. 자동 수정 시도 금지.
- 성공 시에만 다음 단계로.

## 2단계: Commit

**파일 stage**:
- `git status --porcelain` 으로 변경 파일 목록 추출
- 명시적으로 `git add <file1> <file2> ...` (절대 `-A` / `.` 사용 금지)
- 바이너리/대용량 파일(`*.pprof`, `.profiles/`, `*.broken`, `graphify-out/`) 있으면 제외 + 경고

**메시지**:
- prefix: `feat:` / `fix:` / `chore:` / `docs:` / `refactor:` / `test:` / `perf:` 중 하나
- diff 스코프로 prefix 결정:
  - 새 기능 → `feat:`
  - 버그 픽스 → `fix:`
  - 의존성/설정 → `chore:`
  - 문서 → `docs:`
  - 코드 구조 변경(동작 동일) → `refactor:`
  - 테스트 추가/수정만 → `test:`
  - 성능 → `perf:`
- 한국어 또는 영어, **간결하게** (본 레포 최근 커밋 스타일 참조)
- trailer: `Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>`

**실행** (HEREDOC으로):
```bash
git commit -m "$(cat <<'EOF'
<prefix>: <간결 설명>

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

Pre-commit hook 실패 시: 원인 파악 후 수정 → 재스테이지 → **새 커밋** (amend 금지).

## 3단계: Push

```
git push -u origin <current-branch>
```

- force push 절대 금지
- 실패하면 (upstream drift 등) 중단하고 사용자에게 보고

## 4단계: Draft PR

```bash
gh pr create --draft --base main --title "<70자 이하>" --body "$(cat <<'EOF'
## Summary
- <1–3 bullets, diff 기반>

## Test plan
- [x] `just prepush` 로컬 통과
- [ ] CI 그린 확인

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- title: 커밋 prefix + 요약 (70자 제한)
- body: Summary + Test plan 섹션
- 항상 `--draft` + `--base main`

## 5단계: Auto-merge (가능하면)

```bash
PR_NUMBER=$(gh pr view --json number -q .number)
gh pr ready "$PR_NUMBER"
gh pr merge "$PR_NUMBER" --auto --squash --delete-branch
```

**실패 케이스별 처리**:
- branch protection/review 필요 → draft 로 되돌림 (`gh pr ready --undo`), PR URL + 이유 보고
- auto-merge 미활성 저장소 → "auto-merge 설정 없음" 보고, PR URL 남김
- 어떤 경우에도 force merge 시도 금지

## 6단계: 리포트

사용자에게 한 줄로:
- `✓ shipped: <PR-URL> (auto-merge enabled)`
- 또는 `⚠ PR created (draft): <PR-URL> — <실패 사유>`

## 중요 원칙

- `just prepush` 실패하면 절대 push/PR 진행 안 함
- 커밋 메시지는 **확인 없이** 진행 (자동화가 목적)
- 한 번의 stop 훅에서 1회만 실행 (sentinel)
- auto-merge 는 "가능하면" — 실패는 사용자에게 알리되 fatal 취급 안 함
- 스코프 벗어난 정리 작업 추가 금지 (diff 그대로 ship)
