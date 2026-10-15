package wasm

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"testing"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/schema"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

type writeMetadata struct {
	createdAt time.Time
	updatedAt time.Time
	etag      string
}

func readWriteMetadata(t *testing.T, db *sql.DB, table, id string) writeMetadata {
	t.Helper()

	var got writeMetadata
	if err := db.QueryRowContext(t.Context(), "SELECT created_at, updated_at, etag FROM "+table+" WHERE id = $1", id).
		Scan(&got.createdAt, &got.updatedAt, &got.etag); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestORMWriteMetadata_WithoutAudit(t *testing.T) {
	for _, operation := range []string{"write", "write_many", "write_where", "upsert", "batch_upsert", "mutate", "unlink"} {
		t.Run(operation, func(t *testing.T) {
			f := newSoftDeleteFixture(t)
			table := quoteIdentORM("tenant_"+f.ctx.TenantSlug) + ".soft_item"
			if _, err := f.db.ExecContext(t.Context(), "UPDATE "+table+" SET created_at = '2000-01-01', updated_at = '2000-01-01'"); err != nil {
				t.Fatal(err)
			}
			before := readWriteMetadata(t, f.db, table, f.active[0])
			record := map[string]any{"name": "Updated", "code": "0"}
			conflict := &abiv1.ORMOnConflict{Fields: []string{"code"}, Policy: "update"}
			var hostErr *abiv1.HostError
			switch operation {
			case "write":
				_, hostErr = ORMWrite(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.ctx, abiv1.ORMWriteInput{
					Model:  softDeleteModel,
					ID:     f.active[0],
					Record: record,
				})
			case "write_many":
				_, hostErr = ORMWriteMany(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), f.ctx, abiv1.ORMWriteManyInput{
					Model:  softDeleteModel,
					IDs:    []string{f.active[0]},
					Record: record,
				})
			case "write_where":
				_, hostErr = ORMWriteWhere(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), f.ctx, abiv1.ORMWriteWhereInput{
					Model:  softDeleteModel,
					Domain: "record.code = '0'",
					Record: record,
				})
			case "upsert":
				_, hostErr = ORMCreate(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.ctx, abiv1.ORMCreateInput{
					Model:      softDeleteModel,
					Record:     record,
					OnConflict: conflict,
				})
			case "batch_upsert":
				_, hostErr = ORMCreateBatch(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), f.ctx, abiv1.ORMCreateBatchInput{
					Model:      softDeleteModel,
					Records:    []map[string]any{record},
					OnConflict: conflict,
				})
			case "mutate":
				_, hostErr = ORMMutate(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), f.ctx, abiv1.ORMMutateInput{
					Model: softDeleteModel,
					ID:    f.active[0],
					Ops:   []abiv1.ORMMutateOp{{Field: "amount", Delta: 1}},
				})
			case "unlink":
				_, hostErr = ORMUnlink(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.ctx, abiv1.ORMUnlinkInput{
					Model: softDeleteModel,
					IDs:   []string{f.active[0]},
				})
			}
			if hostErr != nil {
				t.Fatal(hostErr)
			}
			after := readWriteMetadata(t, f.db, table, f.active[0])
			if !after.updatedAt.After(before.updatedAt) || !after.createdAt.Equal(before.createdAt) || after.etag == before.etag {
				t.Fatalf("metadata before = %+v, after = %+v; want advanced updated_at and etag with unchanged created_at", before, after)
			}
		})
	}
}

