# Security and privacy rules (all paths)

- Secrets never enter the repo: `.env`, `keys.properties`, keystores, admin tokens, salts, DSNs with
  passwords. Config is documented in `*.example` files with placeholders. If a value must exist for a
  test, generate a throwaway one inside the test.
- `HASH_SALT` must never be rotated casually: every device hash, block and quota depends on it.
- The API stores `sha256(HASH_SALT + install_id)` only. Never log IPs, install ids, raw bodies of
  reports or contributions. Logs are structured (`slog`) and must stay free of personal data.
- The app has no accounts, analytics, ads or crash reporters. Do not add Firebase, Play Services
  analytics or any SDK that phones home. Location is used in memory only (`PRIVACY.md`).
- Admin endpoints are protected by `X-Admin-Token`; new admin routes go through the `admin(...)`
  wrapper in `backend/internal/httpapi/server.go`.
- Every request body has a size cap (`bodyLimit`), every write endpoint a quota or rate limit. A new
  write endpoint without both is a bug.
- Validate coordinates against `METRO_BOUNDS`; reject payloads outside it with 400.
- Never disable TLS verification. Cleartext is allowed only for local development
  (`usesCleartextTraffic` placeholder) and must be off when `API_BASE_URL` is https.
- When unsure whether something is sensitive, treat it as sensitive and ask.
