package notifications

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/notiftemplate"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

func newTemplatesStore(t *testing.T) (*Store, string) {
	t.Helper()
	conn, err := db.New(testPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", testPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	slug := fmt.Sprintf("notiftpltest%d", time.Now().UnixNano())
	schema := tenantschema.Name(slug)
	if _, err := conn.ExecContext(t.Context(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec("DROP SCHEMA " + schema + " CASCADE") })

	s := NewStore(conn)
	if err := s.BootstrapTemplates(t.Context(), slug); err != nil {
		t.Fatalf("BootstrapTemplates() error: %v", err)
	}
	if err := s.BootstrapTemplates(t.Context(), slug); err != nil {
		t.Fatalf("BootstrapTemplates() again error: %v", err)
	}
	return s, slug
}

func inAppRow(key, locale, title string) notiftemplate.Row {
	return notiftemplate.Row{TemplateKey: key, Channel: "in_app", Locale: locale, Fields: map[string]string{notiftemplate.ColTitle: title}}
}

func TestTemplates_OverrideWinsAndResetFallsBackToTheDefault(t *testing.T) {
	s, slug := newTemplatesStore(t)
	ctx := t.Context()

	if err := s.SeedDefaultTemplates(ctx, slug, "sales", []notiftemplate.Row{inAppRow("sales.order", "en", "Default")}); err != nil {
		t.Fatalf("SeedDefaultTemplates() error: %v", err)
	}
	got, err := s.Template(ctx, slug, "sales.order", "in_app", "en")
	if err != nil || got.IsOverride || got.Fields[notiftemplate.ColTitle] != "Default" {
		t.Fatalf("Template() = (%+v, %v), want the default", got, err)
	}

	if err := s.SaveTemplateOverride(ctx, slug, inAppRow("sales.order", "en", "Mine")); err != nil {
		t.Fatalf("SaveTemplateOverride() error: %v", err)
	}
	if err := s.SaveTemplateOverride(ctx, slug, inAppRow("sales.order", "en", "Mine v2")); err != nil {
		t.Fatalf("SaveTemplateOverride() again error: %v", err)
	}
	got, err = s.Template(ctx, slug, "sales.order", "in_app", "en")
	if err != nil || !got.IsOverride || got.Fields[notiftemplate.ColTitle] != "Mine v2" {
		t.Fatalf("Template() = (%+v, %v), want the override", got, err)
	}

	if err := s.ResetTemplate(ctx, slug, "sales.order", "in_app", "en"); err != nil {
		t.Fatalf("ResetTemplate() error: %v", err)
	}
	got, err = s.Template(ctx, slug, "sales.order", "in_app", "en")
	if err != nil || got.IsOverride || got.Fields[notiftemplate.ColTitle] != "Default" {
		t.Fatalf("Template() after reset = (%+v, %v), want the default", got, err)
	}
	if err := s.ResetTemplate(ctx, slug, "sales.order", "in_app", "en"); err != nil {
		t.Errorf("ResetTemplate() with no override error: %v", err)
	}
}

func TestTemplates_ReseedingKeepsOverridesRefreshesDefaultsAndDropsStaleOnes(t *testing.T) {
	s, slug := newTemplatesStore(t)
	ctx := t.Context()

	first := []notiftemplate.Row{inAppRow("sales.order", "en", "v1"), inAppRow("sales.order", "fr", "v1 fr"), inAppRow("sales.invoice", "en", "inv")}
	if err := s.SeedDefaultTemplates(ctx, slug, "sales", first); err != nil {
		t.Fatal(err)
	}
	if err := s.SeedDefaultTemplates(ctx, slug, "crm", []notiftemplate.Row{inAppRow("crm.lead", "en", "lead")}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveTemplateOverride(ctx, slug, inAppRow("sales.order", "en", "Mine")); err != nil {
		t.Fatal(err)
	}

	upgrade := []notiftemplate.Row{inAppRow("sales.order", "en", "v2"), inAppRow("sales.invoice", "en", "inv")}
	if err := s.SeedDefaultTemplates(ctx, slug, "sales", upgrade); err != nil {
		t.Fatal(err)
	}

	if got, _ := s.Template(ctx, slug, "sales.order", "in_app", "en"); got == nil || !got.IsOverride || got.Fields[notiftemplate.ColTitle] != "Mine" {
		t.Errorf("override after reseed = %+v, want it untouched", got)
	}
	if err := s.ResetTemplate(ctx, slug, "sales.order", "in_app", "en"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Template(ctx, slug, "sales.order", "in_app", "en"); got == nil || got.Fields[notiftemplate.ColTitle] != "v2" {
		t.Errorf("default after reseed = %+v, want v2", got)
	}
	if _, err := s.Template(ctx, slug, "sales.order", "in_app", "fr"); !errors.Is(err, ErrTemplateNotFound) {
		t.Errorf("Template(fr) error = %v, want ErrTemplateNotFound for a dropped variant", err)
	}
	if _, err := s.Template(ctx, slug, "crm.lead", "in_app", "en"); err != nil {
		t.Errorf("another module's default was removed by reseeding sales: %v", err)
	}
}

func TestTemplates_UpsertKeepsDefaultsAbsentFromRowsAndSeedRejectsForeignKeys(t *testing.T) {
	s, slug := newTemplatesStore(t)
	ctx := t.Context()

	if err := s.SeedDefaultTemplates(ctx, slug, "sales", []notiftemplate.Row{inAppRow("sales.order", "en", "a"), inAppRow("sales.order", "fr", "b")}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertDefaultTemplates(ctx, slug, "sales", []notiftemplate.Row{inAppRow("sales.order", "en", "a2")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Template(ctx, slug, "sales.order", "in_app", "fr"); err != nil {
		t.Errorf("Template(fr) error = %v, want the default kept by UpsertDefaultTemplates", err)
	}
	if got, _ := s.Template(ctx, slug, "sales.order", "in_app", "en"); got == nil || got.Fields[notiftemplate.ColTitle] != "a2" {
		t.Errorf("Template(en) = %+v, want it refreshed", got)
	}

	if err := s.SeedDefaultTemplates(ctx, slug, "sales", []notiftemplate.Row{inAppRow("crm.lead", "en", "x")}); err == nil {
		t.Error("SeedDefaultTemplates() with a key outside the module error = nil, want one")
	}
}
