package tenantimport

import (
	"archive/zip"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json/v2"
	"fmt"
	"io"
	"time"
)

// manifest mirrors tenantexport's own (unexported, package-private) archive
// manifest shape exactly — the wire format goerp#156 already fixed. Field
// order and tags must stay in sync with that package's manifest/
// manifestModule types.
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

// exportRecord mirrors tenantexport's own exportRecord — one JSON object
// per line of a module's .jsonl file.
type exportRecord struct {
	Model  string         `json:"model"`
	Record map[string]any `json:"record"`
}

// decryptArchive reverses tenantexport's encryptArchive: AES-256-GCM with
// the nonce prepended to the ciphertext, so decryption needs only the
// caller-supplied key. A wrong key or corrupted/tampered ciphertext fails
// GCM's authentication tag check here — the archive's real integrity check,
// stronger than a plain checksum comparison since it also rejects tampering.
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

// parseArchive unzips plaintext (the decrypted archive), reads manifest.json,
// and returns every module's raw .jsonl bytes keyed by module name.
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

	moduleData := make(map[string][]byte, len(man.Modules))
	for _, m := range man.Modules {
		data, ok := files[m.File]
		if !ok {
			return manifest{}, nil, fmt.Errorf("archive manifest references %s, but it is missing from the archive", m.File)
		}
		moduleData[m.Name] = data
	}

	return man, moduleData, nil
}
