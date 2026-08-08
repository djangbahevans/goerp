package engine

import "time"

type EngineRequest struct {
	ID          string
	Method      string
	Path        string
	PathParams  map[string]string
	QueryParams map[string]string
	Headers     map[string]string
	Body        map[string]any
	UserID      string
	TenantID    string
	TenantSlug  string
	Locale      string
	Timezone    time.Location
	Currency    string
	Direction   string
	TraceID     string
	RequestAt   time.Time
}
