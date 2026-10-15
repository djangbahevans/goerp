package tenant

import (
	"archive/zip"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/tenant/transfer"
	"github.com/spf13/cobra"
)

func TestImportDryRunRelationCountsAndOmissions(t *testing.T) {
	man := importManifest{
		TenantSlug: "source",
		Modules:    []importManifestModule{{Name: "contacts", Version: "1.0.0", File: "contacts.jsonl"}},
		Relations: []transfer.RelationSummary{
			{Module: "contacts", Model: "contacts.contact", Field: "tag_ids", Count: 2},
			{Module: "contacts", Model: "contacts.contact", Field: "empty_tag_ids", Count: 0},
		},
		OmittedRelations: []transfer.RelationOmission{{Module: "contacts", Model: "contacts.contact", Field: "external_tag_ids", MissingModules: []string{"labels"}}},
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	writer, err := zw.Create("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.MarshalWrite(writer, man); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	key := bytes.Repeat([]byte{1}, 32)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, gcm.NonceSize())
	ciphertext := gcm.Seal(nonce, nonce, buf.Bytes(), nil)
	for _, jsonOut := range []bool{false, true} {
		var output bytes.Buffer
		cmd := &cobra.Command{}
		cmd.SetOut(&output)
		if err := runImportDryRun(cmd, ciphertext, base64.RawURLEncoding.EncodeToString(key), jsonOut); err != nil {
			t.Fatal(err)
		}
		if jsonOut {
			var got importManifest
			if err := json.Unmarshal(output.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if len(got.Relations) != 2 || got.Relations[0].Count != 2 || got.Relations[1].Count != 0 || len(got.OmittedRelations) != 1 {
				t.Fatalf("dry-run output %s", output.String())
			}
			continue
		}
		for _, want := range []string{"contacts.contact.tag_ids: 2 links", "contacts.contact.empty_tag_ids: 0 links", "omitted contacts: contacts.contact.external_tag_ids (modules excluded: [labels])"} {
			if !strings.Contains(output.String(), want) {
				t.Errorf("output %q missing %q", output.String(), want)
			}
		}
	}
}
