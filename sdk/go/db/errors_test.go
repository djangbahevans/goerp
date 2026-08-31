package db

import (
	"errors"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

func TestIsNotFound(t *testing.T) {
	if !IsNotFound(ErrNotFound) {
		t.Error("IsNotFound(ErrNotFound) = false, want true")
	}
	if IsNotFound(ErrEtagMismatch) {
		t.Error("IsNotFound(ErrEtagMismatch) = true, want false")
	}
	if IsNotFound(errors.New("unrelated")) {
		t.Error("IsNotFound(unrelated error) = true, want false")
	}
}

func TestIsEtagMismatch(t *testing.T) {
	if !IsEtagMismatch(ErrEtagMismatch) {
		t.Error("IsEtagMismatch(ErrEtagMismatch) = false, want true")
	}
	if IsEtagMismatch(ErrNotFound) {
		t.Error("IsEtagMismatch(ErrNotFound) = true, want false")
	}
}

func TestWrapExecError_EtagMismatch(t *testing.T) {
	raw := &hostcall.HostError{Code: "db.etag_mismatch", Message: "record has been modified since it was last read"}
	if got := wrapExecError(raw); !errors.Is(got, ErrEtagMismatch) {
		t.Errorf("wrapExecError(etag_mismatch) = %v, want ErrEtagMismatch", got)
	}
}

func TestWrapExecError_NilPassesThrough(t *testing.T) {
	if wrapExecError(nil) != nil {
		t.Error("wrapExecError(nil) != nil")
	}
}

func TestWrapExecError_UnrelatedErrorPassesThroughUnchanged(t *testing.T) {
	orig := errors.New("some other failure")
	if got := wrapExecError(orig); got != orig {
		t.Errorf("wrapExecError(unrelated) = %v, want the original error unchanged", got)
	}
}

func TestWrapExecError_NonHostErrorPassesThroughUnchanged(t *testing.T) {
	// A marshal/unmarshal-layer error (e.g. from hostcall.Do itself) has
	// no *hostcall.HostError anywhere in its chain — must pass through,
	// not be silently swallowed into one of this package's own sentinels.
	orig := errors.New("marshal request: some encoding failure")
	if got := wrapExecError(orig); got != orig {
		t.Errorf("wrapExecError(non-HostError) = %v, want unchanged", got)
	}
}

func TestWrapExecError_UniqueViolation_ProducesPGError(t *testing.T) {
	raw := &hostcall.HostError{
		Code:    "db.unique_violation",
		Message: `duplicate key value violates unique constraint "widget_name_key"`,
		Details: map[string]any{"constraint": "widget_name_key", "sqlstate": "23505"},
	}
	got := wrapExecError(raw)

	var pgErr *PGError
	if !errors.As(got, &pgErr) {
		t.Fatalf("errors.As(%v, &pgErr) = false, want true", got)
	}
	if pgErr.Code != "23505" {
		t.Errorf("Code = %q, want %q", pgErr.Code, "23505")
	}
	if pgErr.ConstraintName != "widget_name_key" {
		t.Errorf("ConstraintName = %q, want %q", pgErr.ConstraintName, "widget_name_key")
	}
}

func TestWrapExecError_ForeignKeyViolation_ProducesPGError(t *testing.T) {
	raw := &hostcall.HostError{
		Code:    "db.foreign_key_violation",
		Message: "insert or update on table violates foreign key constraint",
		Details: map[string]any{"table": "widget", "column": "parent_id", "sqlstate": "23503"},
	}
	got := wrapExecError(raw)

	var pgErr *PGError
	if !errors.As(got, &pgErr) {
		t.Fatalf("errors.As(%v, &pgErr) = false, want true", got)
	}
	if pgErr.TableName != "widget" || pgErr.ColumnName != "parent_id" {
		t.Errorf("TableName/ColumnName = %q/%q, want widget/parent_id", pgErr.TableName, pgErr.ColumnName)
	}
	if pgErr.Code != "23503" {
		t.Errorf("Code = %q, want %q", pgErr.Code, "23503")
	}
}

func TestIsUniqueViolation(t *testing.T) {
	uniqueErr := wrapExecError(&hostcall.HostError{Code: "db.unique_violation", Details: map[string]any{"sqlstate": "23505"}})
	if !IsUniqueViolation(uniqueErr) {
		t.Error("IsUniqueViolation(unique violation) = false, want true")
	}

	fkErr := wrapExecError(&hostcall.HostError{Code: "db.foreign_key_violation", Details: map[string]any{"sqlstate": "23503"}})
	if IsUniqueViolation(fkErr) {
		t.Error("IsUniqueViolation(foreign key violation) = true, want false")
	}
	if IsUniqueViolation(errors.New("unrelated")) {
		t.Error("IsUniqueViolation(unrelated error) = true, want false")
	}

	// Classifies a raw, unwrapped host error the same way it classifies
	// one already wrapped into *PGError above.
	rawUniqueErr := &hostcall.HostError{Code: "db.unique_violation"}
	if !IsUniqueViolation(rawUniqueErr) {
		t.Error("IsUniqueViolation(raw unique violation) = false, want true")
	}
}

func TestIsForeignKeyViolation(t *testing.T) {
	fkErr := wrapExecError(&hostcall.HostError{Code: "db.foreign_key_violation", Details: map[string]any{"sqlstate": "23503"}})
	if !IsForeignKeyViolation(fkErr) {
		t.Error("IsForeignKeyViolation(foreign key violation) = false, want true")
	}

	uniqueErr := wrapExecError(&hostcall.HostError{Code: "db.unique_violation", Details: map[string]any{"sqlstate": "23505"}})
	if IsForeignKeyViolation(uniqueErr) {
		t.Error("IsForeignKeyViolation(unique violation) = true, want false")
	}
}

func TestIsDeadlock(t *testing.T) {
	deadlockErr := &hostcall.HostError{Code: "db.exec_error", Message: "ERROR: deadlock detected (SQLSTATE 40P01)", Details: map[string]any{"sqlstate": "40P01"}}
	if !IsDeadlock(deadlockErr) {
		t.Error("IsDeadlock(deadlock error) = false, want true")
	}

	// A generic db.exec_error with a different (or no) sqlstate must not
	// be misclassified as a deadlock.
	otherExecErr := &hostcall.HostError{Code: "db.exec_error", Message: "ERROR: null value in column violates not-null constraint", Details: map[string]any{"sqlstate": "23502"}}
	if IsDeadlock(otherExecErr) {
		t.Error("IsDeadlock(not-null violation) = true, want false")
	}
	noDetailsErr := &hostcall.HostError{Code: "db.exec_error", Message: "some other failure"}
	if IsDeadlock(noDetailsErr) {
		t.Error("IsDeadlock(no Details) = true, want false")
	}

	if IsDeadlock(wrapExecError(&hostcall.HostError{Code: "db.unique_violation", Details: map[string]any{"sqlstate": "23505"}})) {
		t.Error("IsDeadlock(unique violation) = true, want false")
	}
}
