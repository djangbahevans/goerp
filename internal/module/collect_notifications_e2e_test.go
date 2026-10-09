package module

import (
	"encoding/json/v2"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/notiftemplate"
)

const notificationsFixtureMain = `package main

import "github.com/djangbahevans/goerp/sdk/go/notify"

type Item struct { Name string }
type OrderData struct {
 Reference string ` + "`json:\"reference\"`" + `
 Count int
 Total float64
 Paid bool
 Items []Item
 Ignored string ` + "`json:\"-\"`" + `
}

var Confirmed = notify.Define[OrderData]("confirmed",
 notify.Label("Confirmed"),
 notify.Description("An order is confirmed"),
 notify.DefaultChannels(notify.ChannelInApp, notify.ChannelEmail),
 notify.AvailableChannels(notify.ChannelInApp, notify.ChannelEmail, notify.ChannelSMS, notify.ChannelPush),
 notify.DefaultPriority(notify.High),
 notify.Template(notify.ChannelEmail, "emails/order.{locale}.html"),
)

func main() {}
`

func TestGenerate_NotificationsAndPackagedTemplates(t *testing.T) {
	dir := writeCollectFixture(t, notificationsFixtureMain, collectFixtureManifest)
	files := notificationFiles()
	files["notifications/confirmed/in_app.en.json"] = `{"title":"{{printf \"%s\" .reference}}", "body":"{{.Count}}"}`
	writeNotificationFiles(t, dir, files)
	ctx := generateCtx(t)

	if _, err := Generate(ctx, dir, GenerateOptions{Check: true}); err == nil || !strings.Contains(err.Error(), "notification_types") {
		t.Fatalf("--check on stale manifest = %v", err)
	}
	if got := readFile(t, filepath.Join(dir, "manifest.json")); got != collectFixtureManifest {
		t.Fatal("--check changed the manifest")
	}
	result, err := Generate(ctx, dir, GenerateOptions{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !slices.Equal(result.Blocks, []string{"notification_types"}) {
		t.Errorf("blocks = %v", result.Blocks)
	}
	got := readFile(t, filepath.Join(dir, "manifest.json"))
	if !strings.Contains(got, `"version":    "1.0.0"`) || !strings.Contains(got, `"depends_on": [ "core" ]`) {
		t.Errorf("generation changed unrelated formatting:\n%s", got)
	}
	var m manifest.Manifest
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatal(err)
	}
	if len(m.NotificationTypes) != 1 || m.NotificationTypes[0].DataSchema["reference"] != "string" || m.NotificationTypes[0].DataSchema["Count"] != "int" {
		t.Fatalf("notification types = %+v", m.NotificationTypes)
	}
	if _, ok := m.NotificationTypes[0].DataSchema["Ignored"]; ok {
		t.Error("JSON-ignored field appears in data_schema")
	}
	if _, err := Generate(ctx, dir, GenerateOptions{Check: true}); err != nil {
		t.Fatalf("--check on generated output: %v", err)
	}

	packaged, err := Package(ctx, dir, PackageOptions{SkipWasm: true, SkipFrontend: true, SkipWorker: true})
	if err != nil {
		t.Fatalf("Package: %v", err)
	}
	members := zipMembers(t, packaged.ArchivePath)
	for name, source := range files {
		if string(members[name]) != source {
			t.Errorf("package member %s = %q, want %q", name, members[name], source)
		}
	}
	loaded, err := notiftemplate.Load(m.NotificationTypes, packaged.ArchivePath)
	if err != nil {
		t.Fatalf("load packaged templates: %v", err)
	}
	rows, err := loaded.Rows(m.Name)
	if err != nil {
		t.Fatalf("decode packaged templates: %v", err)
	}
	if len(rows) != 6 {
		t.Errorf("packaged rows = %d, want six channel/locale variants", len(rows))
	}

	writeNotificationFiles(t, dir, map[string]string{"emails/order.en.html": "<p>{{.Misspelled}}</p>"})
	if _, err := Generate(ctx, dir, GenerateOptions{Check: true}); err == nil || !strings.Contains(err.Error(), ".Misspelled") {
		t.Errorf("--check with a current manifest and invalid template = %v", err)
	}
	if after := readFile(t, filepath.Join(dir, "manifest.json")); after != got {
		t.Error("failed template validation changed the manifest")
	}
	writeNotificationFiles(t, dir, map[string]string{"emails/order.en.html": files["emails/order.en.html"]})

	changed := strings.Replace(notificationsFixtureMain, `notify.Label("Confirmed")`, `notify.Label("Order Confirmed")`, 1)
	writeNotificationFiles(t, dir, map[string]string{"cmd/module/main.go": changed})
	if _, err := Generate(ctx, dir, GenerateOptions{Check: true}); err == nil || !strings.Contains(err.Error(), "notification_types") {
		t.Errorf("--check after definition edit = %v", err)
	}
	if after := readFile(t, filepath.Join(dir, "manifest.json")); after != got {
		t.Error("stale --check changed the manifest")
	}
}

func TestGenerate_NotificationTemplateFailureWritesNothing(t *testing.T) {
	dir := writeCollectFixture(t, notificationsFixtureMain, collectFixtureManifest)
	files := notificationFiles()
	files["emails/order.en.html"] = "<p>{{.Misspelled}}</p>"
	writeNotificationFiles(t, dir, files)

	for _, check := range []bool{false, true} {
		_, err := Generate(generateCtx(t), dir, GenerateOptions{Check: check})
		if err == nil || !strings.Contains(err.Error(), "emails/order.en.html") || !strings.Contains(err.Error(), ".Misspelled") {
			t.Fatalf("Generate(check=%v) = %v", check, err)
		}
		if got := readFile(t, filepath.Join(dir, "manifest.json")); got != collectFixtureManifest {
			t.Error("failed generation changed the manifest")
		}
		if _, err := os.Stat(filepath.Join(dir, "models")); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("failed generation wrote models: %v", err)
		}
	}
}
