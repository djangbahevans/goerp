package wasm

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/fieldsec"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/djangbahevans/goerp/sdk/go/perm"
)

const dynamicReadPerm = "testmodule:comment:restricted_read"

func dynamicReadID(n int) string {
	return fmt.Sprintf("00000000-0000-0000-0000-%012d", n)
}

type dynamicReadFixture struct {
	db      *sql.DB
	slug    string
	comment model.ModelDeclaration
	order   model.ModelDeclaration
	contact model.ModelDeclaration
}

func newDynamicReadFixture(t *testing.T) *dynamicReadFixture {
	t.Helper()
	conn := openTestPrimaryDB(t)
	slug := fmt.Sprintf("dynamicread%d", time.Now().UnixNano())
	createFixtureTenantSchema(t, conn, slug)
	schema := "tenant_" + slug

	for _, stmt := range []string{
		`CREATE TABLE ` + schema + `.comments (id UUID PRIMARY KEY, reference_type TEXT, reference_id UUID)`,
		`CREATE TABLE ` + schema + `.orders (id UUID PRIMARY KEY, display_name TEXT, deleted_at TIMESTAMPTZ)`,
		`CREATE TABLE ` + schema + `.contacts (id UUID PRIMARY KEY, display_name TEXT, deleted_at TIMESTAMPTZ)`,
		`INSERT INTO ` + schema + `.orders VALUES ('` + dynamicReadID(101) + `', 'Order', NULL)`,
		`INSERT INTO ` + schema + `.contacts VALUES ('` + dynamicReadID(101) + `', 'Contact', NULL), ('` + dynamicReadID(102) + `', 'Deleted', NOW()), ('` + dynamicReadID(103) + `', 'Hidden', NULL)`,
		`INSERT INTO ` + schema + `.comments VALUES
			('` + dynamicReadID(1) + `', 'testmodule.order', '` + dynamicReadID(101) + `'),
			('` + dynamicReadID(2) + `', 'contacts.contact', '` + dynamicReadID(101) + `'),
			('` + dynamicReadID(3) + `', 'contacts.contact', NULL),
			('` + dynamicReadID(4) + `', 'contacts.contact', '` + dynamicReadID(999) + `'),
			('` + dynamicReadID(5) + `', 'contacts.contact', '` + dynamicReadID(102) + `'),
			('` + dynamicReadID(6) + `', 'absent.target', '` + dynamicReadID(101) + `'),
			('` + dynamicReadID(7) + `', 'contacts.missing', '` + dynamicReadID(101) + `'),
			('` + dynamicReadID(8) + `', NULL, '` + dynamicReadID(101) + `'),
			('` + dynamicReadID(9) + `', 'contacts.contact', '` + dynamicReadID(103) + `')`,
	} {
		if _, err := conn.ExecContext(t.Context(), stmt); err != nil {
			t.Fatalf("create DynamicLink fixture: %v", err)
		}
	}

	return &dynamicReadFixture{
		db:   conn,
		slug: slug,
		comment: model.ModelDeclaration{
			Name:  "comment",
			Table: "comments",
			Fields: []model.NamedField{
				{Name: "id", Def: model.UUID().PrimaryKey()},
				{Name: "reference_type", Def: model.Selection("testmodule.order", "contacts.contact", "absent.target", "contacts.missing")},
				{Name: "reference_id", Def: model.DynamicLink("reference_type")},
			},
		},
		order: model.ModelDeclaration{
			Name:  "order",
			Table: "orders",
			Fields: []model.NamedField{
				{Name: "id", Def: model.UUID().PrimaryKey()},
				{Name: "display_name", Def: model.Text()},
				{Name: "deleted_at", Def: model.TimestampTZ()},
			},
		},
		contact: model.ModelDeclaration{
			Name:  "contact",
			Table: "contacts",
			Fields: []model.NamedField{
				{Name: "id", Def: model.UUID().PrimaryKey()},
				{Name: "display_name", Def: model.Text()},
				{Name: "deleted_at", Def: model.TimestampTZ()},
			},
		},
	}
}

