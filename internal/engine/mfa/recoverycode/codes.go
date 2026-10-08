package recoverycode

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

const base32Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"

// Ten base32 symbols provide 50 bits of entropy per recovery code.
const (
	codeCount      = 10
	symbolsPerCode = 10
)

// generateCode draws symbolsPerCode independent uniform base32 symbols and formats them as
// XXXXX-XXXXX.
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
