package wasm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"time"
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/fieldsec"
	"github.com/djangbahevans/goerp/internal/engine/files"
	"github.com/djangbahevans/goerp/internal/engine/modelmeta"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// FileURL signs an authorized storage key. Callers must check the tenant file
// and field access before requesting a URL.
func (r *Runtime) FileURL(ctx context.Context, key string, public bool) (string, *time.Time, error) {
	if r.storageBackend == nil {
		return "", nil, fmt.Errorf("no object storage backend is configured")
	}

	if public {
		url, err := r.storageBackend.PublicURL(ctx, key)
		return url, nil, err
	}

	expires := new(r.Now().Add(time.Hour).UTC())
	url, err := r.storageBackend.SignedURL(ctx, key, time.Hour)
	return url, expires, err
}

func checkFileReferences(ctx context.Context, tx *sql.Tx, mc *ModuleContext, md model.ModelDeclaration, record map[string]any) *abiv1.HostError {
	for _, f := range md.Fields {
		value, present := record[f.Name]
		if f.Def.Kind != model.KindFile || !present || value == nil {
			continue
		}

		if f.Def.IsReadonly || f.Def.IsComputed {
			return &abiv1.HostError{Code: abiv1.ErrCodeFieldNotWritable, Message: "field " + f.Name + " cannot be written directly", Details: map[string]any{"field": f.Name}}
		}

		if rule, denied := writeDeniedBy(mc, md.QualifiedName(mc.ModuleName), f.Name); denied {
			if rule.OnDeniedWrite == fieldsec.Ignore {
				continue
			}
			return &abiv1.HostError{Code: abiv1.ErrCodeFieldWriteDenied, Message: "field write denied", Details: map[string]any{"field": f.Name}}
		}

		id, ok := value.(string)
		if _, err := uuid.Parse(id); !ok || err != nil {
			return invalidFileReference(f.Name)
		}

		if _, err := files.ActiveReference(ctx, tx, mc.TenantSlug, mc.TenantID, id); err != nil {
			if errors.Is(err, files.ErrFileNotFound) {
				return invalidFileReference(f.Name)
			}
			return ormSQLError(err)
		}
	}

	return nil
}

func expandFileRelation(ctx context.Context, tx *sql.Tx, mc *ModuleContext, qualifiedModel string, f model.NamedField, records []map[string]any, maskRelated bool) error {
	key := modelmeta.ReadKey(f.Name, f.Def.Kind.String())
	nullRelation(records, f.Name, key)

	if reg := mc.FieldSecRegistry(); maskRelated && reg != nil {
		if rule, ok := reg.Rule(qualifiedModel, f.Name); ok && !fieldReadAllowed(mc, mc.PermissionRegistry(), rule) {
			return nil
		}
	}

	expansions, err := fileExpansions(ctx, tx, mc, distinctNonNilStrings(records, f.Name))
	if err != nil {
		return err
	}

	for _, record := range records {
		if id, ok := record[f.Name].(string); ok {
			if expanded, found := expansions[id]; found {
				record[key] = expanded
			}
		}
	}

	return nil
}

func invalidFileReference(field string) *abiv1.HostError {
	return &abiv1.HostError{Code: abiv1.ErrCodeValidationFailed, Message: "field " + field + " references an unavailable file", Details: map[string]any{"field": field}}
}

func fileExpansions(ctx context.Context, tx *sql.Tx, mc *ModuleContext, ids []string) (map[string]map[string]any, error) {
	expansions := make(map[string]map[string]any)
	for _, id := range ids {
		file, err := files.ActiveReference(ctx, tx, mc.TenantSlug, mc.TenantID, id)
		if errors.Is(err, files.ErrFileNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}

		if mc.snapshot.FileURL == nil {
			return nil, fmt.Errorf("file URL service is unavailable")
		}

		url, expires, err := mc.snapshot.FileURL(ctx, file.StorageKey, file.IsPublic)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("obtain file URL: %w", err)
		}

		var expiry any
		if expires != nil {
			expiry = expires.UTC().Format(time.RFC3339)
		}

		expansions[id] = map[string]any{
			"id":             file.ID,
			"name":           file.OriginalName,
			"content_type":   file.ContentType,
			"size_bytes":     file.SizeBytes,
			"url":            url,
			"url_expires_at": expiry,
		}
	}

	return expansions, nil
}

func expandFileMultiRelation(ctx context.Context, tx *sql.Tx, mc *ModuleContext, qualifiedModel string, f model.NamedField, records []map[string]any, maskRelated bool) error {
	key := modelmeta.ReadKey(f.Name, f.Def.Kind.String())
	if reg := mc.FieldSecRegistry(); maskRelated && reg != nil {
		if rule, ok := reg.Rule(qualifiedModel, f.Name); ok && !fieldReadAllowed(mc, mc.PermissionRegistry(), rule) {
			nullRelation(records, f.Name, key)
			return nil
		}
	}
	var ids []string
	for _, record := range records {
		if set, ok := record[f.Name].([]string); ok {
			ids = append(ids, set...)
			record[key] = []map[string]any{}
		}
	}
	slices.Sort(ids)
	expansions, err := fileExpansions(ctx, tx, mc, slices.Compact(ids))
	if err != nil {
		return err
	}
	for _, record := range records {
		if set, ok := record[f.Name].([]string); ok {
			for _, id := range set {
				if ref, found := expansions[id]; found {
					record[key] = append(record[key].([]map[string]any), ref)
				}
			}
		}
	}
	return nil
}
