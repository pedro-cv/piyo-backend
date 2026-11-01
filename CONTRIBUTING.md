# Contributing to Piyo

Thanks for helping build a free, ad-free transit app for Santa Cruz de la Sierra. This guide applies to
both public repositories (`piyo-app`, `piyo-backend`). Issues and PRs may be written in Spanish or English.

## Ways to help

- **Report wrong line data** from inside the app (Reportar) or with the *Data error* issue template.
- **Draw or correct routes** in the app (Datos → tus rutas → Enviar a revisión). Reviewers merge accepted
  contributions into the next dataset release.
- **Code**: pick an issue labelled `good first issue` or `help wanted`, or open an issue first for
  anything larger than a small fix so the approach can be agreed.

## Ground rules

1. **Privacy by design.** No accounts, analytics, ads, crash reporters or SDKs that phone home. The app keeps
   location in memory; the API stores only a salted hash of an install id. See `docs/implementation/PRIVACY.md`.
2. **Offline first.** Planning, lines, favourites and the route editor must work without a connection.
3. **No secrets in git.** `.env`, `keys.properties`, keystores and tokens are ignored; new configuration goes
   into the `*.example` files with a placeholder. The pre-commit hook and CI run a secret scan.
4. **Copy is Spanish with Bolivian voseo** ("Probá", "Elegí"), short and plain. Code, comments and commit
   messages are English.
5. **Tests green.** Backend: `go test ./... && go vet ./...`. App: `./gradlew :app:testDebugUnitTest`.
   Touching the parser, the planner, a handler or a store means adding or updating a test.
6. **Dependencies are a decision.** Every new library needs a line in `docs/implementation/DEPENDENCIES.md`
   (why, license, size). Prefer the standard library / AndroidX.
7. **Data provider branding stays out of code and UI.** Attribution is shown from the dataset's own
   `attribution` / `license` fields (`docs/DATA_SOURCES.md`).

## Workflow

```sh
git clone <repo> && cd <repo>
git config core.hooksPath .githooks        # enables the secret-scan pre-commit hook
git switch -c feat/<area>-<short-name>     # feat/ fix/ docs/ chore/
# ... work, test ...
git push -u origin feat/<area>-<short-name>
gh pr create --fill                        # the PR template asks how you verified it
```

- `main` is protected: pull requests only, CI must pass, linear history, no force pushes.
- One logical change per PR. Keep refactors separate from behaviour changes.
- Commit subject: imperative, ≤ 72 characters. Body: why, and what you ran to verify.
- UI changes include light and dark screenshots from a device or emulator.
- Say "not run" for anything you did not verify. Honest beats complete.
- Squash merge; the branch is deleted automatically.

## Local setup

- **App**: Android Studio or JDK 17 + the Gradle wrapper. Copy `keys.properties.example` to
  `keys.properties` to point at a backend; without it the app uses the emulator's `10.0.2.2:8080`.
- **Backend**: Go 1.25. `go run ./cmd/piyo-api` starts a dev server with demo data; Docker Compose in
  `deploy/` runs the full stack (PostGIS, API, tiles, Caddy) from `deploy/.env.example`.
- Full guide: `docs/implementation/SETUP_AND_RUN.md`.

## Working with an AI assistant

The repos ship a `CLAUDE.md`, scoped rules in `.claude/rules/` and skills in `.claude/skills/`
(`/ship`, `/new-screen`, `/new-endpoint`, `/verify-app`, …). They encode the rules above; a PR produced
with an assistant is reviewed like any other and must state what was actually run.

## Licensing of contributions

By submitting a contribution you agree it is licensed under the repository's LICENSE. Route data you
draw in the app is contributed under the terms shown in the app before sending.

## Code of conduct

Participation is governed by `CODE_OF_CONDUCT.md`. Security issues: see `SECURITY.md`, not a public issue.
