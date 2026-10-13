package modeltest

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"uuid"

	"github.com/djangbahevans/goerp/sdk/go/jobs"
	fixture "github.com/djangbahevans/goerp/sdk/go/modeltest/testdata/storagefixture"
	"github.com/djangbahevans/goerp/sdk/go/storage"
)

func storageHarness(t *testing.T) *Harness {
	t.Helper()
	t.Chdir("testdata/storagefixture")
	return NewHarness(t)
}

func assertLocalFile(t *testing.T, rawURL string, want []byte) {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "file" {
		t.Fatalf("local file URL = %q, error = %v", rawURL, err)
	}
	got, err := os.ReadFile(filepath.FromSlash(u.Path))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("file contents = %q, error = %v, want %q", got, err, want)
	}
}

func TestStorageMockFileDefaultsAndJobAccess(t *testing.T) {
	h := storageHarness(t)
	fileID := h.Storage.MockFile("contacts.csv")
	if id, err := uuid.Parse(fileID); err != nil || id[6]>>4 != 7 {
		t.Fatalf("file ID = %q, error = %v, want UUID v7", fileID, err)
	}
	h.Storage.AssertUploaded(fileID)
	h.Storage.AssertContentType(fileID, "text/csv; charset=utf-8")
	h.Storage.AssertPurpose(fileID, "attachments")

	if err := h.RunJob(fixture.Inspect, fixture.Payload{FileID: fileID}); err != nil {
		t.Fatal(err)
	}
	run := h.Jobs.LastRun(fixture.Inspect)
	metadata, err := run.DecodeResult[storage.FileMetadata]()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("contacts.csv")
	if err != nil {
		t.Fatal(err)
	}
	checksum := fmt.Sprintf("%x", sha256.Sum256(data))
	if metadata.ID != fileID || metadata.ScanStatus != "clean" || metadata.OriginalName != "contacts.csv" ||
		metadata.ContentType != "text/csv; charset=utf-8" || metadata.Purpose != "attachments" ||
		metadata.SizeBytes != int64(len(data)) || metadata.Checksum != checksum ||
		metadata.UploadedBy != h.UserID || metadata.IsPublic || metadata.CreatedAt.IsZero() {
		t.Fatalf("seeded metadata = %+v", metadata)
	}

	response := h.GET("/storageprobe/url", WithQuery("file_id", fileID))
	if response.StatusCode != 200 {
		t.Fatalf("URL response = %d: %s", response.StatusCode, response.body)
	}
	assertLocalFile(t, response.JSON("url").(string), data)

	unknownPath := filepath.Join(t.TempDir(), "fixture.unknown-extension")
	if err := os.WriteFile(unknownPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	unknownID := h.Storage.MockFile(unknownPath)
	h.Storage.AssertContentType(unknownID, "application/octet-stream")
	h.Storage.AssertPurpose(unknownID, "attachments")
}

func TestStorageMockFileOptionsAndScanStatuses(t *testing.T) {
	h := storageHarness(t)
	for _, status := range []string{"pending", "quarantined", "skipped"} {
		t.Run(status, func(t *testing.T) {
			fileID := h.Storage.MockFile("contacts.csv",
				MockFileContentType("text/csv"),
				MockFilePurpose("imports"),
				MockFileScanStatus(status),
			)
			h.Storage.AssertContentType(fileID, "text/csv")
			h.Storage.AssertPurpose(fileID, "imports")

			if err := h.RunJob(fixture.Inspect, fixture.Payload{FileID: fileID}); err != nil {
				t.Fatal(err)
			}
			run := h.Jobs.LastRun(fixture.Inspect)
			file, err := run.DecodeResult[storage.FileMetadata]()
			if err != nil || file.ScanStatus != status {
				t.Fatalf("job metadata = %+v, error = %v, want scan status %q", file, err, status)
			}
		})
	}

	if err := h.RunJob(fixture.Inspect, fixture.Payload{FileID: uuid.NewV7().String()}); !jobs.IsPermanentError(err) {
		t.Fatalf("missing file job error = %v, want permanent failure", err)
	}
}

func TestStorageModuleUploadAndDeletion(t *testing.T) {
	h := storageHarness(t)
	response := h.POST("/storageprobe/upload", nil)
	if response.StatusCode != 200 {
		t.Fatalf("upload response = %d: %s", response.StatusCode, response.body)
	}
	fileID := response.JSON("file_id").(string)
	h.Storage.AssertUploaded(fileID)
	h.Storage.AssertContentType(fileID, "application/pdf")
	h.Storage.AssertPurpose(fileID, "reports")
	assertLocalFile(t, response.JSON("url").(string), []byte("%PDF-1.7\nfixture invoice\n"))

	response = h.POST("/storageprobe/delete", nil, WithQuery("file_id", fileID))
	if response.StatusCode != 200 {
		t.Fatalf("delete response = %d: %s", response.StatusCode, response.body)
	}
	assertions := *h.Storage
	assertions.t = panicFailer{t}
	message := failure(func() { assertions.AssertUploaded(fileID) })
	if !strings.Contains(message, fileID) || !strings.Contains(message, "was not uploaded") {
		t.Fatalf("deleted file failure = %q", message)
	}
}

func TestStorageAssertionFailuresAndTenantIsolation(t *testing.T) {
	h := storageHarness(t)
	fileID := h.Storage.MockFile("contacts.csv", MockFileContentType("text/csv"))
	assertions := *h.Storage
	assertions.t = panicFailer{t}
	for _, test := range []struct {
		name string
		call func()
		want []string
	}{
		{"unknown", func() { assertions.AssertUploaded(uuid.NewV7().String()) }, []string{"was not uploaded"}},
		{"malformed", func() { assertions.AssertUploaded("unknown-id") }, []string{"unknown-id", "was not uploaded"}},
		{"content type", func() { assertions.AssertContentType(fileID, "image/png") }, []string{fileID, `expected "image/png"`, `actual "text/csv"`}},
		{"purpose", func() { assertions.AssertPurpose(fileID, "reports") }, []string{fileID, `expected "reports"`, `actual "attachments"`}},
		{"missing fixture", func() { assertions.MockFile("missing.csv") }, []string{"read storage fixture", "missing.csv"}},
		{"unsafe purpose", func() { assertions.MockFile("contacts.csv", MockFilePurpose("../escape")) }, []string{"invalid mock file purpose", "../escape"}},
		{"invalid status", func() { assertions.MockFile("contacts.csv", MockFileScanStatus("invalid")) }, []string{"invalid mock file scan status", "invalid"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			message := failure(test.call)
			for _, want := range test.want {
				if !strings.Contains(message, want) {
					t.Errorf("failure = %q, want %q", message, want)
				}
			}
		})
	}

	if _, err := h.DB.db.ExecContext(t.Context(), "UPDATE "+h.DB.schema()+".files SET tenant_id = $1 WHERE id = $2", uuid.NewV7().String(), fileID); err != nil {
		t.Fatal(err)
	}
	for _, assertion := range []func(){
		func() { assertions.AssertUploaded(fileID) },
		func() { assertions.AssertContentType(fileID, "text/csv") },
		func() { assertions.AssertPurpose(fileID, "attachments") },
	} {
		if message := failure(assertion); !strings.Contains(message, "was not uploaded") {
			t.Fatalf("cross-tenant file failure = %q", message)
		}
	}
	response := h.GET("/storageprobe/metadata", WithQuery("file_id", fileID))
	if response.StatusCode != 400 || response.ErrorCode() != "invalid_body" ||
		!strings.Contains(fmt.Sprint(response.JSON("error.message")), "file not found") {
		t.Fatalf("cross-tenant metadata response = %d: %s", response.StatusCode, response.body)
	}
}
