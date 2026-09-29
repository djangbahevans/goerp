package jobqueue

import "github.com/riverqueue/river"

// RecordCommentNotifyArgs is the job POST /_meta/activity enqueues in the
// transaction that inserts a comment (record-activity.md §10): it works
// out who the comment notifies and enqueues one
// RecordCommentRecipientArgs per recipient. Its worker lives in package
// engine, which holds the record reads recipients are checked against.
type RecordCommentNotifyArgs struct {
	TenantID   string `json:"tenant_id"`
	TenantSlug string `json:"tenant_slug"`
	EntryID    string `json:"entry_id"`
}

func (RecordCommentNotifyArgs) Kind() string { return "record_comment_notify" }

func (RecordCommentNotifyArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueDefault}
}

// RecordCommentRecipientArgs sends one comment's notification, of the
// engine type NotificationType, to one recipient, once they are checked
// to still be able to read the record.
type RecordCommentRecipientArgs struct {
	TenantID         string `json:"tenant_id"`
	TenantSlug       string `json:"tenant_slug"`
	EntryID          string `json:"entry_id"`
	UserID           string `json:"user_id"`
	NotificationType string `json:"notification_type"`
}

func (RecordCommentRecipientArgs) Kind() string { return "record_comment_recipient" }

func (RecordCommentRecipientArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueDefault}
}
