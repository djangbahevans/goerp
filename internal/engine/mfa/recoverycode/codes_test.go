package recoverycode

import (
	"regexp"
	"testing"
)

var codeFormat = regexp.MustCompile(`^[A-Z2-7]{5}-[A-Z2-7]{5}$`)

func TestGenerateCodes_ReturnsTenCodesInXXXXXFormat(t *testing.T) {
	codes, err := GenerateCodes()
	if err != nil {
		t.Fatalf("GenerateCodes() error: %v", err)
	}
	if len(codes) != codeCount {
		t.Fatalf("len(codes) = %d, want %d", len(codes), codeCount)
	}
	for _, c := range codes {
		if !codeFormat.MatchString(c) {
			t.Errorf("code %q doesn't match XXXXX-XXXXX base32 format", c)
		}
	}
}

func TestGenerateCodes_AllCodesAreDistinct(t *testing.T) {
	codes, err := GenerateCodes()
	if err != nil {
		t.Fatalf("GenerateCodes() error: %v", err)
	}

	seen := make(map[string]bool, len(codes))
	for _, c := range codes {
		if seen[c] {
			t.Errorf("duplicate code %q — 50 bits of entropy makes this astronomically unlikely, suggests a broken RNG", c)
		}
		seen[c] = true
	}
}

func TestGenerateCodes_SuccessiveCallsProduceDifferentSets(t *testing.T) {
	first, err := GenerateCodes()
	if err != nil {
		t.Fatalf("first GenerateCodes() error: %v", err)
	}
	second, err := GenerateCodes()
	if err != nil {
		t.Fatalf("second GenerateCodes() error: %v", err)
	}

	identical := true
	for i := range first {
		if first[i] != second[i] {
			identical = false
			break
		}
	}
	if identical {
		t.Error("two successive GenerateCodes() calls produced an identical set")
	}
}
