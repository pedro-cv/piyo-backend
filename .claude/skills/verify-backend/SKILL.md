---
name: verify-backend
description: Verify the Piyo Go API - gofmt, vet, unit tests, run in dev mode, curl smoke checks, and optionally the compose stack with PostGIS. Use before claiming any backend change works.
---

# /verify-backend — prove it works

## 1. Static + unit (always)
```sh
cd backend
gofmt -l .              # must print nothing
go vet ./...
go test ./... -count=1
```
Quote the `ok` lines per package in the PR. Tests need no database.

## 2. Dev server smoke
```sh
go run ./cmd/piyo-api &                       # DEV_MODE=true, :8080, demo data
B=http://localhost:8080/v1; H='X-Install-Id: demo-device-0001'; A='X-Admin-Token: dev-admin-token'
curl -s $B/health
curl -s $B/manifest
curl -s -H "$H" $B/reports/quota
curl -s -H "$A" "$B/admin/reports?status=pending&limit=5"
```
Plus one call per endpoint you touched, including a failing case (400/401/403/429) to show validation.

## 3. Compose stack (when stores, migrations, releases or the map changed)
```sh
cd deploy && docker compose up -d --build && docker compose ps      # 4 services Up, db healthy
docker compose logs api --tail 50                                    # migrations applied, no errors
curl -s localhost/v1/health && curl -s localhost/v1/datasets && curl -s localhost/v1/map/style.json | head -c 300
```
Never run `docker compose down -v` (deletes the database) without the user.

## 4. Report
Table: check · command · result. Secrets never appear in the output you paste (redact DSNs; the server
already logs them redacted).
