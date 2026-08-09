package engine

import "github.com/djangbahevans/goerp/sdk/go/model"

func WriteModels(schema model.Schema) uint64 { return writePacked(schema) }
