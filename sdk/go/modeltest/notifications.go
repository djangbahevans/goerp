package modeltest

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/notiftemplate"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/sdk/go/internal/notifydata"
	"github.com/djangbahevans/goerp/sdk/go/notify"
)

// Notification is a committed notification from the module under test.
type Notification struct {
	ID        string
	UserID    string
	Type      string
	Title     string
	Body      string
	ActionURL string
	Icon      string
	CreatedAt time.Time
}

// RenderedTemplate contains the bundled channel's rendered fields. Email HTML is
// the template body before the delivery layout and unsubscribe footer are applied.
type RenderedTemplate struct {
	Title     string
	Body      string
	ActionURL string
	Icon      string
	Subject   string
	HTML      string
	Text      string
}

// TestNotifications reads committed notifications and delivery plans for this
// harness. Channel assertions check delivery rows, without waiting for providers.
type TestNotifications struct {
	t          failer
	ctx        context.Context
	db         *sql.DB
	tenantID   string
	tenantSlug string
	moduleName string
	userID     string
	rows       []notiftemplate.Row
	now        func() time.Time
}

// AssertSent fails unless def has at least one committed notification.
func (n *TestNotifications) AssertSent[D any](def notify.Def[D]) {
	n.t.Helper()
	n.assertCount(def.Name(), "", true)
}

// AssertNotSent fails if def has a committed notification.
func (n *TestNotifications) AssertNotSent[D any](def notify.Def[D]) {
	n.t.Helper()
	n.assertCount(def.Name(), "", false)
}

// AssertSentN fails unless def has exactly count committed notifications.
func (n *TestNotifications) AssertSentN[D any](def notify.Def[D], count int) {
	n.t.Helper()
	if got := n.count(def.Name(), ""); got != count {
		n.t.Fatalf("modeltest: notification %q sent %d times, want %d", n.qualified(def.Name()), got, count)
	}
}

// Last returns def's most recent committed notification across all recipients,
// or fails the test if none exists. Read and dismissed notifications are included.
func (n *TestNotifications) Last[D any](def notify.Def[D]) *Notification {
	n.t.Helper()
	query := fmt.Sprintf(`SELECT id, user_id, type, title, COALESCE(body, ''),
        COALESCE(action_url, ''), COALESCE(icon, ''), created_at
        FROM %s.notifications WHERE tenant_id = $1 AND module = $2 AND type = $3
        ORDER BY id DESC LIMIT 1`, tenantschema.Name(n.tenantSlug))

	var result Notification
	if err := n.db.QueryRowContext(n.ctx, query, n.tenantID, n.moduleName, n.qualified(def.Name())).Scan(
		&result.ID, &result.UserID, &result.Type, &result.Title, &result.Body,
		&result.ActionURL, &result.Icon, &result.CreatedAt,
	); err != nil {
		n.t.Fatalf("modeltest: last notification %q: %v", n.qualified(def.Name()), err)
	}

	return &result
}

// AssertSentViaInApp fails unless def has an in-app delivery.
func (n *TestNotifications) AssertSentViaInApp[D any](def notify.Def[D]) {
	n.t.Helper()
	n.assertCount(def.Name(), notify.ChannelInApp, true)
}

// AssertNotSentViaInApp fails if def has an in-app delivery.
func (n *TestNotifications) AssertNotSentViaInApp[D any](def notify.Def[D]) {
	n.t.Helper()
	n.assertCount(def.Name(), notify.ChannelInApp, false)
}

// AssertSentViaEmail fails unless def has an email delivery.
func (n *TestNotifications) AssertSentViaEmail[D any](def notify.Def[D]) {
	n.t.Helper()
	n.assertCount(def.Name(), notify.ChannelEmail, true)
}

// AssertNotSentViaEmail fails if def has an email delivery.
func (n *TestNotifications) AssertNotSentViaEmail[D any](def notify.Def[D]) {
	n.t.Helper()
	n.assertCount(def.Name(), notify.ChannelEmail, false)
}

// AssertSentViaSMS fails unless def has an SMS delivery.
func (n *TestNotifications) AssertSentViaSMS[D any](def notify.Def[D]) {
	n.t.Helper()
	n.assertCount(def.Name(), notify.ChannelSMS, true)
}

// AssertNotSentViaSMS fails if def has an SMS delivery.
func (n *TestNotifications) AssertNotSentViaSMS[D any](def notify.Def[D]) {
	n.t.Helper()
	n.assertCount(def.Name(), notify.ChannelSMS, false)
}

// AssertSentViaPush fails unless def has a push delivery.
func (n *TestNotifications) AssertSentViaPush[D any](def notify.Def[D]) {
	n.t.Helper()
	n.assertCount(def.Name(), notify.ChannelPush, true)
}

// AssertNotSentViaPush fails if def has a push delivery.
func (n *TestNotifications) AssertNotSentViaPush[D any](def notify.Def[D]) {
	n.t.Helper()
	n.assertCount(def.Name(), notify.ChannelPush, false)
}

