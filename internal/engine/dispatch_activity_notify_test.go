package engine

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/recordactivity"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/djangbahevans/goerp/internal/engine/user"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// recordingJobs is a txJobInserter that records the jobs the engine
// inserts instead of queueing them.
type recordingJobs struct {
	mu   sync.Mutex
	jobs []river.JobArgs
}

func (j *recordingJobs) InsertTx(_ context.Context, _ *sql.Tx, args river.JobArgs, _ *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.jobs = append(j.jobs, args)
	return &rivertype.JobInsertResult{}, nil
}

func (j *recordingJobs) InsertManyTx(_ context.Context, _ *sql.Tx, params []river.InsertManyParams) ([]*rivertype.JobInsertResult, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]*rivertype.JobInsertResult, len(params))
	for i, p := range params {
		j.jobs = append(j.jobs, p.Args)
		out[i] = &rivertype.JobInsertResult{}
	}
	return out, nil
}

// take returns what has been inserted since the last take.
func (j *recordingJobs) take() []river.JobArgs {
	j.mu.Lock()
	defer j.mu.Unlock()
	jobs := j.jobs
	j.jobs = nil
	return jobs
}

type commentNotifyFixture struct {
	*scheduledActivityFixture
	notifier *recordingNotifier
	jobs     *recordingJobs
	// kofiID is a second active member, with a profile name; memberID
	// has none, so mentions of them fall back to their email.
	kofiID string
}

func newCommentNotifyFixture(t *testing.T) *commentNotifyFixture {
	t.Helper()
	f, n := newActivityNotifyFixture(t)
	jobs := &recordingJobs{}
	f.e.txJobs = jobs
	kofi := f.newUser(t, user.StatusActive, true)
	if err := f.e.userStore.EnsureProfile(t.Context(), kofi, "Kofi Boateng"); err != nil {
		t.Fatalf("EnsureProfile() error: %v", err)
	}
	return &commentNotifyFixture{scheduledActivityFixture: f, notifier: n, jobs: jobs, kofiID: kofi}
}

func mention(id string) string { return "<@" + id + ">" }

func (f *commentNotifyFixture) comment(t *testing.T, recordID, text string, notifyFollowers bool) map[string]any {
	t.Helper()
	w := f.post(t, f.callerID, map[string]any{"model": activityTestModel, "record_id": recordID, "body": text, "notify_followers": notifyFollowers})
	if w.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201; body: %s", w.Code, w.Body.String())
	}
	return decodeObject(t, w)
}

// deliver runs the comment's notification jobs the post enqueued, as
// River would, and returns what they sent.
func (f *commentNotifyFixture) deliver(t *testing.T) []sentNotification {
	t.Helper()
	ctx := t.Context()
	for len(f.jobs.jobs) > 0 {
		for _, job := range f.jobs.take() {
			var err error
			switch args := job.(type) {
			case jobqueue.RecordCommentNotifyArgs:
				err = f.e.fanOutComment(ctx, args)
			case jobqueue.RecordCommentRecipientArgs:
				err = f.e.notifyCommentRecipient(ctx, args)
			default:
				t.Fatalf("unexpected job %T", job)
			}
			if err != nil {
				t.Fatalf("%s job error: %v", job.Kind(), err)
			}
		}
	}
	return f.notifier.take()
}

func sentTo(sent []sentNotification) map[string]string {
	out := map[string]string{}
	for _, s := range sent {
		out[s.userID] = s.notificationType
	}
	return out
}

func (f *commentNotifyFixture) follow(t *testing.T, userID, recordID string) {
	t.Helper()
	if err := f.e.recordActivityStore.Follow(t.Context(), f.slug, activityTestModel, recordID, userID); err != nil {
		t.Fatalf("Follow() error: %v", err)
	}
}

func TestCommentNotifications_ANoteNotifiesOnlyMentionedUsers(t *testing.T) {
	f := newCommentNotifyFixture(t)
	f.follow(t, f.memberID, f.recordID)

	f.comment(t, f.recordID, "No one is told about this.", false)
	if jobs := f.jobs.take(); len(jobs) != 0 {
		t.Errorf("a note mentioning no one enqueued %v, want nothing", jobs)
	}
	f.comment(t, f.recordID, mention(f.callerID)+" a reminder to myself", false)
	if jobs := f.jobs.take(); len(jobs) != 0 {
		t.Errorf("a note mentioning only its author enqueued %v, want nothing", jobs)
	}

	c := f.comment(t, f.recordID, mention(f.kofiID)+" can you confirm the Friday slot?", false)
	sent := f.deliver(t)
	if got := sentTo(sent); len(got) != 1 || got[f.kofiID] != recordMentionType {
		t.Fatalf("a note mentioning Kofi sent %v, want only engine.record_mention to Kofi", got)
	}
	got := sent[0]
	if got.opts.IdempotencyKey != "record_comment:"+c["id"].(string) {
		t.Errorf("IdempotencyKey = %q, want one naming the comment", got.opts.IdempotencyKey)
	}
	want := map[string]any{
		"EntryID": c["id"], "Model": activityTestModel, "RecordID": f.recordID, "RecordName": "Widget A",
		"AuthorName": "Ama Owusu", "Body": "@Kofi Boateng can you confirm the Friday slot?",
		"Excerpt": "@Kofi Boateng can you confirm the Friday slot?",
	}
	for k, v := range want {
		if got.data[k] != v {
			t.Errorf("data[%s] = %v, want %v", k, got.data[k], v)
		}
	}
}

