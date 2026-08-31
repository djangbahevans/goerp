package engine

import (
	"errors"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestDispatchDataMigration_InvokesRegisteredHandlerWithDecodedContext(t *testing.T) {
	var got *model.MigrationContext
	OnDataMigration("backfill_test", func(ctx *model.MigrationContext) error {
		got = ctx
		return nil
	})
	t.Cleanup(func() { delete(migrationHandlers, "backfill_test") })

	payload, err := marshal(model.MigrationJobPayload{
		Handler:     "backfill_test",
		TenantID:    "tenant-1",
		FromVersion: "1.3.0",
		ToVersion:   "1.4.0",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	ptr := Allocate(uint32(len(payload)))
	WriteMem(ptr, payload)

	status := DispatchDataMigration(ptr, uint32(len(payload)))
	if status != 0 {
		t.Fatalf("DispatchDataMigration() status = %d, want 0", status)
	}
	if got == nil {
		t.Fatal("handler was never invoked")
	}
	if got.TenantID != "tenant-1" || got.FromVersion != "1.3.0" || got.ToVersion != "1.4.0" {
		t.Errorf("MigrationContext = %+v, want TenantID=tenant-1 FromVersion=1.3.0 ToVersion=1.4.0", got)
	}
}

func TestDispatchDataMigration_UnregisteredHandlerReturnsNonZeroStatus(t *testing.T) {
	payload, err := marshal(model.MigrationJobPayload{Handler: "no_such_handler"})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	ptr := Allocate(uint32(len(payload)))
	WriteMem(ptr, payload)

	status := DispatchDataMigration(ptr, uint32(len(payload)))
	if status == 0 {
		t.Fatal("DispatchDataMigration() status = 0, want non-zero for an unregistered handler")
	}
}

func TestDispatchDataMigration_HandlerErrorReturnsNonZeroStatus(t *testing.T) {
	OnDataMigration("failing_test", func(ctx *model.MigrationContext) error {
		return errors.New("boom")
	})
	t.Cleanup(func() { delete(migrationHandlers, "failing_test") })

	payload, err := marshal(model.MigrationJobPayload{Handler: "failing_test"})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	ptr := Allocate(uint32(len(payload)))
	WriteMem(ptr, payload)

	status := DispatchDataMigration(ptr, uint32(len(payload)))
	if status == 0 {
		t.Fatal("DispatchDataMigration() status = 0, want non-zero when the handler returns an error")
	}
}

func TestDispatchDataMigration_MalformedPayloadReturnsNonZeroStatus(t *testing.T) {
	bad := []byte("not msgpack")
	ptr := Allocate(uint32(len(bad)))
	WriteMem(ptr, bad)

	status := DispatchDataMigration(ptr, uint32(len(bad)))
	if status == 0 {
		t.Fatal("DispatchDataMigration() status = 0, want non-zero for an undecodable payload")
	}
}
