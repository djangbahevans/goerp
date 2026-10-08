package wasm

import (
	"database/sql"
	"fmt"
	"slices"
	"testing"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/computed"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

const softDeleteModel = "testmodule.soft_item"

type softDeleteFixture struct {
	db      *sql.DB
	runtime *Runtime
	ctx     *ModuleContext
	active  []string
	deleted string
}

func newSoftDeleteFixture(t *testing.T) softDeleteFixture {
	t.Helper()

	db := openTestPrimaryDB(t)
	slug := fmt.Sprintf("ormsoft%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, db, slug)

	decl := *model.Define("soft_item").WithStandardFields().
		Field("name", model.Text()).
		Field("amount", model.Integer()).
		Field("code", model.Text()).
		Index("soft_item_code", model.BTreeIndex("code").Unique())
	mc, _ := newORMWriteTestModuleContext(slug, []model.ModelDeclaration{decl})

	_, err := db.ExecContext(t.Context(), `CREATE TABLE tenant_`+slug+`.soft_item (
		id UUID PRIMARY KEY DEFAULT uuidv7(), tenant_id UUID NOT NULL,
		created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW(),
		deleted_at TIMESTAMPTZ, created_by UUID, etag TEXT DEFAULT '',
		name TEXT, amount INTEGER, code TEXT UNIQUE
	)`)
	if err != nil {
		t.Fatal(err)
	}

	ids := []string{
		"00000000-0000-0000-0000-000000000001",
		"00000000-0000-0000-0000-000000000002",
		"00000000-0000-0000-0000-000000000003",
	}
	for i, id := range ids {
		_, err := db.ExecContext(t.Context(), `INSERT INTO tenant_`+slug+`.soft_item
			(id, tenant_id, name, amount, code) VALUES ($1, $2, 'item', $3, $4)`, id, mc.TenantID, (i+1)*10, fmt.Sprint(i))
		if err != nil {
			t.Fatal(err)
		}
	}

	rt := newComputeTestRuntime(t, db)
	_, hostErr := ORMUnlink(t.Context(), rt, db, rt.EventInsertClient(), nil, mc, abiv1.ORMUnlinkInput{
		Model: softDeleteModel, IDs: []string{ids[1]},
	})
	if hostErr != nil {
		t.Fatal(hostErr)
	}

	return softDeleteFixture{db: db, runtime: rt, ctx: mc, active: []string{ids[0], ids[2]}, deleted: ids[1]}
}

func requireSoftDeleteError(t *testing.T, err *abiv1.HostError, code string) {
	t.Helper()
	if err == nil || err.Code != code {
		t.Fatalf("error = %v, want %s", err, code)
	}
}

func TestORMSoftDelete_ReadsPaginationAndAggregates(t *testing.T) {
	f := newSoftDeleteFixture(t)

	for _, domain := range []string{"", "record.amount = 10 OR record.amount = 20 OR record.amount = 30", "record.deleted_at IS NOT NULL"} {
		out, err := ORMSearch(t.Context(), f.db, f.ctx, abiv1.ORMSearchInput{Model: softDeleteModel, Domain: domain, Order: "id"})
		if err != nil {
			t.Fatal(err)
		}

		want := f.active
		if domain == "record.deleted_at IS NOT NULL" {
			want = nil
		}
		if !slices.Equal(out.IDs, want) || out.Count != int64(len(want)) {
			t.Fatalf("domain %q: search = %+v, want IDs %v", domain, out, want)
		}
	}

	read, err := ORMRead(t.Context(), f.db, nil, f.ctx, abiv1.ORMReadInput{
		Model: softDeleteModel, IDs: []string{f.active[0], f.deleted, f.active[1]},
	})
	if err != nil || len(read.Records) != 2 {
		t.Fatalf("read = %+v, error = %v", read, err)
	}

	cursor := ""
	for _, id := range f.active {
		page, err := ORMSearchRead(t.Context(), f.db, f.ctx, abiv1.ORMSearchReadInput{
			Model: softDeleteModel, Cursor: cursor, Limit: 1,
		})
		if err != nil || len(page.Records) != 1 || page.Records[0]["id"] != id || page.NextCursor != id {
			t.Fatalf("page = %+v, error = %v, want %s", page, err, id)
		}

		cursor = page.NextCursor
	}

	agg, err := ORMAggregate(t.Context(), f.db, f.ctx, abiv1.ORMAggregateInput{
		Model: softDeleteModel, Values: []abiv1.ORMAggregateValue{{Aggregation: "count"}, {Field: "amount", Aggregation: "sum"}},
	})
	if err != nil || agg.Values["_count"] != int64(2) || agg.Values["amount_sum"] != int64(40) {
		t.Fatalf("aggregate = %+v, error = %v", agg, err)
	}

	pivot, err := ORMPivot(t.Context(), f.db, f.ctx, ORMPivotInput{
		Model: softDeleteModel, Rows: []string{"name"}, Values: []PivotValue{{Field: "amount", Aggregation: "sum"}},
	})
	if err != nil || len(pivot.Cells) != 2 {
		t.Fatalf("pivot = %+v, error = %v", pivot, err)
	}
	for _, cell := range pivot.Cells {
		if cell["values"].(map[string]any)["amount_sum"] != int64(40) {
			t.Fatalf("pivot cell = %+v, want sum 40", cell)
		}
	}
}

func TestORMSoftDelete_WritesTreatDeletedIDsAsMissing(t *testing.T) {
	f := newSoftDeleteFixture(t)
	rt, db, mc := f.runtime, f.db, f.ctx

	for _, etag := range []*string{nil, new(""), new("stale")} {
		_, err := ORMWrite(t.Context(), rt, db, rt.EventInsertClient(), nil, mc, abiv1.ORMWriteInput{
			Model: softDeleteModel, ID: f.deleted, Record: map[string]any{"name": "changed"}, ExpectedEtag: etag,
		})
		requireSoftDeleteError(t, err, abiv1.ErrCodeNotFound)
	}

	for _, guard := range []string{"", "record.amount > 999"} {
		_, err := ORMMutate(t.Context(), rt, db, rt.EventInsertClient(), mc, abiv1.ORMMutateInput{
			Model: softDeleteModel, ID: f.deleted, Guard: guard, Ops: []abiv1.ORMMutateOp{{Field: "amount", Delta: int64(1)}},
		})
		requireSoftDeleteError(t, err, abiv1.ErrCodeNotFound)
	}

	_, err := ORMWriteMany(t.Context(), rt, db, rt.EventInsertClient(), mc, abiv1.ORMWriteManyInput{
		Model: softDeleteModel, IDs: []string{f.active[0], f.deleted}, Record: map[string]any{"name": "changed"},
	})
	requireSoftDeleteError(t, err, abiv1.ErrCodeNotFound)

	_, err = ORMUnlink(t.Context(), rt, db, rt.EventInsertClient(), nil, mc, abiv1.ORMUnlinkInput{
		Model: softDeleteModel, IDs: []string{f.active[0], f.deleted},
	})
	requireSoftDeleteError(t, err, abiv1.ErrCodeNotFound)

	var name string
	var deleted sql.NullTime
	if err := db.QueryRowContext(t.Context(), `SELECT name, deleted_at FROM tenant_`+mc.TenantSlug+`.soft_item WHERE id = $1`, f.active[0]).Scan(&name, &deleted); err != nil {
		t.Fatal(err)
	}
	if name != "item" || deleted.Valid {
		t.Fatalf("failed batch changed the active row: name=%s, deleted=%v", name, deleted)
	}

	result, err := ORMWriteWhere(t.Context(), rt, db, rt.EventInsertClient(), mc, abiv1.ORMWriteWhereInput{
		Model: softDeleteModel, Domain: "record.amount = 10 OR record.amount = 20 OR record.amount = 30", Record: map[string]any{"name": "changed"},
	})
	if err != nil || result.Count != 2 {
		t.Fatalf("write_where = %+v, error = %v", result, err)
	}
	if err := db.QueryRowContext(t.Context(), `SELECT name FROM tenant_`+mc.TenantSlug+`.soft_item WHERE id = $1`, f.deleted).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "item" {
		t.Fatal("write_where changed the deleted record")
	}
}

func TestORMSoftDelete_RelationExpansionHidesDeletedTarget(t *testing.T) {
	f := newSoftDeleteFixture(t)
	child := *model.Define("child").Field("id", model.UUID().PrimaryKey()).Field("parent_id", model.Many2One(softDeleteModel))
	f.ctx.snapshot.ModelDecls = append(f.ctx.snapshot.ModelDecls, child)
	_, err := f.db.ExecContext(t.Context(), `CREATE TABLE tenant_`+f.ctx.TenantSlug+`.child (id UUID PRIMARY KEY, parent_id UUID);
		INSERT INTO tenant_`+f.ctx.TenantSlug+`.child VALUES ('00000000-0000-0000-0000-000000000004', '`+f.deleted+`')`)
	if err != nil {
		t.Fatal(err)
	}

	out, hostErr := ORMSearchRead(t.Context(), f.db, f.ctx, abiv1.ORMSearchReadInput{Model: "testmodule.child"})
	if hostErr != nil || len(out.Records) != 1 || out.Records[0]["parent_id"] != f.deleted || out.Records[0]["parent"] != nil {
		t.Fatalf("relation read = %+v, error = %v", out, hostErr)
	}
}

func TestORMSoftDelete_CreateDoesNotReturnOrUpdateDeletedRows(t *testing.T) {
	f := newSoftDeleteFixture(t)
	_, err := ORMCreate(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.ctx, abiv1.ORMCreateInput{
		Model: softDeleteModel, Record: map[string]any{"code": "1", "name": "changed"},
		OnConflict: &abiv1.ORMOnConflict{Fields: []string{"code"}, Policy: "update"},
	})
	requireSoftDeleteError(t, err, abiv1.ErrCodeNotFound)

	_, err = ORMFirstOrCreate(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), f.ctx, abiv1.ORMFirstOrCreateInput{
		Model: softDeleteModel, UniqueVals: map[string]any{"code": "1"}, CreateVals: map[string]any{"name": "new"},
	})
	requireSoftDeleteError(t, err, abiv1.ErrCodeUniqueViolation)

	var name string
	if err := f.db.QueryRowContext(t.Context(), `SELECT name FROM tenant_`+f.ctx.TenantSlug+`.soft_item WHERE id = $1`, f.deleted).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "item" {
		t.Fatal("upsert changed a deleted record")
	}
}

func TestORMSoftDelete_TreeParentsAndDeletedDescendants(t *testing.T) {
	f := newSoftDeleteFixture(t)
	md := categoryModelDecl()
	md.Fields = append(md.Fields, model.NamedField{Name: "deleted_at", Def: model.TimestampTZ().Readonly()})
	f.ctx.snapshot.ModelDecls = append(f.ctx.snapshot.ModelDecls, md)
	createFixtureCategoryTable(t, f.db, f.ctx.TenantSlug)
	if _, err := f.db.ExecContext(t.Context(), `ALTER TABLE tenant_`+f.ctx.TenantSlug+`.category ADD COLUMN deleted_at TIMESTAMPTZ`); err != nil {
		t.Fatal(err)
	}

	create := func(parent any) string {
		t.Helper()
		out, err := ORMCreate(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.ctx, abiv1.ORMCreateInput{
			Model: "testmodule.category", Record: map[string]any{"parent_id": parent, "name": "category"},
		})
		if err != nil {
			t.Fatal(err)
		}
		return out.Record["id"].(string)
	}
	root := create(nil)
	child := create(root)
	deletedChild := create(child)
	oldPath := categoryPath(t, f.db, f.ctx.TenantSlug, deletedChild)

	_, err := ORMUnlink(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.ctx, abiv1.ORMUnlinkInput{
		Model: "testmodule.category", IDs: []string{deletedChild},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = ORMWrite(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.ctx, abiv1.ORMWriteInput{
		Model: "testmodule.category", ID: child, Record: map[string]any{"parent_id": nil},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := categoryPath(t, f.db, f.ctx.TenantSlug, deletedChild); got != oldPath {
		t.Fatalf("deleted descendant path = %q, want unchanged %q", got, oldPath)
	}

	_, err = ORMCreate(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.ctx, abiv1.ORMCreateInput{
		Model: "testmodule.category", Record: map[string]any{"parent_id": deletedChild},
	})
	requireSoftDeleteError(t, err, abiv1.ErrCodeForeignKeyViolation)

	_, err = ORMWrite(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.ctx, abiv1.ORMWriteInput{
		Model: "testmodule.category", ID: child, Record: map[string]any{"parent_id": deletedChild},
	})
	requireSoftDeleteError(t, err, abiv1.ErrCodeForeignKeyViolation)
	if got := categoryPath(t, f.db, f.ctx.TenantSlug, child); got != ltreeLabel(child) {
		t.Fatalf("rejected parent changed child path to %q", got)
	}
}

func TestORMSoftDelete_DynamicLinkRejectsDeletedTarget(t *testing.T) {
	f := newSoftDeleteFixture(t)
	md := *model.Define("comment").WithStandardFields().
		Field("reference_type", model.Selection(softDeleteModel)).
		Field("reference_id", model.DynamicLink("reference_type"))
	f.ctx.snapshot.ModelDecls = append(f.ctx.snapshot.ModelDecls, md)
	f.ctx.snapshot.ComputeTargets = map[string]ComputeTarget{"testmodule": {ModelDecls: f.ctx.snapshot.ModelDecls}}

	_, err := ORMCreate(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.ctx, abiv1.ORMCreateInput{
		Model: "testmodule.comment", Record: map[string]any{"reference_type": softDeleteModel, "reference_id": f.deleted},
	})
	requireSoftDeleteError(t, err, abiv1.ErrCodeDynamicLinkTargetNotFound)
}

func TestORMSoftDelete_ModelWithoutDeletedAtRetainsCRUD(t *testing.T) {
	f := newSoftDeleteFixture(t)
	f.ctx.snapshot.ModelDecls = append(f.ctx.snapshot.ModelDecls, hardDeleteItemModelDecl())
	createFixtureHardItemsTable(t, f.db, f.ctx.TenantSlug)
	id := f.deleted
	if _, err := f.db.ExecContext(t.Context(), `INSERT INTO tenant_`+f.ctx.TenantSlug+`.hard_item (id, name) VALUES ($1, 'Original')`, id); err != nil {
		t.Fatal(err)
	}

	read, err := ORMRead(t.Context(), f.db, nil, f.ctx, abiv1.ORMReadInput{Model: "testmodule.hard_item", IDs: []string{id}})
	if err != nil || len(read.Records) != 1 {
		t.Fatalf("read = %+v, error = %v", read, err)
	}
	write, err := ORMWrite(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.ctx, abiv1.ORMWriteInput{
		Model: "testmodule.hard_item", ID: id, Record: map[string]any{"name": "Changed"},
	})
	if err != nil || write.Record["name"] != "Changed" {
		t.Fatalf("write = %+v, error = %v", write, err)
	}

	_, err = ORMUnlink(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.ctx, abiv1.ORMUnlinkInput{
		Model: "testmodule.hard_item", IDs: []string{id},
	})
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM tenant_`+f.ctx.TenantSlug+`.hard_item WHERE id = $1`, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("unlink retained a row on a model without deleted_at")
	}
}

func TestORMSoftDelete_ChildChangesRecomputeDeletedComputedParent(t *testing.T) {
	f := newSoftDeleteFixture(t)
	parent := lineOrderModelDecl()
	parent.Fields = append(parent.Fields, model.NamedField{Name: "deleted_at", Def: model.TimestampTZ().Readonly()})
	decls := []model.ModelDeclaration{parent, orderLineFixtureModelDecl()}
	f.ctx.snapshot.ModelDecls = append(f.ctx.snapshot.ModelDecls, decls...)
	idx := computed.New()
	idx.Register("testmodule", decls)
	f.ctx.snapshot.ComputedIndex = idx
	f.ctx.snapshot.ComputeTargets = map[string]ComputeTarget{"testmodule": newComputeTarget(t, t.Context(), f.runtime, decls)}
	createFixtureLineOrderTables(t, f.db, f.ctx.TenantSlug)

	_, sqlErr := f.db.ExecContext(t.Context(), `ALTER TABLE tenant_`+f.ctx.TenantSlug+`.line_order ADD COLUMN deleted_at TIMESTAMPTZ;
		INSERT INTO tenant_`+f.ctx.TenantSlug+`.line_order (id, tenant_id, lines_total, deleted_at)
		VALUES ('`+f.deleted+`', '`+f.ctx.TenantID+`', 42, NOW())`)
	if sqlErr != nil {
		t.Fatal(sqlErr)
	}

	out, err := ORMCreate(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.ctx, abiv1.ORMCreateInput{
		Model: "testmodule.order_line", Record: map[string]any{"id": f.active[0], "order_id": f.deleted, "quantity": int64(1)},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ORMWrite(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.ctx, abiv1.ORMWriteInput{
		Model: "testmodule.order_line", ID: out.Record["id"].(string), Record: map[string]any{"quantity": int64(2)},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ORMUnlink(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.ctx, abiv1.ORMUnlinkInput{
		Model: "testmodule.order_line", IDs: []string{out.Record["id"].(string)},
	})
	if err != nil {
		t.Fatal(err)
	}

	var total int64
	if err := f.db.QueryRowContext(t.Context(), `SELECT lines_total FROM tenant_`+f.ctx.TenantSlug+`.line_order WHERE id = $1`, f.deleted).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("deleted parent total = %d, want recomputed marker 1", total)
	}
}

func TestORMSoftDelete_RelatedChangesRecomputeDeletedComputedDependents(t *testing.T) {
	f := newSoftDeleteFixture(t)
	dependent := hopOrderModelDecl()
	dependent.Fields = append(dependent.Fields, model.NamedField{Name: "deleted_at", Def: model.TimestampTZ().Readonly()})
	decls := []model.ModelDeclaration{contactModelDecl(), dependent}
	f.ctx.snapshot.ModelDecls = append(f.ctx.snapshot.ModelDecls, decls...)
	idx := computed.New()
	idx.Register("testmodule", decls)
	f.ctx.snapshot.ComputedIndex = idx
	f.ctx.snapshot.ComputeTargets = map[string]ComputeTarget{"testmodule": newComputeTarget(t, t.Context(), f.runtime, decls)}
	createFixtureContactAndHopOrderTables(t, f.db, f.ctx.TenantSlug)

	_, sqlErr := f.db.ExecContext(t.Context(), `ALTER TABLE tenant_`+f.ctx.TenantSlug+`.hop_order ADD COLUMN deleted_at TIMESTAMPTZ;
		INSERT INTO tenant_`+f.ctx.TenantSlug+`.contact (id, tenant_id, credit_limit)
		VALUES ('`+f.active[0]+`', '`+f.ctx.TenantID+`', 10);
		INSERT INTO tenant_`+f.ctx.TenantSlug+`.hop_order (id, tenant_id, customer_id, touched_flag, deleted_at)
		VALUES ('`+f.active[1]+`', '`+f.ctx.TenantID+`', '`+f.active[0]+`', 42, NULL),
		('`+f.deleted+`', '`+f.ctx.TenantID+`', '`+f.active[0]+`', 42, NOW())`)
	if sqlErr != nil {
		t.Fatal(sqlErr)
	}

	_, err := ORMWrite(t.Context(), f.runtime, f.db, f.runtime.EventInsertClient(), nil, f.ctx, abiv1.ORMWriteInput{
		Model: "testmodule.contact", ID: f.active[0], Record: map[string]any{"credit_limit": int64(20)},
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		id   string
		want int64
	}{
		{f.active[1], 1},
		{f.deleted, 1},
	} {
		var marker int64
		if err := f.db.QueryRowContext(t.Context(), `SELECT touched_flag FROM tenant_`+f.ctx.TenantSlug+`.hop_order WHERE id = $1`, tt.id).Scan(&marker); err != nil {
			t.Fatal(err)
		}
		if marker != tt.want {
			t.Fatalf("dependent %s marker = %d, want %d", tt.id, marker, tt.want)
		}
	}
}
