package contacts_test

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json/v2"
	"os"
	"strings"
	"testing"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/modules/contacts/schema"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/djangbahevans/goerp/sdk/go/modeltest"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestIdentifierRLSAcrossReadPaths(t *testing.T) {
	h := modeltest.NewHarness(t)
	contact := createContact(t, h, map[string]any{"type": "person", "given_name": "Ama"})
	publicType := createIdentifierType(t, h, "public", nil)
	sensitiveType := createIdentifierType(t, h, "sensitive", map[string]any{"is_sensitive": true})
	createIdentifier(t, h, contact, publicType, "PUBLIC")
	sensitiveID := createIdentifier(t, h, contact, sensitiveType, "PRIVATE")

	dsn := cmp.Or(os.Getenv("GOERP_TEST_DB_DSN"), "postgres://goerp:dev@localhost:15432/goerp")
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	var slug string
	if err := admin.QueryRowContext(t.Context(), `SELECT slug FROM system.tenants WHERE id=$1`, h.TenantID).Scan(&slug); err != nil {
		t.Fatal(err)
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	reader := stdlib.OpenDB(*config, stdlib.OptionAfterConnect(func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE "+pgx.Identifier{"tenant_" + slug}.Sanitize())
		return err
	}))
	defer reader.Close()
	var bypass bool
	if err := reader.QueryRowContext(t.Context(), `SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname=current_user`).Scan(&bypass); err != nil || bypass {
		t.Fatalf("RLS reader bypass=%v error=%v", bypass, err)
	}

	raw, err := os.ReadFile("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var mf manifest.Manifest
	if err := json.Unmarshal(raw, &mf); err != nil {
		t.Fatal(err)
	}
	registry := permission.NewPermissionRegistry()
	registry.Register("contacts", mf.Permissions)
	var declarations []model.ModelDeclaration
	for _, declaration := range schema.Schema.Models {
		declarations = append(declarations, *declaration)
	}

	for _, tc := range []struct {
		name        string
		permissions []string
		want        int
	}{
		{"no permissions", nil, 0},
		{"identifier reader", []string{"contacts:identifier:read"}, 1},
		{"personal permission alone", []string{"contacts:personal:read"}, 0},
		{"writer without personal read", []string{"contacts:identifier:read", "contacts:identifier:write", "contacts:personal:write"}, 1},
		{"personal reader", []string{"contacts:identifier:read", "contacts:personal:read"}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var bits permission.PermissionBitfield
			for _, name := range tc.permissions {
				index, _ := registry.Index(name)
				bits.Set(index)
			}
			mc := wasm.NewModuleContext("rls", "contacts", h.UserID, h.UserID, nil, bits, h.TenantID, slug, "trace",
				abi.CapDBRead, nil, wasm.ModuleSnapshot{ModelDecls: declarations, PermissionRegistry: registry})
			out, hostErr := wasm.ORMSearchRead(t.Context(), reader, mc, abiv1.ORMSearchReadInput{
				Model: "contacts.identifier", Fields: []string{"id", "value"},
			})
			if hostErr != nil || len(out.Records) != tc.want {
				t.Fatalf("ORMSearchRead records=%v error=%v, want %d", out.Records, hostErr, tc.want)
			}
			read, hostErr := wasm.ORMRead(t.Context(), reader, nil, mc, abiv1.ORMReadInput{Model: "contacts.identifier", IDs: []string{sensitiveID}, Fields: []string{"id", "value"}})
			if hostErr != nil {
				t.Fatal(hostErr)
			}
			wantSensitive := 0
			if tc.want == 2 {
				wantSensitive = 1
			}
			if len(read.Records) != wantSensitive {
				t.Errorf("ORMRead leaked or hid a sensitive row: %v", read.Records)
			}

			tx, err := reader.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			if _, err := tx.ExecContext(t.Context(), `SELECT set_config('search_path',$1,true), set_config('app.current_user_permissions',$2,true)`,
				pgx.Identifier{"tenant_" + slug}.Sanitize()+", public", strings.Join(tc.permissions, ",")); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := tx.QueryRowContext(t.Context(), `SELECT count(*) FROM contact_identifiers`).Scan(&count); err != nil || count != tc.want {
				t.Fatalf("raw SQL rows=%d error=%v, want %d", count, err, tc.want)
			}
		})
	}
}
