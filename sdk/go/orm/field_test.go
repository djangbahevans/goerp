package orm

import (
	"reflect"
	"testing"
	"time"
)

// TestField_ComparisonMethods pins the domain-string output of every
// Field/StringField/OrderedField/TimeField comparison method — one row
// per comparison operator in manifest-spec.md §8's supported-tokens
// table.
func TestField_ComparisonMethods(t *testing.T) {
	str := NewField[testModel, string]("type")
	num := NewOrderedField[testModel, int64]("price")
	text := NewStringField[testModel]("name")
	at := NewTimeField[testModel]("created_at")

	when := time.Date(2026, 8, 2, 16, 32, 19, 0, time.UTC)

	tests := []struct {
		name string
		got  Condition[testModel]
		want string
	}{
		{"Eq", str.Eq("person"), "record.type = 'person'"},
		{"Neq", str.Neq("person"), "record.type != 'person'"},
		{"In", str.In("a", "b"), "record.type IN ('a', 'b')"},
		{"In empty", str.In(), "false"},
		{"NotIn", str.NotIn("a", "b"), "NOT record.type IN ('a', 'b')"},
		{"NotIn empty", str.NotIn(), "true"},
		{"IsNull", str.IsNull(), "record.type IS NULL"},
		{"IsNotNull", str.IsNotNull(), "record.type IS NOT NULL"},
		{"Like", text.Like("Ac%"), "record.name LIKE 'Ac%'"},
		{"ILike", text.ILike("ac%"), "record.name ILIKE 'ac%'"},
		{"Gt", num.Gt(10), "record.price > 10"},
		{"Gte", num.Gte(10), "record.price >= 10"},
		{"Lt", num.Lt(10), "record.price < 10"},
		{"Lte", num.Lte(10), "record.price <= 10"},
		{"Between", num.Between(10, 20), "record.price >= 10 AND record.price <= 20"},
		{"Before", at.Before(when), "record.created_at < '2026-08-02T16:32:19Z'"},
		{"After", at.After(when), "record.created_at > '2026-08-02T16:32:19Z'"},
		{"TimeBetween", at.Between(when, when), "record.created_at >= '2026-08-02T16:32:19Z' AND record.created_at <= '2026-08-02T16:32:19Z'"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got.expr != tt.want {
				t.Errorf("expr = %q, want %q", tt.got.expr, tt.want)
			}
		})
	}
}

// TestField_MatchesHandWrittenDomain compiles one Condition per
// comparison operator via the typed builder and via the existing
// hand-written Domain() helper, and requires them to match byte-for-
// byte — the acceptance criterion that the typed builder isn't a second,
// divergent implementation of the same grammar.
func TestField_MatchesHandWrittenDomain(t *testing.T) {
	str := NewField[testModel, string]("state")
	num := NewOrderedField[testModel, int64]("price")
	text := NewStringField[testModel]("name")
	at := NewTimeField[testModel]("created_at")
	when := time.Date(2026, 8, 2, 16, 32, 19, 0, time.UTC)

	tests := []struct {
		name string
		got  Condition[testModel]
		want string
	}{
		{"Eq", str.Eq("draft"), Domain("record.state = ?", "draft")},
		{"Neq", str.Neq("draft"), Domain("record.state != ?", "draft")},
		{"IsNull", str.IsNull(), "record.state IS NULL"},
		{"IsNotNull", str.IsNotNull(), "record.state IS NOT NULL"},
		{"Like", text.Like("Ac%"), Domain("record.name LIKE ?", "Ac%")},
		{"ILike", text.ILike("ac%"), Domain("record.name ILIKE ?", "ac%")},
		{"Gt", num.Gt(10), Domain("record.price > ?", 10)},
		{"Lt", num.Lt(10), Domain("record.price < ?", 10)},
		{"Before", at.Before(when), Domain("record.created_at < ?", when)},
		{"After", at.After(when), Domain("record.created_at > ?", when)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got.expr != tt.want {
				t.Errorf("typed builder = %q, orm.Domain = %q — must match byte-for-byte", tt.got.expr, tt.want)
			}
		})
	}
}

func TestBytesField(t *testing.T) {
	f := NewBytesField[testModel]("metadata")

	if got, want := f.Name(), "metadata"; got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}
	if got, want := f.IsNull().expr, "record.metadata IS NULL"; got != want {
		t.Errorf("IsNull() = %q, want %q", got, want)
	}
	if got, want := f.IsNotNull().expr, "record.metadata IS NOT NULL"; got != want {
		t.Errorf("IsNotNull() = %q, want %q", got, want)
	}
}

// TestBytesField_HasNoValueComparisonMethods pins the acceptance
// criterion that BytesField exposes no Eq/Neq/In — exact-byte equality
// is rarely meaningful and would be outright wrong for JSONB.
func TestBytesField_HasNoValueComparisonMethods(t *testing.T) {
	typ := reflect.TypeFor[BytesField[testModel]]()
	for _, name := range []string{"Eq", "Neq", "In", "NotIn"} {
		if _, ok := typ.MethodByName(name); ok {
			t.Errorf("BytesField has method %s(); want none", name)
		}
	}
}

func TestAnyField_AcceptsEveryFieldKind(t *testing.T) {
	var fields []AnyField[testModel]
	fields = append(fields, NewField[testModel, string]("name"))
	fields = append(fields, NewStringField[testModel]("name"))
	fields = append(fields, NewOrderedField[testModel, int64]("price"))
	fields = append(fields, NewTimeField[testModel]("created_at"))
	fields = append(fields, NewBytesField[testModel]("metadata"))

	for _, f := range fields {
		if f.Name() == "" {
			t.Errorf("AnyField.Name() returned empty string for %T", f)
		}
	}
}
