package domain

import (
	"slices"
	"testing"
)

func TestRecordFields(t *testing.T) {
	tests := []struct {
		src  string
		want []string
	}{
		{"record.owner_id = current_user.id", []string{"owner_id"}},
		{"record.state IN ('a', 'b') AND NOT record.paid OR record.owner_id = record.manager_id", []string{"manager_id", "owner_id", "paid", "state"}},
		{"record.note IS NULL AND record.note != 'x'", []string{"note"}},
		{"user_has_role('admin')", nil},
	}
	for _, tc := range tests {
		expr, err := Parse(tc.src)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.src, err)
		}
		if got := RecordFields(expr); !slices.Equal(got, tc.want) {
			t.Errorf("RecordFields(%q) = %v, want %v", tc.src, got, tc.want)
		}
	}
}
