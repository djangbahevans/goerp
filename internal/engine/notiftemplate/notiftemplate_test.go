package notiftemplate

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// captureLog temporarily redirects the global zerolog logger to a buffer
// for the duration of a test, restoring it on cleanup — there's no
// existing precedent for this in the codebase, so this is a one-off
// local helper rather than a shared testing package addition.
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

// writeDirFixture writes files (relative path -> content) under root.
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

// writeZipFixture writes files (relative path -> content) into a new zip
// at zipPath.
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

	locale, tmpl, ok := mt.Resolve("order_confirmed", "email", "fr-CA")
	if !ok {
		t.Fatal("Resolve(email, fr-CA) = not ok, want a language-only fallback match")
	}
	if locale != "fr" {
		t.Errorf("locale = %q, want %q (language-only fallback)", locale, "fr")
	}
	if tmpl.Ext != "html" {
		t.Errorf("Ext = %q, want %q", tmpl.Ext, "html")
	}

	out, err := Render(tmpl, locale, map[string]any{"UserFirstName": "Ama", "OrderReference": "ORD-1"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out, "Ama") || !strings.Contains(out, "ORD-1") {
		t.Errorf("rendered output = %q, want it to contain the substituted values", out)
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

	locale, tmpl, ok := mt.Resolve("order_confirmed", "in_app", "en")
	if !ok || locale != "en" {
		t.Fatalf("Resolve(in_app, en) = (%q, %v), want (\"en\", true)", locale, ok)
	}
	if tmpl.Ext != "json" {
		t.Errorf("Ext = %q, want %q", tmpl.Ext, "json")
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

func TestResolve_FallsBackToEnWhenNoMatchAtAll(t *testing.T) {
	root := t.TempDir()
	writeDirFixture(t, root, map[string]string{
		"notifications/order_confirmed/sms.en.txt": "hi",
	})

	mt, err := Load(orderConfirmedType(map[string]string{
		"sms": "notifications/order_confirmed/sms.{locale}.txt",
	}), root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	locale, _, ok := mt.Resolve("order_confirmed", "sms", "de-DE")
	if !ok || locale != "en" {
		t.Fatalf("Resolve(sms, de-DE) = (%q, %v), want (\"en\", true) via final-resort fallback", locale, ok)
	}
}

func TestResolve_NoMatchReturnsNotOk(t *testing.T) {
	mt, err := Load(orderConfirmedType(map[string]string{}), t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	_, _, ok := mt.Resolve("order_confirmed", "push", "en")
	if ok {
		t.Error("expected ok=false for a channel with no declared template at all")
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
	// Exactly 160 runes of a 2-byte-each character (320 UTF-8 bytes) — a
	// byte-counting implementation would wrongly warn here (320 > 160);
	// a rune-counting one (the confirmed design) must not, since the
	// rune count is exactly at, not over, the 160 threshold.
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
