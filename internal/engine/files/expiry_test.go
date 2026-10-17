package files

import (
	"errors"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

func TestPurposeExpiryReadsAndSweep(t *testing.T) {
	store, conn, slug := openTestStore(t)
	for _, tc := range []struct {
		purpose string
		age     time.Duration
		expired bool
	}{
		{purpose: "imports", age: 31 * 24 * time.Hour, expired: true},
		{purpose: "imports", age: 29 * 24 * time.Hour},
		{purpose: "exports", age: 8 * 24 * time.Hour, expired: true},
		{purpose: "reports", age: 8 * 24 * time.Hour, expired: true},
		{purpose: "reports", age: 6 * 24 * time.Hour},
		{purpose: "attachments", age: 400 * 24 * time.Hour},
		{purpose: "avatars", age: 400 * 24 * time.Hour},
		{purpose: "custom", age: 400 * 24 * time.Hour},
	} {
		t.Run(tc.purpose+tc.age.String(), func(t *testing.T) {
			row := insertFixtureFile(t, store, slug, tc.purpose)
			query := "UPDATE " + tenantschema.Name(slug) + ".files SET purpose = $1, created_at = $2 WHERE id = $3"
			if _, err := conn.ExecContext(t.Context(), query, tc.purpose, time.Now().Add(-tc.age), row.ID); err != nil {
				t.Fatal(err)
			}

			file, err := store.GetByID(t.Context(), slug, row.ID)
			if err != nil || (file.DeletedAt != nil) != tc.expired {
				t.Fatalf("file before sweep = %+v, %v", file, err)
			}

			tx, err := conn.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = ActiveReference(t.Context(), tx, slug, row.TenantID, row.ID)
			_ = tx.Rollback()
			if errors.Is(err, ErrFileNotFound) != tc.expired || err != nil && !tc.expired {
				t.Fatalf("active reference = %v, expired %v", err, tc.expired)
			}

			for range 2 {
				if err := store.Expire(t.Context(), slug); err != nil {
					t.Fatal(err)
				}
			}
			var deleted bool
			if err := conn.QueryRowContext(t.Context(), "SELECT deleted_at IS NOT NULL FROM "+tenantschema.Name(slug)+".files WHERE id = $1", row.ID).Scan(&deleted); err != nil || deleted != tc.expired {
				t.Fatalf("persisted expiry = %v, %v", deleted, err)
			}
		})
	}
}
