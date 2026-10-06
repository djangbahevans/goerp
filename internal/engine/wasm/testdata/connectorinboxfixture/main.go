// Command connectorinboxfixture is a real Go module compiled to wasip1 WASM
// for internal/engine/wasm's host.connector tests. It reads and marks inbox
// rows through the real sdk/go/connector package.
//
// Must be built with:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o connectorinboxfixture.wasm .
package main

import (
	"errors"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/connector"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/vmihailenco/msgpack/v5"
)

type request struct {
	InboxID string `msgpack:"inbox_id"`
	Reason  string `msgpack:"reason,omitempty"`
}

type body struct {
	Reference string         `msgpack:"reference"`
	Amount    int64          `msgpack:"amount"`
	Fraction  float64        `msgpack:"fraction"`
	Paid      bool           `msgpack:"paid"`
	Tags      []string       `msgpack:"tags"`
	Meta      map[string]any `msgpack:"meta"`
}

type result struct {
	ErrCode    string `msgpack:"err_code,omitempty"`
	Error      string `msgpack:"error,omitempty"`
	ID         string `msgpack:"id,omitempty"`
	EventID    string `msgpack:"event_id,omitempty"`
	Status     string `msgpack:"status,omitempty"`
	ReceivedAt int64  `msgpack:"received_at,omitempty"`
	Body       body   `msgpack:"body"`
}

func readRequest(ptr, size uint32) (request, error) {
	var r request
	err := msgpack.Unmarshal(engine.ReadMem(ptr, size), &r)
	return r, err
}

func writeResult(r result) uint64 {
	data, err := msgpack.Marshal(r)
	if err != nil {
		data, _ = msgpack.Marshal(result{Error: "marshal result: " + err.Error()})
	}
	ptr := engine.Allocate(uint32(len(data)))
	engine.WriteMem(ptr, data)
	return uint64(ptr)<<32 | uint64(len(data))
}

func failure(err error) result {
	if hostErr, ok := errors.AsType[*abi.HostError](err); ok {
		return result{ErrCode: hostErr.Code, Error: hostErr.Message}
	}
	return result{Error: err.Error()}
}

//go:wasmexport run_get
func runGet(ptr, size uint32) uint64 {
	req, err := readRequest(ptr, size)
	if err != nil {
		return writeResult(failure(err))
	}
	row, err := connector.InboxGet(req.InboxID)
	if err != nil {
		return writeResult(failure(err))
	}
	out := result{ID: row.ID, EventID: row.ProviderEventID, Status: row.Status, ReceivedAt: row.ReceivedAt.Unix()}
	if err := msgpack.Unmarshal(row.Payload, &out.Body); err != nil {
		return writeResult(failure(err))
	}
	return writeResult(out)
}

//go:wasmexport run_mark_processed
func runMarkProcessed(ptr, size uint32) uint64 {
	req, err := readRequest(ptr, size)
	if err != nil {
		return writeResult(failure(err))
	}
	if err := connector.InboxMarkProcessed(req.InboxID); err != nil {
		return writeResult(failure(err))
	}
	return writeResult(result{})
}

//go:wasmexport run_mark_failed
func runMarkFailed(ptr, size uint32) uint64 {
	req, err := readRequest(ptr, size)
	if err != nil {
		return writeResult(failure(err))
	}
	if err := connector.InboxMarkFailed(req.InboxID, req.Reason); err != nil {
		return writeResult(failure(err))
	}
	return writeResult(result{})
}

//go:wasmexport allocate
func allocate(size uint32) uint32 {
	return engine.Allocate(size)
}

//go:wasmexport deallocate
func deallocate(ptr, size uint32) {
	engine.Deallocate(ptr, size)
}

func main() {}
