package adminapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json/v2"
	"fmt"
	"maps"
	"net/http"
	"time"
	"uuid"

	"github.com/rs/zerolog/log"
)

const idempotencyTTL = 24 * time.Hour

type IdempotencyStore interface {
	Get(context.Context, string) (string, bool, error)
	SetNXWithTTL(context.Context, string, string, time.Duration) (bool, error)
	SetIfEqualWithTTL(context.Context, string, string, string, time.Duration) (bool, error)
}

type savedAdminResponse struct {
	Status  int         `json:"status"`
	Header  http.Header `json:"header"`
	Body    []byte      `json:"body"`
	Pattern string      `json:"pattern"`
	Slug    string      `json:"slug,omitempty"`
	ID      string      `json:"id,omitempty"`
}

func adminIdempotencyKey(r *http.Request) string {
	sum := sha256.Sum256([]byte(r.Method + "\x00" + r.URL.RequestURI() + "\x00" + r.Header.Get("Idempotency-Key")))
	return fmt.Sprintf("idempotent:admin:%x", sum)
}

func idempotencyMiddleware(store IdempotencyStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions || r.Header.Get("Idempotency-Key") == "" {
				next.ServeHTTP(w, r)
				return
			}

			unavailable := func(err error) {
				log.Error().Err(err).Msg("admin api: idempotency store unavailable")
				writeError(w, http.StatusServiceUnavailable, "idempotency_unavailable", "idempotency response unavailable; retry with the same key")
			}
			if store == nil {
				unavailable(fmt.Errorf("idempotency store is not configured"))
				return
			}

			key := adminIdempotencyKey(r)
			claim := "pending:" + uuid.New().String()
			claimed, err := store.SetNXWithTTL(r.Context(), key, claim, idempotencyTTL)
			if err != nil {
				unavailable(err)
				return
			}
			if !claimed {
				value, found, err := store.Get(r.Context(), key)
				if err != nil {
					unavailable(err)
					return
				}
				var saved savedAdminResponse
				if found && json.Unmarshal([]byte(value), &saved) == nil && saved.Status >= 200 && saved.Status <= 599 {
					r.Pattern = saved.Pattern
					r.SetPathValue("slug", saved.Slug)
					r.SetPathValue("id", saved.ID)
					replayAdminResponse(w, saved)
					return
				}
				w.Header().Set("Retry-After", "1")
				writeError(w, http.StatusConflict, "idempotency_in_progress", "request with this key is awaiting a recorded response")
				return
			}

			// Buffer until Redis holds the response, including when the client disconnects.
			buffered := &bufferedAdminResponse{header: make(http.Header)}
			next.ServeHTTP(buffered, r)
			if buffered.status == 0 {
				buffered.WriteHeader(http.StatusOK)
			}

			saved := savedAdminResponse{
				Status:  buffered.status,
				Header:  buffered.sentHeader,
				Body:    buffered.body.Bytes(),
				Pattern: r.Pattern,
				Slug:    r.PathValue("slug"),
				ID:      r.PathValue("id"),
			}
			encoded, err := json.Marshal(saved)
			if err != nil {
				unavailable(err)
				return
			}

			ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
			defer cancel()
			stored, err := store.SetIfEqualWithTTL(ctx, key, claim, string(encoded), idempotencyTTL)
			if err == nil && !stored {
				err = fmt.Errorf("idempotency claim expired or changed")
			}
			if err != nil {
				unavailable(fmt.Errorf("record idempotency response: %w", err))
				return
			}
			replayAdminResponse(w, saved)
		})
	}
}

func replayAdminResponse(w http.ResponseWriter, saved savedAdminResponse) {
	maps.Copy(w.Header(), saved.Header)
	w.WriteHeader(saved.Status)
	_, _ = w.Write(saved.Body)
}

type bufferedAdminResponse struct {
	header     http.Header
	sentHeader http.Header
	status     int
	body       bytes.Buffer
}

func (w *bufferedAdminResponse) Header() http.Header { return w.header }

func (w *bufferedAdminResponse) WriteHeader(status int) {
	if w.status == 0 && status >= 200 {
		w.status = status
		w.sentHeader = w.header.Clone()
	}
}

func (w *bufferedAdminResponse) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.body.Write(body)
}
