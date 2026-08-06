package engine

import "github.com/djangbahevans/goerp/sdk/go/model"

func WriteModels(schema model.Schema) uint64 {
	data, err := marshal(schema)
	if err != nil {
		data, _ = marshal(map[string]any{
			"error": map[string]any{
				"code":    "engine.marshal_failed",
				"message": err.Error(),
			},
		})
	}

	ptr := Allocate(uint32(len(data)))
	WriteMem(ptr, data)
	return uint64(ptr)<<32 | uint64(len(data))
}