func TestCommentNotifications_AMessageAlsoNotifiesFollowersButNeverTheAuthor(t *testing.T) {
	f := newCommentNotifyFixture(t)
	f.follow(t, f.memberID, f.recordID)
	f.follow(t, f.kofiID, f.recordID)

	f.comment(t, f.recordID, mention(f.kofiID)+" and everyone: delivery moved to Friday.", true)
	got := sentTo(f.deliver(t))
	want := map[string]string{f.kofiID: recordMentionType, f.memberID: recordMessageType}
	if len(got) != len(want) || got[f.kofiID] != want[f.kofiID] || got[f.memberID] != want[f.memberID] {
		t.Errorf("a message sent %v, want the mention to Kofi and the message to the other follower only", got)
	}
}

func TestCommentNotifications_AFollowerWhoCannotReadIsSkippedAndUnfollowed(t *testing.T) {
	f := newCommentNotifyFixture(t)
	f.restrictWidgetsToOwner(t)
	widget := f.insertWidget(t, "Widget B", nil)
	f.follow(t, f.memberID, widget)
	f.follow(t, f.kofiID, widget)
	if _, err := f.admin.ExecContext(t.Context(), fmt.Sprintf(`UPDATE %s.widget SET internal_ref = $1 WHERE id = $2`, tenantschema.Name(f.slug)), f.callerID, widget); err != nil {
		t.Fatalf("restrict widget: %v", err)
	}

	f.comment(t, widget, "Only I can read this now.", true)
	if sent := f.deliver(t); len(sent) != 0 {
		t.Errorf("a message no follower can read sent %+v, want nothing", sent)
	}
	followers, err := f.e.recordActivityStore.ListFollowers(t.Context(), f.slug, activityTestModel, widget)
	if err != nil {
		t.Fatalf("ListFollowers() error: %v", err)
	}
	if len(followers) != 1 || followers[0].UserID != f.callerID {
		t.Errorf("followers = %+v, want only the author left", followers)
	}
}

func TestCommentNotifications_ADeletedCommentSendsNothingFurther(t *testing.T) {
	f := newCommentNotifyFixture(t)
	c := f.comment(t, f.recordID, mention(f.kofiID)+" never mind", false)
	if w := f.deleteAs(f.callerID, c["id"].(string)); w.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d", w.Code)
	}
	if sent := f.deliver(t); len(sent) != 0 {
		t.Errorf("a deleted comment sent %+v, want nothing", sent)
	}
}

