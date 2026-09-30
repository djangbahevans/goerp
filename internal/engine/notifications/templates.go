package notifications

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/notiftemplate"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

// TemplatesTable is the editable-templates table's unqualified name.
const TemplatesTable = "notification_templates"

// ErrTemplateNotFound: no default or override row exists for the
// requested (template key, channel, locale).
var ErrTemplateNotFound = errors.New("notification template not found")

// BootstrapTemplates creates notification_templates in the given tenant's
// schema if it doesn't already exist. A (template key, channel, locale) has
// at most one shipped default row (is_default = true) and at most one
// tenant override row (is_default = false), which coexist so that
// deleting the override falls back to the default.
func (s *Store) BootstrapTemplates(ctx context.Context, tenantSlug string) error {
	schema := tenantschema.Name(tenantSlug)
	return s.bootstrap(ctx, "notifications.BootstrapTemplates:"+tenantSlug, []string{
		fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s.notification_templates (
			    id                   UUID PRIMARY KEY DEFAULT uuidv7(),
			    template_key         TEXT NOT NULL,
			    channel              TEXT NOT NULL CHECK (channel IN ('in_app','email','sms','push')),
			    locale               TEXT NOT NULL DEFAULT 'en',
			    title_template       TEXT,
			    body_template        TEXT,
			    action_url_template  TEXT,
			    icon                 TEXT,
			    subject_template     TEXT,
			    html_template        TEXT,
			    text_template        TEXT,
			    sms_template         TEXT,
			    push_title_template  TEXT,
			    push_body_template   TEXT,
			    is_default           BOOLEAN NOT NULL DEFAULT false,
			    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			    UNIQUE (template_key, channel, locale, is_default)
			)
		`, schema),
	})
}

// SeedDefaultTemplates makes the is_default rows of every template key
// under moduleName ("{moduleName}.{type}") exactly rows: it inserts new
// ones, refreshes the content of existing ones, and deletes a default
// whose variant rows no longer include. Tenant override rows are never
// touched.
func (s *Store) SeedDefaultTemplates(ctx context.Context, tenantSlug, moduleName string, rows []notiftemplate.Row) error {
	return s.seedDefaults(ctx, tenantSlug, moduleName, rows, true)
}

// UpsertDefaultTemplates is SeedDefaultTemplates without the deletion of
// defaults absent from rows, for a caller whose rows may be incomplete.
func (s *Store) UpsertDefaultTemplates(ctx context.Context, tenantSlug, moduleName string, rows []notiftemplate.Row) error {
	return s.seedDefaults(ctx, tenantSlug, moduleName, rows, false)
}

func (s *Store) seedDefaults(ctx context.Context, tenantSlug, moduleName string, rows []notiftemplate.Row, prune bool) error {
	table := tenantschema.Name(tenantSlug) + "." + TemplatesTable
	type variant struct {
		Key     string `json:"k"`
		Channel string `json:"c"`
		Locale  string `json:"l"`
	}
	kept := make([]variant, len(rows))
	for i, r := range rows {
		if !strings.HasPrefix(r.TemplateKey, moduleName+".") {
			return fmt.Errorf("template key %q is not under module %q", r.TemplateKey, moduleName)
		}
		kept[i] = variant{r.TemplateKey, r.Channel, r.Locale}
	}
	keptJSON, err := json.Marshal(kept)
	if err != nil {
		return fmt.Errorf("encode kept template variants: %w", err)
	}

	lock := db.AdvisoryLockKey("notifications.SeedDefaultTemplates:" + tenantSlug)
	return db.WithAdvisoryLock(ctx, s.db, []int64{lock}, func(tx *sql.Tx) error {
		if prune {
			if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
				DELETE FROM %s t
				WHERE t.is_default AND starts_with(t.template_key, $1)
				  AND NOT EXISTS (
				      SELECT 1 FROM jsonb_to_recordset($2::jsonb) AS kept(k text, c text, l text)
				      WHERE kept.k = t.template_key AND kept.c = t.channel AND kept.l = t.locale)`, table),
				moduleName+".", string(keptJSON)); err != nil {
				return fmt.Errorf("delete stale default templates: %w", err)
			}
		}
		for _, r := range rows {
			if err := upsertTemplate(ctx, tx, table, r, true); err != nil {
				return err
			}
		}
		return nil
	})
}

