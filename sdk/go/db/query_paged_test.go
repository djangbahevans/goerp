package db

import "testing"

func TestDecodeCursor_EmptyIsOffsetZero(t *testing.T) {
	got, err := decodeCursor("")
	if err != nil {
		t.Fatalf("decodeCursor(\"\"): %v", err)
	}
	if got != 0 {
		t.Errorf("decodeCursor(\"\") = %d, want 0", got)
	}
}

func TestDecodeCursor_ParsesOffset(t *testing.T) {
	got, err := decodeCursor("50")
	if err != nil {
		t.Fatalf("decodeCursor(\"50\"): %v", err)
	}
	if got != 50 {
		t.Errorf("decodeCursor(\"50\") = %d, want 50", got)
	}
}

func TestDecodeCursor_RejectsInvalidToken(t *testing.T) {
	if _, err := decodeCursor("not-a-number"); err == nil {
		t.Error("decodeCursor(\"not-a-number\") = nil error, want error")
	}
	if _, err := decodeCursor("-1"); err == nil {
		t.Error("decodeCursor(\"-1\") = nil error, want error")
	}
}

func TestPagedRows_FullPagePlusOneYieldsNextCursor(t *testing.T) {
	rows := []int{1, 2, 3}
	got, cursor := pagedRows(rows, 2, 10)
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("pagedRows() rows = %v, want [1 2]", got)
	}
	if cursor != "12" {
		t.Errorf("pagedRows() cursor = %q, want %q", cursor, "12")
	}
}

func TestPagedRows_ShortPageYieldsNoCursor(t *testing.T) {
	rows := []int{1, 2}
	got, cursor := pagedRows(rows, 2, 10)
	if len(got) != 2 {
		t.Errorf("pagedRows() rows = %v, want [1 2]", got)
	}
	if cursor != "" {
		t.Errorf("pagedRows() cursor = %q, want empty", cursor)
	}
}

func TestQueryPaged_RejectsNonPositiveLimit(t *testing.T) {
	if _, _, err := QueryPaged[firstRowTestRecord]("SELECT id, name FROM x", "", 0, nil); err == nil {
		t.Error("QueryPaged() with limit=0 = nil error, want error")
	}
	if _, _, err := QueryPaged[firstRowTestRecord]("SELECT id, name FROM x", "", -1, nil); err == nil {
		t.Error("QueryPaged() with limit=-1 = nil error, want error")
	}
}
