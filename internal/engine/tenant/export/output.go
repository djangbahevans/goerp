package tenantexport

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"

	"github.com/djangbahevans/goerp/internal/engine/auth/rowcrypt"
)

// DecryptOutput is adminapi/jobs.go's OutputDecryptor hook for this
// package's job kind — reverses the rowcrypt encryption Worker.run applies
// to Result.DecryptionKey before recording it, so a completed export job's
// Output still hands a polling caller the plaintext key it needs
// (cli-reference.md §5: "the completed job's result includes... the
// decryption key"), even though what's persisted in river_job.output is
// ciphertext. Any kind other than "tenant.export" is returned unchanged —
// the same dispatch-by-kind shape adminapi/jobs.go's own hook signature
// expects, since one engine-wide OutputDecryptor is threaded through every
// job kind, not just this package's.
func DecryptOutput(keys *rowcrypt.RowKeySet, kind string, output jsontext.Value) (jsontext.Value, error) {
	if kind != (Args{}).Kind() {
		return output, nil
	}

	var result Result
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("decode tenant.export output: %w", err)
	}

	plaintext, err := keys.Decrypt([]byte(result.DecryptionKey))
	if err != nil {
		return nil, fmt.Errorf("decrypt tenant.export decryption key: %w", err)
	}
	result.DecryptionKey = string(plaintext)

	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("re-encode tenant.export output: %w", err)
	}
	return encoded, nil
}
