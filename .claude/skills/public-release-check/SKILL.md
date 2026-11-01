---
name: public-release-check
description: Audit a repository before it is pushed to a public GitHub remote - secret scan of tree and history, ignore coverage, provider branding, licence and community files. Use before the first public push and before publishing releases.
---

# /public-release-check — is this tree safe to make public?

Works on the workspace or on an exported public repo (`/split-public-repos` output). Report each check
as PASS / FAIL with the evidence line; fix FAILs that are mechanical, list the rest for the user.

## 1. Secrets in the working tree
```sh
bash scripts/check-secrets.sh            # pattern scan of tracked + staged files
git ls-files | grep -Ei '(^|/)\.env($|\.)|keys\.properties$|\.(jks|keystore|p12|pem|pfx)$' | grep -v '\.example$'
```
Both must print nothing. Check that every value in `*.env.example` / `keys.properties.example` is a
placeholder (`change-me…`, `dev-…`, empty) and that `docker-compose*.yml` only uses `${VAR:-dev-default}`.

## 2. Secrets in history (only if history is being published)
```sh
git log --all -p -- . ':!*.png' ':!*.json' | grep -P '^\+' | grep -P 'AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9]{30,}|-----BEGIN .*PRIVATE KEY|POSTGRES_PASSWORD=(?!change-me|\$)|ADMIN_TOKEN=[a-f0-9]{32,}|HASH_SALT=[a-f0-9]{32,}|postgres://[^:]+:[^@$<]{6,}@' | head
git log --all --pretty=format: --name-only --diff-filter=A | sort -u | grep -Ei '\.env$|keys\.properties$|\.jks$|\.keystore$'
```
Any hit → do not publish that history; export with fresh history instead (`/split-public-repos`).
If `gitleaks` is installed, also run `gitleaks git --redact -v` (CI runs it on every push).

## 3. Ignore coverage
`git status --ignored --short | grep '^!!'` should list build dirs, `.env`, `keys.properties`, `deploy/data`.
`git check-ignore -v deploy/.env app/keys.properties app/app/build` must match a rule for each.

## 4. Provider branding out of code and copy
```sh
grep -rIli microcruz --exclude-dir=.git --exclude-dir=build --exclude-dir=node_modules --exclude-dir=data . \
  | grep -vE 'docs/DATA_SOURCES.md|internal/dataset/sources.go|_test\.(go|kt)$|dataset-seed.json|PROGRESS.md|EVIDENCE.md|DATA_MODEL.md'
```
Should print nothing. Hits in UI strings, READMEs or handlers are FAIL; fixtures/tests parsing the
upstream format are fine. The bundled seed carries the attribution the licence requires (keep it).

## 5. Data redistribution
Read `docs/DATA_SOURCES.md` §"Before publishing". If the seed comes from the named-routes provider and
redistribution rights are not confirmed, flag it as the one blocking item.

## 6. Community and legal files present
`LICENSE`, `README.md`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md`,
`.github/PULL_REQUEST_TEMPLATE.md`, `.github/ISSUE_TEMPLATE/*`, `.github/CODEOWNERS`,
`.github/dependabot.yml`, `.github/workflows/*.yml`, `.gitleaks.toml`, `.editorconfig`.
Licence headers are not required; the LICENSE file is.

## 7. Private material not included
No `PROGRESS.md`, `PLAN.md`, `design/`, `docs/audit-screens/` (third-party app screenshots),
`docs/01-moovit-audit.md`, scratch screenshots, or phone serials in the public tree.

## 8. Builds from a clean clone
Backend: `go build ./... && go test ./...`. App: `./gradlew :app:testDebugUnitTest` without
`keys.properties` present (defaults must work). Record the output in the report.
