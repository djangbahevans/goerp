package registry

import (
	"github.com/djangbahevans/goerp/internal/engine/event"
	"github.com/djangbahevans/goerp/internal/engine/fieldsec"
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
	jobRegistry      *JobRegistry
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

// Populated by future tickets (backlog #35, #37, and whatever resolves
// schemaRegistry). Never rebuilt by any build* step here;
// carried over unchanged from the prior snapshot on every write.
type JobRegistry struct{}
type CronRegistry struct{}
type SchemaRegistry struct{}
