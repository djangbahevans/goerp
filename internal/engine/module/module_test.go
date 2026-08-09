package module

import "testing"

func TestLoadedModule_Fail_SetsStatusAndReason(t *testing.T) {
	m := &LoadedModule{Status: StatusWarming}

	m.Fail("checksum mismatch")

	if m.Status != StatusFailed {
		t.Errorf("Status = %v, want %v", m.Status, StatusFailed)
	}
	if m.FailureReason != "checksum mismatch" {
		t.Errorf("FailureReason = %q, want %q", m.FailureReason, "checksum mismatch")
	}
}

func TestLoadedModule_FailDependency_SetsStatusAndFormattedReason(t *testing.T) {
	m := &LoadedModule{Status: StatusPending}

	m.FailDependency("accounting")

	if m.Status != StatusFailed {
		t.Errorf("Status = %v, want %v", m.Status, StatusFailed)
	}
	want := "skipped: depends on failed module 'accounting'"
	if m.FailureReason != want {
		t.Errorf("FailureReason = %q, want %q", m.FailureReason, want)
	}
}

func TestModuleStatus_String(t *testing.T) {
	cases := map[ModuleStatus]string{
		StatusPending:    "pending",
		StatusCompiling:  "compiling",
		StatusSyncing:    "syncing",
		StatusWarming:    "warming",
		StatusReady:      "ready",
		StatusFailed:     "failed",
		StatusDraining:   "draining",
		StatusDisabled:   "disabled",
		StatusUnloaded:   "unloaded",
		ModuleStatus(99): "unknown_status",
	}
	for status, want := range cases {
		if got := status.String(); got != want {
			t.Errorf("ModuleStatus(%d).String() = %q, want %q", status, got, want)
		}
	}
}

func TestSchemaSyncStatus_String(t *testing.T) {
	cases := map[SchemaSyncStatus]string{
		SchemaSyncOK:         "ok",
		SchemaSyncFailed:     "failed",
		SchemaSyncInProgress: "in_progress",
		SchemaSyncStatus(99): "unknown_status",
	}
	for status, want := range cases {
		if got := status.String(); got != want {
			t.Errorf("SchemaSyncStatus(%d).String() = %q, want %q", status, got, want)
		}
	}
}
