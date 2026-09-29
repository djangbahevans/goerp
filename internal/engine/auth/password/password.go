// Package password holds the Argon2id parameters and the password
// strength rules every password-setting path shares, and the sign-in
// check against a tenant's current rules (auth-internals.md §3
// "Hashing", "Password strength validation" and "Password policy at
// sign-in").
package password

import (
	"bufio"
	_ "embed"
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

// minUserInfoLength keeps a very short email local part (e.g. "a@")
// from rejecting most passwords as containing it.
const minUserInfoLength = 3

var (
	ErrTooShort      = errors.New("password is shorter than the minimum length")
	ErrTooLong       = errors.New("password is longer than the maximum length")
	ErrCommon        = errors.New("password is too common")
	ErrContainsEmail = errors.New("password contains the account's email username")
)

// commonPasswords is the lowercased NCSC top-100k password list
// (SecLists, MIT), keeping only entries at least Global.MinLength
// characters long: no effective policy allows a shorter password, so the
// rest could never match.
//
//go:embed common_passwords.txt
var commonPasswordsFile string

var commonPasswords = func() map[string]struct{} {
	set := map[string]struct{}{}
	sc := bufio.NewScanner(strings.NewReader(commonPasswordsFile))
	for sc.Scan() {
		if line := sc.Text(); line != "" {
			set[line] = struct{}{}
		}
	}
	return set
}()

// Policy is a minimum length plus the always-on checks: the maximum
// length, the common-password list and the email's local part. There are
// no composition rules (auth-internals.md §3 "Password strength
// validation").
type Policy struct {
	MinLength int
	MaxLength int
}

// Global is the platform policy. MaxLength bounds Argon2id input size.
var Global = Policy{
	MinLength: 12,
	MaxLength: 128,
}

// TenantMaxMinLength caps the minimum a tenant can require: a password
// serves every tenant an account belongs to, so one tenant's minimum
// binds its members' passwords everywhere.
const TenantMaxMinLength = 20

// WithMinLength is Global with minLength as its minimum, never below
// Global's.
func WithMinLength(minLength int) Policy {
	return Policy{MinLength: max(minLength, Global.MinLength), MaxLength: Global.MaxLength}
}

// Validate reports the first rule plain breaks. Lengths count
// characters, not bytes.
func (p Policy) Validate(plain, email string) error {
	n := utf8.RuneCountInString(plain)
	if n < p.MinLength {
		return ErrTooShort
	}
	if n > p.MaxLength {
		return ErrTooLong
	}
	lower := strings.ToLower(plain)
	if _, ok := commonPasswords[lower]; ok {
		return ErrCommon
	}
	local, _, _ := strings.Cut(strings.ToLower(email), "@")
	if len(local) >= minUserInfoLength && strings.Contains(lower, local) {
		return ErrContainsEmail
	}
	return nil
}
