package tenantimport

import (
	"archive/zip"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"io"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/tenant/transfer"
)

type manifest struct {
	TenantID   string            `json:"tenant_id"`
	TenantSlug string            `json:"tenant_slug"`
	ExportedAt time.Time         `json:"exported_at"`
	Modules    []manifestModule  `json:"modules"`
	Tables     []transfer.Table  `json:"tables"`
	Objects    []string          `json:"objects"`
	Checksums  map[string]string `json:"checksums"`
}

type manifestModule struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	File    string `json:"file"`
}

type exportRecord struct {
	Model  string         `json:"model"`
	Record map[string]any `json:"record"`
}

func decryptArchive(ciphertext []byte, keyBase64 string) ([]byte, error) {
	key, err := base64.RawURLEncoding.DecodeString(keyBase64)
	if err != nil {
		return nil, fmt.Errorf("decode decryption key: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("archive is too short to contain a nonce")
	}
	nonce, body := ciphertext[:nonceSize], ciphertext[nonceSize:]

	plaintext, err := gcm.Open(nil, nonce, body, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt archive (wrong key, or archive is corrupt/tampered): %w", err)
	}
	return plaintext, nil
}

func parseArchive(plaintext []byte) (manifest, map[string][]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(plaintext), int64(len(plaintext)))
	if err != nil {
		return manifest{}, nil, fmt.Errorf("open archive as zip: %w", err)
	}

	files := make(map[string][]byte, len(zr.File))
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return manifest{}, nil, fmt.Errorf("open %s: %w", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return manifest{}, nil, fmt.Errorf("read %s: %w", f.Name, err)
		}
		if _, exists := files[f.Name]; exists {
			return manifest{}, nil, fmt.Errorf("duplicate archive entry %q", f.Name)
		}
		files[f.Name] = data
	}

	manifestBytes, ok := files["manifest.json"]
	if !ok {
		return manifest{}, nil, fmt.Errorf("archive has no manifest.json")
	}
	var man manifest
	if err := json.Unmarshal(manifestBytes, &man); err != nil {
		return manifest{}, nil, fmt.Errorf("decode manifest.json: %w", err)
	}

	for name, checksum := range man.Checksums {
		data, ok := files[name]
		if !ok {
			return manifest{}, nil, fmt.Errorf("missing checksummed entry %q", name)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != checksum {
			return manifest{}, nil, fmt.Errorf("checksum mismatch for %s", name)
		}
	}
	moduleData := files
	for _, m := range man.Modules {
		data, ok := files[m.File]
		if !ok {
			return manifest{}, nil, fmt.Errorf("archive manifest references %s, but it is missing from the archive", m.File)
		}
		moduleData[m.Name] = data
	}

	return man, moduleData, nil
}

func validateArchive(man manifest, data map[string][]byte) error {
	if _, ok := data["accounts.json"]; !ok {
		return fmt.Errorf("archive has no accounts.json")
	}
	expected := transfer.Tables()
	if len(man.Tables) != len(expected) {
		return fmt.Errorf("archive engine table set is incomplete")
	}
	seen := make(map[string]bool)
	for _, table := range man.Tables {
		if !transfer.KnownTable(table.Name) || table.File != "engine/"+table.Name+".jsonl" || seen[table.Name] {
			return fmt.Errorf("invalid engine table %q", table.Name)
		}
		seen[table.Name] = true
		if _, ok := data[table.File]; !ok {
			return fmt.Errorf("missing table data %s", table.File)
		}
	}
	for _, mod := range man.Modules {
		if mod.Name == "" || seen[mod.Name] || mod.File != mod.Name+".jsonl" {
			return fmt.Errorf("invalid archive module %q", mod.Name)
		}
		seen[mod.Name] = true
	}
	for _, name := range appendArchiveEntries(man) {
		if man.Checksums[name] == "" {
			return fmt.Errorf("archive has no checksum for %s", name)
		}
	}
	return nil
}

func appendArchiveEntries(man manifest) []string {
	entries := []string{"accounts.json"}
	for _, table := range man.Tables {
		entries = append(entries, table.File)
	}
	for _, mod := range man.Modules {
		entries = append(entries, mod.File)
	}
	return append(entries, man.Objects...)
}
