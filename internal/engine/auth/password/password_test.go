package password

import (
	"errors"
	"strings"
	"testing"

	"github.com/alexedwards/argon2id"
)

func TestPolicyValidate(t *testing.T) {
	strict := Global
	strict.RequireUppercase, strict.RequireDigit, strict.RequireSymbol = true, true, true

	cases := []struct {
		name     string
		policy   Policy
		password string
		email    string
		want     error
	}{
		{"accepts a long passphrase", Global, "correct horse battery staple", "kwame@example.com", nil},
		{"rejects 11 characters", Global, "abcdefghijk", "kwame@example.com", ErrTooShort},
		{"accepts exactly 12 characters", Global, "qzvkplmwxtrb", "kwame@example.com", nil},
		{"counts characters, not bytes", Global, strings.Repeat("é", 12), "kwame@example.com", nil},
		{"rejects over the maximum", Global, strings.Repeat("a", Global.MaxLength+1), "kwame@example.com", ErrTooLong},
		{"rejects the email username, any case", Global, "my-KWAME-password", "Kwame@example.com", ErrContainsEmail},
		{"ignores a very short email username", Global, "ab-long-passphrase", "ab@example.com", nil},
		{"rejects a common password, any case", Global, "Schmetterling", "kwame@example.com", ErrCommon},
		{"common list off allows it", Policy{MinLength: 12, MaxLength: 128}, "schmetterling", "kwame@example.com", nil},
		{"requires an uppercase letter", strict, "long passphrase 1!", "kwame@example.com", ErrMissingUppercase},
		{"requires a digit", strict, "Long passphrase !", "kwame@example.com", ErrMissingDigit},
		{"requires a symbol", strict, "Long passphrase 1", "kwame@example.com", ErrMissingSymbol},
		{"space isn't a symbol", strict, "Long passphrase 1 ", "kwame@example.com", ErrMissingSymbol},
		{"meets every requirement", strict, "Long passphrase 1!", "kwame@example.com", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.policy.Validate(tc.password, tc.email); !errors.Is(got, tc.want) {
				t.Errorf("Validate(%q, %q) = %v, want %v", tc.password, tc.email, got, tc.want)
			}
		})
	}
}

func TestPolicyStrictest_KeepsTighterOfEachField(t *testing.T) {
	tenant := Policy{MinLength: 16, MaxLength: 64, RequireDigit: true}
	got := Global.Strictest(tenant)
	want := Policy{MinLength: 16, MaxLength: 64, RequireDigit: true, BlockCommonList: true, BlockUserInfo: true}
	if got != want {
		t.Errorf("Strictest() = %+v, want %+v", got, want)
	}

	loose := Policy{MinLength: 8, MaxLength: 256}
	if got := Global.Strictest(loose); got != Global {
		t.Errorf("Strictest(looser) = %+v, want Global %+v", got, Global)
	}
}

func TestCommonPasswords_OnlyHoldsEntriesLongEnoughToMatter(t *testing.T) {
	if len(commonPasswords) == 0 {
		t.Fatal("common password list is empty")
	}
	for p := range commonPasswords {
		if n := len([]rune(p)); n < Global.MinLength {
			t.Errorf("entry %q has %d characters, below Global.MinLength", p, n)
		}
		if p != strings.ToLower(p) {
			t.Errorf("entry %q isn't lowercased", p)
		}
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

func TestUpdateRecommended(t *testing.T) {
	tenantA, tenantB := "tenant-a", "tenant-b"
	cases := []struct {
		name           string
		setTenantID    *string
		setVersion     int64
		currentVersion int64
		want           bool
	}{
		{"tenant never changed its policy", nil, 0, 0, false},
		{"global-only password, tenant tightened", nil, 0, 1, true},
		{"validated here at the current version", &tenantA, 2, 2, false},
		{"validated here at an older version", &tenantA, 1, 2, true},
		{"validated in another tenant at a higher version", &tenantB, 5, 2, true},
		{"validated in another tenant, this one unchanged", &tenantB, 5, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := UpdateRecommended(tc.setTenantID, tc.setVersion, tenantA, tc.currentVersion); got != tc.want {
				t.Errorf("UpdateRecommended() = %v, want %v", got, tc.want)
			}
		})
	}
}
