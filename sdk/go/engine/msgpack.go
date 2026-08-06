package engine

import "github.com/vmihailenco/msgpack/v5"

func marshal(v any) ([]byte, error) { return msgpack.Marshal(v) }

func unmarshal(data []byte, v any) error { return msgpack.Unmarshal(data, v) }
