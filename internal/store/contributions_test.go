package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"piyo-backend/internal/model"
	"piyo-backend/internal/store"
	"piyo-backend/internal/store/storetest"
)

func TestMemoryContributions(t *testing.T) { storetest.Contributions(t, store.NewMemory()) }

func TestSQLiteContributions(t *testing.T) {
	st, err := store.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "piyo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	storetest.Contributions(t, st)
}

// A database created before batch_id/published_* existed is upgraded in place.
func TestSQLiteContributionsUpgrade(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE contributions (
		id TEXT PRIMARY KEY, device_hash TEXT NOT NULL, provider_name TEXT NOT NULL DEFAULT '', base_dataset_version INTEGER NOT NULL,
		route_ids TEXT NOT NULL DEFAULT '[]', payload TEXT NOT NULL DEFAULT '{}', comment TEXT NOT NULL DEFAULT '', app_version TEXT NOT NULL,
		status TEXT NOT NULL, review_note TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, reviewed_at TEXT);
		INSERT INTO contributions (id, device_hash, base_dataset_version, route_ids, app_version, status, created_at)
		VALUES ('c_old', 'dev1', 14, '["7","8"]', '1', 'pending', '2026-11-01T00:00:00.000000Z');`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	st, err := store.OpenSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	c, err := st.GetContribution(ctx, "c_old")
	if err != nil || c.BatchID != "" || len(c.RouteIDs) != 2 {
		t.Fatalf("old row: %+v %v", c, err)
	}
	if err := st.CreateContributions(ctx, []*model.Contribution{{ID: "c_n", BatchID: "b_9", DeviceHash: "dev1", BaseDatasetVersion: 15, RouteIDs: []string{"9"}, AppVersion: "1", Status: model.StatusPending, CreatedAt: time.Now().UTC()}}); err != nil {
		t.Fatal(err)
	}
	since, _ := time.Parse(time.RFC3339, "2026-10-01T00:00:00Z")
	if n, err := st.CountContributionsSince(ctx, "dev1", since); err != nil || n != 2 {
		t.Fatalf("count after upgrade = %d, %v", n, err)
	}
}
