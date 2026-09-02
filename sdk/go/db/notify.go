package db

import "github.com/djangbahevans/goerp/sdk/go/internal/hostcall"

// dbNotifyInput mirrors host.db.notify's own wire shape
// (internal/engine/wasm/host_db_notify.go), duplicated rather than
// imported per this package's own convention (compiles into a module's
// own wasip1 binary).
type dbNotifyInput struct {
	Channel string `msgpack:"channel"`
	Payload string `msgpack:"payload"`
	TxID    string `msgpack:"tx_id,omitempty"`
}

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