func (f *dynamicReadFixture) moduleContext(granted bool) *ModuleContext {
	fieldReg := fieldsec.New()
	fieldReg.Register("testmodule", []model.ModelDeclaration{f.comment, f.order})
	fieldReg.Register("contacts", []model.ModelDeclaration{f.contact})

	permReg := permission.NewPermissionRegistry()
	permReg.Register("testmodule", []manifest.Permission{{Name: dynamicReadPerm}})

	var permissions permission.PermissionBitfield
	if granted {
		idx, _ := permReg.Index(dynamicReadPerm)
		permissions.Set(idx)
	}

	return NewModuleContext("req", "testmodule", "user", "contact", nil, permissions,
		"tenant", f.slug, "trace", abi.CapDBRead, nil, ModuleSnapshot{
			ModelDecls: []model.ModelDeclaration{f.comment, f.order},
			ComputeTargets: map[string]ComputeTarget{
				"contacts": {ModelDecls: []model.ModelDeclaration{f.contact}},
			},
			FieldSecRegistry:   fieldReg,
			PermissionRegistry: permReg,
		})
}

func dynamicHostRecords(t *testing.T, conn *sql.DB, mc *ModuleContext, operation string, fields []string) []map[string]any {
	t.Helper()
	r := newHostDBTestRuntime(t, conn, 10)
	inst := newHostORMCaller(t, t.Context(), r, mc)

	var out abiv1.ORMReadOutput
	var input any = abiv1.ORMSearchReadInput{Model: "testmodule.comment", Fields: fields, Order: "id"}
	if operation == "read" {
		ids := make([]string, 9)
		for i := range ids {
			ids[i] = dynamicReadID(i + 1)
		}
		input = abiv1.ORMReadInput{Model: "testmodule.comment", IDs: ids, Fields: fields}
	}

	env := callORMHost(t, t.Context(), inst, "call_"+operation, input, &out)
	if !env.OK {
		t.Fatalf("%s: %+v", operation, env.Error)
	}
	return out.Records
}

func TestHostORM_DynamicLinkRead_MixedTargets(t *testing.T) {
	f := newDynamicReadFixture(t)

	for _, operation := range []string{"read", "search_read"} {
		for _, fields := range [][]string{nil, {"id", "reference_id"}} {
			t.Run(fmt.Sprintf("%s/%v", operation, fields), func(t *testing.T) {
				records := dynamicHostRecords(t, f.db, f.moduleContext(false), operation, fields)
				if len(records) != 9 {
					t.Fatalf("got %d records, want 9", len(records))
				}
				for _, record := range records {
					id := record["id"]
					if _, present := record["reference_type"]; present != (fields == nil) {
						t.Errorf("discriminator projection: %+v", record)
					}
					if id == dynamicReadID(1) || id == dynamicReadID(2) || id == dynamicReadID(9) {
						want := "Contact"
						if id == dynamicReadID(1) {
							want = "Order"
						} else if id == dynamicReadID(9) {
							want = "Hidden"
						}
						reference, ok := record["reference"].(map[string]any)
						if !ok || reference["display_name"] != want || reference["id"] != record["reference_id"] {
							t.Errorf("expansion: %+v, want %s", record, want)
						}
					} else if value, present := record["reference"]; !present || value != nil {
						t.Errorf("unavailable target: %+v", record)
					}
					if raw, present := record["reference_id"]; !present || (id != dynamicReadID(3) && raw == nil) {
						t.Errorf("raw ID lost: %+v", record)
					}
				}
			})
		}
	}
}

