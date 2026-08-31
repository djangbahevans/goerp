package db

import "testing"

func TestUpdateByID_InvalidTableName(t *testing.T) {
	if _, err := UpdateByID("widget; DROP TABLE widget --", "1", map[string]any{"name": "x"}); err == nil {
		t.Fatal("expected an error for an invalid table identifier")
	}
}

func TestUpdateByID_EmptyPatch(t *testing.T) {
	if _, err := UpdateByID("widget", "1", nil); err == nil {
		t.Fatal("expected an error for an empty patch")
	}
	if _, err := UpdateByID("widget", "1", map[string]any{}); err == nil {
		t.Fatal("expected an error for an empty patch")
	}
}

// TestUpdateByID_InvalidPatchKey is a regression test: patch is
// realistically built from less-trusted input (e.g. a decoded JSON
// request body), and its keys are interpolated directly into the UPDATE
// statement's own SET clause — a crafted key must be rejected before it
// ever reaches SQL text, not merely produce a confusing syntax error.
func TestUpdateByID_InvalidPatchKey(t *testing.T) {
	_, err := UpdateByID("widget", "1", map[string]any{
		"name = (SELECT 1); DROP TABLE widget --": "x",
	})
	if err == nil {
		t.Fatal("expected an error for a patch key that isn't a valid identifier")
	}
}
