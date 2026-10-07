package registry

import (
	"reflect"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestStaticDefault(t *testing.T) {
	tests := []struct {
		name string
		kind model.FieldKind
		expr string
		want any
		ok   bool
	}{
		{"quoted string", model.KindSelection, "'company'", "company", true},
		{"quoted string with cast", model.KindChar, "'draft'::text", "draft", true},
		{"escaped quote", model.KindText, "'it''s'", "it's", true},
		{"empty string", model.KindChar, "''", "", true},
		{"colons inside the literal", model.KindChar, "'a::b'", "a::b", true},
		{"true", model.KindBoolean, "true", true, true},
		{"false uppercase", model.KindBoolean, "FALSE", false, true},
		{"integer", model.KindInteger, "0", int64(0), true},
		{"negative bigint", model.KindBigInt, "-5", int64(-5), true},
		{"float", model.KindFloat, "1.5", 1.5, true},
		{"decimal stays a string", model.KindDecimal, "0.00", "0.00", true},
		{"date", model.KindDate, "'2026-01-01'", "2026-01-01", true},
		{"date needs a calendar date", model.KindDate, "'today'", nil, false},
		{"date infinity", model.KindDate, "'infinity'", nil, false},
		{"float NaN", model.KindFloat, "'NaN'", nil, false},
		{"float Inf", model.KindFloat, "Inf", nil, false},
		{"float hex", model.KindFloat, "0x1p-2", nil, false},
		{"decimal NaN", model.KindDecimal, "NaN", nil, false},
		{"signed decimal", model.KindDecimal, "-12.50", "-12.50", true},
		{"double sign", model.KindDecimal, "--1", nil, false},
		{"function call", model.KindTimestampTZ, "now()", nil, false},
		{"generated id", model.KindUUID, "uuidv7()", nil, false},
		{"function in a string kind", model.KindChar, "lower('X')", nil, false},
		{"two literals", model.KindChar, "'a' || 'b'", nil, false},
		{"number for a boolean", model.KindBoolean, "1", nil, false},
		{"unterminated", model.KindChar, "'open", nil, false},
		{"jsonb has no shell form", model.KindJSONB, "'{}'::jsonb", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := staticDefault(tt.kind, tt.expr)
			if ok != tt.ok || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("staticDefault(%v, %q) = %#v, %v; want %#v, %v", tt.kind, tt.expr, got, ok, tt.want, tt.ok)
			}
		})
	}
}
