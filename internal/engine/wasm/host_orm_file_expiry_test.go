package wasm

import (
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

func TestORMFileUnavailableObjects(t *testing.T) {
	for _, reason := range []string{"expired", "missing"} {
		t.Run(reason, func(t *testing.T) {
			f := newFileReferenceFixture(t)
			id := f.create(t)
			if reason == "expired" {
				query := "UPDATE " + tenantschema.Name(f.mc.TenantSlug) + ".files SET purpose = 'reports', created_at = NOW() - INTERVAL '8 days' WHERE id = $1"
				if _, err := f.db.ExecContext(t.Context(), query, f.privateID); err != nil {
					t.Fatal(err)
				}
			} else {
				file, err := f.store.GetByID(t.Context(), f.mc.TenantSlug, f.privateID)
				if err != nil {
					t.Fatal(err)
				}
				if err := f.backend.Delete(t.Context(), file.StorageKey); err != nil {
					t.Fatal(err)
				}
			}

			row := f.read(t, id, "signed_pdf_id")
			if row["signed_pdf"] != nil || row["signed_pdf_id"] != f.privateID {
				t.Fatalf("unavailable file relation = %+v", row)
			}
			if reason == "expired" && f.backend.calls != 0 {
				t.Fatal("expired relation reached storage backend")
			}
		})
	}
}
