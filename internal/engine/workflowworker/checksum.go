package workflowworker

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// verifyChecksum checks data against a manifest checksum string in the
// "sha256:<hex>" form (manifest-spec.md), the same format and check
// internal/engine/loader uses for a module's WASM binary — reimplemented
// here rather than exported from loader, since each subsystem package
// owns its own verification rather than reaching into another's internals.
func verifyChecksum(checksum string, data []byte) error {
	hexPart, ok := strings.CutPrefix(checksum, "sha256:")
	if !ok {
		return fmt.Errorf("checksum %q missing sha256: prefix", checksum)
	}
	want, err := hex.DecodeString(hexPart)
	if err != nil {
		return fmt.Errorf("checksum %q is not valid hex: %w", checksum, err)
	}
	got := sha256.Sum256(data)
	if !bytes.Equal(got[:], want) {
		return fmt.Errorf("checksum mismatch: manifest declares %s, binary hashes to sha256:%x", checksum, got)
	}
	return nil
}
