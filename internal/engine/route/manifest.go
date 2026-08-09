package route

import "time"

type RouteManifest struct {
	AuthRequired bool
	Permissions  []string

	RateLimit *RateLimitConfig // nil = use the engine-wide default, not "no limit"

	Model          string // "{module}.{resource}"; "" = no model binding
	ResponseIsList bool

	MaxBodyBytes int64
	RawBody      bool

	Timeout time.Duration

	CrudAction string // "get"|"list"|"create"|"update"|"delete"|"preview"|""

	EngineNative   bool
	StorageBackend string // "table"|"transient"|"virtual"
}

type RateLimitConfig struct {
	Requests      int
	WindowSeconds int
	Scope         string
}
