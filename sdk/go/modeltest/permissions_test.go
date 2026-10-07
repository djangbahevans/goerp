package modeltest

import (
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/sdk/go/perm"
)

func TestPermissionsToBitfield_GrantsExactlyTheGivenPermissions(t *testing.T) {
	reg := permission.NewPermissionRegistry()
	reg.Register("contacts", []manifest.Permission{
		{Name: "contacts:contact:read"},
		{Name: "contacts:contact:write"},
	})
	read, _ := reg.Index("contacts:contact:read")
	write, _ := reg.Index("contacts:contact:write")

	got := permissionsToBitfield(reg, []perm.Permission{perm.Ref("contacts:contact:read"), perm.Ref("other:thing:read")})

	if !got.Has(read) || got.Has(write) {
		t.Errorf("bitfield holds read=%v write=%v, want read only", got.Has(read), got.Has(write))
	}
}

func TestPermissionsToBitfield_NoPermissionsGrantsNothing(t *testing.T) {
	reg := permission.NewPermissionRegistry()
	reg.Register("contacts", []manifest.Permission{{Name: "contacts:contact:read"}})
	read, _ := reg.Index("contacts:contact:read")

	if got := permissionsToBitfield(reg, nil); got.Has(read) {
		t.Error("an empty permission set holds a permission")
	}
}
