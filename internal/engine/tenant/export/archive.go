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
)

// manifest is the archive's own manifest.json — "a manifest naming the
// exact module/version set plus per-module data files" (cli-reference.md
// §5), which goerp#157 (tenant import) reuses to know what it's
// restoring.
type manifest struct {
	TenantID   string           `json:"tenant_id"`
	TenantSlug string           `json:"tenant_slug"`
	ExportedAt time.Time        `json:"exported_at"`
	Modules    []manifestModule `json:"modules"`
}

type manifestModule struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	File    string `json:"file"`
}

// buildArchive assembles the final plaintext zip from each module's
// already-uploaded JSONL bytes (moduleData, keyed by module name) plus
// manifest.json.
func buildArchive(m manifest, moduleData map[string][]byte) ([]byte, error) {
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

	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("close zip writer: %w", err)
	}
	return buf.Bytes(), nil
}

// encryptArchive AES-256-GCM encrypts plaintext with a freshly generated,
// never-persisted 32-byte key — security-model.md's application-layer
// encryption convention, the same AEAD construction
// internal/engine/auth/rowcrypt uses (aes.NewCipher → cipher.NewGCM),
// simplified to a one-shot key here since there is exactly one key per
// export and nothing to rotate. The nonce is prepended to the ciphertext
// so decryption needs only the returned key, nothing else.
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

// checksumHex is the SHA-256 of the final (encrypted) archive —
// transfer-integrity verification only, deliberately not a signing
// chain (cli-reference.md §5).
func checksumHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
