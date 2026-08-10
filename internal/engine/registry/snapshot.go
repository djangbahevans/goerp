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

// Populated by future tickets (backlog #35, #37, and whatever resolves
// schemaRegistry). Never rebuilt by any build* step here;
// carried over unchanged from the prior snapshot on every write.
type JobRegistry struct{}
type CronRegistry struct{}
type SchemaRegistry struct{}
