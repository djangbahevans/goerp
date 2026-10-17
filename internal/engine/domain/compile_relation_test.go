package domain

import (
	"slices"
	"strings"
	"testing"
)

func TestCompileRelationMembership(t *testing.T) {
	const first = "11111111-1111-1111-1111-111111111111"
	const second = "22222222-2222-2222-2222-222222222222"
	relations := map[string]SQLRelation{
		"tag_ids": {From: "links j JOIN tags t ON t.id = j.tag_id", Where: "j.contact_id = contacts.id AND t.deleted_at IS NULL", Column: "j.tag_id"},
	}
	for _, tc := range []struct {
		source  string
		args    []any
		negated bool
	}{
		{"record.tag_ids = '" + first + "'", []any{first}, false},
		{"record.tag_ids != '" + first + "'", []any{first}, true},
		{"record.name = 'Ama' AND record.tag_ids IN ('" + first + "', '" + second + "')", []any{"Ama", first, second}, false},
		{"record.tag_ids IS NULL", nil, true},
		{"record.tag_ids IS NOT NULL", nil, false},
	} {
		t.Run(tc.source, func(t *testing.T) {
			expr, err := Parse(tc.source)
			if err != nil {
				t.Fatal(err)
			}

			sql, args, err := CompileToSQLWithRelations(expr, relations)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(args, tc.args) || strings.Contains(sql, "NOT EXISTS") != tc.negated || !strings.Contains(sql, "t.deleted_at IS NULL") {
				t.Fatalf("SQL = %s, args = %v", sql, args)
			}
			if len(args) > 0 && !strings.Contains(sql, "$1") {
				t.Errorf("missing placeholder: %s", sql)
			}
			for _, id := range []string{first, second} {
				if strings.Contains(sql, id) {
					t.Errorf("UUID was not parameterized: %s", sql)
				}
			}
		})
	}

	for _, source := range []string{"record.tag_ids LIKE 'x'", "record.tag_ids = 'not-a-uuid'", "record.tag_ids = record.id", "record.tag_ids IN (true)", "record.tag_ids"} {
		expr, err := Parse(source)
		if err != nil {
			t.Fatal(err)
		}
		if sql, _, err := CompileToSQLWithRelations(expr, relations); err == nil {
			t.Errorf("accepted %s as %s", source, sql)
		}
	}
}
