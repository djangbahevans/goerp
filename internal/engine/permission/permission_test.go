package permission

import (
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
)

func TestPermissionRegistry_Register_AssignsStableIndex(t *testing.T) {
	r := NewPermissionRegistry()

	r.Register("contacts", []manifest.Permission{
		{Name: "contacts:contact:read"},
		{Name: "contacts:contact:write"},
	})

	readIdx, ok := r.Index("contacts:contact:read")
	if !ok {
		t.Fatal("Index(contacts:contact:read) not found")
	}
	writeIdx, ok := r.Index("contacts:contact:write")
	if !ok {
		t.Fatal("Index(contacts:contact:write) not found")
	}
	if readIdx == writeIdx {
		t.Fatalf("both permissions got index %d, want distinct indices", readIdx)
	}

	// Re-registering the same module (e.g. a hot reload) must not reassign
	// an already-indexed permission's index.
	r.Register("contacts", []manifest.Permission{
		{Name: "contacts:contact:read"},
	})
	if got, _ := r.Index("contacts:contact:read"); got != readIdx {
		t.Fatalf("re-Register changed index: got %d, want %d", got, readIdx)
	}
}

func TestPermissionRegistry_Unregister_DoesNotReuseIndices(t *testing.T) {
	r := NewPermissionRegistry()

	r.Register("contacts", []manifest.Permission{
		{Name: "contacts:contact:read"},
		{Name: "contacts:contact:write"},
	})
	oldIdx, _ := r.Index("contacts:contact:write")

	r.Unregister("contacts")

	r.Register("orders", []manifest.Permission{
		{Name: "orders:order:read"},
	})
	newIdx, ok := r.Index("orders:order:read")
	if !ok {
		t.Fatal("Index(orders:order:read) not found")
	}

	if newIdx <= oldIdx {
		t.Fatalf("orders:order:read got index %d, want an index greater than the unregistered module's last index %d", newIdx, oldIdx)
	}

	// The unregistered module's permission names still resolve — the
	// index is orphaned, not deleted.
	if _, ok := r.Index("contacts:contact:write"); !ok {
		t.Fatal("Index(contacts:contact:write) no longer resolves after Unregister — indices must survive unload")
	}

	if got := r.ModulePermissions("contacts"); got != nil {
		t.Fatalf("ModulePermissions(contacts) = %v, want nil after Unregister", got)
	}
}

func TestPermissionBitfield_SetAndHas(t *testing.T) {
	var b PermissionBitfield

	b.Set(3)
	b.Set(70) // past the first 64-bit word

	if !b.Has(3) {
		t.Error("Has(3) = false, want true")
	}
	if !b.Has(70) {
		t.Error("Has(70) = false, want true")
	}
	if b.Has(4) {
		t.Error("Has(4) = true, want false")
	}
	if b.Has(200) {
		t.Error("Has(200) = true, want false — beyond the bitfield's current length")
	}
}
