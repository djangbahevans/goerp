// Package password holds the Argon2id parameters and the password
// strength policy every password-setting path shares (auth-internals.md
// §3 "Hashing", "Password strength validation" and "Password policy
// versioning").
package password

import (
	"bufio"
	_ "embed"
	"errors"
	"strings"
	"unicode"
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
	ErrTooShort         = errors.New("password is shorter than the minimum length")
	ErrTooLong          = errors.New("password is longer than the maximum length")
	ErrMissingUppercase = errors.New("password must contain an uppercase letter")
	ErrMissingDigit     = errors.New("password must contain a digit")
	ErrMissingSymbol    = errors.New("password must contain a symbol")
	ErrCommon           = errors.New("password is too common")
	ErrContainsEmail    = errors.New("password contains the account's email username")
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

type Policy struct {
	MinLength        int
	MaxLength        int
	RequireUppercase bool
	RequireDigit     bool
	RequireSymbol    bool
	BlockCommonList  bool
	BlockUserInfo    bool
}

// Global is the platform policy; a tenant policy can only tighten it.
// MaxLength bounds Argon2id input size.
var Global = Policy{
	MinLength:       12,
	MaxLength:       128,
	BlockCommonList: true,
	BlockUserInfo:   true,
}

// Strictest merges p and o field by field, keeping the tighter of each.
func (p Policy) Strictest(o Policy) Policy {
	return Policy{
		MinLength:        max(p.MinLength, o.MinLength),
		MaxLength:        min(p.MaxLength, o.MaxLength),
		RequireUppercase: p.RequireUppercase || o.RequireUppercase,
		RequireDigit:     p.RequireDigit || o.RequireDigit,
		RequireSymbol:    p.RequireSymbol || o.RequireSymbol,
		BlockCommonList:  p.BlockCommonList || o.BlockCommonList,
		BlockUserInfo:    p.BlockUserInfo || o.BlockUserInfo,
	}
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
	if p.RequireUppercase && !strings.ContainsFunc(plain, unicode.IsUpper) {
		return ErrMissingUppercase
	}
	if p.RequireDigit && !strings.ContainsFunc(plain, unicode.IsDigit) {
		return ErrMissingDigit
	}
	if p.RequireSymbol && !strings.ContainsFunc(plain, isSymbol) {
		return ErrMissingSymbol
	}
	lower := strings.ToLower(plain)
	if p.BlockCommonList {
		if _, ok := commonPasswords[lower]; ok {
			return ErrCommon
		}
	}
	if p.BlockUserInfo {
		local, _, _ := strings.Cut(strings.ToLower(email), "@")
		if len(local) >= minUserInfoLength && strings.Contains(lower, local) {
			return ErrContainsEmail
		}
	}
	return nil
}

func isSymbol(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsSpace(r)
}
