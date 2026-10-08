package model

import (
	"fmt"
	"os"
	"strings"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/db"
)

type DataMigration struct {
	FromVersion string `msgpack:"from_version"`
	ToVersion   string `msgpack:"to_version"`
	Description string `msgpack:"description,omitempty"`
	Handler     string `msgpack:"handler"`
}

// MigrationJobPayload is the wire shape a data migration job carries
// across the WASM boundary as its jobqueue.WASMJobArgs.Payload.
type MigrationJobPayload = abi.MigrationJobPayload

// MigrationContext carries the tenant and version bounds of one data
// migration handler invocation, plus progress reporting and logging. Log
// and RecordProgress write to the module's stdout, which the engine
// forwards to its structured log.
type MigrationContext struct {
	TenantID    string
	FromVersion string
	ToVersion   string

	// handler prefixes Log and RecordProgress lines so they're
	// identifiable in the shared module log stream.
	handler string
}

// NewMigrationContext builds a MigrationContext from a decoded
// MigrationJobPayload.
func NewMigrationContext(payload MigrationJobPayload) *MigrationContext {
	return &MigrationContext{
		TenantID:    payload.TenantID,
		FromVersion: payload.FromVersion,
		ToVersion:   payload.ToVersion,
		handler:     payload.Handler,
	}
}

// Log writes msg, with fields appended as key=value pairs, to the
// engine's structured log.
func (c *MigrationContext) Log(msg string, fields ...any) {
	fmt.Fprintf(os.Stdout, "[data_migration] tenant=%s handler=%s %s%s\n", c.TenantID, c.handler, msg, formatFields(fields))
}

// RecordProgress logs that n more records were processed.
func (c *MigrationContext) RecordProgress(n int) {
	c.Log("progress", "records", n)
}

// DropColumn drops table's column immediately. Schema sync never drops
// columns on its own; this is the explicit opt-in. Only valid from inside
// a data migration handler; the host rejects it otherwise.
func (c *MigrationContext) DropColumn(table, column string) error {
	return db.MigrationDropColumn(table, column)
}

// DropTable drops table immediately; the table-level counterpart of
// DropColumn.
func (c *MigrationContext) DropTable(table string) error {
	return db.MigrationDropTable(table)
}

// formatFields renders key/value pairs as " key=value key=value". An
// unpaired trailing key gets the value "!MISSING" so the mistake shows in
// the log.
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
