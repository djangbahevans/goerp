package wasm

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/files"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/vmihailenco/msgpack/v5"
)

type storageURLBackend struct {
	storage.Backend
	key    string
	expiry time.Duration
	err    error
}

func (b *storageURLBackend) SignedURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	b.key = key
	b.expiry = expiry
	return "https://storage.test/signed", b.err
}

func (b *storageURLBackend) PublicURL(ctx context.Context, key string) (string, error) {
	b.key = key
	return "https://storage.test/public", b.err
}

func TestStorageAccessCapabilities(t *testing.T) {
	rt := newHostStorageTestRuntime(t, nil, nil)
	inst := newHostStorageCaller(t, t.Context(), rt, newTestModuleContext("acme", 0, rt.TxLimiter()))
	for _, test := range []struct {
		operation  string
		capability abi.CapabilitySet
	}{
		{"url", abi.CapStorageWrite},
		{"metadata", abi.CapStorageWrite},
		{"delete", abi.CapStorageRead},
	} {
		inst.SetModuleContext(newTestModuleContext("acme", test.capability, rt.TxLimiter()))
		env := callHost(t, t.Context(), inst, "call_"+test.operation, abiv1.StorageFileInput{})
		if env.OK || env.Error.Code != abiv1.ErrCodeCapabilityDenied {
			t.Fatalf("%s: %+v", test.operation, env)
		}
	}
}

func TestStorageAccessLifecycle(t *testing.T) {
	primaryDB, store, slug := newHostStorageFixture(t)
	backend := &storageURLBackend{Backend: newTestLocalBackend(t)}
	rt := newHostStorageTestRuntime(t, primaryDB, backend)
	ctx := t.Context()
	tenantID, userID := uuid.NewV7().String(), uuid.NewV7().String()
	mc := NewModuleContext("request", "storage", userID, "", nil, nil, tenantID, slug, "trace", abi.CapStorageRead|abi.CapStorageWrite, rt.TxLimiter(), ModuleSnapshot{})
	inst := newHostStorageCaller(t, ctx, rt, mc)

	upload := callHost(t, ctx, inst, "call_upload", abiv1.StorageUploadInput{
		Filename:    "invoice.txt",
		ContentType: "text/plain",
		Data:        []byte("invoice"),
		Opts:        abiv1.StorageUploadOpts{Purpose: "reports"},
	})
	if !upload.OK {
		t.Fatal(upload.Error)
	}

	var uploaded abiv1.StorageUploadOutput
	if err := msgpack.Unmarshal(upload.Data, &uploaded); err != nil {
		t.Fatal(err)
	}
	input := abiv1.StorageFileInput{FileID: uploaded.FileID}
	env := callHost(t, ctx, inst, "call_metadata", input)
	if !env.OK {
		t.Fatal(env.Error)
	}

	var metadata abiv1.FileMetadata
	if err := msgpack.Unmarshal(env.Data, &metadata); err != nil {
		t.Fatal(err)
	}
	row, err := store.GetByID(ctx, slug, uploaded.FileID)
	if err != nil {
		t.Fatal(err)
	}
	want := abiv1.FileMetadata{
		ID:           uploaded.FileID,
		OriginalName: "invoice.txt",
		ContentType:  "text/plain",
		SizeBytes:    7,
		Checksum:     uploaded.ChecksumSHA256,
		UploadedBy:   userID,
		Purpose:      "reports",
		ScanStatus:   "pending",
		CreatedAt:    row.CreatedAt,
	}
	if metadata != want || metadata.CreatedAt.IsZero() {
		t.Fatalf("metadata = %+v, want %+v", metadata, want)
	}

	for _, seconds := range []*int64{nil, new(int64(1)), new(int64(86400))} {
		before := rt.Now().Unix()
		env := callHost(t, ctx, inst, "call_url", abiv1.StorageURLInput{FileID: uploaded.FileID, ExpiresInSeconds: seconds})
		if !env.OK {
			t.Fatal(env.Error)
		}

		var out abiv1.StorageURLOutput
		if err := msgpack.Unmarshal(env.Data, &out); err != nil {
			t.Fatal(err)
		}
		wantSeconds := int64(3600)
		if seconds != nil {
			wantSeconds = *seconds
		}
		if out.URL != "https://storage.test/signed" || backend.key != uploaded.StorageKey || backend.expiry != time.Duration(wantSeconds)*time.Second || out.ExpiresAt < before+wantSeconds || out.ExpiresAt > rt.Now().Unix()+wantSeconds {
			t.Fatalf("URL output = %+v, backend = %+v", out, backend)
		}
	}
	for _, seconds := range []int64{-1, 0, 86401, 1 << 62} {
		env := callHost(t, ctx, inst, "call_url", abiv1.StorageURLInput{FileID: uploaded.FileID, ExpiresInSeconds: new(seconds)})
		if env.OK || env.Error.Code != "storage.invalid_expiry" {
			t.Fatalf("expiry %d: %+v", seconds, env)
		}
	}

	backend.err = errors.New("backend credentials must stay private")
	env = callHost(t, ctx, inst, "call_url", input)
	if env.OK || env.Error.Code != "storage.backend_unavailable" || env.Error.Message != "could not obtain file URL" || !env.Error.Retry {
		t.Fatalf("backend error: %+v", env)
	}
	backend.err = nil

	_, _, otherSlug := newHostStorageFixture(t)
	other := NewModuleContext("other", "storage", "", "", nil, nil, uuid.NewV7().String(), otherSlug, "trace", abi.CapStorageRead|abi.CapStorageWrite, rt.TxLimiter(), ModuleSnapshot{})
	for _, invalid := range []string{"", "invalid", uuid.NewV7().String()} {
		for _, op := range []string{"url", "metadata", "delete"} {
			env := callHost(t, ctx, inst, "call_"+op, abiv1.StorageFileInput{FileID: invalid})
			if env.OK || env.Error.Code != "storage.file_not_found" {
				t.Fatalf("%s(%q): %+v", op, invalid, env)
			}
		}
	}
	inst.SetModuleContext(other)
	for _, op := range []string{"url", "metadata", "delete"} {
		env := callHost(t, ctx, inst, "call_"+op, input)
		if env.OK || env.Error.Code != "storage.file_not_found" {
			t.Fatalf("cross-tenant %s: %+v", op, env)
		}
	}
	inst.SetModuleContext(mc)
	if env := callHost(t, ctx, inst, "call_delete", input); !env.OK {
		t.Fatal(env.Error)
	}
	for _, op := range []string{"url", "metadata", "delete"} {
		env := callHost(t, ctx, inst, "call_"+op, input)
		if env.OK || env.Error.Code != "storage.file_not_found" {
			t.Fatalf("deleted %s: %+v", op, env)
		}
	}
	exists, err := backend.Exists(ctx, uploaded.StorageKey)
	if err != nil || !exists {
		t.Fatalf("object after soft delete: exists=%v, err=%v", exists, err)
	}
}

