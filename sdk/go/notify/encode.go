package notify

import (
	"github.com/djangbahevans/goerp/sdk/go/internal/notifydata"
	"github.com/vmihailenco/msgpack/v5"
)

func encodeData(data any) ([]byte, error) {
	values, err := notifydata.Values(data)
	if err != nil {
		return nil, err
	}

	return msgpack.Marshal(values)
}
