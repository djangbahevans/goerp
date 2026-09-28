package tenant

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Profile is a tenant's company identity, the tenant settings page's
// General section (shell-ux.md §5.5). Every field but Name is optional
// and nil when unset.
type Profile struct {
	Name            string  `json:"name"`
	LogoURL         *string `json:"logo_url"`
	Address         *string `json:"address"`
	Website         *string `json:"website"`
	Country         *string `json:"country"`
	DefaultCurrency *string `json:"default_currency"`
}

// ProfileUpdate is a partial Profile write: a nil field is left as it
// is, and a pointer to "" clears an optional one. The logo is set by
// SetLogoURL instead, alongside the upload that produces it.
type ProfileUpdate struct {
	Name            *string
	Address         *string
	Website         *string
	Country         *string
	DefaultCurrency *string
}

// GetProfile returns tenantID's Profile, or ErrTenantNotFound.
func (s *Store) GetProfile(ctx context.Context, tenantID string) (*Profile, error) {
	return scanProfile(s.db.QueryRowContext(ctx, `
		SELECT name, logo_url, address, website, country, default_currency
		FROM system.tenants WHERE id = $1
	`, tenantID))
}

// UpdateProfile applies u to tenantID and returns the resulting Profile,
// or ErrTenantNotFound.
func (s *Store) UpdateProfile(ctx context.Context, tenantID string, u ProfileUpdate) (*Profile, error) {
	return scanProfile(s.db.QueryRowContext(ctx, `
		UPDATE system.tenants SET
			name             = COALESCE($2, name),
			address          = CASE WHEN $3::text IS NULL THEN address ELSE NULLIF($3, '') END,
			website          = CASE WHEN $4::text IS NULL THEN website ELSE NULLIF($4, '') END,
			country          = CASE WHEN $5::text IS NULL THEN country ELSE NULLIF($5, '') END,
			default_currency = CASE WHEN $6::text IS NULL THEN default_currency ELSE NULLIF($6, '') END,
			updated_at       = NOW()
		WHERE id = $1
		RETURNING name, logo_url, address, website, country, default_currency
	`, tenantID, u.Name, u.Address, u.Website, u.Country, u.DefaultCurrency))
}

// SetLogoURL sets tenantID's logo_url; "" clears it. It returns the URL
// it replaced (nil if there was none), or ErrTenantNotFound.
func (s *Store) SetLogoURL(ctx context.Context, tenantID, logoURL string) (previous *string, err error) {
	var prev sql.NullString
	err = s.db.QueryRowContext(ctx, `
		UPDATE system.tenants t SET logo_url = NULLIF($2, ''), updated_at = NOW()
		FROM (SELECT id, logo_url FROM system.tenants WHERE id = $1 FOR UPDATE) old
		WHERE t.id = old.id
		RETURNING old.logo_url
	`, tenantID, logoURL).Scan(&prev)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTenantNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("set tenant logo: %w", err)
	}
	if prev.Valid {
		return &prev.String, nil
	}
	return nil, nil
}

func scanProfile(row *sql.Row) (*Profile, error) {
	var p Profile
	var logoURL, address, website, country, currency sql.NullString
	err := row.Scan(&p.Name, &logoURL, &address, &website, &country, &currency)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTenantNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read tenant profile: %w", err)
	}
	p.LogoURL = nullable(logoURL)
	p.Address = nullable(address)
	p.Website = nullable(website)
	p.Country = nullable(country)
	p.DefaultCurrency = nullable(currency)
	return &p, nil
}

func nullable(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}
