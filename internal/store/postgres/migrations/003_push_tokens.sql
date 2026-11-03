-- FCM token per device for review-result notifications
-- (docs/implementation/PRIVACY.md §push). Keyed by the salted device hash the
-- other tables already use; no new identifier. Stale rows (not refreshed in
-- 60 days) are purged by the API. Idempotent.
CREATE TABLE IF NOT EXISTS push_tokens (
    device_hash TEXT PRIMARY KEY,
    token       TEXT NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_push_tokens_updated ON push_tokens(updated_at);
