package cronsettings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/auth/membership/membershiptest"
	"github.com/djangbahevans/goerp/internal/engine/authaudit"
	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

func setup(t *testing.T) (*Store, *sql.DB, tenant.Tenant) {
	t.Helper()
	conn := membershiptest.New(t)
	store := tenant.NewStore(conn)
	if err := authaudit.NewStore(conn, store).Bootstrap(t.Context()); err != nil {
		t.Fatal(err)
	}
	return NewStore(conn), conn, createTenant(t, conn, store, "cronchoices")
}

func createTenant(t *testing.T, conn *sql.DB, store *tenant.Store, slug string) tenant.Tenant {
	t.Helper()
	tn, err := store.CreateTenant(t.Context(), slug, "Cron choices")
	if err != nil {
		t.Fatal(err)
	}
	if err := tenantschema.Create(t.Context(), conn, slug); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tenantschema.Drop(context.Background(), conn, slug) })
	return *tn
}

func read(t *testing.T, s *Store, slug string, identity Identity) State {
	t.Helper()
	states, err := s.Read(t.Context(), slug, []Identity{identity})
	if err != nil {
		t.Fatal(err)
	}
	return states[identity]
}

func TestEmptyInitializationDoesNotUseDatabase(t *testing.T) {
	s, conn, tn := setup(t)
	id := Identity{Module: "emptyfixture", Name: "dormant"}
	if err := s.Initialize(t.Context(), tn.Slug, id.Module, []manifest.CronJob{{Name: id.Name, EnabledByDefault: new(false)}}); err != nil {
		t.Fatal(err)
	}
	initial := read(t, s, tn.Slug, id)

	var database string
	if err := conn.QueryRowContext(t.Context(), `SELECT current_database()`).Scan(&database); err != nil {
		t.Fatal(err)
	}
	closed := membershiptest.Open(t, database, "goerp")
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	closedStore := NewStore(closed)

	for _, jobs := range [][]manifest.CronJob{nil, {}} {
		if err := closedStore.Initialize(t.Context(), tn.Slug, id.Module, jobs); err != nil {
			t.Fatalf("empty initialization accessed closed database: %v", err)
		}
	}

	if got := read(t, s, tn.Slug, id); got != initial {
		t.Fatalf("empty initialization changed dormant state: got=%+v want=%+v", got, initial)
	}
}

func TestEmptyInitializationDoesNotBootstrapTable(t *testing.T) {
	s, conn, tn := setup(t)
	if err := s.Initialize(t.Context(), tn.Slug, "emptyfixture", nil); err != nil {
		t.Fatal(err)
	}

	var exists bool
	if err := conn.QueryRowContext(t.Context(), `SELECT to_regclass($1) IS NOT NULL`, tenantschema.Name(tn.Slug)+"."+TableName).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("empty initialization created cron settings table")
	}
}

func TestInitializationAndIdentityLifecycle(t *testing.T) {
	s, conn, tn := setup(t)
	job := manifest.CronJob{Name: "opt_in", EnabledByDefault: new(false)}
	id := Identity{Module: "cronfixture", Name: job.Name}
	if err := s.Initialize(t.Context(), tn.Slug, id.Module, []manifest.CronJob{job, {Name: "default_on"}}); err != nil {
		t.Fatal(err)
	}
	initial := read(t, s, tn.Slug, id)
	if initial.Enabled || initial.UpdatedBy.Valid || initial.Generation == "" || initial.UpdatedAt.IsZero() {
		t.Fatalf("false default initialization = %+v", initial)
	}
	if !read(t, s, tn.Slug, Identity{Module: id.Module, Name: "default_on"}).Enabled {
		t.Fatal("omitted default did not initialize enabled")
	}

	job.EnabledByDefault = new(true)
	for range 2 {
		if err := s.Initialize(t.Context(), tn.Slug, id.Module, []manifest.CronJob{job, {Name: "renamed"}}); err != nil {
			t.Fatal(err)
		}
	}
	if got := read(t, s, tn.Slug, id); got != initial {
		t.Fatalf("initialization overwrote choice: %+v, want %+v", got, initial)
	}
	if !read(t, s, tn.Slug, Identity{Module: id.Module, Name: "renamed"}).Enabled {
		t.Fatal("renamed job did not initialize a new choice")
	}
	if err := s.Initialize(t.Context(), tn.Slug, id.Module, nil); err != nil {
		t.Fatal(err)
	}
	if read(t, s, tn.Slug, id) != initial {
		t.Fatal("removed declaration lost its dormant choice")
	}

	other := createTenant(t, conn, tenant.NewStore(conn), "othercronchoices")
	if err := s.Initialize(t.Context(), other.Slug, id.Module, []manifest.CronJob{job}); err != nil {
		t.Fatal(err)
	}
	if !read(t, s, other.Slug, id).Enabled || read(t, s, tn.Slug, id).Enabled {
		t.Fatal("tenant choices are not independent")
	}

	if err := s.RemoveModule(t.Context(), id.Module); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{tn.Slug, other.Slug} {
		if _, err := s.Read(t.Context(), slug, []Identity{id}); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("read after uninstall = %v", err)
		}
		if err := s.Initialize(t.Context(), slug, id.Module, []manifest.CronJob{job}); err != nil {
			t.Fatal(err)
		}
		state := read(t, s, slug, id)
		if !state.Enabled || state.Generation == initial.Generation {
			t.Fatalf("reinstall retained old generation/default: %+v", state)
		}
	}
}

