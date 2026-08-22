package recoverycode

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

const base32Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"

// codeCount and symbolsPerCode match auth-internals.md §8's "Recovery
// codes" reference implementation: 10 codes, each 10 base32 symbols
// (10 * 5 = 50 bits of entropy).
const (
	codeCount      = 10
	symbolsPerCode = 10
)

// generateCode draws symbolsPerCode independent uniform base32 symbols —
// sizing the random input to the code length rather than generating a
// larger byte buffer, base32-encoding it, and truncating, which would
// silently discard entropy without a truncation-aware reader knowing it.
// Formatted XXXXX-XXXXX for readability, per the doc's own example.
func generateCode() (string, error) {
	symbols := make([]byte, symbolsPerCode)
	for i := range symbols {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(base32Alphabet))))
		if err != nil {
			return "", fmt.Errorf("draw recovery code symbol: %w", err)
		}
		symbols[i] = base32Alphabet[n.Int64()]
	}
	return string(symbols[:5]) + "-" + string(symbols[5:]), nil
}

// GenerateCodes returns codeCount independently-drawn recovery codes.
func GenerateCodes() ([]string, error) {
	codes := make([]string, codeCount)
	for i := range codes {
		code, err := generateCode()
		if err != nil {
			return nil, err
		}
		codes[i] = code
	}
	return codes, nil
}
