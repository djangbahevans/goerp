package abi

// ConnectorInboxGetInput is the request of host.connector.inbox_get.
type ConnectorInboxGetInput struct {
	InboxID string `msgpack:"inbox_id"`
}

// ConnectorInboxGetOutput is the response of host.connector.inbox_get.
// Payload is the accepted webhook body, msgpack-encoded; ReceivedAt is a unix
// timestamp in seconds; Status is "pending", "processed" or "failed".
type ConnectorInboxGetOutput struct {
	ID              string `msgpack:"id"`
	ProviderEventID string `msgpack:"provider_event_id"`
	Payload         []byte `msgpack:"payload"`
	ReceivedAt      int64  `msgpack:"received_at"`
	Status          string `msgpack:"status"`
}

// ConnectorInboxMarkProcessedInput is the request of
// host.connector.inbox_mark_processed.
type ConnectorInboxMarkProcessedInput struct {
	InboxID string `msgpack:"inbox_id"`
}

// ConnectorInboxMarkProcessedOutput is the response of
// host.connector.inbox_mark_processed.
type ConnectorInboxMarkProcessedOutput struct{}

// ConnectorInboxMarkFailedInput is the request of
// host.connector.inbox_mark_failed.
type ConnectorInboxMarkFailedInput struct {
	InboxID string `msgpack:"inbox_id"`
	Reason  string `msgpack:"reason"`
}

// ConnectorInboxMarkFailedOutput is the response of
// host.connector.inbox_mark_failed.
type ConnectorInboxMarkFailedOutput struct{}
