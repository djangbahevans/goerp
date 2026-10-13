package tenantexport

import (
	"archive/zip"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
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

func buildArchive(m manifest, moduleData map[string][]byte) ([]byte, error) {
	m.Checksums = make(map[string]string)
	for _, mod := range m.Modules {
		m.Checksums[mod.File] = checksumHex(moduleData[mod.Name])
	}
	for _, table := range m.Tables {
		m.Checksums[table.File] = checksumHex(moduleData[table.File])
	}
	for _, object := range m.Objects {
		m.Checksums[object] = checksumHex(moduleData[object])
	}
	if accounts, ok := moduleData["accounts.json"]; ok {
		m.Checksums["accounts.json"] = checksumHex(accounts)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	manifestBytes, err := json.Marshal(m, jsontext.WithIndent("  "))
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}
	mw, err := zw.Create("manifest.json")
	if err != nil {
		return nil, fmt.Errorf("create manifest.json entry: %w", err)
	}
	if _, err := mw.Write(manifestBytes); err != nil {
		return nil, fmt.Errorf("write manifest.json: %w", err)
	}

	for _, mod := range m.Modules {
		data, ok := moduleData[mod.Name]
		if !ok {
			return nil, fmt.Errorf("missing data for module %q", mod.Name)
		}
		fw, err := zw.Create(mod.File)
		if err != nil {
			return nil, fmt.Errorf("create %s entry: %w", mod.File, err)
		}
		if _, err := fw.Write(data); err != nil {
			return nil, fmt.Errorf("write %s: %w", mod.File, err)
		}
	}

	entries := make([]string, 0, len(m.Tables)+len(m.Objects)+1)
	for _, table := range m.Tables {
		entries = append(entries, table.File)
	}
	entries = append(entries, m.Objects...)
	if _, ok := moduleData["accounts.json"]; ok {
		entries = append(entries, "accounts.json")
	}
	for _, name := range entries {
		data, ok := moduleData[name]
		if !ok {
			return nil, fmt.Errorf("missing archive entry %q", name)
		}
		writer, err := zw.Create(name)
		if err != nil {
			return nil, err
		}
		if _, err := writer.Write(data); err != nil {
			return nil, err
		}
	}

	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("close zip writer: %w", err)
	}
	return buf.Bytes(), nil
}

// The nonce travels with the ciphertext; the fresh per-export key is returned separately.
func encryptArchive(plaintext []byte) (ciphertext []byte, keyBase64 string, err error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, "", fmt.Errorf("generate encryption key: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, "", fmt.Errorf("create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, "", fmt.Errorf("create GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, "", fmt.Errorf("generate nonce: %w", err)
	}

	sealed := gcm.Seal(nonce, nonce, plaintext, nil)
	return sealed, base64.RawURLEncoding.EncodeToString(key), nil
}

func checksumHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
