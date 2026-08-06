package model

import (
	"fmt"
	"strings"
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

// columnDDL/columnType/indexDDL are test-only: they exist to prove the
// declaration types above capture everything a real DDL compiler would need
// (correct Postgres type, NOT NULL, DEFAULT, PRIMARY KEY, CHECK, index
// syntax), not as a production DDL generator. That's #19's job, once it
// exists — this only has to prove the data these tests exercise is enough
// to build it from.

func columnType(f FieldDef) string {
	switch f.Kind {
	case KindChar:
		if f.Length > 0 {
			return fmt.Sprintf("VARCHAR(%d)", f.Length)
		}
		return "TEXT"
	case KindText:
		return "TEXT"
	case KindInteger:
		return "INTEGER"
	case KindBigInt:
		return "BIGINT"
	case KindFloat:
		return "DOUBLE PRECISION"
	case KindDecimal:
		return fmt.Sprintf("NUMERIC(%d,%d)", f.Precision, f.Scale)
	case KindBoolean:
		return "BOOLEAN"
	case KindUUID:
		return "UUID"
	case KindTimestampTZ:
		return "TIMESTAMPTZ"
	case KindDate:
		return "DATE"
	case KindTime:
		return "TIME"
	case KindJSONB:
		return "JSONB"
	case KindBytea:
		return "BYTEA"
	case KindSelection:
		return "TEXT"
	case KindEnum:
		return f.EnumType
	default:
		panic(fmt.Sprintf("unknown field kind %d", f.Kind))
	}
}

func columnDDL(name string, f FieldDef) string {
	col := fmt.Sprintf("%q %s", name, columnType(f))
	if f.IsPrimaryKey {
		col += " PRIMARY KEY"
	}
	if f.IsRequired {
		col += " NOT NULL"
	}
	if f.DefaultExpr != nil {
		col += " DEFAULT " + *f.DefaultExpr
	}
	if f.Kind == KindSelection {
		quoted := make([]string, len(f.SelectionValues))
		for i, v := range f.SelectionValues {
			quoted[i] = "'" + v + "'"
		}
		col += fmt.Sprintf(" CHECK (%q IN (%s))", name, strings.Join(quoted, ", "))
	}
	return col
}

func indexDDL(table, name string, idx IndexDef) string {
	var b strings.Builder
	b.WriteString("CREATE ")
	if idx.IsUnique {
		b.WriteString("UNIQUE ")
	}
	fmt.Fprintf(&b, "INDEX %q ON %q USING ", name, table)
	switch idx.Kind {
	case KindBTree:
		b.WriteString("btree")
	case KindGIN:
		b.WriteString("gin")
	}
	if idx.Kind == KindGIN && idx.Ops != "" {
		fmt.Fprintf(&b, " (%s %s)", idx.Columns[0], idx.Ops)
	} else {
		b.WriteString(" (")
		b.WriteString(strings.Join(idx.Columns, ", "))
		b.WriteString(")")
	}
	if idx.WhereExpr != "" {
		b.WriteString(" WHERE ")
		b.WriteString(idx.WhereExpr)
	}
	return b.String()
}

func TestScalarFieldTypes_CompileToCorrectColumnDDL(t *testing.T) {
	tests := []struct {
		name string
		def  FieldDef
		want string
	}{
		{"unbounded char", Char(), `"v" TEXT`},
		{"bounded char", Char(40), `"v" VARCHAR(40)`},
		{"text", Text(), `"v" TEXT`},
		{"integer", Integer(), `"v" INTEGER`},
		{"bigint", BigInt(), `"v" BIGINT`},
		{"float", Float(), `"v" DOUBLE PRECISION`},
		{"decimal", Decimal(12, 2), `"v" NUMERIC(12,2)`},
		{"boolean", Boolean(), `"v" BOOLEAN`},
		{"uuid", UUID(), `"v" UUID`},
		{"timestamptz", TimestampTZ(), `"v" TIMESTAMPTZ`},
		{"date", Date(), `"v" DATE`},
		{"time", Time(), `"v" TIME`},
		{"jsonb", JSONB(), `"v" JSONB`},
		{"bytea", Bytea(), `"v" BYTEA`},
		{"selection", Selection("draft", "done"), `"v" TEXT CHECK ("v" IN ('draft', 'done'))`},
		{"enum", Enum("order_state_enum"), `"v" order_state_enum`},
		{"required", Char().Required(), `"v" TEXT NOT NULL`},
		{"default", Char().Default("'x'"), `"v" TEXT DEFAULT 'x'`},
		{"primary key", UUID().PrimaryKey(), `"v" UUID PRIMARY KEY`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := columnDDL("v", tt.def); got != tt.want {
				t.Errorf("columnDDL(%q, %+v) = %q, want %q", "v", tt.def, got, tt.want)
			}
		})
	}
}

