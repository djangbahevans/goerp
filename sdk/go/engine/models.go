package engine

import "github.com/djangbahevans/goerp/sdk/go/model"

func WriteModels(schema model.Schema) uint64 { return writePacked(schema) }

func WriteDataMigrations(migrations []model.DataMigration) uint64 { return writePacked(migrations) }
