package notiftemplate

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	original := log.Logger
	log.Logger = zerolog.New(&buf)
	t.Cleanup(func() { log.Logger = original })
	return &buf
}

func orderConfirmedType(templates map[string]string) []manifest.NotificationType {
	return []manifest.NotificationType{
		{
			Name:              "order_confirmed",
			Label:             "Order Confirmed",
			DefaultChannels:   []string{"in_app"},
			AvailableChannels: []string{"in_app", "email", "sms"},
			Templates:         templates,
			DataSchema:        map[string]string{"OrderReference": "string"},
		},
	}
}

func writeDirFixture(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
}

func writeZipFixture(t *testing.T, zipPath string, files map[string]string) {
	t.Helper()
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create %s: %v", zipPath, err)
	}
	defer f.Close()

	w := zip.NewWriter(f)
	for name, content := range files {
		entry, err := w.Create(name)
		if err != nil {
			t.Fatalf("create entry %s: %v", name, err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatalf("write entry %s: %v", name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
}

func TestLoad_NoNotificationTypesIsNoop(t *testing.T) {
	mt, err := Load(nil, t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if mt != nil {
		t.Errorf("expected nil ModuleTemplates, got %+v", mt)
	}
}

func TestLoad_FromLooseDirectory(t *testing.T) {
	root := t.TempDir()
	writeDirFixture(t, root, map[string]string{
		"notifications/order_confirmed/email.en.html":  "<p>Hi {{.UserFirstName}}, order {{.OrderReference}}</p>",
		"notifications/order_confirmed/email.fr.html":  "<p>Bonjour {{.UserFirstName}}, commande {{.OrderReference}}</p>",
		"notifications/order_confirmed/in_app.en.json": `{"title":"Order confirmed","body":"{{.OrderReference}}"}`,
		"notifications/order_confirmed/sms.en.txt":     "{{.TenantName}}: order {{.OrderReference}} confirmed",
	})

	mt, err := Load(orderConfirmedType(map[string]string{
		"email":  "notifications/order_confirmed/email.{locale}.html",
		"in_app": "notifications/order_confirmed/in_app.{locale}.json",
		"sms":    "notifications/order_confirmed/sms.{locale}.txt",
	}), root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if mt == nil {
		t.Fatal("expected non-nil ModuleTemplates")
	}

	email := mt.templates["order_confirmed.email"]
	if email == nil || email.Ext != "html" || len(email.Sources) != 2 || !strings.Contains(string(email.Sources["fr"]), "Bonjour") {
		t.Fatalf("email template = %+v, want the html en and fr variants", email)
	}
	for _, name := range []string{"order_confirmed.in_app", "order_confirmed.sms"} {
		if tmpl := mt.templates[name]; tmpl == nil || len(tmpl.Sources) != 1 {
			t.Errorf("%s = %+v, want its en variant", name, tmpl)
		}
	}
}

func TestLoad_FromZipPackage(t *testing.T) {
	root := t.TempDir()
	zipPath := filepath.Join(root, "demo.erp")
	writeZipFixture(t, zipPath, map[string]string{
		"notifications/order_confirmed/in_app.en.json": `{"title":"Order confirmed","body":"{{.OrderReference}}"}`,
	})

	mt, err := Load(orderConfirmedType(map[string]string{
		"in_app": "notifications/order_confirmed/in_app.{locale}.json",
	}), zipPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	tmpl := mt.templates["order_confirmed.in_app"]
	if tmpl == nil || tmpl.Ext != "json" || string(tmpl.Sources["en"]) != `{"title":"Order confirmed","body":"{{.OrderReference}}"}` {
		t.Fatalf("in_app template = %+v, want the zip's en variant", tmpl)
	}
}

func TestLoad_ErrorsWhenEnVariantMissing(t *testing.T) {
	root := t.TempDir()
	writeDirFixture(t, root, map[string]string{
		"notifications/order_confirmed/email.fr.html": "<p>Bonjour</p>",
	})

	_, err := Load(orderConfirmedType(map[string]string{
		"email": "notifications/order_confirmed/email.{locale}.html",
	}), root)
	if err == nil {
		t.Fatal("expected an error when the declared channel has no en variant")
	}
	if !strings.Contains(err.Error(), "en") {
		t.Errorf("error = %q, want it to mention the missing en variant", err)
	}
}

func TestLoad_ErrorsOnUnparseableTemplate(t *testing.T) {
	root := t.TempDir()
	writeDirFixture(t, root, map[string]string{
		"notifications/order_confirmed/email.en.html": "<p>{{.Unclosed",
	})

	_, err := Load(orderConfirmedType(map[string]string{
		"email": "notifications/order_confirmed/email.{locale}.html",
	}), root)
	if err == nil {
		t.Fatal("expected a parse error for malformed template syntax")
	}
}

func TestLocaleCandidates(t *testing.T) {
	for locale, want := range map[string][]string{
		"fr-GH": {"fr-GH", "fr", "en"},
		"fr":    {"fr", "en"},
		"en-GB": {"en-GB", "en"},
		"en":    {"en"},
		"":      {"en"},
	} {
		if got := LocaleCandidates(locale); !slices.Equal(got, want) {
			t.Errorf("LocaleCandidates(%q) = %v, want %v", locale, got, want)
		}
	}
}

func TestRenderColumn_EscapesOnlyTheHTMLColumn(t *testing.T) {
	vars := map[string]any{"Name": `<b>"Ama"</b>`}
	if got, err := RenderColumn(ColHTML, "<p>{{.Name}}</p>", vars); err != nil || got != "<p>&lt;b&gt;&#34;Ama&#34;&lt;/b&gt;</p>" {
		t.Errorf("RenderColumn(html) = %q, %v, want the value escaped", got, err)
	}
	if got, err := RenderColumn(ColTitle, "Hi {{.Name}}", vars); err != nil || got != `Hi <b>"Ama"</b>` {
		t.Errorf("RenderColumn(title) = %q, %v, want the value verbatim", got, err)
	}
	if _, err := RenderColumn(ColTitle, "{{.Unclosed", vars); err == nil {
		t.Error("RenderColumn() of an unparseable template: error = nil")
	}
}

func TestLoad_SMSOverLengthWarnsButDoesNotFail(t *testing.T) {
	buf := captureLog(t)
	root := t.TempDir()
	long := strings.Repeat("a", 200)
	writeDirFixture(t, root, map[string]string{
		"notifications/order_confirmed/sms.en.txt": long,
	})

	mt, err := Load(orderConfirmedType(map[string]string{
		"sms": "notifications/order_confirmed/sms.{locale}.txt",
	}), root)
	if err != nil {
		t.Fatalf("Load: %v (expected only a warning, not a failure, for an over-length SMS template)", err)
	}
	if mt == nil {
		t.Fatal("expected a non-nil ModuleTemplates despite the over-length SMS template")
	}
	if !strings.Contains(buf.String(), "exceeds the documented 160-character guideline") {
		t.Errorf("log output = %q, want it to contain the SMS-length warning", buf.String())
	}
}

func TestLoad_SMSLengthCountsRunesNotBytes(t *testing.T) {
	buf := captureLog(t)
	root := t.TempDir()
	sms := strings.Repeat("é", 160)
	writeDirFixture(t, root, map[string]string{
		"notifications/order_confirmed/sms.en.txt": sms,
	})

	if _, err := Load(orderConfirmedType(map[string]string{
		"sms": "notifications/order_confirmed/sms.{locale}.txt",
	}), root); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if strings.Contains(buf.String(), "exceeds the documented 160-character guideline") {
		t.Errorf("log output = %q, want no SMS-length warning for exactly 160 runes (320 bytes)", buf.String())
	}
}

func TestLoad_EmailSiblingsResolveBesideTheHTMLTemplate(t *testing.T) {
	root := t.TempDir()
	writeDirFixture(t, root, map[string]string{
		"notifications/order_confirmed/email.en.html": "<p>{{.OrderReference}}</p>",
		"notifications/order_confirmed/email.en.json": `{"subject": "Order {{.OrderReference}}"}`,
		"notifications/order_confirmed/email.en.txt":  "Order {{.OrderReference}} & more",
	})
	mt, err := Load(orderConfirmedType(map[string]string{"email": "notifications/order_confirmed/email.{locale}.html"}), root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	for channel, want := range map[string]string{
		ChannelEmailSubject: `{"subject": "Order {{.OrderReference}}"}`,
		ChannelEmailText:    "Order {{.OrderReference}} & more",
	} {
		if tmpl := mt.templates["order_confirmed."+channel]; tmpl == nil || string(tmpl.Sources["en"]) != want {
			t.Errorf("%s = %+v, want the en sibling %q", channel, tmpl, want)
		}
	}
}

func TestLoad_EmailSiblingsAreOptionalButNeedEn(t *testing.T) {
	root := t.TempDir()
	writeDirFixture(t, root, map[string]string{"notifications/order_confirmed/email.en.html": "<p>hi</p>"})
	templates := orderConfirmedType(map[string]string{"email": "notifications/order_confirmed/email.{locale}.html"})
	mt, err := Load(templates, root)
	if err != nil {
		t.Fatalf("Load without siblings: %v", err)
	}
	if _, ok := mt.templates["order_confirmed."+ChannelEmailText]; ok {
		t.Error("email_text template loaded with no .txt in the package")
	}

	writeDirFixture(t, root, map[string]string{"notifications/order_confirmed/email.fr.txt": "salut"})
	if _, err := Load(templates, root); err == nil || !strings.Contains(err.Error(), `"en"`) {
		t.Errorf("Load with a fr-only text sibling = %v, want a missing-en error", err)
	}
}

func TestReadPackageFile_RejectsPathsOutsideThePackage(t *testing.T) {
	root := t.TempDir()
	writeDirFixture(t, root, map[string]string{"emails/layout.html": "ok"})
	if got, err := ReadPackageFile(root, "emails/layout.html"); err != nil || string(got) != "ok" {
		t.Errorf("ReadPackageFile = %q, %v", got, err)
	}
	if _, err := ReadPackageFile(root, "../outside.html"); err == nil {
		t.Error("ReadPackageFile(../outside.html) error = nil")
	}
}

func TestLoadFS_LoadsEveryLocaleFromAnFS(t *testing.T) {
	fsys := fstest.MapFS{
		"t/order_confirmed/in_app.en.json": {Data: []byte(`{"title": "Order {{.OrderReference}}"}`)},
		"t/order_confirmed/in_app.fr.json": {Data: []byte(`{"title": "Commande {{.OrderReference}}"}`)},
	}
	mt, err := LoadFS(orderConfirmedType(map[string]string{"in_app": "t/order_confirmed/in_app.{locale}.json"}), fsys)
	if err != nil {
		t.Fatalf("LoadFS() error: %v", err)
	}
	rows, err := mt.Rows("sales")
	if err != nil || len(rows) != 2 || rows[1].Locale != "fr" || rows[1].Fields[ColTitle] != "Commande {{.OrderReference}}" {
		t.Errorf("Rows() = %+v, %v, want the en and fr in_app rows", rows, err)
	}
}

func TestRows_SplitsEachChannelIntoItsColumns(t *testing.T) {
	fsys := fstest.MapFS{
		"in_app.en.json":  {Data: []byte(`{"title": "Order {{.Ref}}", "body": "B", "action_url": "/_m/o/{{.ID}}", "icon": "cart"}`)},
		"email.en.html":   {Data: []byte(`<p>{{.Ref}}</p>`)},
		"email.en.json":   {Data: []byte(`{"subject": "S {{.Ref}}"}`)},
		"email.en.txt":    {Data: []byte(`text {{.Ref}}`)},
		"email.fr.html":   {Data: []byte(`<p>fr</p>`)},
		"sms.en.txt":      {Data: []byte(`sms {{.Ref}}`)},
		"push.en.json":    {Data: []byte(`{"title": "PT", "body": "PB"}`)},
		"in_app.fr.json":  {Data: []byte(`{"title": "Commande"}`)},
		"unused/x.en.txt": {Data: []byte(`ignored`)},
	}
	types := orderConfirmedType(map[string]string{
		"in_app": "in_app.{locale}.json", "email": "email.{locale}.html", "sms": "sms.{locale}.txt", "push": "push.{locale}.json",
	})
	mt, err := LoadFS(types, fsys)
	if err != nil {
		t.Fatalf("LoadFS() error: %v", err)
	}
	rows, err := mt.Rows("sales")
	if err != nil {
		t.Fatalf("Rows() error: %v", err)
	}

	got := map[string]map[string]string{}
	for _, r := range rows {
		if r.TemplateKey != "sales.order_confirmed" {
			t.Errorf("TemplateKey = %q, want sales.order_confirmed", r.TemplateKey)
		}
		got[r.Channel+"/"+r.Locale] = r.Fields
	}
	want := map[string]map[string]string{
		"in_app/en": {ColTitle: "Order {{.Ref}}", ColBody: "B", ColActionURL: "/_m/o/{{.ID}}", ColIcon: "cart"},
		"in_app/fr": {ColTitle: "Commande"},
		"email/en":  {ColHTML: "<p>{{.Ref}}</p>", ColSubject: "S {{.Ref}}", ColText: "text {{.Ref}}"},
		"email/fr":  {ColHTML: "<p>fr</p>"},
		"sms/en":    {ColSMS: "sms {{.Ref}}"},
		"push/en":   {ColPushTitle: "PT", ColPushBody: "PB"},
	}
	if len(got) != len(want) {
		t.Errorf("Rows() = %d rows, want %d: %v", len(got), len(want), got)
	}
	for id, fields := range want {
		for col, v := range fields {
			if got[id][col] != v {
				t.Errorf("%s %s = %q, want %q", id, col, got[id][col], v)
			}
		}
		if len(got[id]) != len(fields) {
			t.Errorf("%s has fields %v, want only %v", id, got[id], fields)
		}
	}
}

func TestRows_NilTemplatesHaveNoRows(t *testing.T) {
	var mt *ModuleTemplates
	rows, err := mt.Rows("sales")
	if rows != nil || err != nil {
		t.Errorf("Rows() = (%v, %v), want (nil, nil)", rows, err)
	}
}

func TestLoad_IgnoresExtraJSONKeysAndRejectsAnUnusableVariant(t *testing.T) {
	fsys := fstest.MapFS{
		"in_app.en.json": {Data: []byte(`{"title": "T", "meta": {"a": 1}}`)},
	}
	mt, err := LoadFS(orderConfirmedType(map[string]string{"in_app": "in_app.{locale}.json"}), fsys)
	if err != nil {
		t.Fatalf("LoadFS() error: %v", err)
	}
	rows, err := mt.Rows("sales")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Locale != "en" || rows[0].Fields[ColTitle] != "T" {
		t.Errorf("Rows() = %+v, want only the en variant", rows)
	}
	fsys["in_app.fr.json"] = new(fstest.MapFile{Data: []byte(`{"title": 5}`)})
	if _, err := LoadFS(orderConfirmedType(map[string]string{"in_app": "in_app.{locale}.json"}), fsys); err == nil || !strings.Contains(err.Error(), "not a string") {
		t.Errorf("LoadFS() = %v, want rejection of the non-string title", err)
	}
}

func TestLoad_JSONTemplateExpressionsAreDecodedBeforeParsing(t *testing.T) {
	source := `{"title":"{{printf \"%s\" .OrderReference}}"}`
	fsys := fstest.MapFS{"in_app.en.json": {Data: []byte(source)}}
	loaded, err := LoadFS(orderConfirmedType(map[string]string{"in_app": "in_app.{locale}.json"}), fsys)
	if err != nil {
		t.Fatal(err)
	}

	rows, err := loaded.Rows("sales")
	if err != nil {
		t.Fatal(err)
	}
	got, err := RenderColumn(ColTitle, rows[0].Fields[ColTitle], map[string]any{"OrderReference": "SO-001"})
	if err != nil || got != "SO-001" {
		t.Errorf("RenderColumn() = %q, %v", got, err)
	}
	if string(loaded.Files()["in_app.en.json"]) != source {
		t.Errorf("Files() changed the raw JSON template: %v", loaded.Files())
	}
}

func TestLoad_RejectsInvalidTemplatePaths(t *testing.T) {
	for _, path := range []string{"../email.{locale}.html", "/email.{locale}.html", `dir\email.{locale}.html`, "email.{locale}.{locale}.html"} {
		t.Run(path, func(t *testing.T) {
			_, err := LoadFS(orderConfirmedType(map[string]string{"email": path}), fstest.MapFS{})
			if err == nil || !strings.Contains(err.Error(), "must be a package path") {
				t.Errorf("LoadFS() = %v", err)
			}
		})
	}
}