// SaveTemplateOverride upserts the tenant's override (is_default = false)
// of r's (template key, channel, locale).
func (s *Store) SaveTemplateOverride(ctx context.Context, tenantSlug string, r notiftemplate.Row) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin save template override: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := upsertTemplate(ctx, tx, tenantschema.Name(tenantSlug)+"."+TemplatesTable, r, false); err != nil {
		return err
	}
	return tx.Commit()
}

// ResetTemplate deletes the tenant's override of the (template key,
// channel, locale), so lookups fall back to its default again. It does
// nothing when there is no override.
func (s *Store) ResetTemplate(ctx context.Context, tenantSlug, templateKey, channel, locale string) error {
	_, err := s.db.ExecContext(ctx, fmt.Sprintf(
		`DELETE FROM %s.notification_templates WHERE template_key = $1 AND channel = $2 AND locale = $3 AND NOT is_default`,
		tenantschema.Name(tenantSlug)), templateKey, channel, locale)
	if err != nil {
		return fmt.Errorf("reset notification template: %w", err)
	}
	return nil
}

// Templates returns the rows a send of templateKey renders from in any of
// locales: for each (channel, locale), the tenant's override if it has
// one, else the shipped default. Each row's IsOverride reports which.
func (s *Store) Templates(ctx context.Context, tenantSlug, templateKey string, locales []string) ([]StoredTemplate, error) {
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(
		`SELECT DISTINCT ON (channel, locale) channel, locale, is_default, %s FROM %s.notification_templates
		 WHERE template_key = $1 AND locale = ANY($2)
		 ORDER BY channel, locale, is_default`, strings.Join(notiftemplate.Columns, ", "), tenantschema.Name(tenantSlug)),
		templateKey, locales)
	if err != nil {
		return nil, fmt.Errorf("load notification templates: %w", err)
	}
	defer rows.Close()

	var out []StoredTemplate
	for rows.Next() {
		t := StoredTemplate{TemplateKey: templateKey, Fields: map[string]string{}}
		fields := make([]sql.NullString, len(notiftemplate.Columns))
		var isDefault bool
		dest := []any{&t.Channel, &t.Locale, &isDefault}
		for i := range fields {
			dest = append(dest, &fields[i])
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("scan notification template: %w", err)
		}
		t.IsOverride = !isDefault
		for i, col := range notiftemplate.Columns {
			if fields[i].Valid {
				t.Fields[col] = fields[i].String
			}
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load notification templates: %w", err)
	}
	return out, nil
}

// Template returns the row a send of (templateKey, channel, locale) uses:
// the tenant's override if it has one, else the shipped default.
func (s *Store) Template(ctx context.Context, tenantSlug, templateKey, channel, locale string) (*StoredTemplate, error) {
	rows, err := s.Templates(ctx, tenantSlug, templateKey, []string{locale})
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if r.Channel == channel {
			return &r, nil
		}
	}
	return nil, ErrTemplateNotFound
}

// StoredTemplate is a notification_templates row as a send reads it.
type StoredTemplate struct {
	notiftemplate.Row
	IsOverride bool
}

func upsertTemplate(ctx context.Context, tx *sql.Tx, table string, r notiftemplate.Row, isDefault bool) error {
	cols := []string{"template_key", "channel", "locale", "is_default"}
	args := []any{r.TemplateKey, r.Channel, r.Locale, isDefault}
	updates := []string{"updated_at = NOW()"}
	for _, col := range notiftemplate.Columns {
		cols = append(cols, col)
		if v, ok := r.Fields[col]; ok {
			args = append(args, v)
		} else {
			args = append(args, nil)
		}
		updates = append(updates, col+" = EXCLUDED."+col)
	}
	placeholders := make([]string, len(args))
	for i := range args {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}
	_, err := tx.ExecContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (%s) VALUES (%s) ON CONFLICT (template_key, channel, locale, is_default) DO UPDATE SET %s`,
		table, strings.Join(cols, ", "), strings.Join(placeholders, ", "), strings.Join(updates, ", ")), args...)
	if err != nil {
		return fmt.Errorf("upsert notification template %s/%s/%s: %w", r.TemplateKey, r.Channel, r.Locale, err)
	}
	return nil
}
