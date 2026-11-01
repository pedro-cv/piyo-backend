# Backend and deploy rules

- Standard library HTTP (Go 1.22+ patterns: `mux.HandleFunc("GET /v1/…")`). No frameworks.
- Every endpoint: JSON, `/v1` prefix, errors `{"error":{"code","message"}}`, UTC RFC 3339 times,
  device endpoints read `X-Install-Id`, admin endpoints wrapped with `admin(...)`.
- Persistence goes through the `store.Store` interface. A new method must be implemented in
  **memory**, **sqlite** and **postgres** stores, with a migration for the SQL ones
  (`internal/store/postgres/migrations/NNN_*.sql`, applied at startup; SQLite schema in `sqlite.go`).
- Tests: `go test ./...` runs with no database (memory store, dataset builder fixtures). Handler tests
  use `httptest` + the memory store. Postgres paths are verified against the compose stack and the
  result goes in the PR / EVIDENCE, not in CI.
- Config: `internal/config`. New variables get a default, a comment in `backend/.env.example`, a row in
  `docs/implementation/SETUP_AND_RUN.md` and, if used by compose, a `${VAR:-default}` in
  `deploy/docker-compose.yml` + `deploy/.env.example`.
- Dataset releases are immutable: never edit a published `vN`; publish `vN+1`.
- Importer source layouts are named by shape (`osm`, `named-routes`); provider brands live only in
  `internal/dataset/sources.go` and `docs/DATA_SOURCES.md`.
- Deploy: images pinned by version, API runs as non-root distroless, Caddy terminates TLS. Secrets
  come from `deploy/.env` only; compose files contain `${VAR:-dev-default}` placeholders.
- `gofmt` clean, `go vet` clean, no new module without a line in `DEPENDENCIES.md`.
