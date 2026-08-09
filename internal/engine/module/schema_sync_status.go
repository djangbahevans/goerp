package module

type SchemaSyncStatus int

const (
	SchemaSyncOK SchemaSyncStatus = iota
	SchemaSyncFailed
	SchemaSyncInProgress
)

func (s SchemaSyncStatus) String() string {
	switch s {
	case SchemaSyncOK:
		return "ok"
	case SchemaSyncFailed:
		return "failed"
	case SchemaSyncInProgress:
		return "in_progress"
	default:
		return "unknown_status"
	}
}
