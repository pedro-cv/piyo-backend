-- Piyo · PostgreSQL + PostGIS schema (applied by the postgres store at startup;
-- every statement is idempotent). See docs/implementation/DATABASE_CHOICE.md.

CREATE EXTENSION IF NOT EXISTS postgis;

-- Working transit data (the editable master; releases are snapshots of it).
CREATE TABLE IF NOT EXISTS lines (
    line_id     TEXT PRIMARY KEY,
    display     TEXT NOT NULL,
    name        TEXT NOT NULL,
    mode        TEXT NOT NULL CHECK (mode IN ('micro','trufi','other')),
    color       TEXT NOT NULL DEFAULT '',
    fare_bs     TEXT NOT NULL DEFAULT '',
    source_url  TEXT NOT NULL DEFAULT '',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS variants (
    variant_id  TEXT PRIMARY KEY,
    line_id     TEXT NOT NULL REFERENCES lines(line_id) ON DELETE CASCADE,
    seq         INTEGER NOT NULL DEFAULT 0,
    direction   TEXT NOT NULL,
    headsign    TEXT NOT NULL DEFAULT '',
    origin      TEXT NOT NULL DEFAULT '',
    geom        GEOGRAPHY(LINESTRING, 4326) NOT NULL,
    length_m    INTEGER NOT NULL DEFAULT 0,
    gaps        INTEGER NOT NULL DEFAULT 0,
    streets     JSONB NOT NULL DEFAULT '[]',
    source_url  TEXT NOT NULL DEFAULT '',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_variants_line ON variants(line_id);

-- Iteration 3 (named-routes source): colour provenance, named branches, per-variant colours.
ALTER TABLE lines    ADD COLUMN IF NOT EXISTS color_source      TEXT NOT NULL DEFAULT '';
ALTER TABLE lines    ADD COLUMN IF NOT EXISTS source_updated_at TIMESTAMPTZ;
ALTER TABLE variants ADD COLUMN IF NOT EXISTS name         TEXT NOT NULL DEFAULT '';
ALTER TABLE variants ADD COLUMN IF NOT EXISTS branch       TEXT NOT NULL DEFAULT '';
ALTER TABLE variants ADD COLUMN IF NOT EXISTS color        TEXT NOT NULL DEFAULT '';
ALTER TABLE variants ADD COLUMN IF NOT EXISTS color_source TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_variants_geom ON variants USING GIST (geom);

CREATE TABLE IF NOT EXISTS stops (
    stop_id     TEXT PRIMARY KEY,
    name        TEXT NOT NULL DEFAULT '',
    geom        GEOGRAPHY(POINT, 4326) NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_stops_geom ON stops USING GIST (geom);

CREATE TABLE IF NOT EXISTS variant_stops (
    variant_id  TEXT NOT NULL REFERENCES variants(variant_id) ON DELETE CASCADE,
    seq         INTEGER NOT NULL,
    stop_id     TEXT NOT NULL REFERENCES stops(stop_id) ON DELETE CASCADE,
    PRIMARY KEY (variant_id, seq)
);
CREATE INDEX IF NOT EXISTS idx_variant_stops_stop ON variant_stops(stop_id);

-- Published releases (immutable snapshots; files live in RELEASES_DIR).
CREATE TABLE IF NOT EXISTS datasets (
    version       INTEGER PRIMARY KEY,
    generated_at  TIMESTAMPTZ NOT NULL,
    published_at  TIMESTAMPTZ,
    manifest      JSONB NOT NULL,
    notes         TEXT NOT NULL DEFAULT ''
);

-- User input. device_hash = sha256(HASH_SALT + install_id); never the raw id.
CREATE TABLE IF NOT EXISTS reports (
    id           TEXT PRIMARY KEY,
    device_hash  TEXT NOT NULL,
    line_id      TEXT NOT NULL DEFAULT '',
    type         TEXT NOT NULL,
    geom         GEOGRAPHY(POINT, 4326) NOT NULL,
    comment      TEXT NOT NULL DEFAULT '',
    app_version  TEXT NOT NULL,
    data_version INTEGER NOT NULL,
    status       TEXT NOT NULL CHECK (status IN ('pending','accepted','rejected')),
    created_at   TIMESTAMPTZ NOT NULL,
    reviewed_at  TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_reports_device_created ON reports(device_hash, created_at);
CREATE INDEX IF NOT EXISTS idx_reports_status_created ON reports(status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_reports_geom ON reports USING GIST (geom);

CREATE TABLE IF NOT EXISTS suggestions (
    id              TEXT PRIMARY KEY,
    device_hash     TEXT NOT NULL,
    kind            TEXT NOT NULL,
    line_id         TEXT NOT NULL DEFAULT '',
    variant_id      TEXT NOT NULL DEFAULT '',
    dataset_version INTEGER NOT NULL,
    geom            GEOGRAPHY(POINT, 4326) NOT NULL,
    payload         JSONB NOT NULL DEFAULT '{}',
    comment         TEXT NOT NULL DEFAULT '',
    app_version     TEXT NOT NULL,
    status          TEXT NOT NULL CHECK (status IN ('pending','accepted','rejected')),
    review_note     TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL,
    reviewed_at     TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_sugg_device_created ON suggestions(device_hash, created_at);
CREATE INDEX IF NOT EXISTS idx_sugg_status_created ON suggestions(status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_sugg_line ON suggestions(line_id);
CREATE INDEX IF NOT EXISTS idx_sugg_geom ON suggestions USING GIST (geom);

CREATE TABLE IF NOT EXISTS nps (
    id           TEXT PRIMARY KEY,
    device_hash  TEXT NOT NULL,
    score        SMALLINT NOT NULL CHECK (score BETWEEN 0 AND 10),
    reasons      JSONB NOT NULL DEFAULT '[]',
    comment      TEXT NOT NULL DEFAULT '',
    app_version  TEXT NOT NULL,
    data_version INTEGER NOT NULL,
    device_class TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_nps_created ON nps(created_at DESC);

CREATE TABLE IF NOT EXISTS blocks (
    device_hash TEXT PRIMARY KEY,
    until_at    TIMESTAMPTZ NOT NULL,
    reason      TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL
);
