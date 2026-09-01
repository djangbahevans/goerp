package orm

import "testing"

func TestSearchOptions_SetFields(t *testing.T) {
	var o searchOpts

	OrderBy("name", false)(&o)
	if o.Order != "name" {
		t.Errorf("Order = %q, want %q", o.Order, "name")
	}

	OrderBy("created_at", true)(&o)
	if o.Order != "created_at DESC" {
		t.Errorf("Order = %q, want %q", o.Order, "created_at DESC")
	}

	Limit(50)(&o)
	if o.Limit != 50 {
		t.Errorf("Limit = %d, want 50", o.Limit)
	}

	Cursor("abc123")(&o)
	if o.Cursor != "abc123" {
		t.Errorf("Cursor = %q, want %q", o.Cursor, "abc123")
	}
}
