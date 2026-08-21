package permission

import "testing"

func TestPermissionBitfield_AndRestrictsToIntersection(t *testing.T) {
	var b PermissionBitfield
	b.Set(1) // only on b
	b.Set(2) // on both
	b.Set(3) // on both

	var other PermissionBitfield
	other.Set(2)
	other.Set(3)
	other.Set(4) // only on other

	b.And(other)

	if b.Has(1) {
		t.Error("Has(1) = true, want false — bit only on b must be cleared")
	}
	if !b.Has(2) {
		t.Error("Has(2) = false, want true — bit set on both must remain")
	}
	if !b.Has(3) {
		t.Error("Has(3) = false, want true — bit set on both must remain")
	}
	if b.Has(4) {
		t.Error("Has(4) = true, want false — bit only on other must not appear")
	}
}

func TestPermissionBitfield_AndWithEmptyOtherClearsAll(t *testing.T) {
	var b PermissionBitfield
	b.Set(0)
	b.Set(10)

	var other PermissionBitfield
	b.And(other)

	if b.Has(0) || b.Has(10) {
		t.Error("expected And with an empty bitfield to clear all bits")
	}
}
