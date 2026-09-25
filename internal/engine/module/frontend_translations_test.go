package module

import (
	"errors"
	"io"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/storage"
)

func TestFrontendTranslationStorageKey(t *testing.T) {
	if got, want := FrontendTranslationStorageKey("contacts", "1.2.0", "abc123", "pt-BR"), "contacts/translations/1.2.0/abc123/pt-BR.json"; got != want {
		t.Errorf("FrontendTranslationStorageKey() = %q, want %q", got, want)
	}
}

func TestFrontendTranslationsDigest_ChangesWithLocalesAndContent(t *testing.T) {
	base := frontendTranslationsDigest(map[string][]byte{"en": []byte(`{"a":"A"}`), "fr": []byte(`{"a":"À"}`)})
	for name, files := range map[string]map[string][]byte{
		"a locale dropped": {"en": []byte(`{"a":"A"}`)},
		"content changed":  {"en": []byte(`{"a":"B"}`), "fr": []byte(`{"a":"À"}`)},
		"locale renamed":   {"en": []byte(`{"a":"A"}`), "de": []byte(`{"a":"À"}`)},
	} {
		if frontendTranslationsDigest(files) == base {
			t.Errorf("%s: digest unchanged", name)
		}
	}
	if again := frontendTranslationsDigest(map[string][]byte{"fr": []byte(`{"a":"À"}`), "en": []byte(`{"a":"A"}`)}); again != base {
		t.Error("the same set gave a different digest")
	}
}

func liveFile(t *testing.T, backend storage.Backend, locale string) (string, bool) {
	t.Helper()
	key, err := LiveFrontendTranslationKey(t.Context(), backend, "contacts", "1.0.0", locale)
	if err != nil {
		t.Fatalf("LiveFrontendTranslationKey() error: %v", err)
	}
	if key == "" {
		return "", false
	}
	if ok, _ := backend.Exists(t.Context(), key); !ok {
		return "", false
	}
	rc, _, err := backend.Download(t.Context(), key)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	defer func() { _ = rc.Close() }()
	data, _ := io.ReadAll(rc)
	return string(data), true
}

func TestFrontendTranslations_UploadStagesAndActivateSwitches(t *testing.T) {
	t.Setenv("GOERP_STORAGE_LOCAL_DIR", t.TempDir())
	backend, err := storage.New("local")
	if err != nil {
		t.Fatalf("storage.New(local): %v", err)
	}
	ctx := t.Context()

	if err := PublishFrontendTranslations(ctx, backend, "contacts", "1.0.0", map[string][]byte{"en": []byte(`{"a":"1"}`), "de": []byte(`{"a":"D"}`)}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	digest, err := UploadFrontendTranslations(ctx, backend, "contacts", "1.0.0", map[string][]byte{"en": []byte(`{"a":"2"}`)})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if got, _ := liveFile(t, backend, "en"); got != `{"a":"1"}` {
		t.Errorf("en after an upload that wasn't activated = %s, want the live set's", got)
	}

	if err := ActivateFrontendTranslations(ctx, backend, "contacts", "1.0.0", digest); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if got, _ := liveFile(t, backend, "en"); got != `{"a":"2"}` {
		t.Errorf("en after activating = %s, want the new set's", got)
	}
	if _, ok := liveFile(t, backend, "de"); ok {
		t.Error("de, which the new set dropped, is still served")
	}

	if err := ActivateFrontendTranslations(ctx, backend, "contacts", "1.0.0", ""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, ok := liveFile(t, backend, "en"); ok {
		t.Error("en is still served after clearing the live set")
	}
	if err := ActivateFrontendTranslations(ctx, backend, "contacts", "1.0.0", ""); err != nil {
		t.Errorf("clearing an already-clear version: %v", err)
	}
}

func TestPublishFrontendTranslations_NoBackend(t *testing.T) {
	ctx := t.Context()
	if err := PublishFrontendTranslations(ctx, nil, "contacts", "1.0.0", nil); err != nil {
		t.Errorf("no files, no backend: error = %v, want nil", err)
	}
	err := PublishFrontendTranslations(ctx, nil, "contacts", "1.0.0", map[string][]byte{"en": []byte(`{}`)})
	if !errors.Is(err, ErrNoStorageBackend) {
		t.Errorf("files, no backend: error = %v, want ErrNoStorageBackend", err)
	}
}
