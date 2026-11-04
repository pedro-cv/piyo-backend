-- One row per contributed route (docs/implementation/CONTRIBUTIONS_DESIGN.md §3.3):
-- a request with several routes becomes several rows sharing batch_id, and the
-- monthly quota counts batches. published_version / published_at record the
-- dataset release that includes the route (set by
-- POST /v1/admin/datasets/{version}/publish). Rows written before this
-- migration keep batch_id = '' (they count as their own batch). Idempotent.
ALTER TABLE contributions ADD COLUMN IF NOT EXISTS batch_id TEXT NOT NULL DEFAULT '';
ALTER TABLE contributions ADD COLUMN IF NOT EXISTS published_version INTEGER NOT NULL DEFAULT 0;
ALTER TABLE contributions ADD COLUMN IF NOT EXISTS published_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_contrib_status_published ON contributions(status, published_version);
