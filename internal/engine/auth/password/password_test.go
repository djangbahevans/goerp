package password

import (
	"errors"
	"strings"
	"testing"

	"github.com/alexedwards/argon2id"
)

func TestValidate(t *testing.T) {
	cases := []struct {
		name     string
		password string
		email    string
		want     error
	}{
		{"accepts a long passphrase", "correct horse battery staple", "kwame@example.com", nil},
		{"rejects 11 characters", "abcdefghijk", "kwame@example.com", ErrTooShort},
		{"accepts exactly 12 characters", "abcdefghijkl", "kwame@example.com", nil},
		{"counts characters, not bytes", strings.Repeat("é", 12), "kwame@example.com", nil},
		{"rejects over the maximum", strings.Repeat("a", MaxLength+1), "kwame@example.com", ErrTooLong},
		{"rejects the email username, any case", "my-KWAME-password", "Kwame@example.com", ErrContainsEmail},
		{"ignores a very short email username", "ab-long-passphrase", "ab@example.com", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Validate(tc.password, tc.email); !errors.Is(got, tc.want) {
				t.Errorf("Validate(%q, %q) = %v, want %v", tc.password, tc.email, got, tc.want)
			}
		})
	}
}

func TestHash_UsesDocumentedParams(t *testing.T) {
	h, err := Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash() error: %v", err)
	}
	match, params, err := argon2id.CheckHash("correct horse battery staple", h)
	if err != nil || !match {
		t.Fatalf("CheckHash() = %v, %v, want match", match, err)
	}
	if *params != *ArgonParams {
		t.Errorf("params = %+v, want %+v", *params, *ArgonParams)
	}
}
