package orm

import "testing"

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
