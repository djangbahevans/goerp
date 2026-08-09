package module

type ModuleStatus int

const (
	StatusPending ModuleStatus = iota
	StatusCompiling
	StatusSyncing
	StatusWarming
	StatusReady
	StatusFailed
	StatusDraining
	StatusDisabled
	StatusUnloaded
)

func (s ModuleStatus) String() string {
	switch s {
	case StatusPending:
		return "pending"
	case StatusCompiling:
		return "compiling"
	case StatusSyncing:
		return "syncing"
	case StatusWarming:
		return "warming"
	case StatusReady:
		return "ready"
	case StatusFailed:
		return "failed"
	case StatusDraining:
		return "draining"
	case StatusDisabled:
		return "disabled"
	case StatusUnloaded:
		return "unloaded"
	default:
		return "unknown_status"
	}
}
