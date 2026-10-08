package main

import (
	"errors"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/vmihailenco/msgpack/v5"
)

//go:wasmimport host.db exec
func hostExec(ptr, size uint32) uint64

//go:wasmimport host.db commit
func hostCommit(ptr, size uint32) uint64

//go:wasmimport host.db rollback
func hostRollback(ptr, size uint32) uint64

func call(invoke func(uint32, uint32) uint64, input any) error {
	data, err := msgpack.Marshal(input)
	if err != nil {
		return err
	}

	ptr := engine.Allocate(uint32(len(data)))
	engine.WriteMem(ptr, data)
	packed := invoke(ptr, uint32(len(data)))
	engine.Deallocate(ptr, uint32(len(data)))

	responsePtr, responseSize := uint32(packed>>32), uint32(packed)
	defer engine.Deallocate(responsePtr, responseSize)

	var response abiv1.Envelope
	if err := msgpack.Unmarshal(engine.ReadMem(responsePtr, responseSize), &response); err != nil {
		return err
	}
	if !response.OK {
		return response.Error
	}

	return nil
}

//go:wasmexport handle_event
func handleEvent(ptr, length uint32) uint32 {
	var env abiv1.EventEnvelope
	if err := msgpack.Unmarshal(engine.ReadMem(ptr, length), &env); err != nil || env.TxID == "" {
		return 1
	}

	var mode string
	if err := msgpack.Unmarshal(env.Payload, &mode); err != nil {
		return 1
	}

	if mode == "ownership" {
		for _, invoke := range []func(uint32, uint32) uint64{hostCommit, hostRollback} {
			err := call(invoke, abiv1.DBTxIDInput{TxID: env.TxID})
			if hostErr, ok := errors.AsType[*abiv1.HostError](err); !ok || hostErr.Code != abiv1.ErrCodeTransactionManaged {
				return 1
			}
		}

		_, err := db.Begin()
		if hostErr, ok := errors.AsType[*abiv1.HostError](err); !ok || hostErr.Code != abiv1.ErrCodeTransactionAlreadyOpen {
			return 1
		}
	}

	err := call(hostExec, abiv1.DBExecInput{
		TxID: env.TxID,
		SQL: `INSERT INTO effects (event_id, tx_id, mode, role_name, user_id)
			VALUES ($1, $2, $3, current_user, current_setting('app.current_user_id'))`,
		Params: []any{env.ID, env.TxID, mode},
	})
	if err != nil {
		panic(err)
	}

	switch mode {
	case "retry":
		return 1
	case "permanent":
		return 2
	case "trap":
		panic("trap after transactional write")
	case "commit_failure":
		if err := call(hostExec, abiv1.DBExecInput{TxID: env.TxID, SQL: "INSERT INTO pending_reference (ref_id) VALUES (1)"}); err != nil {
			panic(err)
		}
	}

	return 0
}

//go:wasmexport allocate
func allocate(size uint32) uint32 { return engine.Allocate(size) }

//go:wasmexport deallocate
func deallocate(ptr, size uint32) { engine.Deallocate(ptr, size) }

func main() {}
