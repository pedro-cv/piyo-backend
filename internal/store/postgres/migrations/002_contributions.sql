-- Route contributions sent from a local data provider (iteration 4,
-- docs/implementation/CONTRIBUTIONS_DESIGN.md §3.3). Idempotent.
CREATE TABLE IF NOT EXISTS contributions (
    id                   TEXT PRIMARY KEY,
    device_hash          TEXT NOT NULL,
    provider_name        TEXT NOT NULL DEFAULT '',
    base_dataset_version INTEGER NOT NULL,
    route_ids            TEXT[] NOT NULL DEFAULT '{}',
    payload              JSONB NOT NULL DEFAULT '{}',
    comment              TEXT NOT NULL DEFAULT '',
    app_version          TEXT NOT NULL,
    status               TEXT NOT NULL CHECK (status IN ('pending','accepted','rejected')),
    review_note          TEXT NOT NULL DEFAULT '',
    created_at           TIMESTAMPTZ NOT NULL,
    reviewed_at          TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_contrib_device_created ON contributions(device_hash, created_at);
CREATE INDEX IF NOT EXISTS idx_contrib_status_created ON contributions(status, created_at DESC);
