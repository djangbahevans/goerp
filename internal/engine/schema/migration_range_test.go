package schema

import (
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

func handlerNames(migrations []model.DataMigration) []string {
	names := make([]string, len(migrations))
	for i, m := range migrations {
		names[i] = m.Handler
	}
	return names
}

func assertHandlerOrder(t *testing.T, got []model.DataMigration, want []string) {
	t.Helper()
	gotNames := handlerNames(got)
	if len(gotNames) != len(want) {
		t.Fatalf("got %v, want %v", gotNames, want)
	}
	for i := range want {
		if gotNames[i] != want[i] {
			t.Errorf("got %v, want %v", gotNames, want)
			return
		}
	}
}

func TestApplicableDataMigrations_NoApplicableMigrations(t *testing.T) {
	migrations := []model.DataMigration{
		{FromVersion: "< 1.0.0", ToVersion: ">= 1.0.0", Handler: "split_names"},
	}

	got, err := ApplicableDataMigrations("1.5.0", "1.6.0", migrations)
	if err != nil {
		t.Fatalf("ApplicableDataMigrations() error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want no applicable migrations", handlerNames(got))
	}
}

func TestApplicableDataMigrations_SingleApplicableMigration(t *testing.T) {
	migrations := []model.DataMigration{
		{FromVersion: "< 1.4.0", ToVersion: ">= 1.4.0", Handler: "split_names"},
	}

	got, err := ApplicableDataMigrations("1.3.0", "1.5.0", migrations)
	if err != nil {
		t.Fatalf("ApplicableDataMigrations() error: %v", err)
	}
	assertHandlerOrder(t, got, []string{"split_names"})
}

func TestApplicableDataMigrations_MultipleApplicableMigrations(t *testing.T) {
	migrations := []model.DataMigration{
		{FromVersion: "< 1.4.0", ToVersion: ">= 1.4.0", Handler: "split_names"},
		{FromVersion: "< 1.6.0", ToVersion: ">= 1.6.0", Handler: "add_defaults"},
	}

	got, err := ApplicableDataMigrations("1.3.0", "1.7.0", migrations)
	if err != nil {
		t.Fatalf("ApplicableDataMigrations() error: %v", err)
	}
	assertHandlerOrder(t, got, []string{"split_names", "add_defaults"})
}

func TestApplicableDataMigrations_OutOfOrderDeclarationsResolveCorrectOrder(t *testing.T) {
	// Declared with the later-boundary migration first.
	migrations := []model.DataMigration{
		{FromVersion: "< 1.6.0", ToVersion: ">= 1.6.0", Handler: "add_defaults"},
		{FromVersion: "< 1.4.0", ToVersion: ">= 1.4.0", Handler: "split_names"},
		{FromVersion: "< 1.8.0", ToVersion: ">= 1.8.0", Handler: "backfill_slugs"},
	}

	got, err := ApplicableDataMigrations("1.3.0", "1.9.0", migrations)
	if err != nil {
		t.Fatalf("ApplicableDataMigrations() error: %v", err)
	}
	assertHandlerOrder(t, got, []string{"split_names", "add_defaults", "backfill_slugs"})
}

func TestApplicableDataMigrations_PartialUpgradeOnlyIncludesInRangeMigrations(t *testing.T) {
	migrations := []model.DataMigration{
		{FromVersion: "< 1.4.0", ToVersion: ">= 1.4.0", Handler: "split_names"},
		{FromVersion: "< 1.6.0", ToVersion: ">= 1.6.0", Handler: "add_defaults"},
	}

	// Upgrade only crosses the 1.4.0 boundary, not 1.6.0.
	got, err := ApplicableDataMigrations("1.3.0", "1.5.0", migrations)
	if err != nil {
		t.Fatalf("ApplicableDataMigrations() error: %v", err)
	}
	assertHandlerOrder(t, got, []string{"split_names"})
}

func TestApplicableDataMigrations_InvalidCurrentVersionFails(t *testing.T) {
	if _, err := ApplicableDataMigrations("not-a-version", "1.0.0", nil); err == nil {
		t.Fatal("ApplicableDataMigrations() error = nil, want an error for an invalid current version")
	}
}

func TestApplicableDataMigrations_InvalidTargetVersionFails(t *testing.T) {
	if _, err := ApplicableDataMigrations("1.0.0", "not-a-version", nil); err == nil {
		t.Fatal("ApplicableDataMigrations() error = nil, want an error for an invalid target version")
	}
}

func TestApplicableDataMigrations_InvalidConstraintFails(t *testing.T) {
	migrations := []model.DataMigration{
		{FromVersion: "not a constraint", ToVersion: ">= 1.4.0", Handler: "split_names"},
	}
	if _, err := ApplicableDataMigrations("1.3.0", "1.5.0", migrations); err == nil {
		t.Fatal("ApplicableDataMigrations() error = nil, want an error for an invalid constraint")
	}
}
