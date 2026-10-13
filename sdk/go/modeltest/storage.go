package modeltest

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/files"
	"github.com/djangbahevans/goerp/internal/engine/storage"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

// TestStorage seeds file fixtures and asserts active files in the harness tenant.
// Fixture bytes and module uploads use a temporary local backend, removed after the test.
type TestStorage struct {
	t          failer
	ctx        context.Context
	db         *sql.DB
	backend    storage.Backend
	store      *files.Store
	tenantID   string
	tenantSlug string
	userID     string
}

func newTestStorage(t failer, ctx context.Context, db *sql.DB, backend storage.Backend, tenantID, tenantSlug, userID string) *TestStorage {
	return &TestStorage{
		t:          t,
		ctx:        ctx,
		db:         db,
		backend:    backend,
		store:      files.NewStore(db),
		tenantID:   tenantID,
		tenantSlug: tenantSlug,
		userID:     userID,
	}
}

// MockFileOption configures a file seeded by MockFile.
type MockFileOption func(*mockFileOptions)

type mockFileOptions struct {
	contentType string
	purpose     string
	scanStatus  string
}

// MockFileContentType overrides the MIME type inferred from the fixture extension.
// An unknown extension defaults to application/octet-stream.
func MockFileContentType(contentType string) MockFileOption {
	return func(o *mockFileOptions) { o.contentType = contentType }
}

// MockFilePurpose overrides the default attachments purpose with a safe storage key segment.
func MockFilePurpose(purpose string) MockFileOption {
	return func(o *mockFileOptions) { o.purpose = purpose }
}

// MockFileScanStatus overrides the default clean status.
// Valid statuses are pending, clean, quarantined and skipped.
func MockFileScanStatus(status string) MockFileOption {
	return func(o *mockFileOptions) { o.scanStatus = status }
}

// MockFile seeds a private file from a local fixture and returns its UUID v7.
// Relative paths resolve from the test's working directory. The uploader is the harness user.
// Missing fixtures, invalid options and storage failures fail the test.
func (s *TestStorage) MockFile(fixturePath string, opts ...MockFileOption) string {
	s.t.Helper()

	data, err := os.ReadFile(fixturePath)
	if err != nil {
		s.t.Fatalf("modeltest: read storage fixture %q: %v", fixturePath, err)
	}

	ext := filepath.Ext(fixturePath)
	o := mockFileOptions{
		contentType: cmp.Or(mime.TypeByExtension(ext), "application/octet-stream"),
		purpose:     "attachments",
		scanStatus:  "clean",
	}
	for _, opt := range opts {
		opt(&o)
	}
	if !storage.ValidPurpose(o.purpose) {
		s.t.Fatalf("modeltest: invalid mock file purpose %q: expected a safe storage key segment", o.purpose)
	}
	switch o.scanStatus {
	case "pending", "clean", "quarantined", "skipped":
	default:
		s.t.Fatalf("modeltest: invalid mock file scan status %q: expected pending, clean, quarantined or skipped", o.scanStatus)
	}

	fileID := uuid.NewV7().String()
	key := storage.BuildKey(o.purpose, s.tenantID, fileID, ext)
	if _, err := s.backend.Upload(s.ctx, key, bytes.NewReader(data), storage.UploadOptions{ContentType: o.contentType}); err != nil {
		s.t.Fatalf("modeltest: store fixture %q: %v", fixturePath, err)
	}

	checksum := sha256.Sum256(data)
	if err := s.store.Insert(s.ctx, s.tenantSlug, files.InsertRow{
		ID:             fileID,
		TenantID:       s.tenantID,
		StorageKey:     key,
		OriginalName:   filepath.Base(fixturePath),
		ContentType:    o.contentType,
		SizeBytes:      int64(len(data)),
		ChecksumSHA256: fmt.Sprintf("%x", checksum),
		UploadedBy:     s.userID,
		Purpose:        o.purpose,
	}); err != nil {
		_ = s.backend.Delete(s.ctx, key)
		s.t.Fatalf("modeltest: seed file from fixture %q: %v", fixturePath, err)
	}

	query := fmt.Sprintf(`UPDATE %s.files SET scan_status = $1 WHERE id = $2 AND tenant_id = $3`, tenantschema.Name(s.tenantSlug))
	if _, err := s.db.ExecContext(s.ctx, query, o.scanStatus, fileID, s.tenantID); err != nil {
		s.t.Fatalf("modeltest: set fixture %q scan status: %v", fixturePath, err)
	}

	return fileID
}

// AssertUploaded fails the test unless fileID identifies an active file in the harness tenant.
// Seeded fixtures and files uploaded by module code both satisfy this assertion.
func (s *TestStorage) AssertUploaded(fileID string) {
	s.t.Helper()
	s.activeFile(fileID)
}

// AssertContentType fails the test unless the active file has the expected content type.
func (s *TestStorage) AssertContentType(fileID, contentType string) {
	s.t.Helper()

	file := s.activeFile(fileID)
	if file.ContentType != contentType {
		s.t.Fatalf("modeltest: file %q content type: expected %q, actual %q", fileID, contentType, file.ContentType)
	}
}

// AssertPurpose fails the test unless the active file has the expected storage purpose.
func (s *TestStorage) AssertPurpose(fileID, purpose string) {
	s.t.Helper()

	file := s.activeFile(fileID)
	if file.Purpose != purpose {
		s.t.Fatalf("modeltest: file %q purpose: expected %q, actual %q", fileID, purpose, file.Purpose)
	}
}

func (s *TestStorage) activeFile(fileID string) *files.File {
	s.t.Helper()

	if _, err := uuid.Parse(fileID); err != nil {
		s.t.Fatalf("modeltest: file %q was not uploaded in the harness tenant", fileID)
	}

	file, err := s.store.GetByID(s.ctx, s.tenantSlug, fileID)
	if errors.Is(err, files.ErrFileNotFound) {
		s.t.Fatalf("modeltest: file %q was not uploaded in the harness tenant", fileID)
	}
	if err != nil {
		s.t.Fatalf("modeltest: get file %q: %v", fileID, err)
	}
	if file.TenantID != s.tenantID || file.DeletedAt != nil {
		s.t.Fatalf("modeltest: file %q was not uploaded in the harness tenant", fileID)
	}
	return file
}
