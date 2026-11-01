## What changes

<!-- One or two sentences. Link the issue: Closes #NN -->

## Why

<!-- The problem or decision behind it. Link the doc in docs/implementation/ if one applies. -->

## How it was verified

<!-- Paste the commands and the result lines. Write "not run" where applicable. -->

- Backend: `go test ./... && go vet ./...` →
- App: `./gradlew :app:testDebugUnitTest` →
- Device / emulator check (model, flow, result) →
- Screenshots (light + dark for UI changes):

## Checklist

- [ ] No secrets, tokens, `.env`, `keys.properties` or keystores in the diff (`bash scripts/check-secrets.sh` OK)
- [ ] No new analytics, ads, accounts or SDKs that phone home; `PRIVACY.md` updated if stored data changed
- [ ] Offline behaviour kept (app) / quota, rate limit and body cap present (new API write endpoint)
- [ ] Strings in `strings.xml`, Spanish voseo; no provider branding in code or copy
- [ ] New dependency justified in `docs/implementation/DEPENDENCIES.md` (or none added)
- [ ] Docs updated (README table, `DATA_MODEL.md`, `SETUP_AND_RUN.md`, `.env.example`) where relevant
- [ ] Tests added or updated for parser / planner / handler / store changes