func TestToggleAuditsAndAdmissionGeneration(t *testing.T) {
	s, conn, tn := setup(t)
	id := Identity{Module: "cronfixture", Name: "digest"}
	if err := s.Initialize(t.Context(), tn.Slug, id.Module, []manifest.CronJob{{Name: id.Name}}); err != nil {
		t.Fatal(err)
	}
	initial := read(t, s, tn.Slug, id)
	actor := uuid.NewV7().String()
	if err := s.Admit(t.Context(), tn.Slug, id, initial.Generation); err != nil {
		t.Fatal(err)
	}

	disabled, err := s.SetEnabled(t.Context(), tn.ID, tn.Slug, id, false, initial.Generation, actor)
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Enabled || disabled.Generation == initial.Generation || !disabled.UpdatedAt.After(initial.UpdatedAt) || disabled.UpdatedBy.String != actor {
		t.Fatalf("disabled state = %+v", disabled)
	}
	if err := s.Admit(t.Context(), tn.Slug, id, initial.Generation); !errors.Is(err, ErrObsolete) {
		t.Fatalf("disabled admission = %v", err)
	}
	same, err := s.SetEnabled(t.Context(), tn.ID, tn.Slug, id, false, initial.Generation, uuid.NewV7().String())
	if err != nil || same != disabled {
		t.Fatalf("same-state setter = %+v, %v", same, err)
	}
	if _, err := s.SetEnabled(t.Context(), tn.ID, tn.Slug, id, true, initial.Generation, actor); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale toggle = %v", err)
	}

	enabled, err := s.SetEnabled(t.Context(), tn.ID, tn.Slug, id, true, disabled.Generation, actor)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Admit(t.Context(), tn.Slug, id, initial.Generation); !errors.Is(err, ErrObsolete) {
		t.Fatalf("old generation after re-enable = %v", err)
	}
	if err := s.Admit(t.Context(), tn.Slug, id, enabled.Generation); err != nil {
		t.Fatal(err)
	}

	var n int
	var about sql.NullString
	var auditActor, generation string
	var previous, current bool
	err = conn.QueryRowContext(t.Context(), `SELECT count(*) FROM system.auth_audit_log WHERE tenant_id = $1`, tn.ID).Scan(&n)
	if err != nil || n != 2 {
		t.Fatalf("audit count = %d, %v; want one per real change", n, err)
	}
	err = conn.QueryRowContext(t.Context(), `SELECT user_id, actor_user_id, metadata->>'generation',
		(metadata->>'previous_enabled')::boolean, (metadata->>'enabled')::boolean
		FROM system.auth_audit_log WHERE tenant_id = $1 AND event_type = 'cron.enabled'`, tn.ID).
		Scan(&about, &auditActor, &generation, &previous, &current)
	if err != nil || about.Valid || auditActor != actor || generation != enabled.Generation || previous || !current {
		t.Fatalf("toggle audit about=%v actor=%s generation=%s transition=%v/%v err=%v", about, auditActor, generation, previous, current, err)
	}
}

