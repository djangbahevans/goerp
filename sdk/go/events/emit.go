package events

import (
	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/events/def"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

func init() { def.SetEmitter(hostEmitter{}) }

// hostEmitter performs the emit host calls behind def.Def's emit methods.
type hostEmitter struct{}

func (hostEmitter) Emit(in abi.EventEmitInput) (string, error) {
	var out abi.EventEmitOutput
	if err := hostcall.Do(hostEventEmit, in, &out); err != nil {
		return "", err
	}
	return out.EventID, nil
}

func (hostEmitter) EmitTx(txID string, in abi.EventEmitInput) (string, error) {
	txIn := abi.EventEmitTxInput{
		TxID: txID, Name: in.Name, Version: in.Version, Payload: in.Payload,
		DelayMs: in.DelayMs, IdempotencyKey: in.IdempotencyKey,
	}
	var out abi.EventEmitTxOutput
	if err := hostcall.Do(hostEventEmitTx, txIn, &out); err != nil {
		return "", err
	}
	return out.EventID, nil
}
