package module

import (
	"encoding/json/v2"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/sdk/go/notify"
)

func writeNotificationFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()

	for name, source := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func notificationDeclaration() notify.NotificationDeclaration {
	return notify.NotificationDeclaration{
		Name:              "confirmed",
		Label:             "Confirmed",
		Description:       "An order is confirmed",
		DefaultChannels:   []string{"in_app", "email"},
		AvailableChannels: []string{"in_app", "email", "sms", "push"},
		DefaultPriority:   notify.High,
		Templates:         map[string]string{"email": "emails/order.{locale}.html"},
		DataStruct:        true,
		DataSchema: map[string]string{
			"reference": "string",
			"Count":     "int",
			"Total":     "float",
			"Paid":      "bool",
			"Items":     "other",
		},
	}
}

func notificationFiles() map[string]string {
	return map[string]string{
		"notifications/confirmed/in_app.en.json": `{"title":"{{.reference}}", "body":"{{if .Paid}}{{.Count}}{{else}}{{.Total}}{{end}}", "action_url":"{{.ActionURL}}"}`,
		"notifications/confirmed/in_app.fr.json": `{"title":"Commande {{.reference}}"}`,
		"emails/order.en.html":                   `<p>{{.TenantName}} {{.TenantLogoURL}} {{.UserName}} {{.UserFirstName}} {{.ActionURL}} {{.UnsubscribeURL}} {{.Year}} {{.reference}}</p>{{range .Items}}{{.NestedField}}{{end}}`,
		"emails/order.fr.html":                   `<p>Commande {{.reference}}</p>`,
		"emails/order.en.json":                   `{"subject":"{{.reference}}"}`,
		"emails/order.en.txt":                    "{{.reference}}",
		"notifications/confirmed/sms.en.txt":     "{{.reference}}",
		"notifications/confirmed/push.en.json":   `{"title":"{{.reference}}"}`,
	}
}

func TestNotificationsCollector_MetadataAndTemplates(t *testing.T) {
	dir := t.TempDir()
	writeNotificationFiles(t, dir, notificationFiles())
	definition := notificationDeclaration()
	declarations := decls(t, map[string][]any{notify.KindNotification: {definition}})

	raw := collectJSON(t, notificationsCollector{}, declarations, ModuleInfo{Name: "orders", Dir: dir})
	var entries []manifest.NotificationType
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %v, want one notification", entries)
	}
	entry := entries[0]
	if entry.Name != definition.Name || entry.Label != definition.Label || entry.Description != definition.Description || entry.DefaultPriority != "high" {
		t.Errorf("metadata = %+v", entry)
	}
	if !reflect.DeepEqual(entry.DefaultChannels, definition.DefaultChannels) || !reflect.DeepEqual(entry.AvailableChannels, definition.AvailableChannels) {
		t.Errorf("channels = %+v", entry)
	}
	wantSchema := maps.Clone(definition.DataSchema)
	delete(wantSchema, "Items")
	if !reflect.DeepEqual(entry.DataSchema, wantSchema) {
		t.Errorf("data_schema = %v, want %v", entry.DataSchema, wantSchema)
	}
	wantPaths := map[string]string{
		"in_app": "notifications/confirmed/in_app.{locale}.json",
		"email":  "emails/order.{locale}.html",
		"sms":    "notifications/confirmed/sms.{locale}.txt",
		"push":   "notifications/confirmed/push.{locale}.json",
	}
	if !reflect.DeepEqual(entry.Templates, wantPaths) {
		t.Errorf("templates = %v, want %v", entry.Templates, wantPaths)
	}
}

