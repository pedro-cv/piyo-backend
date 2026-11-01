---
name: new-migration
description: Add a schema change to the Piyo API for both SQL stores (PostgreSQL/PostGIS migration file and SQLite schema) and the in-memory store. Use whenever a table or column changes.
---

# /new-migration — schema change in all stores

Argument: short description, e.g. `/new-migration add review_note to contributions`.

## Steps
1. **PostgreSQL**: create `internal/store/postgres/migrations/NNN_<snake_name>.sql` with the next
   number (`ls` the directory). Migrations are applied in order at startup and must be idempotent
   (`CREATE TABLE IF NOT EXISTS`, `ADD COLUMN IF NOT EXISTS`). PostGIS geometry columns use SRID 4326.
   Never edit an applied migration; add a new one.
2. **SQLite**: mirror the change in the schema/upgrade code in `internal/store/sqlite.go`
   (same column names and semantics; `TEXT` for timestamps in RFC 3339 UTC).
3. **Memory**: extend the structs in `internal/store/memory*.go` and `internal/model` so tests see
   the field.
4. **Model + JSON**: `internal/model/model.go` field with the exact JSON name the README documents.
5. **Docs**: `docs/implementation/DATA_MODEL.md` (table/column), README if a response changes.
6. **Verify**:
   ```sh
   go test ./... && go vet ./...
   cd ../deploy && docker compose up -d --build api && docker compose logs api | grep -i migrat
   ```
   Then one curl that touches the new column. Paste both in the PR.
7. **Data safety**: a migration that rewrites rows needs a backup note in `DEPLOY_VPS.md`
   (`pg_dump` before deploy) and must be reversible or documented as one-way.
