package password

import (
	"context"
	"fmt"
	"strconv"

	"github.com/rs/zerolog/log"

	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
)

// Tenant policy keys, each under tenantconfig.PasswordPolicyPrefix. A
// write to any of them bumps tenantconfig.PasswordPolicyVersionKey.
const (
	KeyMinLength        = tenantconfig.PasswordPolicyPrefix + "min_length"
	KeyMaxLength        = tenantconfig.PasswordPolicyPrefix + "max_length"
	KeyRequireUppercase = tenantconfig.PasswordPolicyPrefix + "require_uppercase"
	KeyRequireDigit     = tenantconfig.PasswordPolicyPrefix + "require_digit"
	KeyRequireSymbol    = tenantconfig.PasswordPolicyPrefix + "require_symbol"
	KeyBlockCommonList  = tenantconfig.PasswordPolicyPrefix + "block_common_list"
	KeyBlockUserInfo    = tenantconfig.PasswordPolicyPrefix + "block_user_info"
)

type PolicyStore struct {
	config *tenantconfig.Store
}

func NewPolicyStore(config *tenantconfig.Store) *PolicyStore {
	return &PolicyStore{config: config}
}

// Effective returns the strictest of Global and tenantID's policy, and
// the tenant's current policy version (0 until its policy first
// changes). An unparseable tenant value is ignored with a warning rather
// than failing the password operation.
func (s *PolicyStore) Effective(ctx context.Context, tenantID string) (Policy, int64, error) {
	values, err := s.config.GetPrefix(ctx, tenantID, "auth.password_policy")
	if err != nil {
		return Policy{}, 0, fmt.Errorf("load password policy: %w", err)
	}

	tenant := Policy{MaxLength: Global.MaxLength}
	parseInt(tenantID, values, KeyMinLength, &tenant.MinLength)
	parseInt(tenantID, values, KeyMaxLength, &tenant.MaxLength)
	parseBool(tenantID, values, KeyRequireUppercase, &tenant.RequireUppercase)
	parseBool(tenantID, values, KeyRequireDigit, &tenant.RequireDigit)
	parseBool(tenantID, values, KeyRequireSymbol, &tenant.RequireSymbol)
	parseBool(tenantID, values, KeyBlockCommonList, &tenant.BlockCommonList)
	parseBool(tenantID, values, KeyBlockUserInfo, &tenant.BlockUserInfo)

	// Either bound out of range would reject every password.
	if tenant.MinLength > Global.MaxLength {
		log.Warn().Str("tenant_id", tenantID).Int("min_length", tenant.MinLength).Msg("password: tenant min length above the global max length, ignoring it")
		tenant.MinLength = 0
	}
	effective := Global.Strictest(tenant)
	if effective.MaxLength < effective.MinLength {
		log.Warn().Str("tenant_id", tenantID).Int("max_length", tenant.MaxLength).Msg("password: tenant max length below min length, ignoring it")
		effective.MaxLength = Global.MaxLength
	}

	var version int64
	if v, ok := values[tenantconfig.PasswordPolicyVersionKey]; ok {
		version, err = strconv.ParseInt(v, 10, 64)
		if err != nil {
			return Policy{}, 0, fmt.Errorf("parse password policy version %q: %w", v, err)
		}
	}

	return effective, version, nil
}

func parseInt(tenantID string, values map[string]string, key string, dst *int) {
	v, ok := values[key]
	if !ok {
		return
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		log.Warn().Str("tenant_id", tenantID).Str("key", key).Str("value", v).Msg("password: invalid policy value, ignoring it")
		return
	}
	*dst = n
}

func parseBool(tenantID string, values map[string]string, key string, dst *bool) {
	v, ok := values[key]
	if !ok {
		return
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		log.Warn().Str("tenant_id", tenantID).Str("key", key).Str("value", v).Msg("password: invalid policy value, ignoring it")
		return
	}
	*dst = b
}

// UpdateRecommended reports whether a login into tenantID, currently at
// policy version currentVersion, should carry the password-update nudge.
// A password validated against another tenant's policy (or only the
// global one) was never checked against this tenant's, so it's behind
// once this tenant has changed its policy at all.
func UpdateRecommended(setTenantID *string, setVersion int64, tenantID string, currentVersion int64) bool {
	if currentVersion == 0 {
		return false
	}
	if setTenantID == nil || *setTenantID != tenantID {
		return true
	}
	return setVersion < currentVersion
}
