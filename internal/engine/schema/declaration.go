package schema

import (
	"ariga.io/atlas/sql/schema"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

type moduleSchemaDeclaration struct {
	ModuleName  string
	Atlas       *schema.Schema
	ModelDecls  []model.ModelDeclaration
	OwnedTables []string
}

func newModuleSchemaDeclaration(schemaName, moduleName string, modelDecls []model.ModelDeclaration, typeDecls []model.TypeDeclaration) (*moduleSchemaDeclaration, error) {
	atlas, err := ToAtlasSchema(schemaName, moduleName, modelDecls, typeDecls)
	if err != nil {
		return nil, err
	}

	tables := make([]string, len(atlas.Tables))
	for i, t := range atlas.Tables {
		tables[i] = t.Name
	}

	return &moduleSchemaDeclaration{
		ModuleName:  moduleName,
		Atlas:       atlas,
		ModelDecls:  modelDecls,
		OwnedTables: tables,
	}, nil
}
