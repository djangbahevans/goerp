package wasm

import (
	"database/sql"
	"sync"

	"github.com/djangbahevans/goerp/internal/engine/abi"
	"go.opentelemetry.io/otel/trace"
)

type ModuleContext struct {
	RequestID  string
	UserID     string
	ContactID  string
	Roles      []string
	TenantID   string
	TenantSlug string
	TraceID    string
	SpanStack  []trace.Span

	transactions map[string]*sql.Tx
	txMu         sync.Mutex

	capabilities abi.CapabilitySet
}
