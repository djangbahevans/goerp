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

// TestSearch_CursorOptionErrors pins Search's rejection of Cursor —
// checked before any host call, so this doesn't need a wasip1 build to
// exercise.
func TestSearch_CursorOptionErrors(t *testing.T) {
	if _, err := Search("contacts.contact", "", Cursor("abc123")); err == nil {
		t.Fatal("Search() with a Cursor option: error = nil, want an error")
	}
}