func TestHostORM_DynamicLinkRead_FieldSecurity(t *testing.T) {
	tests := []struct {
		name        string
		denied      model.DeniedReadBehaviour
		wantPresent bool
		wantValue   any
	}{
		{name: "omit", denied: model.Omit},
		{
			name:        "nullify",
			denied:      model.Nullify,
			wantPresent: true,
		},
		{
			name:        "mask",
			denied:      model.Mask("****{last4}"),
			wantPresent: true,
			wantValue:   "****tact",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			denied := tt.denied
			f := newDynamicReadFixture(t)
			f.contact.Fields[0].Def = f.contact.Fields[0].Def.Access(model.AccessRead(perm.Ref(dynamicReadPerm))).OnDeniedRead(model.Omit)
			f.contact.Fields[1].Def = model.Text().Access(model.AccessRead(perm.Ref(dynamicReadPerm))).OnDeniedRead(denied)
			f.comment.Fields[1].Def = f.comment.Fields[1].Def.Access(model.AccessRead(perm.Ref(dynamicReadPerm))).OnDeniedRead(denied)

			for _, operation := range []string{"read", "search_read"} {
				records := dynamicHostRecords(t, f.db, f.moduleContext(false), operation, nil)
				for _, record := range records {
					if record["id"] != dynamicReadID(1) && record["id"] != dynamicReadID(2) {
						continue
					}

					reference, ok := record["reference"].(map[string]any)
					if !ok {
						t.Fatalf("masked discriminator blocked expansion: %+v", record)
					}

					value, present := reference["display_name"]
					if record["id"] == dynamicReadID(1) {
						if value != "Order" {
							t.Errorf("unrestricted target masked: %+v", reference)
						}
					} else {
						if present != tt.wantPresent || value != tt.wantValue {
							t.Errorf("target masking: %+v, want display_name %v (present=%v)", reference, tt.wantValue, tt.wantPresent)
						}
						discriminator, present := record["reference_type"]
						if present != tt.wantPresent || discriminator != tt.wantValue {
							t.Errorf("discriminator masking: %+v, want %v (present=%v)", record, tt.wantValue, tt.wantPresent)
						}
					}
					if reference["id"] != record["reference_id"] {
						t.Errorf("expanded primary key lost: %+v", reference)
					}
				}
			}

			parentDenied := denied
			if denied.Kind == model.ReadKindMask {
				parentDenied = model.Mask(dynamicReadID(101))
			}
			f.comment.Fields[2].Def = f.comment.Fields[2].Def.Access(model.AccessRead(perm.Ref(dynamicReadPerm))).OnDeniedRead(parentDenied)
			for _, operation := range []string{"read", "search_read"} {
				for _, record := range dynamicHostRecords(t, f.db, f.moduleContext(false), operation, nil) {
					if value, present := record["reference"]; present && value != nil {
						t.Errorf("denied parent field exposed expansion: %+v", record)
					}
				}
			}

			for _, opts := range [][]ORMReadOption{nil, {SkipFieldSecurity()}} {
				out, hostErr := ORMRead(t.Context(), f.db, nil, f.moduleContext(opts == nil), abiv1.ORMReadInput{
					Model:  "testmodule.comment",
					IDs:    []string{dynamicReadID(2)},
					Fields: []string{"reference_id"},
				}, opts...)
				if hostErr != nil {
					t.Fatalf("read: %+v", hostErr)
				}

				reference, ok := out.Records[0]["reference"].(map[string]any)
				if !ok || reference["display_name"] != "Contact" || len(out.Records[0]) != 2 {
					t.Fatalf("granted or bypass read: %+v", out.Records)
				}
			}
		})
	}
}

