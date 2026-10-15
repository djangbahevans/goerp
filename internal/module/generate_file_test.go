package module

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGenerateFileCompilesAndScans(t *testing.T) {
	const declaration = `package schema
import "github.com/djangbahevans/goerp/sdk/go/model"
var Schema = model.Schema{Models: []*model.ModelDeclaration{
 model.Define("widgets.contract").WithStandardFields().
 Field("signed_pdf_id", model.File().Required()).Field("avatar_id", model.File()).Field("attachment_ids", model.FileMulti("contract_attachments")),
}}
`
	dir := writeGenerateFixtureWithManifest(t, declaration, generateFixtureManifest("widgets"))
	if _, err := Generate(t.Context(), dir, GenerateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(t.Context(), dir, GenerateOptions{Check: true}); err != nil {
		t.Fatal(err)
	}

	const usage = `package models
import (
 "testing"
 "github.com/djangbahevans/goerp/sdk/go/orm"
)
func TestFileScan(t *testing.T) {
 var row Contract
 if err := row.Scan(map[string]any{
  "signed_pdf_id": "file-1",
  "signed_pdf": map[string]any{"id": "file-1", "name": "contract.pdf", "content_type": "application/pdf", "size_bytes": int64(42), "url": "https://files.test/contract", "url_expires_at": "2026-10-15T13:00:00Z"},
  "avatar_id": nil, "avatar": nil,
 "attachment_ids": []any{"file-1"}, "attachments": []any{map[string]any{"id":"file-1", "name":"attachment.pdf", "size_bytes":int64(12)}},
 }); err != nil { t.Fatal(err) }

 if row.SignedPDFID != "file-1" || row.SignedPDF == nil || row.SignedPDF.Name != "contract.pdf" || row.SignedPDF.SizeBytes != 42 || row.SignedPDF.URLExpiresAt == nil || row.AvatarID != nil || row.Avatar != nil { t.Fatalf("scan: %+v", row) }
 if len(row.AttachmentIDs) != 1 || len(row.Attachments) != 1 || row.Attachments[0].Name != "attachment.pdf" { t.Fatalf("attachments: %+v", row) }
 var _ orm.Field[Contract, []string] = ContractFields.AttachmentIDs
 if err := row.Scan(map[string]any{"attachments": nil}); err != nil || row.Attachments != nil { t.Fatalf("clear attachments: %+v, %v", row, err) }
 values := NewContractValues().SetAttachmentIDs([]string{"file-1"}).SetSignedPDFID("file-1").SetAvatarID("file-2")
 var _ *orm.Values[Contract] = &values.Values
 var _ orm.Field[Contract, string] = ContractFields.SignedPDFID
 if err := row.Scan(map[string]any{"signed_pdf": nil}); err != nil || row.SignedPDF != nil { t.Fatalf("clear expansion: %+v, %v", row, err) }
 if err := row.Scan(map[string]any{"signed_pdf": "invalid"}); err == nil { t.Fatal("invalid file expansion accepted") }
}
`
	if err := os.WriteFile(filepath.Join(dir, "models", "usage_test.go"), []byte(usage), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.CommandContext(t.Context(), "go", "test", "./models")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-buildvcs=false")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated models: %v\n%s", err, out)
	}
}
