package main

import (
	"errors"
	"fmt"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/db"
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/events"
	"github.com/djangbahevans/goerp/sdk/go/orm"
	"github.com/vmihailenco/msgpack/v5"
)

//go:wasmimport host.db commit
func hostCommit(ptr, size uint32) uint64

//go:wasmimport host.db rollback
func hostRollback(ptr, size uint32) uint64

type effect struct {
	ID string
}

func (effect) ResourceName() string { return "transactionfixture.effect" }

func (e *effect) Scan(row map[string]any) error {
	id, ok := row["id"].(string)
	if !ok {
		return orm.NewDecodeError("effect", "id", "string", row["id"])
	}

	e.ID = id
	return nil
}

func init() {
	engine.SubscribeTx(events.Define[string]("test.event.happened"), handleTransactionalEvent)
}

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
	return engine.DispatchEvent(ptr, length)
}

func handleTransactionalEvent(tx *db.Tx, evt events.Event[string]) error {
	mode := evt.Payload
	if mode == "ownership" {
		for _, operation := range []func() error{tx.Commit, tx.Rollback} {
			err := operation()
			if hostErr, ok := errors.AsType[*abiv1.HostError](err); !ok || hostErr.Code != abiv1.ErrCodeTransactionManaged {
				return fmt.Errorf("managed transaction ownership: %v", err)
			}
		}

		for _, invoke := range []func(uint32, uint32) uint64{hostCommit, hostRollback} {
			err := call(invoke, abiv1.DBTxIDInput{TxID: tx.TxID()})
			if hostErr, ok := errors.AsType[*abiv1.HostError](err); !ok || hostErr.Code != abiv1.ErrCodeTransactionManaged {
				return fmt.Errorf("host transaction ownership: %v", err)
			}
		}

		_, err := db.Begin()
		if hostErr, ok := errors.AsType[*abiv1.HostError](err); !ok || hostErr.Code != abiv1.ErrCodeTransactionAlreadyOpen {
			return fmt.Errorf("nested transaction: %v", err)
		}
	}

	_, err := tx.Exec(`INSERT INTO effects (event_id, tx_id, mode, role_name, user_id)
		VALUES ($1, $2, $3, current_user, current_setting('app.current_user_id'))`, evt.ID, tx.TxID(), mode)
	if err != nil {
		panic(err)
	}

	count, err := orm.From[effect]().Tx(tx).Where(orm.NewField[effect, string]("tx_id").Eq(tx.TxID())).Count()
	if err != nil {
		panic(err)
	}
	if count != 1 {
		return fmt.Errorf("ORM read in delivery transaction found %d effects, want 1", count)
	}

	switch mode {
	case "retry":
		return errors.New("retry after transactional write")
	case "permanent":
		return events.PermanentError(errors.New("permanent failure after transactional write"))
	case "trap":
		panic("trap after transactional write")
	case "commit_failure":
		if _, err := tx.Exec("INSERT INTO pending_reference (ref_id) VALUES (1)"); err != nil {
			panic(err)
		}
	}

	return nil
}

//go:wasmexport allocate
func allocate(size uint32) uint32 { return engine.Allocate(size) }

//go:wasmexport deallocate
func deallocate(ptr, size uint32) { engine.Deallocate(ptr, size) }

func main() {}
