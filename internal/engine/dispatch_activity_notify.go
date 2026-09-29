package engine

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/djangbahevans/goerp/internal/engine/enginenotif"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/notify"
	"github.com/djangbahevans/goerp/internal/engine/recordactivity"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// A comment's notifications (record-activity.md §10): engine.record_mention
// to each user it mentions and, for a message, engine.record_message to
// each follower. The post enqueues one RecordCommentNotifyArgs job, which
// enqueues one RecordCommentRecipientArgs job per recipient.

const (
	recordMentionType = enginenotif.Module + "." + enginenotif.RecordMention
	recordMessageType = enginenotif.Module + "." + enginenotif.RecordMessage

	// commentExcerptLength is how many characters of a comment its in-app
	// notification body keeps.
	commentExcerptLength = 200
)

// txJobInserter inserts River jobs inside a database/sql transaction —
// satisfied by the never-started *river.Client[*sql.Tx] of
// wasm.Runtime.EventInsertClient.
type txJobInserter interface {
	InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
	InsertManyTx(ctx context.Context, tx *sql.Tx, params []river.InsertManyParams) ([]*rivertype.JobInsertResult, error)
}

type commentRecipient struct {
	userID, notificationType string
}

// commentRecipients is who entry notifies, given the record's followers:
// each user it mentions, in order, with engine.record_mention; then, for a
// message, each follower not already mentioned with engine.record_message.
// The author is never a recipient.
func commentRecipients(entry *recordactivity.Entry, followerIDs []string) []commentRecipient {
	author := ""
	if entry.AuthorID != nil {
		author = *entry.AuthorID
	}
	seen := map[string]bool{author: true}
	var out []commentRecipient
	add := func(userID, typ string) {
		if !seen[userID] {
			seen[userID] = true
			out = append(out, commentRecipient{userID: userID, notificationType: typ})
		}
	}
	if entry.Body != nil {
		for _, id := range recordactivity.MentionIDs(*entry.Body) {
			add(id, recordMentionType)
		}
	}
	if entry.NotifyFollowers {
		for _, id := range followerIDs {
			add(id, recordMessageType)
		}
	}
	return out
}

