package job

import (
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
)

func TestJobRegistry_Register_RecordsOwningModule(t *testing.T) {
	r := New()

	if err := r.Register("mailer", []manifest.JobType{
		{Name: "mailer.send_digest"},
		{Name: "mailer.retry_bounced"},
	}); err != nil {
		t.Fatalf("Register() error: %v", err)
	}

	owner, ok := r.Owner("mailer.send_digest")
	if !ok {
		t.Fatal("Owner(mailer.send_digest) not found")
	}
	if owner != "mailer" {
		t.Errorf("owner = %q, want %q", owner, "mailer")
	}

	owner, ok = r.Owner("mailer.retry_bounced")
	if !ok {
		t.Fatal("Owner(mailer.retry_bounced) not found")
	}
	if owner != "mailer" {
		t.Errorf("owner = %q, want %q", owner, "mailer")
	}
}

func TestJobRegistry_Owner_UnregisteredReturnsFalse(t *testing.T) {
	r := New()

	if _, ok := r.Owner("does.not.exist"); ok {
		t.Fatal("expected ok=false for an unregistered job type name")
	}
}

func TestJobRegistry_Register_SameModuleIsIdempotent(t *testing.T) {
	r := New()

	if err := r.Register("mailer", []manifest.JobType{{Name: "mailer.send_digest"}}); err != nil {
		t.Fatalf("first Register() error: %v", err)
	}
	if err := r.Register("mailer", []manifest.JobType{{Name: "mailer.send_digest"}}); err != nil {
		t.Fatalf("re-Register() by the same module should not error: %v", err)
	}

	owner, _ := r.Owner("mailer.send_digest")
	if owner != "mailer" {
		t.Errorf("owner = %q, want %q", owner, "mailer")
	}
}

func TestJobRegistry_Register_DifferentModuleNameConflictErrors(t *testing.T) {
	r := New()

	if err := r.Register("mailer", []manifest.JobType{{Name: "shared.cleanup"}}); err != nil {
		t.Fatalf("first Register() error: %v", err)
	}

	err := r.Register("billing", []manifest.JobType{{Name: "shared.cleanup"}})
	if err == nil {
		t.Fatal("expected an error when a second module registers an already-owned job type name")
	}

	owner, _ := r.Owner("shared.cleanup")
	if owner != "mailer" {
		t.Errorf("owner after failed conflicting Register() = %q, want %q (unchanged)", owner, "mailer")
	}
}

func TestJobRegistry_Register_ConflictIsAllOrNothingForTheCall(t *testing.T) {
	r := New()

	if err := r.Register("mailer", []manifest.JobType{{Name: "shared.cleanup"}}); err != nil {
		t.Fatalf("first Register() error: %v", err)
	}

	// billing's call also declares a non-conflicting name — the whole call should still fail.
	err := r.Register("billing", []manifest.JobType{
		{Name: "billing.charge_invoice"},
		{Name: "shared.cleanup"},
	})
	if err == nil {
		t.Fatal("expected an error for the conflicting call")
	}

	if _, ok := r.Owner("billing.charge_invoice"); ok {
		t.Error("expected billing.charge_invoice to not be registered after a conflicting call")
	}
}
