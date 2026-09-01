package orm

import (
	"testing"
	"time"
)

func TestDomain_ReplacesPlaceholdersByType(t *testing.T) {
	got := Domain("record.name = ? AND record.price = ? AND record.is_active = ?", "Acme", 42, true)
	want := "record.name = 'Acme' AND record.price = 42 AND record.is_active = true"
	if got != want {
		t.Errorf("Domain() = %q, want %q", got, want)
	}
}

func TestDomain_DoublesEmbeddedQuotes(t *testing.T) {
	got := Domain("record.name = ?", "O'Brien")
	want := "record.name = 'O''Brien'"
	if got != want {
		t.Errorf("Domain() = %q, want %q", got, want)
	}
}

func TestDomain_NilRendersNullKeyword(t *testing.T) {
	got := Domain("record.deleted_at = ?", nil)
	want := "record.deleted_at = null"
	if got != want {
		t.Errorf("Domain() = %q, want %q", got, want)
	}
}

func TestDomain_FloatNeverUsesScientificNotation(t *testing.T) {
	got := Domain("record.price = ?", 0.0000001)
	if got != "record.price = 0.0000001" {
		t.Errorf("Domain() = %q, want %q (no scientific notation)", got, "record.price = 0.0000001")
	}
}

// TestDomain_TimeRendersRFC3339 pins go-sdk-reference.md §6a's own
// worked example: `orm.Domain("state = 'draft' AND created_at < ?",
// cutoff)` with a time.Time cutoff — Postgres parses RFC 3339 directly.
func TestDomain_TimeRendersRFC3339(t *testing.T) {
	at := time.Date(2026, 8, 2, 16, 32, 19, 123456789, time.UTC)
	got := Domain("record.created_at < ?", at)
	want := "record.created_at < '2026-08-02T16:32:19.123456789Z'"
	if got != want {
		t.Errorf("Domain() = %q, want %q", got, want)
	}
}

func TestDomain_UnknownTypeFallsBackToQuotedString(t *testing.T) {
	type customType struct{ V int }
	got := Domain("record.custom = ?", customType{V: 1})
	want := "record.custom = '{1}'"
	if got != want {
		t.Errorf("Domain() = %q, want %q", got, want)
	}
}

func TestDomain_ExtraPlaceholdersPassThroughUnreplaced(t *testing.T) {
	got := Domain("record.a = ? AND record.b = ?", "x")
	want := "record.a = 'x' AND record.b = ?"
	if got != want {
		t.Errorf("Domain() = %q, want %q", got, want)
	}
}

func TestDomain_ExtraArgsAreIgnored(t *testing.T) {
	got := Domain("record.a = ?", "x", "y", "z")
	want := "record.a = 'x'"
	if got != want {
		t.Errorf("Domain() = %q, want %q", got, want)
	}
}
