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
	IdentifierRead = perm.Define("contacts:identifier:read",
		perm.Description("View identifiers of non-sensitive types"),
		perm.DefaultRoles(perm.User, perm.Admin),
	)
	IdentifierWrite = perm.Define("contacts:identifier:write",
		perm.Description("Create and edit identifiers of non-sensitive types"),
		perm.DefaultRoles(perm.User, perm.Admin),
	)
	PersonalRead = perm.Define("contacts:personal:read",
		perm.Description("View dates of birth, gender and sensitive identifiers"),
		perm.DefaultRoles(perm.Admin),
	)
	PersonalWrite = perm.Define("contacts:personal:write",
		perm.Description("Edit dates of birth, gender and sensitive identifiers"),
		perm.DefaultRoles(perm.Admin),
	)
)

const identifierVisibility = "user_has_permission('contacts:identifier:read') AND (record.is_sensitive = false OR user_has_permission('contacts:personal:read'))"

var (
	IdentifierSensitive    = perm.DefinePolicy("contacts:identifier:sensitive", IdentifierRead, identifierVisibility)
	IdentifierVisibleWrite = perm.DefinePolicy("contacts:identifier:visible_write", IdentifierWrite, identifierVisibility)
)
