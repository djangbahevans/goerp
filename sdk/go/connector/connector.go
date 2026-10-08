// Package connector reads and marks processed the webhook inbox rows the
// engine creates for a connector module.
package connector

import (
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/internal/hostcall"
)

// Inbox statuses.
const (
	StatusPending   = "pending"
	StatusProcessed = "processed"
	StatusFailed    = "failed"
)

// InboxRow is one accepted webhook delivery. Payload is the accepted body,
// msgpack-encoded.
type InboxRow struct {
	ID              string
	ProviderEventID string
	Payload         []byte
	ReceivedAt      time.Time
	Status          string
}

// InboxGet returns the inbox row the engine's ingress pipeline created for the
// calling tenant and module. A row of another tenant or module, or one that
// does not exist, returns a host error with code connector.inbox_not_found.
func InboxGet(inboxID string) (*InboxRow, error) {
	var out abi.ConnectorInboxGetOutput
	if err := hostcall.Do(hostConnectorInboxGet, abi.ConnectorInboxGetInput{InboxID: inboxID}, &out); err != nil {
		return nil, err
	}
	return &InboxRow{
		ID:              out.ID,
		ProviderEventID: out.ProviderEventID,
		Payload:         out.Payload,
		ReceivedAt:      time.Unix(out.ReceivedAt, 0),
		Status:          out.Status,
	}, nil
}

// InboxMarkProcessed marks the row processed. Marking a processed row again is
// a no-op, since a retried job handler can reach this call twice.
func InboxMarkProcessed(inboxID string) error {
	return hostcall.Do(hostConnectorInboxMarkProcessed, abi.ConnectorInboxMarkProcessedInput{InboxID: inboxID}, nil)
}

// InboxMarkFailed marks the row failed and records reason for the connector's
// status UI. It neither retries the row nor deletes it.
func InboxMarkFailed(inboxID, reason string) error {
	return hostcall.Do(hostConnectorInboxMarkFailed, abi.ConnectorInboxMarkFailedInput{InboxID: inboxID, Reason: reason}, nil)
}