func TestAuditFailureRollsBackToggle(t *testing.T) {
	s, conn, tn := setup(t)
	id := Identity{Module: "cronfixture", Name: "digest"}
	if err := s.Initialize(t.Context(), tn.Slug, id.Module, []manifest.CronJob{{Name: id.Name}}); err != nil {
		t.Fatal(err)
	}
	initial := read(t, s, tn.Slug, id)
	if _, err := conn.ExecContext(t.Context(), `CREATE FUNCTION system.reject_cron_audit() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'audit unavailable'; END $$;
		CREATE TRIGGER reject_cron_audit BEFORE INSERT ON system.auth_audit_log FOR EACH ROW EXECUTE FUNCTION system.reject_cron_audit()`); err != nil {
		t.Fatal(err)
	}

	_, err := s.SetEnabled(t.Context(), tn.ID, tn.Slug, id, false, initial.Generation, uuid.NewV7().String())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("audit failure = %v", err)
	}
	if read(t, s, tn.Slug, id) != initial {
		t.Fatal("audit failure committed the choice")
	}
}

func TestAdmissionWaitsForToggleAndReleasesLock(t *testing.T) {
	s, conn, tn := setup(t)
	id := Identity{Module: "cronfixture", Name: "digest"}
	if err := s.Initialize(t.Context(), tn.Slug, id.Module, []manifest.CronJob{{Name: id.Name}}); err != nil {
		t.Fatal(err)
	}
	initial := read(t, s, tn.Slug, id)
	tx, err := conn.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(t.Context(), fmt.Sprintf(`UPDATE %s.cron_job_settings SET enabled = false WHERE module_name = $1`, tenantschema.Name(tn.Slug)), id.Module); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if err := s.Admit(ctx, tn.Slug, id, initial.Generation); err == nil {
		t.Fatal("admission did not wait for overlapping state write")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := s.Admit(t.Context(), tn.Slug, id, initial.Generation); err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := s.SetEnabled(ctx, tn.ID, tn.Slug, id, false, initial.Generation, uuid.NewV7().String()); err != nil {
		t.Fatalf("admitted handler boundary held settings lock: %v", err)
	}
}

func TestReadFailureAndModuleRoleIsolation(t *testing.T) {
	s, conn, tn := setup(t)
	id := Identity{Module: "cronfixture", Name: "digest"}
	if _, err := s.Read(t.Context(), tn.Slug, []Identity{id}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("absent table read = %v", err)
	}
	if err := s.Initialize(t.Context(), tn.Slug, id.Module, []manifest.CronJob{{Name: id.Name}}); err != nil {
		t.Fatal(err)
	}
	for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
		var allowed bool
		err := conn.QueryRowContext(t.Context(), `SELECT has_table_privilege($1, $2, $3)`, "tenant_"+tn.Slug, tenantschema.Name(tn.Slug)+".cron_job_settings", privilege).Scan(&allowed)
		if err != nil || allowed {
			t.Fatalf("module role %s permission=%v error=%v", privilege, allowed, err)
		}
	}
	if _, err := s.Read(t.Context(), tn.Slug, []Identity{{Module: id.Module, Name: "missing"}}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing row read = %v", err)
	}
}

func TestConcurrentInitializationAndCleanupSerialization(t *testing.T) {
	s, conn, tn := setup(t)
	id := Identity{Module: "cronfixture", Name: "digest"}
	jobs := []manifest.CronJob{{Name: id.Name}}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := s.Initialize(t.Context(), tn.Slug, id.Module, jobs); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	initial := read(t, s, tn.Slug, id)

	err := db.WithAdvisoryLock(t.Context(), conn, []int64{moduleLock(id.Module)}, func(tx *sql.Tx) error {
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		if err := s.Initialize(ctx, tn.Slug, id.Module, jobs); err == nil {
			t.Error("initialization bypassed uninstall serialization")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Initialize(t.Context(), tn.Slug, id.Module, jobs); err != nil {
		t.Fatal(err)
	}
	if read(t, s, tn.Slug, id) != initial {
		t.Fatal("concurrent initializers changed existing state")
	}
}
