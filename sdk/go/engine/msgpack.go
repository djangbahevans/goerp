package engine

import "github.com/vmihailenco/msgpack/v5"

func marshal(v any) ([]byte, error) { return msgpack.Marshal(v) }

func unmarshal(data []byte, v any) error { return msgpack.Unmarshal(data, v) }

func writePacked(v any) uint64 {
	data, err := marshal(v)
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
