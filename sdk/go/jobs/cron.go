package jobs

import "github.com/djangbahevans/goerp/sdk/go/jobs/def"

// CronDef is a typed cron job definition (see def.CronDef).
type CronDef = def.CronDef

// CronContext is what a cron handler receives when its schedule fires.
// TenantID is empty for a Global cron job, which runs once for the platform.
type CronContext struct {
	TenantID string
	TraceID  string
}

// DefineCron declares a cron job named name. It panics unless Label and a
// 5-field UTC Schedule are given, and when an option that applies only to
// jobs (MaxAttempts, Priority, UniqueBy) is given.
func DefineCron(name string, opts ...DefineOption) CronDef {
	return def.DefineCron(name, opts...)
}

var (
	// Schedule sets a cron job's 5-field cron expression (minute hour day
	// month weekday), in UTC. It is required by DefineCron.
	Schedule = def.Schedule
	// DisabledByDefault leaves a cron job off until a tenant admin enables
	// it.
	DisabledByDefault = def.DisabledByDefault
	// Global runs a cron job once globally instead of once per active
	// tenant.
	Global = def.Global
)
