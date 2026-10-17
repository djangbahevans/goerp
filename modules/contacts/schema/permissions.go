package schema

import "github.com/djangbahevans/goerp/sdk/go/perm"

var (
	ConfigWrite = perm.Define("contacts:config:write",
		perm.Description("Manage tags, relationship types and identifier types"),
		perm.DefaultRoles(perm.Admin),
	)
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
	PersonalRead = perm.Define("contacts:personal:read",
		perm.Description("View dates of birth and gender"),
		perm.DefaultRoles(perm.Admin),
	)
	PersonalWrite = perm.Define("contacts:personal:write",
		perm.Description("Edit dates of birth and gender"),
		perm.DefaultRoles(perm.Admin),
	)
)
