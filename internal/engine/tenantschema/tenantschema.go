// Package tenantschema is the one place the tenant_{slug} schema-naming
// convention lives — every per-tenant-schema table (roles/role_permissions/
// user_roles, tenant_invitations, and eventually event_log/audit_log/
// notifications/saved_filters per auth-internals.md §10) schema-qualifies
// its queries with Name(slug) rather than relying on a session's
// search_path, since a pooled PgBouncer connection can't safely carry a
// per-request SET search_path the way a dedicated, held-for-the-session
// connection (schema.SchemaSyncPool's BeginSync) can.
package tenantschema

// Name quotes tenant_{slug} as a safe identifier, ready to interpolate
// directly into SQL. Safe because slug's character set is already
// DB-constrained (system.tenants' own CHECK:
// ^[a-z][a-z0-9\-]{1,62}[a-z0-9]$) by the time it reaches here.
func Name(slug string) string {
	return `"tenant_` + slug + `"`
}
