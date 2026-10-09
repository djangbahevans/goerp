package notify

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestSend_OpenSendTxKeyReturnsLockTimeout(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t, "Ama Owusu", "")
	opts := Options{IdempotencyKey: "open-send"}

	tx, err := env.conn.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()

	first, err := env.sender.SendTx(t.Context(), tx, env.tenant.ID, "sales", orderConfirmed, userID, nil, opts)
	if err != nil {
		t.Fatalf("SendTx: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	started := time.Now()
	_, err = env.sender.Send(ctx, env.tenant.ID, "sales", orderConfirmed, userID, nil, opts)
	if !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("Send error = %v, want ErrLockTimeout", err)
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok || pgErr.Code != "55P03" {
		t.Fatalf("Send error = %v, want preserved PostgreSQL lock timeout", err)
	}
	if elapsed := time.Since(started); elapsed >= 5*time.Second {
		t.Errorf("Send took %v, want a prompt lock error before the request deadline", elapsed)
	}

	if env.notificationCount(t) != 0 || env.deliveryCount(t) != 0 || env.jobCount(t) != 0 || len(env.hub.sent) != 0 {
		t.Fatal("failed Send left visible notification side effects")
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("caller transaction unusable after failed Send: %v", err)
	}

	again, err := env.sender.Send(t.Context(), env.tenant.ID, "sales", orderConfirmed, userID, nil, opts)
	if err != nil {
		t.Fatalf("Send after commit: %v", err)
	}
	if !again.Deduplicated || again.NotificationID != first.NotificationID {
		t.Fatalf("Send after commit = %+v, want original notification %s", again, first.NotificationID)
	}
}

func TestHostSender_BulkLockTimeoutRollsBackEveryRecipient(t *testing.T) {
	env := openTestEnv(t)
	freeUser := env.createUser(t, "Kofi Mensah", "")
	lockedUser := env.createUser(t, "Ama Owusu", "")
	host := HostSender{env.sender}

	tx, err := env.conn.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()

	lockedReq := hostRequest(env, orderConfirmed, lockedUser)
	lockedReq.Opts.IdempotencyKey = "bulk-lock"
	if _, _, err := host.SendTx(t.Context(), tx, lockedReq); err != nil {
		t.Fatalf("SendTx: %v", err)
	}

	bulkReq := hostRequest(env, orderConfirmed, freeUser, lockedUser)
	bulkReq.Opts = lockedReq.Opts
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err = host.SendBulk(ctx, bulkReq)
	requireHostErrorCode(t, err, abiv1.ErrCodeNotifyLockTimeout)
	hostErr, _ := errors.AsType[*abiv1.HostError](err)
	if hostErr.Retry {
		t.Fatal("lock timeout requests an automatic retry while the caller still holds the key")
	}
	if env.notificationCount(t) != 0 || env.deliveryCount(t) != 0 || env.jobCount(t) != 0 || len(env.hub.sent) != 0 {
		t.Fatal("bulk lock timeout left notification side effects for an earlier recipient")
	}

	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	results, err := host.SendBulk(t.Context(), bulkReq)
	if err != nil {
		t.Fatalf("SendBulk after rollback: %v", err)
	}
	if len(results) != 2 || results[0].Deduplicated || results[1].Deduplicated {
		t.Fatalf("SendBulk after rollback = %+v, want two new notifications", results)
	}
}

func TestSend_LockTimeoutDoesNotLeakToPooledConnection(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t, "Ama Owusu", "")
	env.conn.SetMaxIdleConns(1)
	env.conn.SetMaxOpenConns(1)

	var before string
	if err := env.conn.QueryRowContext(t.Context(), "SHOW lock_timeout").Scan(&before); err != nil {
		t.Fatal(err)
	}

	_, err := env.sender.Send(t.Context(), env.tenant.ID, "sales", orderConfirmed, userID, nil, Options{IdempotencyKey: "local-timeout"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	var after string
	if err := env.conn.QueryRowContext(t.Context(), "SHOW lock_timeout").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Errorf("pooled connection lock_timeout = %q, want original %q", after, before)
	}
}

func TestSend_ConcurrentCommitStillDeduplicates(t *testing.T) {
	env := openTestEnv(t)
	userID := env.createUser(t, "Ama Owusu", "")
	opts := Options{IdempotencyKey: "concurrent-commit"}

	tx, err := env.conn.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()

	first, err := env.sender.SendTx(t.Context(), tx, env.tenant.ID, "sales", orderConfirmed, userID, nil, opts)
	if err != nil {
		t.Fatalf("SendTx: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	type outcome struct {
		result *Result
		err    error
	}
	done := make(chan outcome, 1)
	var sends sync.WaitGroup
	sends.Go(func() {
		res, err := env.sender.Send(ctx, env.tenant.ID, "sales", orderConfirmed, userID, nil, opts)
		done <- outcome{result: res, err: err}
	})
	defer func() {
		cancel()
		sends.Wait()
	}()

	ticks := time.Tick(10 * time.Millisecond)
	for {
		var waiting bool
		err := env.conn.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM pg_stat_activity
				WHERE wait_event_type = 'Lock' AND query LIKE $1
			)
		`, "%INSERT INTO "+tenantschema.Name(env.tenant.Slug)+".notifications%").Scan(&waiting)
		if err != nil {
			t.Fatalf("observe competing insert: %v", err)
		}
		if waiting {
			break
		}

		select {
		case out := <-done:
			t.Fatalf("competing Send returned before waiting for the open transaction: %v", out.err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticks:
		}
	}

	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	out := <-done
	if out.err != nil {
		t.Fatalf("competing Send: %v", out.err)
	}
	if !out.result.Deduplicated || out.result.NotificationID != first.NotificationID {
		t.Fatalf("competing Send = %+v, want original notification %s", out.result, first.NotificationID)
	}
	if env.notificationCount(t) != 1 || len(env.hub.sent) != 0 {
		t.Fatal("deduplicated competing Send created or announced a second notification")
	}
}
