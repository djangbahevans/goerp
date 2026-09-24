// Package password holds the Argon2id parameters and the global minimum
// strength rules every password-setting path shares (auth-internals.md §3
// "Hashing" and "Password strength validation"). The per-tenant
// PasswordPolicy merge, the common-password blocklist, and policy
// versioning are backlog #251's scope.
package password

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/alexedwards/argon2id"
)

// ArgonParams matches auth-internals.md §3 "Hashing" exactly.
var ArgonParams = &argon2id.Params{
	Memory:      64 * 1024,
	Iterations:  3,
	Parallelism: 4,
	SaltLength:  16,
	KeyLength:   32,
}

const (
	MinLength = 12
	// MaxLength bounds Argon2id input size, per PasswordPolicy's default.
	MaxLength = 128
	// minUserInfoLength keeps a very short email local part (e.g. "a@")
	// from rejecting most passwords as containing it.
	minUserInfoLength = 3
)

var (
	ErrTooShort      = errors.New("password is shorter than the minimum length")
	ErrTooLong       = errors.New("password is longer than the maximum length")
	ErrContainsEmail = errors.New("password contains the account's email username")
)

func Hash(plain string) (string, error) {
	return argon2id.CreateHash(plain, ArgonParams)
}

// Validate applies the global PasswordPolicy defaults: length bounds
// (counted in characters, not bytes) and BlockUserInfo.
func Validate(plain, email string) error {
	n := utf8.RuneCountInString(plain)
	if n < MinLength {
		return ErrTooShort
	}
	if n > MaxLength {
		return ErrTooLong
	}
	local, _, _ := strings.Cut(strings.ToLower(email), "@")
	if len(local) >= minUserInfoLength && strings.Contains(strings.ToLower(plain), local) {
		return ErrContainsEmail
	}
	return nil
}