// liveComment returns the comment entryID names, or nil when there is
// nothing to notify about: it doesn't exist, isn't a comment, or has been
// deleted.
func (e *Engine) liveComment(ctx context.Context, tenantSlug, entryID string) (*recordactivity.Entry, error) {
	entry, err := e.recordActivityStore.Get(ctx, tenantSlug, entryID)
	if errors.Is(err, recordactivity.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if entry.Kind != recordactivity.KindComment || entry.DeletedAt != nil || entry.Body == nil {
		return nil, nil
	}
	return entry, nil
}

// fanOutComment enqueues one recipient job for each user args' comment
// notifies, all in one transaction.
func (e *Engine) fanOutComment(ctx context.Context, args jobqueue.RecordCommentNotifyArgs) error {
	entry, err := e.liveComment(ctx, args.TenantSlug, args.EntryID)
	if err != nil || entry == nil {
		return err
	}
	var followerIDs []string
	if entry.NotifyFollowers {
		followers, err := e.recordActivityStore.ListFollowers(ctx, args.TenantSlug, entry.Model, entry.RecordID)
		if err != nil {
			return err
		}
		for _, f := range followers {
			followerIDs = append(followerIDs, f.UserID)
		}
	}
	recipients := commentRecipients(entry, followerIDs)
	if len(recipients) == 0 {
		return nil
	}

	params := make([]river.InsertManyParams, len(recipients))
	for i, r := range recipients {
		params[i] = river.InsertManyParams{Args: jobqueue.RecordCommentRecipientArgs{
			TenantID: args.TenantID, TenantSlug: args.TenantSlug, EntryID: entry.ID,
			UserID: r.userID, NotificationType: r.notificationType,
		}}
	}
	tx, err := e.primaryDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("enqueue comment recipients: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := e.txJobs.InsertManyTx(ctx, tx, params); err != nil {
		return fmt.Errorf("enqueue comment recipients: %w", err)
	}
	return tx.Commit()
}

// notifyCommentRecipient sends args' notification to its recipient once
// they are checked to still be able to read the record; one who can't is
// skipped and stops following it (record-activity.md §8). The send's
// idempotency key names the comment, so a retried job never sends twice.
func (e *Engine) notifyCommentRecipient(ctx context.Context, args jobqueue.RecordCommentRecipientArgs) error {
	entry, err := e.liveComment(ctx, args.TenantSlug, args.EntryID)
	if err != nil || entry == nil {
		return err
	}
	tenantCtx := &tenantresolve.TenantContext{TenantID: args.TenantID, Slug: args.TenantSlug}
	recordName, readable, err := e.recordNameAs(ctx, tenantCtx, args.UserID, entry.Model, entry.RecordID)
	if err != nil {
		return err
	}
	if !readable {
		return e.recordActivityStore.Unfollow(ctx, args.TenantSlug, entry.Model, entry.RecordID, args.UserID)
	}

	data, opts, err := e.commentNotification(ctx, entry, recordName)
	if err != nil {
		return err
	}
	opts.IdempotencyKey = "record_comment:" + entry.ID
	_, err = e.notifier.Send(ctx, args.TenantID, notify.EngineModule, args.NotificationType, args.UserID, data, opts)
	if errors.Is(err, notify.ErrUnknownUser) {
		return nil
	}
	return err
}

// commentNotification is the data and options both of entry's
// notification types carry (record-activity.md §10 "Content"): its
// author's name, the record's display name as the recipient reads it,
// the model's label, and the comment with each mention rendered as
// "@{name}", whole and cut for the in-app body, with a link to the
// record's form view. A failed user lookup is an error, so the job
// retries rather than sending a name it couldn't resolve.
func (e *Engine) commentNotification(ctx context.Context, entry *recordactivity.Entry, recordName *string) (map[string]any, notify.Options, error) {
	users := e.newActivityUserResolver()
	authorName := "Unknown user"
	if entry.AuthorID != nil {
		var err error
		if authorName, err = users.label(ctx, *entry.AuthorID); err != nil {
			return nil, notify.Options{}, err
		}
	}
	var lookupErr error
	body := recordactivity.RenderMentions(*entry.Body, func(id string) string {
		label, err := users.label(ctx, id)
		lookupErr = cmp.Or(lookupErr, err)
		return label
	})
	if lookupErr != nil {
		return nil, notify.Options{}, lookupErr
	}

	modelLabel := entry.Model
	var opts notify.Options
	if snap := e.moduleRegistry.Snapshot(); snap != nil {
		if _, _, md, ok := snap.ModelByName(entry.Model); ok {
			modelLabel = cmp.Or(md.Label, entry.Model)
		}
		opts.ActionURL = snap.RecordFormPath(entry.Model, entry.RecordID)
	}
	name := ""
	if recordName != nil {
		name = *recordName
	}
	return map[string]any{
		"EntryID":    entry.ID,
		"Model":      entry.Model,
		"ModelLabel": modelLabel,
		"RecordID":   entry.RecordID,
		"RecordName": name,
		"AuthorName": authorName,
		"Body":       body,
		"Excerpt":    excerpt(body, commentExcerptLength),
	}, opts, nil
}

// excerpt is s cut to its first n characters, ending in "…" when cut.
func excerpt(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

// recordCommentNotifyWorker works jobqueue.RecordCommentNotifyArgs. It is
// registered before the Engine it reads through exists, which New sets
// once built.
type recordCommentNotifyWorker struct {
	river.WorkerDefaults[jobqueue.RecordCommentNotifyArgs]
	engine *Engine
}

func (w *recordCommentNotifyWorker) Work(ctx context.Context, job *river.Job[jobqueue.RecordCommentNotifyArgs]) error {
	if w.engine == nil {
		return errors.New("record comment notify: engine not ready")
	}
	return w.engine.fanOutComment(ctx, job.Args)
}

// recordCommentRecipientWorker works jobqueue.RecordCommentRecipientArgs,
// wired the same way as recordCommentNotifyWorker.
type recordCommentRecipientWorker struct {
	river.WorkerDefaults[jobqueue.RecordCommentRecipientArgs]
	engine *Engine
}

func (w *recordCommentRecipientWorker) Work(ctx context.Context, job *river.Job[jobqueue.RecordCommentRecipientArgs]) error {
	if w.engine == nil {
		return errors.New("record comment recipient: engine not ready")
	}
	return w.engine.notifyCommentRecipient(ctx, job.Args)
}
