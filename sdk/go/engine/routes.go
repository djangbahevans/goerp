package engine

import abi "github.com/djangbahevans/goerp/contract/abi/v1"

type RateLimitScope = abi.RateLimitScope

const (
	PerUser   = abi.RateLimitScopeUser
	PerTenant = abi.RateLimitScopeTenant
	PerIP     = abi.RateLimitScopeIP
	PerAPIKey = abi.RateLimitScopeAPIKey
)

type (
	RouteDeclaration = abi.RouteDeclaration
	RateLimitDecl    = abi.RateLimitDecl
	EmbeddedDecl     = abi.EmbeddedDecl
	TypeDesc         = abi.TypeDesc
	FieldDesc        = abi.FieldDesc
)

func WriteRoutes(routes []RouteDeclaration) uint64 { return writePacked(routes) }

// SerialiseRouteTable is what the SDK's auto-generated get_routes export
// calls — routes aren't module-author-declared data the way schema/data
// migrations are; they're collected automatically by DefaultRouter as a
// side effect of GET/POST/etc. calls in init().
func SerialiseRouteTable() uint64 {
	return WriteRoutes(routeDeclarations(DefaultRouter.routes))
}
