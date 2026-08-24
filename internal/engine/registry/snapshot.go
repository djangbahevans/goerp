package registry

import (
	"github.com/djangbahevans/goerp/internal/engine/event"
	"github.com/djangbahevans/goerp/internal/engine/fieldsec"
	"github.com/djangbahevans/goerp/internal/engine/job"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/route"
)

type RegistrySnapshot struct {
	modules          map[string]*module.LoadedModule
	routeTable       *route.RouteTable
	eventRegistry    *event.EventRegistry
	permRegistry     *permission.PermissionRegistry
	fieldSecRegistry *fieldsec.FieldSecurityRegistry
	jobRegistry      *job.JobRegistry
	cronRegistry     *CronRegistry
	schemaRegistry   *SchemaRegistry
}

// Modules returns this snapshot's backing map, successful and
// StatusFailed alike. Callers must treat it as read-only.
func (s *RegistrySnapshot) Modules() map[string]*module.LoadedModule {
	return s.modules
}

// RouteTable returns this snapshot's route table — module-declared routes
// and engine built-ins (/_health, /_ready) alike, the single router the
// HTTP server's dispatch handler consults for every request.
func (s *RegistrySnapshot) RouteTable() *route.RouteTable {
	return s.routeTable
}

// PermissionRegistry returns this snapshot's permission registry — the
// current process's live permission-name-to-bitfield-index assignments
// (permcache.RolePermissionMap.RebuildAll needs these to resolve each
// role's bitfield against this process's own indices).
func (s *RegistrySnapshot) PermissionRegistry() *permission.PermissionRegistry {
	return s.permRegistry
}

// FieldSecRegistry returns this snapshot's field security registry —
// always read this way rather than cached on a longer-lived struct, so a
// hot reload's rebuilt registry takes effect on the next request rather
// than never (see buildFieldSecRegistry).
func (s *RegistrySnapshot) FieldSecRegistry() *fieldsec.FieldSecurityRegistry {
	return s.fieldSecRegistry
}

// EventRegistry returns this snapshot's event registry.
func (s *RegistrySnapshot) EventRegistry() *event.EventRegistry {
	return s.eventRegistry
}

// Populated by future tickets (backlog #35, #37). Never rebuilt by any
// build* step here; carried over unchanged from the prior snapshot on
// every write.
type CronRegistry struct{}
type SchemaRegistry struct{}
