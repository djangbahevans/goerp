package schema

import "github.com/djangbahevans/goerp/sdk/go/perm"

var (
	ContactRead = perm.Define("contacts:contact:read",
		perm.Description("View contacts"),
		perm.DefaultRoles(perm.User, perm.Admin),
	)
	ContactWrite = perm.Define("contacts:contact:write",
		perm.Description("Create and edit contacts"),
		perm.DefaultRoles(perm.User, perm.Admin),
	)
	ContactDelete = perm.Define("contacts:contact:delete",
		perm.Description("Archive contacts"),
		perm.DefaultRoles(perm.Admin),
	)
)