func (n *TestNotifications) qualified(name string) string {
	if name == "" {
		n.t.Fatalf("modeltest: notification definition has an empty name")
	}

	return n.moduleName + "." + name
}

func (n *TestNotifications) count(name, channel string) int {
	n.t.Helper()
	query := fmt.Sprintf(`SELECT count(*) FROM %s.notifications n
        WHERE n.tenant_id = $1 AND n.module = $2 AND n.type = $3`, tenantschema.Name(n.tenantSlug))
	args := []any{n.tenantID, n.moduleName, n.qualified(name)}
	if channel != "" {
		query += fmt.Sprintf(` AND EXISTS (SELECT 1 FROM %s.notification_deliveries d
            WHERE d.notification_id = n.id AND d.tenant_id = n.tenant_id AND d.channel = $4)`, tenantschema.Name(n.tenantSlug))
		args = append(args, channel)
	}

	var count int
	if err := n.db.QueryRowContext(n.ctx, query, args...).Scan(&count); err != nil {
		n.t.Fatalf("modeltest: read notification %q deliveries (%s): %v", n.qualified(name), channel, err)
	}

	return count
}

func (n *TestNotifications) assertCount(name, channel string, sent bool) {
	n.t.Helper()
	count := n.count(name, channel)
	if (count > 0) != sent {
		n.t.Fatalf("modeltest: notification %q via %q: found %d, want sent=%t", n.qualified(name), channel, count, sent)
	}
}

// RenderTemplate renders def's bundled template against data using its JSON
// field names. Locale lookup tries the exact locale, language, then English;
// email parts fall back independently. Missing channels and rendering errors return errors.
// Standard variables describe the harness's default user and tenant; URL variables
// default to empty strings and may be supplied in data. Tenant overrides are ignored.
func (n *TestNotifications) RenderTemplate[D any](def notify.Def[D], channel, locale string, data D) (*RenderedTemplate, error) {
	if def.Name() == "" {
		return nil, fmt.Errorf("modeltest: notification definition has an empty name")
	}
	columns, ok := notiftemplate.ChannelColumns[channel]
	if !ok {
		return nil, fmt.Errorf("modeltest: unknown notification channel %q", channel)
	}
	vars, err := notifydata.Values(data)
	if err != nil {
		return nil, fmt.Errorf("modeltest: template data: %w", err)
	}
	if err := n.standardVars(vars); err != nil {
		return nil, err
	}

	fields := map[string]string{}
	found := false
	for _, candidate := range notiftemplate.LocaleCandidates(locale) {
		for _, row := range n.rows {
			if row.TemplateKey != n.moduleName+"."+def.Name() || row.Channel != channel || row.Locale != candidate {
				continue
			}
			found = true
			for _, col := range columns {
				if _, exists := fields[col]; exists {
					continue
				}
				if src, exists := row.Fields[col]; exists {
					fields[col] = src
				}
			}
			// In-app and push JSON fields form one locale variant, while email
			// subject, HTML and text originate from independent files.
			if channel != notify.ChannelEmail {
				break
			}
		}
		if found && channel != notify.ChannelEmail {
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("modeltest: bundled template %q (%s, %s) not found", def.Name(), channel, locale)
	}

	result := &RenderedTemplate{}
	destinations := map[string]*string{
		notiftemplate.ColTitle:     &result.Title,
		notiftemplate.ColBody:      &result.Body,
		notiftemplate.ColActionURL: &result.ActionURL,
		notiftemplate.ColIcon:      &result.Icon,
		notiftemplate.ColSubject:   &result.Subject,
		notiftemplate.ColHTML:      &result.HTML,
		notiftemplate.ColText:      &result.Text,
		notiftemplate.ColSMS:       &result.Body,
		notiftemplate.ColPushTitle: &result.Title,
		notiftemplate.ColPushBody:  &result.Body,
	}
	for _, col := range columns {
		if src, exists := fields[col]; exists {
			rendered, err := notiftemplate.RenderColumn(col, src, vars)
			if err != nil {
				return nil, fmt.Errorf("modeltest: render %s %s: %w", def.Name(), channel, err)
			}
			*destinations[col] = rendered
		}
	}

	return result, nil
}

func (n *TestNotifications) standardVars(vars map[string]any) error {
	var tenantName, userName string
	query := `SELECT t.name, COALESCE(p.name, '') FROM system.tenants t
        LEFT JOIN system.user_profiles p ON p.user_id = $2 WHERE t.id = $1`
	if err := n.db.QueryRowContext(n.ctx, query, n.tenantID, n.userID).Scan(&tenantName, &userName); err != nil {
		return fmt.Errorf("modeltest: load template standard variables: %w", err)
	}

	firstName, _, _ := strings.Cut(userName, " ")
	vars["TenantName"] = tenantName
	vars["UserName"] = userName
	vars["UserFirstName"] = firstName
	vars["Year"] = n.now().Year()
	for _, key := range []string{"TenantLogoURL", "UnsubscribeURL", "ActionURL"} {
		if _, exists := vars[key]; !exists {
			vars[key] = ""
		}
	}

	return nil
}