func TestWithStandardFields_ExpandsToExactlyTheDocumentedSeven(t *testing.T) {
	d := Define("contacts.contact").WithStandardFields()

	want := []string{"id", "tenant_id", "created_at", "updated_at", "deleted_at", "created_by", "etag"}
	if len(d.Fields) != len(want) {
		t.Fatalf("got %d fields, want %d: %+v", len(d.Fields), len(want), d.Fields)
	}
	for i, name := range want {
		if d.Fields[i].Name != name {
			t.Errorf("field %d = %q, want %q", i, d.Fields[i].Name, name)
		}
	}

	ddls := map[string]string{}
	for _, f := range d.Fields {
		ddls[f.Name] = columnDDL(f.Name, f.Def)
	}

	wantDDL := map[string]string{
		"id":         `"id" UUID PRIMARY KEY DEFAULT uuidv7()`,
		"tenant_id":  `"tenant_id" UUID NOT NULL`,
		"created_at": `"created_at" TIMESTAMPTZ NOT NULL DEFAULT NOW()`,
		"updated_at": `"updated_at" TIMESTAMPTZ NOT NULL DEFAULT NOW()`,
		"deleted_at": `"deleted_at" TIMESTAMPTZ`,
		"created_by": `"created_by" UUID`,
		"etag":       `"etag" TEXT NOT NULL DEFAULT `,
	}
	for name, want := range wantDDL {
		if ddls[name] != want {
			t.Errorf("%s DDL = %q, want %q", name, ddls[name], want)
		}
	}
}

func TestIndexes_CompileToCorrectDDL(t *testing.T) {
	tests := []struct {
		name string
		idx  IndexDef
		want string
	}{
		{
			"btree unique with where",
			BTreeIndex("tenant_id", "lower(email)").Unique().Where("email IS NOT NULL AND deleted_at IS NULL"),
			`CREATE UNIQUE INDEX "idx_contacts_email" ON "contacts" USING btree (tenant_id, lower(email)) WHERE email IS NOT NULL AND deleted_at IS NULL`,
		},
		{
			"gin trigram with where",
			GINIndex("name").WithOps("gin_trgm_ops").Where("deleted_at IS NULL"),
			`CREATE INDEX "idx_contacts_name_trgm" ON "contacts" USING gin (name gin_trgm_ops) WHERE deleted_at IS NULL`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := "idx_contacts_email"
			if tt.name == "gin trigram with where" {
				name = "idx_contacts_name_trgm"
			}
			if got := indexDDL("contacts", name, tt.idx); got != tt.want {
				t.Errorf("indexDDL(...) = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestModelDeclaration_MsgpackRoundTrip(t *testing.T) {
	original := Define("contacts.contact",
		Table("contacts"),
		Label("Contact"),
		LabelPlural("Contacts"),
	).
		WithStandardFields().
		Field("name", Char().Required()).
		Field("type", Selection("person", "company").Default("person")).
		Field("credit_limit", Decimal(12, 2)).
		Index("idx_contacts_email",
			BTreeIndex("tenant_id", "lower(email)").Unique().Where("deleted_at IS NULL")).
		Index("idx_contacts_name_trgm",
			GINIndex("name").WithOps("gin_trgm_ops"))

	schema := Schema{Models: []*ModelDeclaration{original}}

	data, err := msgpack.Marshal(schema)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded Schema
	if err := msgpack.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if len(decoded.Models) != 1 {
		t.Fatalf("got %d models, want 1", len(decoded.Models))
	}
	got := decoded.Models[0]

	if got.Name != original.Name || got.Table != original.Table ||
		got.Label != original.Label || got.LabelPlural != original.LabelPlural {
		t.Fatalf("model metadata mismatch: got %+v, want %+v", got, original)
	}
	if len(got.Fields) != len(original.Fields) {
		t.Fatalf("got %d fields, want %d", len(got.Fields), len(original.Fields))
	}
	for i, wantField := range original.Fields {
		gotField := got.Fields[i]
		if gotField.Name != wantField.Name {
			t.Errorf("field %d name = %q, want %q", i, gotField.Name, wantField.Name)
		}
		if columnDDL(gotField.Name, gotField.Def) != columnDDL(wantField.Name, wantField.Def) {
			t.Errorf("field %q DDL after round-trip = %q, want %q",
				wantField.Name, columnDDL(gotField.Name, gotField.Def), columnDDL(wantField.Name, wantField.Def))
		}
	}
	if len(got.Indexes) != len(original.Indexes) {
		t.Fatalf("got %d indexes, want %d", len(got.Indexes), len(original.Indexes))
	}
	for i, wantIdx := range original.Indexes {
		gotIdx := got.Indexes[i]
		gotDDL := indexDDL(got.Table, gotIdx.Name, gotIdx.Def)
		wantDDL := indexDDL(original.Table, wantIdx.Name, wantIdx.Def)
		if gotDDL != wantDDL {
			t.Errorf("index %q DDL after round-trip = %q, want %q", wantIdx.Name, gotDDL, wantDDL)
		}
	}
}
