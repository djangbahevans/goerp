package model

import "testing"

func TestFieldKind_String_CoversEveryDeclaredKind(t *testing.T) {
	kinds := []FieldKind{
		KindChar, KindText, KindInteger, KindBigInt, KindFloat, KindDecimal,
		KindBoolean, KindUUID, KindTimestampTZ, KindDate, KindTime, KindJSONB,
		KindBytea, KindSelection, KindEnum, KindMany2One, KindSequence,
		KindDynamicLink, KindOne2Many,
	}
	seen := map[string]bool{}
	for _, k := range kinds {
		s := k.String()
		if s == "" {
			t.Errorf("FieldKind(%d).String() is empty", k)
		}
		if seen[s] {
			t.Errorf("FieldKind %q used by more than one kind", s)
		}
		seen[s] = true
	}
}

func TestFieldKind_String_UnknownValueIsEmpty(t *testing.T) {
	if got := FieldKind(999).String(); got != "" {
		t.Errorf("FieldKind(999).String() = %q, want \"\"", got)
	}
}