func TestDispatchActivityCreateRoute_RejectsUnmentionableUsers(t *testing.T) {
	f := newCommentNotifyFixture(t)
	f.restrictWidgetsToOwner(t)
	nonMember := f.newUser(t, user.StatusActive, false)
	suspended := f.newUser(t, user.StatusSuspended, true)
	missing := "99999999-9999-9999-9999-999999999999"

	body := strings.Join([]string{mention(f.kofiID), mention(nonMember), mention(suspended), mention(missing), mention(nonMember)}, " ")
	w := f.post(t, f.callerID, map[string]any{"model": activityTestModel, "record_id": f.recordID, "body": body})
	raw := w.Body.Bytes()
	wantError(t, w, http.StatusBadRequest, "invalid_mention", "mentions of non-members")
	var resp struct {
		Error struct {
			Details struct {
				UserIDs []string `json:"user_ids"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	ids := resp.Error.Details.UserIDs
	if !slices.Equal(ids, []string{nonMember, suspended, missing}) {
		t.Errorf("details.user_ids = %v, want each unmentionable user once, in order", ids)
	}

	private := f.insertWidget(t, "Caller's own", &f.callerID)
	w = f.post(t, f.callerID, map[string]any{"model": activityTestModel, "record_id": private, "body": mention(f.kofiID) + " look"})
	wantError(t, w, http.StatusBadRequest, "invalid_mention", "mention of a member who can't read the record")
	if jobs := f.jobs.take(); len(jobs) != 0 {
		t.Errorf("rejected posts enqueued %v, want nothing", jobs)
	}

	var many []string
	for i := range recordactivity.MaxMentions + 1 {
		many = append(many, mention(fmt.Sprintf("00000000-0000-7000-8000-%012d", i)))
	}
	w = f.post(t, f.callerID, map[string]any{"model": activityTestModel, "record_id": f.recordID, "body": strings.Join(many, " ")})
	wantError(t, w, http.StatusBadRequest, "invalid_request", "21 distinct mentions")
}

func TestDispatchActivityRoutes_ReturnMentionsAndNotifyFollowers(t *testing.T) {
	f := newCommentNotifyFixture(t)
	gone := f.newUser(t, user.StatusActive, true)
	erased := f.newUser(t, user.StatusActive, true)

	posted := f.comment(t, f.recordID, mention(f.kofiID)+" and "+mention(f.memberID)+" and "+mention(gone)+" and "+mention(erased)+", "+mention(f.kofiID), true)
	if posted["notify_followers"] != true {
		t.Errorf("POST notify_followers = %v, want true", posted["notify_followers"])
	}
	deleted := f.comment(t, f.recordID, mention(f.kofiID)+" oops", false)
	if w := f.deleteAs(f.callerID, deleted["id"].(string)); w.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d", w.Code)
	}
	if _, err := f.admin.ExecContext(t.Context(), `DELETE FROM system.users WHERE id = $1`, gone); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if _, err := f.admin.ExecContext(t.Context(), `UPDATE system.users SET status = 'deleted', deleted_at = NOW() WHERE id = $1`, erased); err != nil {
		t.Fatalf("soft-delete user: %v", err)
	}
	var memberEmail string
	if err := f.admin.QueryRowContext(t.Context(), `SELECT email FROM system.users WHERE id = $1`, f.memberID).Scan(&memberEmail); err != nil {
		t.Fatalf("read member email: %v", err)
	}

	w := f.list(t, url.Values{"model": {activityTestModel}, "record_id": {f.recordID}})
	if w.Code != http.StatusOK {
		t.Fatalf("GET status = %d; body: %s", w.Code, w.Body.String())
	}
	data := decodeObject(t, w)["data"].([]any)
	byID := map[string]map[string]any{}
	for _, d := range data {
		entry := d.(map[string]any)
		byID[entry["id"].(string)] = entry
	}

	got := byID[posted["id"].(string)]
	mentions := got["mentions"].([]any)
	if len(mentions) != 4 || got["notify_followers"] != true {
		t.Fatalf("comment = %v, want 4 distinct mentions and notify_followers", got)
	}
	want := []map[string]any{
		{"id": f.kofiID, "name": "Kofi Boateng"},
		{"id": f.memberID, "name": nil, "email": memberEmail},
		{"id": gone, "name": nil, "email": nil},
		{"id": erased, "name": nil, "email": nil},
	}
	for i, w := range want {
		m := mentions[i].(map[string]any)
		for k, v := range w {
			if m[k] != v {
				t.Errorf("mentions[%d].%s = %v, want %v", i, k, m[k], v)
			}
		}
	}

	del := byID[deleted["id"].(string)]
	if m, ok := del["mentions"].([]any); !ok || len(m) != 0 || del["notify_followers"] != false {
		t.Errorf("deleted comment = %v, want empty mentions and notify_followers false", del)
	}
}

func TestCommentNotification_RendersMentionFallbacksAndCutsTheExcerpt(t *testing.T) {
	f := newCommentNotifyFixture(t)
	var memberEmail string
	if err := f.admin.QueryRowContext(t.Context(), `SELECT email FROM system.users WHERE id = $1`, f.memberID).Scan(&memberEmail); err != nil {
		t.Fatalf("read member email: %v", err)
	}
	local, _, _ := strings.Cut(memberEmail, "@")
	long := strings.Repeat("é", 250)
	body := mention(f.memberID) + " " + mention("99999999-9999-9999-9999-999999999999") + " " + long
	entry := &recordactivity.Entry{ID: "e", Model: activityTestModel, RecordID: f.recordID, Kind: recordactivity.KindComment, Body: &body, AuthorID: &f.callerID}

	data, _, err := f.e.commentNotification(t.Context(), entry, nil)
	if err != nil {
		t.Fatalf("commentNotification() error: %v", err)
	}
	wantBody := "@" + local + " @Unknown user " + long
	if data["Body"] != wantBody {
		t.Errorf("Body = %q, want %q", data["Body"], wantBody)
	}
	excerpt := data["Excerpt"].(string)
	if want := string([]rune(wantBody)[:commentExcerptLength]) + "…"; excerpt != want {
		t.Errorf("Excerpt = %q, want the first %d characters and an ellipsis", excerpt, commentExcerptLength)
	}
}

func TestCommentRecipients_MentionWinsAndTheAuthorIsNeverOne(t *testing.T) {
	author, ama, kofi, efua := "a0000000-0000-7000-8000-000000000000", "a0000000-0000-7000-8000-000000000001", "a0000000-0000-7000-8000-000000000002", "a0000000-0000-7000-8000-000000000003"
	body := mention(kofi) + " " + mention(author) + " " + mention(ama)
	followers := []string{author, efua, ama}

	note := &recordactivity.Entry{Body: &body, AuthorID: &author}
	if got := commentRecipients(note, followers); !slices.Equal(got, []commentRecipient{{kofi, recordMentionType}, {ama, recordMentionType}}) {
		t.Errorf("note recipients = %v, want the mentioned users only", got)
	}
	message := &recordactivity.Entry{Body: &body, AuthorID: &author, NotifyFollowers: true}
	want := []commentRecipient{{kofi, recordMentionType}, {ama, recordMentionType}, {efua, recordMessageType}}
	if got := commentRecipients(message, followers); !slices.Equal(got, want) {
		t.Errorf("message recipients = %v, want %v", got, want)
	}
}
