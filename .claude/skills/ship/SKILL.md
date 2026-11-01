---
name: ship
description: Format, test, scan for secrets, commit on a branch and open a pull request for the current change. Use when work is done and ready for review.
---

# /ship — finish a change the safe way

Run from the repo root. Arguments (optional): a short branch suffix, e.g. `/ship fix-planner-snap`.

## Steps

1. **Branch check.** `git branch --show-current`. If on `main`/`master`, create
   `feat|fix|docs|chore/<area>-<suffix>` with `git switch -c` first. Never commit on the default branch.
2. **Format.**
   - Backend touched: `cd backend && gofmt -l . ` (must print nothing; run `gofmt -w .` if not) and `go vet ./...`.
   - App touched: no formatter is enforced; keep the file's style and 120-column lines.
3. **Tests.**
   - Backend: `cd backend && go test ./...`
   - App: `cd app && ./gradlew :app:testDebugUnitTest --console=plain` (Windows: `gradlew.bat`).
   Paste the summary line(s) into the PR body. If a test fails, stop and fix; do not ship red.
4. **Secret scan.** `bash scripts/check-secrets.sh`. It exits non-zero on hits; fix before continuing.
5. **Docs.** If config, endpoints, dataset format, dependencies or privacy changed, update the matching file
   in `docs/implementation/` and the README table of the component. New strings: `strings.xml` only.
6. **Commit.** Stage only intended files (`git add -p` or explicit paths; never `git add -A` with
   untracked data). Message: imperative subject ≤ 72 chars, body with *why* and *verified by*.
   Keep the configured co-author trailer.
7. **Push and PR.** `git push -u origin <branch>` then
   `gh pr create --fill --template .github/PULL_REQUEST_TEMPLATE.md` (or `--web`). Fill every section of
   the template; write "not run" for checks you did not run. Attach device screenshots for UI changes.
8. **Report** the PR URL, test summary and anything left out.

## Never

- `--no-verify`, `--force`, pushing to `main`/`master`, committing `.env*` (except `.example`),
  `keys.properties`, `deploy/data/**`, build outputs, or `PROGRESS.md`/`PLAN.md` into a public repo.
