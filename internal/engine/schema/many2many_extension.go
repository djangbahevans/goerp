package schema

import (
	"context"
	"fmt"

	"ariga.io/atlas/sql/migrate"
	"ariga.io/atlas/sql/schema"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func diffMany2ManyExtension(ctx context.Context, sess *SchemaSyncSession, driver migrate.Driver, md model.ModelDeclaration, field model.NamedField, owner string, parent *schema.Table, cfg atlasConfig, owners map[string]string) ([]schema.Change, error) {
	desired := schema.New("tenant_" + sess.tenantSlug)
	if err := addMany2ManyTable(desired, md, field, owner, map[string]*schema.Table{md.QualifiedName(owner): parent}, cfg); err != nil {
		return nil, err
	}

	join := desired.Tables[0]
	live, err := driver.InspectSchema(ctx, desired.Name, &schema.InspectOptions{Tables: []string{join.Name}})
	if err != nil {
		return nil, err
	}
	existing, exists := live.Table(join.Name)
	if !exists {
		return []schema.Change{&schema.AddTable{T: join}}, nil
	}

	if owners[join.Name+"/table/"+join.Name] != sess.moduleName {
		return nil, fmt.Errorf("join table %q belongs to another module", join.Name)
	}
	changes, err := driver.TableDiff(existing, join)
	if err != nil {
		return nil, err
	}
	if len(changes) != 0 {
		return nil, fmt.Errorf("model extensions cannot redefine join table %q", join.Name)
	}
	return nil, nil
}
