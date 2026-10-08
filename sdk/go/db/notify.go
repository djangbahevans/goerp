package db

import (
	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

// Notify sends a Postgres NOTIFY on channel via host.db.notify, delivered
// immediately.
func Notify(channel, payload string) error {
	return notify(abi.DBNotifyInput{Channel: channel, Payload: payload})
}

// Notify is Notify, scoped to tx's open transaction: delivery waits until
// tx commits and is dropped if it rolls back.
func (tx *Tx) Notify(channel, payload string) error {
	return notify(abi.DBNotifyInput{Channel: channel, Payload: payload, TxID: tx.id})
}

func notify(in abi.DBNotifyInput) error {
	var out abi.DBDurationOutput
	return hostcall.Do(hostDBNotify, in, &out)
}
