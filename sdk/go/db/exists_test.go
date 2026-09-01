package db

import "testing"

func TestExists_RejectsInvalidTableIdentifier(t *testing.T) {
	_, err := Exists("orders; DROP TABLE orders", "1=1")
	if err == nil {
		t.Fatal("Exists() with an invalid table name = nil error, want error")
	}
}

func TestCount_RejectsInvalidTableIdentifier(t *testing.T) {
	_, err := Count("orders; DROP TABLE orders", "1=1")
	if err == nil {
		t.Fatal("Count() with an invalid table name = nil error, want error")
	}
}
