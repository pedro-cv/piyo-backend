---
name: new-endpoint
description: Add an HTTP endpoint to the Piyo Go API - route, handler, validation, quota/rate limits, store methods in memory/sqlite/postgres, migration, httptest test and README row. Use for any new or changed /v1 route.
---

# /new-endpoint — the full checklist for a /v1 route

Argument: `METHOD /v1/path` and one line on the behaviour, e.g.
`/new-endpoint POST /v1/alerts  device reports a line that stopped running`.

## Steps

1. **Read the neighbours**: `internal/httpapi/server.go` (`routes()`, middleware, `admin()`,
   `writeJSON`, `writeError`), the closest `handlers_*.go` (reports for device writes, admin for review,
   dataset for files) and its `_test.go`.
2. **Contract first**: write the README row (method, path, auth, body, responses) in `README.md`
   before code. Errors are `{"error":{"code","message"}}`; codes already used: `validation`, `blocked`,
   `quota_exceeded`, `rate_limited`, `not_found`, `unauthorized`, `too_large`.
3. **Store**: add the method(s) to `store.Store` in `internal/store/store.go`, then implement in
   `memory*.go`, `sqlite*.go` and `postgres/*.go`. SQL changes ⇒ `/new-migration`. Memory store is what
   tests use, so it must be complete.
4. **Handler** in the matching `handlers_*.go`:
   - device routes: read `X-Install-Id` via the existing helper → device hash; check block + quota;
   - admin routes: wrap with `admin(...)` in `routes()`;
   - decode with `json.NewDecoder(r.Body)` (body already capped by `bodyLimit`; raise the cap per route
     only like contributions does), validate every field, coordinates inside `cfg.MetroBounds`;
   - log with `s.logger` without personal data; return 201 for creates with the id and `remaining`.
5. **Config**: new limits get a `Config` field, env var with default, comment in `.env.example`, a
   `${VAR:-default}` in `deploy/docker-compose.yml` and `deploy/.env.example`, a row in
   `docs/implementation/SETUP_AND_RUN.md`.
6. **Test** `handlers_<area>_test.go`: `httptest.NewServer(NewHandler(cfg, store.NewMemory(), limiter, logger))`,
   cover 201/200, 400 validation, 401/403 where relevant, 429 quota. `go test ./... && go vet ./...`.
7. **Smoke**: `go run ./cmd/piyo-api` and a curl in the README style; paste the response in the PR.
8. **App side**: if the app consumes it, open the app change with `/new-repository http`.
