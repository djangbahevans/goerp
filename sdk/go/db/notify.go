package db

import (
	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

type dbNotifyInput = abi.DBNotifyInput

// Notify sends a Postgres NOTIFY on channel via host.db.notify, delivered
// immediately.
func Notify(channel, payload string) error {
	return notify(dbNotifyInput{Channel: channel, Payload: payload})
}

// Notify is Notify, scoped to tx's own open transaction — delivery is
// deferred until tx commits (and dropped if it rolls back instead), a
// method rather than a NotifyTx-suffixed free function to match this
// package's own Query/Exec convention (go-sdk-reference.md §6
// "Transactions").
func (tx *Tx) Notify(channel, payload string) error {
	return notify(dbNotifyInput{Channel: channel, Payload: payload, TxID: tx.id})
}

func notify(in dbNotifyInput) error {
	var out dbDurationOutput
	return hostcall.Do(hostDBNotify, in, &out)
}
