package postgres

import (
	"context"
	"os"
	"testing"

	"piyo-backend/internal/store/storetest"
)

// Runs only against a disposable database: PIYO_TEST_DATABASE_URL=postgres://... go test ./internal/store/postgres/
// The contract creates rows for devices dev1/dev2 and deletes them afterwards.
func TestPostgresContributions(t *testing.T) {
	dsn := os.Getenv("PIYO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PIYO_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	clean := func() { _, _ = st.pool.Exec(ctx, `DELETE FROM contributions WHERE device_hash IN ('dev1','dev2')`) }
	clean()
	defer clean()
	storetest.Contributions(t, st)
}
