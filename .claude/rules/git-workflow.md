# Git and review workflow (all paths)

- `main` (public repos) / `master` (workspace) are protected: PR only, CI green, linear history,
  no force pushes. Branch from the default branch, name it `feat/…`, `fix/…`, `docs/…`, `chore/…`.
- Before `git commit`: `go test ./...` + `go vet ./...` (backend), `./gradlew :app:testDebugUnitTest`
  (app), `bash scripts/check-secrets.sh`. The pre-commit hook in `.githooks/` runs the secret scan.
- Commit message: imperative subject ≤ 72 chars; body = why + what was verified (command and result).
- PR description follows `.github/PULL_REQUEST_TEMPLATE.md`: change, why, how verified, screenshots
  for UI, privacy/security checklist. Mark anything not run as "not run".
- Never rewrite published history. Never commit generated data under `deploy/data/`, build outputs,
  or private working notes (`PROGRESS.md`, `PLAN.md`) into the public repos.
- Squash-merge PRs; delete the branch after merge.
