// Package storetest holds store contracts shared by the store tests of every
// implementation (memory, SQLite, PostgreSQL).
package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"piyo-backend/internal/model"
	"piyo-backend/internal/store"
)

func contrib(id, batch, device string, at time.Time) *model.Contribution {
	return &model.Contribution{ID: id, BatchID: batch, DeviceHash: device, BaseDatasetVersion: 15, RouteIDs: []string{id + "-line"},
		Payload: []byte(`{"lines":[]}`), AppVersion: "1", Status: model.StatusPending, CreatedAt: at}
}

// Contributions runs the contribution contract (batches, withdraw, publish) against st.
// st must not hold contributions from devices dev1/dev2.
func Contributions(t *testing.T, st store.Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	month := now.Add(-time.Hour)

	// One request with two routes = two rows, one batch.
	if err := st.CreateContributions(ctx, []*model.Contribution{contrib("c_a", "b_1", "dev1", now), contrib("c_b", "b_1", "dev1", now)}); err != nil {
		t.Fatal(err)
	}
	// A row from before batches existed counts on its own.
	if err := st.CreateContribution(ctx, contrib("c_legacy", "", "dev1", now)); err != nil {
		t.Fatal(err)
	}
	// All or nothing: a duplicate id fails the whole batch.
	if err := st.CreateContributions(ctx, []*model.Contribution{contrib("c_new", "b_2", "dev1", now), contrib("c_a", "b_2", "dev1", now)}); !errors.Is(err, store.ErrAlreadyExists) {
		t.Fatalf("duplicate in batch: %v", err)
	}
	if _, err := st.GetContribution(ctx, "c_new"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("partial batch stored: %v", err)
	}
	if n, err := st.CountContributionsSince(ctx, "dev1", month); err != nil || n != 2 {
		t.Fatalf("quota count = %d, %v (want 2: batch b_1 + legacy row)", n, err)
	}
	got, err := st.GetContribution(ctx, "c_a")
	if err != nil || got.BatchID != "b_1" || got.PublishedVersion != 0 || got.PublishedAt != nil {
		t.Fatalf("get: %+v %v", got, err)
	}

	// Withdraw: only the owner, only while pending.
	if err := st.DeleteContribution(ctx, "c_b", "dev2"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("other device: %v", err)
	}
	if err := st.DeleteContribution(ctx, "c_b", "dev1"); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteContribution(ctx, "c_b", "dev1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	if _, err := st.SetContributionStatus(ctx, "c_legacy", model.StatusRejected, "no", now); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteContribution(ctx, "c_legacy", "dev1"); !errors.Is(err, store.ErrNotPending) {
		t.Fatalf("reviewed delete: %v", err)
	}

	// Publishing marks accepted, unpublished rows once.
	if _, err := st.SetContributionStatus(ctx, "c_a", model.StatusAccepted, "ok", now); err != nil {
		t.Fatal(err)
	}
	marked, err := st.MarkContributionsPublished(ctx, []string{"c_a", "c_legacy", "c_missing"}, 16, now)
	if err != nil || len(marked) != 1 || marked[0].ID != "c_a" || marked[0].PublishedVersion != 16 || marked[0].DeviceHash != "dev1" {
		t.Fatalf("mark: %+v %v", marked, err)
	}
	if again, err := st.MarkContributionsPublished(ctx, []string{"c_a"}, 17, now); err != nil || len(again) != 0 {
		t.Fatalf("second mark: %+v %v", again, err)
	}
	list, err := st.ListContributionsByDevice(ctx, "dev1", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range list {
		if c.ID == "c_a" && (c.PublishedVersion != 16 || c.PublishedAt == nil || !c.PublishedAt.Equal(now)) {
			t.Fatalf("listed: %+v", c)
		}
	}
}