func TestStoragePublicURL(t *testing.T) {
	primaryDB, store, slug := newHostStorageFixture(t)
	backend := &storageURLBackend{Backend: newTestLocalBackend(t)}
	rt := newHostStorageTestRuntime(t, primaryDB, backend)
	tenantID, fileID := uuid.NewV7().String(), uuid.NewV7().String()
	mc := NewModuleContext("req", "storage", "", "", nil, nil, tenantID, slug, "trace", abi.CapStorageRead, rt.TxLimiter(), ModuleSnapshot{})
	inst := newHostStorageCaller(t, t.Context(), rt, mc)
	if err := store.Insert(t.Context(), slug, files.InsertRow{ID: fileID, TenantID: tenantID, StorageKey: "public-key", OriginalName: "logo.png", ContentType: "image/png", Purpose: "avatars", IsPublic: true}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		env := callHost(t, t.Context(), inst, "call_url", abiv1.StorageURLInput{FileID: fileID, ExpiresInSeconds: new(int64(999999))})
		if !env.OK {
			t.Fatal(env.Error)
		}
		var out abiv1.StorageURLOutput
		if err := msgpack.Unmarshal(env.Data, &out); err != nil {
			t.Fatal(err)
		}
		if out.URL != "https://storage.test/public" || out.ExpiresAt != 0 || backend.key != "public-key" || backend.expiry != 0 {
			t.Fatalf("public URL: %+v, backend: %+v", out, backend)
		}
	}
}

func TestStorageAccessWithoutBackend(t *testing.T) {
	primaryDB, store, slug := newHostStorageFixture(t)
	rt := newHostStorageTestRuntime(t, primaryDB, nil)
	tenantID, fileID := uuid.NewV7().String(), uuid.NewV7().String()
	mc := NewModuleContext("req", "storage", "", "", nil, nil, tenantID, slug, "trace", abi.CapStorageRead|abi.CapStorageWrite, rt.TxLimiter(), ModuleSnapshot{})
	inst := newHostStorageCaller(t, t.Context(), rt, mc)
	input := abiv1.StorageFileInput{FileID: fileID}
	if err := store.Insert(t.Context(), slug, files.InsertRow{ID: fileID, TenantID: tenantID, StorageKey: "private-key", OriginalName: "file.txt", ContentType: "text/plain", Purpose: "attachments"}); err != nil {
		t.Fatal(err)
	}
	env := callHost(t, t.Context(), inst, "call_url", input)
	if env.OK || env.Error.Code != "storage.backend_unavailable" {
		t.Fatalf("nil backend: %+v", env)
	}
	if env := callHost(t, t.Context(), inst, "call_metadata", input); !env.OK {
		t.Fatalf("metadata without backend: %+v", env)
	}
	if env := callHost(t, t.Context(), inst, "call_delete", input); !env.OK {
		t.Fatalf("delete without backend: %+v", env)
	}
}