func TestHostORM_DynamicLinkRead_RLSAndTenantIsolation(t *testing.T) {
	f := newDynamicReadFixture(t)
	other := newDynamicReadFixture(t)
	schema := "tenant_" + f.slug
	for _, stmt := range []string{
		`DELETE FROM ` + schema + `.contacts WHERE id = '` + dynamicReadID(101) + `'`,
		`ALTER TABLE ` + schema + `.contacts ENABLE ROW LEVEL SECURITY`,
		`CREATE POLICY visible ON ` + schema + `.contacts FOR SELECT USING (id <> '` + dynamicReadID(103) + `'::uuid)`,
	} {
		if _, err := f.db.ExecContext(t.Context(), stmt); err != nil {
			t.Fatal(err)
		}
	}
	reader := openTestRLSReaderORM(t, f.db, schema, schema+".contacts")
	for _, table := range []string{"comments", "orders"} {
		if _, err := f.db.ExecContext(t.Context(), "GRANT SELECT ON "+schema+"."+table+" TO goerp_test_rls_reader_orm"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.db.ExecContext(t.Context(), "GRANT USAGE ON SCHEMA tenant_"+other.slug+" TO goerp_test_rls_reader_orm"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(t.Context(), "GRANT SELECT ON tenant_"+other.slug+".contacts TO goerp_test_rls_reader_orm"); err != nil {
		t.Fatal(err)
	}

	for _, operation := range []string{"read", "search_read"} {
		for _, record := range dynamicHostRecords(t, reader, f.moduleContext(false), operation, nil) {
			switch record["id"] {
			case dynamicReadID(2), dynamicReadID(9):
				if record["reference"] != nil || record["reference_id"] == nil {
					t.Errorf("tenant or RLS isolation failed: %+v", record)
				}
			case dynamicReadID(1):
				if record["reference"] == nil {
					t.Errorf("unrelated row lost expansion: %+v", record)
				}
			}
		}
	}
}

func TestHostORM_DynamicLinkRead_ProjectionAndTransaction(t *testing.T) {
	f := newDynamicReadFixture(t)
	mc := f.moduleContext(false)
	tx := registerTenantScopedTestTx(t, t.Context(), f.db, mc, "dynamic-read-tx")
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(t.Context(), `UPDATE contacts SET display_name = 'Pending' WHERE id = $1`, dynamicReadID(101)); err != nil {
		t.Fatal(err)
	}

	r := newHostDBTestRuntime(t, f.db, 10)
	inst := newHostORMCaller(t, t.Context(), r, mc)
	var read abiv1.ORMReadOutput
	env := callORMHost(t, t.Context(), inst, "call_read", abiv1.ORMReadInput{
		Model:  "testmodule.comment",
		IDs:    []string{dynamicReadID(2)},
		Fields: []string{"reference_id"},
		TxID:   "dynamic-read-tx",
	}, &read)
	if !env.OK || len(read.Records) != 1 {
		t.Fatalf("read: %+v, %+v", env.Error, read)
	}
	reference, ok := read.Records[0]["reference"].(map[string]any)
	if !ok || reference["display_name"] != "Pending" || len(read.Records[0]) != 2 {
		t.Fatalf("read projection or transaction visibility: %+v", read.Records)
	}

	var page abiv1.ORMSearchReadOutput
	env = callORMHost(t, t.Context(), inst, "call_search_read", abiv1.ORMSearchReadInput{
		Model:  "testmodule.comment",
		Fields: []string{"reference_id"},
		Limit:  1,
		Cursor: dynamicReadID(1),
		TxID:   "dynamic-read-tx",
	}, &page)
	if !env.OK || len(page.Records) != 1 || page.NextCursor != dynamicReadID(2) {
		t.Fatalf("search_read: %+v, %+v", env.Error, page)
	}
	reference, ok = page.Records[0]["reference"].(map[string]any)
	if !ok || reference["display_name"] != "Pending" || len(page.Records[0]) != 2 {
		t.Fatalf("search_read projection or transaction visibility: %+v", page.Records)
	}

	for _, operation := range []string{"read", "search_read"} {
		for _, record := range dynamicHostRecords(t, f.db, mc, operation, []string{"reference_type"}) {
			if len(record) != 1 {
				t.Errorf("unselected reference exposed: %+v", record)
			}
		}
	}

	var stored string
	if err := f.db.QueryRowContext(t.Context(), "SELECT display_name FROM tenant_"+f.slug+".contacts WHERE id = $1", dynamicReadID(101)).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != "Contact" {
		t.Fatalf("borrowed transaction committed: display_name = %q", stored)
	}
}
