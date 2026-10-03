package wasm

import (
	"testing"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
)

func TestDataMigration_MissingSchemaSyncPoolFailsClosed(t *testing.T) {
	ctx := t.Context()
	r := newHostDBTestRuntime(t, nil, 1)
	mc := newTestModuleContext("migration", abi.CapDBRead|abi.CapDBWrite, r.TxLimiter())
	r.SetDataMigrationContext(mc)
	inst := newHostDBCaller(t, ctx, r, mc)

	env := callHost(t, ctx, inst, "call_begin", abiv1.DBBeginInput{})
	if env.OK || env.Error == nil || env.Error.Code != abiv1.ErrCodeUnavailable {
		t.Fatalf("begin without schema-sync pool = %+v, want abi.unavailable", env)
	}

	if !r.TxLimiter().TryAcquire() {
		t.Fatal("failed begin leaked a transaction limiter slot")
	}

	r.TxLimiter().Release()

	mc = newExecTestModuleContext("migration")
	r.SetDataMigrationContext(mc)

	_, hostErr := DBExec(ctx, nil, mc, abiv1.DBExecInput{SQL: "UPDATE widget SET name = 'migrated'"})
	if hostErr == nil || hostErr.Code != abiv1.ErrCodeUnavailable {
		t.Fatalf("exec without schema-sync pool = %+v, want abi.unavailable", hostErr)
	}

	if _, err := beginTenantScopedRead(ctx, nil, mc); err == nil {
		t.Fatal("ORM read without schema-sync pool succeeded")
	}

	if _, err := beginTenantScopedWrite(ctx, nil, mc); err == nil {
		t.Fatal("ORM write without schema-sync pool succeeded")
	}
}
