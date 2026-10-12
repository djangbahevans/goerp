package abi

const (
	HookRequestBefore     = "engine.request.before"
	HookRequestAfter      = "engine.request.after"
	HookTenantProvisioned = "engine.tenant.provisioned"
	HookModuleInstalled   = "engine.module.installed"
	HookModuleUpgraded    = "engine.module.upgraded"
	HookModuleUninstalled = "engine.module.uninstalled"
)

// EngineHook reports whether name identifies a supported engine lifecycle hook.
func EngineHook(name string) bool {
	switch name {
	case HookRequestBefore, HookRequestAfter, HookTenantProvisioned, HookModuleInstalled, HookModuleUpgraded, HookModuleUninstalled:
		return true
	default:
		return false
	}
}

// HookRequest selects a named handler. Validate checks its registration without running
// it. Request carries the invocation identity; Module identifies the lifecycle subject.
type HookRequest struct {
	Hook       string    `msgpack:"hook"`
	Handler    string    `msgpack:"handler"`
	Validate   bool      `msgpack:"validate"`
	Request    Request   `msgpack:"request"`
	Response   *Response `msgpack:"response,omitempty"`
	Module     string    `msgpack:"module"`
	Version    string    `msgpack:"version"`
	OldVersion string    `msgpack:"old_version"`
}

// HookResult carries a replacement request or response for request hooks. Lifecycle
// handlers return neither. Error aborts a request hook or reports a lifecycle failure.
type HookResult struct {
	Request  *Request  `msgpack:"request,omitempty"`
	Response *Response `msgpack:"response,omitempty"`
	Error    string    `msgpack:"error,omitempty"`
}
