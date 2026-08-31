package model

import (
	"fmt"
	"os"
	"strings"
)

type DataMigration struct {
	FromVersion string `msgpack:"from_version"`
	ToVersion   string `msgpack:"to_version"`
	Description string `msgpack:"description,omitempty"`
	Handler     string `msgpack:"handler"`
}

// MigrationJobPayload is the msgpack wire shape a data migration job
// carries across the WASM boundary as its jobqueue.WASMJobArgs.Payload —
// shared directly by import between the engine (which marshals it in
// internal/engine/jobdispatch.EnqueueApplicableDataMigration) and this
// SDK (which unmarshals it in engine.DispatchDataMigration), rather than
// independently mirrored the way internal/engine/event.Envelope and this
// package's own wireEvent are: unlike event.Envelope, nothing here is
// engine-internal, so both sides can share one definition safely.
type MigrationJobPayload struct {
	Handler     string `msgpack:"handler"`
	TenantID    string `msgpack:"tenant_id"`
	FromVersion string `msgpack:"from_version"`
	ToVersion   string `msgpack:"to_version"`
}

// MigrationContext carries the tenant/version bounds of one data
// migration handler invocation (migration-guide.md §4), plus progress
// reporting and logging. Log/RecordProgress write through the module's
// own stdout — already wired via WASI to the engine's structured logger
// (internal/engine/wasm/runtime.go's WithStdout, tagged component=wasm)
// — rather than a dedicated host call; see MigrationContext's own
// construction site (engine.DispatchDataMigration) for how a wire
// MigrationJobPayload becomes one of these.
type MigrationContext struct {
	TenantID    string
	FromVersion string
	ToVersion   string

	// handler is unexported: not part of migration-guide.md §4's
	// documented field list, only used to prefix this context's own
	// Log/RecordProgress lines so they're identifiable in the shared
	// component=wasm stream.
	handler string
}

// NewMigrationContext builds a MigrationContext from a decoded
// MigrationJobPayload — exported so engine.DispatchDataMigration
// (a different package) can construct one without this package exposing
// its otherwise-unexported handler field through a struct literal.
func NewMigrationContext(payload MigrationJobPayload) *MigrationContext {
	return &MigrationContext{
		TenantID:    payload.TenantID,
		FromVersion: payload.FromVersion,
		ToVersion:   payload.ToVersion,
		handler:     payload.Handler,
	}
}

// Log writes msg, with fields appended as key=value pairs, to the
// module's own stdout — see MigrationContext's own doc comment for why
// that reaches the engine's structured logger without a dedicated host
// call.
func (c *MigrationContext) Log(msg string, fields ...any) {
	fmt.Fprintf(os.Stdout, "[data_migration] tenant=%s handler=%s %s%s\n", c.TenantID, c.handler, msg, formatFields(fields))
}

// RecordProgress reports n additional records processed — a Log call
// under a fixed message, so progress lines are identifiable in the same
// stream without a separate reporting path.
func (c *MigrationContext) RecordProgress(n int) {
	c.Log("progress", "records", n)
}

// formatFields renders a Log call's variadic key/value pairs as
// " key=value key=value" (leading space, empty string if there are none)
// — an odd trailing key with no paired value is rendered with a
// "!MISSING" placeholder value rather than silently dropped, so a
// caller's mistake is visible in the log line instead of losing data
// quietly.
func formatFields(fields []any) string {
	if len(fields) == 0 {
		return ""
	}
	var b strings.Builder
	for i := 0; i < len(fields); i += 2 {
		key := fmt.Sprint(fields[i])
		value := "!MISSING"
		if i+1 < len(fields) {
			value = fmt.Sprint(fields[i+1])
		}
		fmt.Fprintf(&b, " %s=%s", key, value)
	}
	return b.String()
}