func TestNotificationsCollector_Failures(t *testing.T) {
	tests := []struct {
		name   string
		change func(*notify.NotificationDeclaration, map[string]string)
		want   []string
	}{
		{
			name: "available in app missing",
			change: func(d *notify.NotificationDeclaration, _ map[string]string) {
				d.AvailableChannels = []string{"email"}
			},
			want: []string{"available channels must include in_app"},
		},
		{
			name: "default not available",
			change: func(d *notify.NotificationDeclaration, _ map[string]string) {
				d.AvailableChannels = []string{"in_app", "sms", "push"}
			},
			want: []string{`default channel "email" is not available`},
		},
		{
			name: "unknown channel",
			change: func(d *notify.NotificationDeclaration, _ map[string]string) {
				d.AvailableChannels = append(d.AvailableChannels, "fax")
			},
			want: []string{`unknown channel "fax"`},
		},
		{
			name: "missing file",
			change: func(_ *notify.NotificationDeclaration, f map[string]string) {
				delete(f, "notifications/confirmed/sms.en.txt")
			},
			want: []string{"sms.{locale}.txt", `no "en" variant`},
		},
		{
			name: "missing english fallback",
			change: func(_ *notify.NotificationDeclaration, f map[string]string) {
				delete(f, "emails/order.en.html")
			},
			want: []string{"order.{locale}.html", `no "en" variant`},
		},
		{
			name: "invalid JSON",
			change: func(_ *notify.NotificationDeclaration, f map[string]string) {
				f["notifications/confirmed/push.en.json"] = "{"
			},
			want: []string{"push.{locale}.json", "not a JSON object"},
		},
		{
			name: "invalid syntax",
			change: func(_ *notify.NotificationDeclaration, f map[string]string) {
				f["emails/order.en.txt"] = "{{if}}"
			},
			want: []string{"order.{locale}.txt", "parse"},
		},
		{
			name: "path outside package",
			change: func(d *notify.NotificationDeclaration, _ map[string]string) {
				d.Templates["email"] = "../order.{locale}.html"
			},
			want: []string{"must be a package path"},
		},
	}
	for _, name := range []string{"notifications/confirmed/in_app.fr.json", "notifications/confirmed/push.en.json", "emails/order.fr.html", "emails/order.en.json", "emails/order.en.txt", "notifications/confirmed/sms.en.txt"} {
		tests = append(tests, struct {
			name   string
			change func(*notify.NotificationDeclaration, map[string]string)
			want   []string
		}{
			name: "unknown variable in " + name,
			change: func(_ *notify.NotificationDeclaration, f map[string]string) {
				f[name] = strings.Replace(f[name], ".reference", ".Typo", 1)
			},
			want: []string{name, ".Typo"},
		})
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			definition := notificationDeclaration()
			files := notificationFiles()
			test.change(&definition, files)
			writeNotificationFiles(t, dir, files)

			_, err := notificationsCollector{}.Collect(decls(t, map[string][]any{notify.KindNotification: {definition}}), ModuleInfo{Name: "orders", Dir: dir})
			if err == nil {
				t.Fatal("Collect succeeded, want failure")
			}
			for _, want := range test.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %v, want %q", err, want)
				}
			}
		})
	}
}

func TestNotificationTemplate_FieldScopes(t *testing.T) {
	tests := []struct {
		source string
		want   string
	}{
		{source: "{{range .Items}}{{.Unlisted}}{{else}}{{.AlsoUnlisted}}{{end}}"},
		{source: "{{with .Items}}{{if .Unlisted}}{{.AlsoUnlisted}}{{end}}{{end}}"},
		{source: "{{if .Paid}}{{.reference}}{{else}}{{.Count}}{{end}}"},
		{source: `{{printf "%s" .reference}}`},
		{source: `{{define "logo"}}Logo{{end}}{{template "logo"}}`},
		{source: `{{define "item"}}{{.NestedField}}{{end}}{{template "item" .Items}}`},
		{source: `{{define "item"}}{{.NestedField}}{{end}}{{range .Items}}{{template "item" .}}{{end}}`},
		{source: `{{define "order"}}{{.reference}}{{end}}{{template "order" .}}`},
		{source: `{{define "order"}}{{.Missing}}{{end}}{{template "order" .}}`, want: ".Missing"},
		{source: "{{range .Missing}}{{.Unlisted}}{{end}}", want: ".Missing"},
		{source: "{{with .Missing}}{{.Unlisted}}{{end}}", want: ".Missing"},
		{source: "{{if .Paid}}{{.Missing}}{{end}}", want: ".Missing"},
		{source: "{{if .Missing}}x{{end}}", want: ".Missing"},
		{source: "{{(.Missing).Nested}}", want: ".Missing"},
	}
	for _, test := range tests {
		t.Run(test.source, func(t *testing.T) {
			err := checkNotificationTemplate("email.en.html", []byte(test.source), notificationDeclaration())
			if test.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Errorf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestNotificationsCollector_EmptyAndDuplicate(t *testing.T) {
	collector := notificationsCollector{}
	if got := collectJSON(t, collector, Declarations{}, ModuleInfo{}); got != "[]" {
		t.Errorf("empty notifications = %s", got)
	}

	dir := t.TempDir()
	writeNotificationFiles(t, dir, notificationFiles())
	definition := notificationDeclaration()
	_, err := collector.Collect(decls(t, map[string][]any{notify.KindNotification: {definition, definition}}), ModuleInfo{Dir: dir})
	if err == nil || !strings.Contains(err.Error(), "declared more than once") {
		t.Errorf("duplicate = %v", err)
	}
}