func TestORMWriteMetadata_RelationSignalAndRollback(t *testing.T) {
	db, r, mc := m2mVersionFixture(t)
	id := m2mCreate(t, db, r, mc, []string{m2mTag1})
	table := quoteIdentORM("tenant_"+mc.TenantSlug) + ".contacts"
	before := readWriteMetadata(t, db, table, id)

	const txID = "m2m-version-rollback"
	tx := registerTenantScopedTestTx(t, t.Context(), db, mc, txID)
	input := abiv1.ORMWriteInput{
		Model:  "testmodule.contact",
		ID:     id,
		Record: map[string]any{"tag_ids": []string{m2mTag2}},
		TxID:   txID,
	}
	changed, hostErr := ORMWrite(t.Context(), r, db, r.EventInsertClient(), nil, mc, input)
	if hostErr != nil {
		t.Fatal(hostErr)
	}
	input.Record = map[string]any{"name": "Kofi"}
	unchanged, hostErr := ORMWrite(t.Context(), r, db, r.EventInsertClient(), nil, mc, input)
	if hostErr != nil {
		t.Fatal(hostErr)
	}
	if unchanged.Record["etag"] != changed.Record["etag"] {
		t.Fatal("relation-change signal leaked to a subsequent no-op in the same transaction")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if after := readWriteMetadata(t, db, table, id); after != before {
		t.Fatalf("rollback metadata: before = %+v, after = %+v", before, after)
	}
	if ids := m2mRead(t, db, mc, id)["tag_ids"].([]string); !slices.Equal(ids, []string{m2mTag1}) {
		t.Fatalf("rollback links = %v, want original set", ids)
	}
}

func m2mVersionFixture(t *testing.T) (*sql.DB, *Runtime, *ModuleContext) {
	t.Helper()

	db, r, mc := m2mFixture(t)
	decls := slices.Clone(mc.ModelDecls())
	decls[0].Fields = append(slices.Clone(decls[0].Fields),
		model.NamedField{Name: "etag", Def: model.Text().Readonly()},
		model.NamedField{Name: "created_at", Def: model.TimestampTZ().Readonly()},
		model.NamedField{Name: "updated_at", Def: model.TimestampTZ().Readonly()},
	)
	if _, err := db.ExecContext(t.Context(), "ALTER TABLE "+quoteIdentORM("tenant_"+mc.TenantSlug)+`.contacts
		ADD COLUMN etag TEXT NOT NULL DEFAULT '',
		ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()`); err != nil {
		t.Fatal(err)
	}
	pool := schema.NewPool(db, 5*time.Second)
	if err := pool.Bootstrap(t.Context()); err != nil {
		t.Fatal(err)
	}
	sess, err := pool.BeginSync(t.Context(), "44444444-4444-4444-4444-444444444444", mc.TenantSlug, "testmodule", &manifest.Manifest{Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close(context.Background()) })
	if err := schema.NewSchemaDiffEngine(&schema.Config{}).SyncEtagTriggers(t.Context(), sess, decls, []manifest.AuditedTable{{Table: "contacts"}}); err != nil {
		t.Fatal(err)
	}
	return db, r, m2mContext(mc.TenantSlug, decls)
}

func TestORMWriteMetadata_AuditedMany2Many(t *testing.T) {
	for _, operation := range []string{"write", "write_many", "write_where", "upsert", "batch_upsert"} {
		t.Run(operation, func(t *testing.T) {
			db, r, mc := m2mVersionFixture(t)
			id := m2mCreate(t, db, r, mc, []string{m2mTag1})
			table := quoteIdentORM("tenant_"+mc.TenantSlug) + ".contacts"
			before := readWriteMetadata(t, db, table, id)
			write := func(ids []string) {
				t.Helper()
				record := map[string]any{"tag_ids": ids}
				conflict := &abiv1.ORMOnConflict{Fields: []string{"id"}, Policy: "update"}
				var returned map[string]any
				var hostErr *abiv1.HostError
				switch operation {
				case "write":
					var out abiv1.ORMWriteOutput
					out, hostErr = ORMWrite(t.Context(), r, db, r.EventInsertClient(), nil, mc, abiv1.ORMWriteInput{
						Model:  "testmodule.contact",
						ID:     id,
						Record: record,
					})
					returned = out.Record
				case "write_many":
					_, hostErr = ORMWriteMany(t.Context(), r, db, r.EventInsertClient(), mc, abiv1.ORMWriteManyInput{
						Model:  "testmodule.contact",
						IDs:    []string{id},
						Record: record,
					})
				case "write_where":
					_, hostErr = ORMWriteWhere(t.Context(), r, db, r.EventInsertClient(), mc, abiv1.ORMWriteWhereInput{
						Model:  "testmodule.contact",
						Domain: fmt.Sprintf("record.id = '%s'", id),
						Record: record,
					})
				case "upsert", "batch_upsert":
					record["id"] = id
					record["name"] = "Kofi"
					if operation == "upsert" {
						var out abiv1.ORMCreateOutput
						out, hostErr = ORMCreate(t.Context(), r, db, r.EventInsertClient(), nil, mc, abiv1.ORMCreateInput{
							Model:      "testmodule.contact",
							Record:     record,
							OnConflict: conflict,
						})
						returned = out.Record
					} else {
						var out abiv1.ORMCreateBatchOutput
						out, hostErr = ORMCreateBatch(t.Context(), r, db, r.EventInsertClient(), mc, abiv1.ORMCreateBatchInput{
							Model:      "testmodule.contact",
							Records:    []map[string]any{record},
							OnConflict: conflict,
						})
						if len(out.Records) == 1 {
							returned = out.Records[0]
						}
					}
				}
				if hostErr != nil {
					t.Fatal(hostErr)
				}
				stored := readWriteMetadata(t, db, table, id)
				if returned != nil && returned["etag"] != stored.etag {
					t.Fatalf("returned etag = %v, stored = %s", returned["etag"], stored.etag)
				}
			}

			write([]string{m2mTag1, m2mTag1})
			if unchanged := readWriteMetadata(t, db, table, id); unchanged != before {
				t.Fatalf("unchanged links rotated metadata: before = %+v, after = %+v", before, unchanged)
			}
			write([]string{m2mTag2, m2mTag1})
			after := readWriteMetadata(t, db, table, id)
			if after.etag == before.etag || !after.updatedAt.After(before.updatedAt) || !after.createdAt.Equal(before.createdAt) {
				t.Fatalf("link change metadata: before = %+v, after = %+v", before, after)
			}
			write([]string{m2mTag1, m2mTag2, m2mTag1})
			if unchanged := readWriteMetadata(t, db, table, id); unchanged != after {
				t.Fatalf("reordered links rotated metadata: before = %+v, after = %+v", after, unchanged)
			}
			_, hostErr := ORMWrite(t.Context(), r, db, r.EventInsertClient(), nil, mc, abiv1.ORMWriteInput{
				Model:        "testmodule.contact",
				ID:           id,
				Record:       map[string]any{"name": "Stale"},
				ExpectedEtag: new(before.etag),
			})
			if hostErr == nil || hostErr.Code != abiv1.ErrCodeEtagMismatch {
				t.Fatalf("stale write error = %v, want etag mismatch", hostErr)
			}
			write([]string{})
			if cleared := readWriteMetadata(t, db, table, id); cleared.etag == after.etag || !cleared.updatedAt.After(after.updatedAt) {
				t.Fatalf("cleared links did not rotate metadata: before = %+v, after = %+v", after, cleared)
			}
		})
	}
}
