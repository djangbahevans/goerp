package domain_test

// sdk/go/orm.Domain can't depend on this package (sdk/go/* can't import
// internal/engine/*), so this is the other half of that contract: proof
// its output is actually valid input to the real parser it targets.

import (
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/domain"
	"github.com/djangbahevans/goerp/sdk/go/orm"
)

func TestOrmDomain_OutputParsesAsValidDomain(t *testing.T) {
	cases := []struct {
		name     string
		template string
		args     []any
	}{
		{"string", "record.name = ?", []any{"Acme"}},
		{"string with embedded quote", "record.name = ?", []any{"O'Brien"}},
		{"int", "record.price = ?", []any{42}},
		{"int64", "record.price = ?", []any{int64(42)}},
		{"float", "record.price = ?", []any{3.14}},
		{"bool", "record.is_active = ?", []any{true}},
		{"nil", "record.deleted_at = ?", []any{nil}},
		{"multiple placeholders", "record.type = ? AND record.is_active = ?", []any{"person", true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := orm.Domain(c.template, c.args...)
			if _, err := domain.Parse(src); err != nil {
				t.Errorf("domain.Parse(orm.Domain(%q, %v)) = %q, error: %v", c.template, c.args, src, err)
			}
		})
	}
}

// TestOrmDomain_NegativeNumberDoesNotParse pins a real grammar
// limitation Domain doesn't try to work around: the domain lexer has no
// unary minus, so a negative numeric arg produces a string the real
// parser rejects. Documents the gap rather than silently mishandling it.
func TestOrmDomain_NegativeNumberDoesNotParse(t *testing.T) {
	src := orm.Domain("record.price = ?", -5)
	if _, err := domain.Parse(src); err == nil {
		t.Errorf("domain.Parse(%q) succeeded, want an error — negative numeric literals aren't supported by this grammar", src)
	}
}
