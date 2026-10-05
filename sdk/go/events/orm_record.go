package events

import (
	abi "github.com/djangbahevans/goerp/contract/abi/v1"
)

// RecordCreatedPayload is orm.record.created's payload
// (host-abi-reference.md §5a).
type RecordCreatedPayload = abi.ORMRecordCreatedPayload

// RecordUpdatedPayload is orm.record.updated's payload
// (host-abi-reference.md §5a).
type RecordUpdatedPayload = abi.ORMRecordUpdatedPayload

// RecordDeletedPayload is orm.record.deleted's payload
// (host-abi-reference.md §5a).
type RecordDeletedPayload = abi.ORMRecordDeletedPayload

// The engine's orm.record.* events, emitted for every ORM write.
var (
	RecordCreated = Define[RecordCreatedPayload]("orm.record.created")
	RecordUpdated = Define[RecordUpdatedPayload]("orm.record.updated")
	RecordDeleted = Define[RecordDeletedPayload]("orm.record.deleted")
)
