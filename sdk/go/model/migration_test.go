package model

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

func TestDataMigration_MsgpackRoundTrip(t *testing.T) {
	original := DataMigration{
		FromVersion: "< 1.4.0",
		ToVersion:   ">= 1.4.0",
		Description: "Backfill display_name from name",
		Handler:     "backfill_display_name",
	}

	data, err := msgpack.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded DataMigration
	if err := msgpack.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded != original {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", decoded, original)
	}
}

// captureStdout redirects os.Stdout for the duration of fn and returns
// everything written to it — Log/RecordProgress write there directly
// (see MigrationContext's own doc comment for why), so this is the only
// way to observe their output from a test.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	original := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = original }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}
	return string(out)
}

func TestMigrationContext_LogIncludesTenantAndHandler(t *testing.T) {
	ctx := NewMigrationContext(MigrationJobPayload{Handler: "backfill_display_name", TenantID: "tenant-9"})

	got := captureStdout(t, func() { ctx.Log("starting backfill") })

	if !strings.Contains(got, "tenant=tenant-9") {
		t.Errorf("Log output = %q, want it to contain tenant=tenant-9", got)
	}
	if !strings.Contains(got, "handler=backfill_display_name") {
		t.Errorf("Log output = %q, want it to contain handler=backfill_display_name", got)
	}
	if !strings.Contains(got, "starting backfill") {
		t.Errorf("Log output = %q, want it to contain the message", got)
	}
}

func TestMigrationContext_LogFormatsFieldPairs(t *testing.T) {
	ctx := NewMigrationContext(MigrationJobPayload{Handler: "h"})

	got := captureStdout(t, func() { ctx.Log("progress", "records", 42, "table", "contacts") })

	if !strings.Contains(got, "records=42") {
		t.Errorf("Log output = %q, want records=42", got)
	}
	if !strings.Contains(got, "table=contacts") {
		t.Errorf("Log output = %q, want table=contacts", got)
	}
}

func TestMigrationContext_LogOddFieldCountMarksMissingValue(t *testing.T) {
	ctx := NewMigrationContext(MigrationJobPayload{Handler: "h"})

	got := captureStdout(t, func() { ctx.Log("msg", "dangling_key") })

	if !strings.Contains(got, "dangling_key=!MISSING") {
		t.Errorf("Log output = %q, want dangling_key=!MISSING", got)
	}
}

func TestMigrationContext_RecordProgressLogsRecordCount(t *testing.T) {
	ctx := NewMigrationContext(MigrationJobPayload{Handler: "h"})

	got := captureStdout(t, func() { ctx.RecordProgress(17) })

	if !strings.Contains(got, "records=17") {
		t.Errorf("RecordProgress output = %q, want records=17", got)
	}
}

func TestNewMigrationContext_CopiesPayloadFields(t *testing.T) {
	ctx := NewMigrationContext(MigrationJobPayload{
		Handler:     "h",
		TenantID:    "t1",
		FromVersion: "1.0.0",
		ToVersion:   "1.1.0",
	})

	if ctx.TenantID != "t1" || ctx.FromVersion != "1.0.0" || ctx.ToVersion != "1.1.0" {
		t.Errorf("NewMigrationContext() = %+v, want TenantID=t1 FromVersion=1.0.0 ToVersion=1.1.0", ctx)
	}
}
