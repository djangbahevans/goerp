package engine

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/httperr"
)

func eventFrames(body []byte, cursor string) ([]byte, bool, string, error) {
	var batch struct {
		Events *[]struct {
			ID   string         `json:"id"`
			Data jsontext.Value `json:"data"`
		} `json:"events"`
		Done *bool `json:"done"`
	}
	if err := json.Unmarshal(body, &batch); err != nil {
		return nil, false, cursor, fmt.Errorf("decode event batch: %w", err)
	}
	if batch.Events == nil || batch.Done == nil || len(*batch.Events) > 100 {
		return nil, false, cursor, errors.New("invalid event batch")
	}

	var frames strings.Builder
	for _, event := range *batch.Events {
		if len(event.Data) == 0 || strings.ContainsAny(event.ID, "\r\n\x00") {
			return nil, false, cursor, errors.New("invalid event data or cursor")
		}
		data, err := json.Marshal(event.Data)
		if err != nil {
			return nil, false, cursor, fmt.Errorf("encode event data: %w", err)
		}
		if len(data) > 64<<10 {
			return nil, false, cursor, errors.New("event data exceeds 64 KiB")
		}
		if event.ID != "" {
			fmt.Fprintf(&frames, "id: %s\n", event.ID)
			cursor = event.ID
		}
		fmt.Fprintf(&frames, "data: %s\n\n", data)
	}
	return []byte(frames.String()), *batch.Done, cursor, nil
}

func (e *Engine) dispatchSSERoute(w http.ResponseWriter, r *http.Request, poll http.Handler) {
	first := newEngineResponseRecorder()
	poll.ServeHTTP(first, r)
	response := first.EngineResponse()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		writeResponse(r.Context(), w, response)
		return
	}

	frames, done, cursor, err := eventFrames(response.Body, r.Header.Get("Last-Event-ID"))
	if err != nil {
		writeDispatchError(r.Context(), w, err, 0)
		return
	}
	auth, tenant := authFromContext(r.Context()), tenantFromContext(r.Context())
	if e.streams == nil || auth == nil || tenant == nil {
		httperr.Write(r.Context(), w, http.StatusServiceUnavailable, "not_ready", "event streams are not ready")
		return
	}
	connection, ok := e.streams.Open(w, r, tenant.TenantID, auth.UserID, auth.AccessTokenExpiresAt)
	if !ok {
		return
	}
	defer connection.Close()

	rr := routeResolutionFromContext(r.Context())
	moduleName := rr.entry.ModuleName
	interval := rr.entry.Manifest.PollInterval

	terminal := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		current := routeResolutionFromContext(req.Context())
		if current.entry.ModuleName != moduleName || !current.entry.Manifest.Streaming || current.entry.Manifest.Websocket {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		poll.ServeHTTP(w, req)
	})
	var later http.Handler = terminal
	chain := []func(http.Handler) http.Handler{
		recoveryMiddleware(),
		routeResolutionMiddleware(e.moduleRegistry),
		ssePollTimeoutMiddleware(),
		tenantResolutionMiddleware(e.tenantResolver),
		authMiddleware(e.authChecker),
		mfaEnforcementMiddleware(e.authChecker),
		routeAuthMiddleware(),
	}
	for _, middleware := range slices.Backward(chain) {
		later = middleware(later)
	}

	state := ssePollState{cursor: cursor}
	connection.RunFrames(frames, done, interval, func(ctx context.Context) ([]byte, bool, error) {
		req := r.Clone(ctx)
		req.Body = http.NoBody
		if state.cursor != "" {
			req.Header.Set("Last-Event-ID", state.cursor)
		}

		recorder := newEngineResponseRecorder()
		later.ServeHTTP(recorder, req)
		response := recorder.EngineResponse()
		if ctx.Err() != nil {
			response.StatusCode = http.StatusServiceUnavailable
		}
		return state.accept(response)
	})
}

func ssePollTimeoutMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			timeout := routeResolutionFromContext(r.Context()).entry.Manifest.Timeout
			if timeout <= 0 {
				timeout = defaultHandlerTimeout
			}
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
			if ctx.Err() != nil {
				writeDispatchError(ctx, w, ctx.Err(), timeout)
			}
		})
	}
}

type ssePollState struct {
	cursor   string
	failures int
}

func (s *ssePollState) accept(response EngineResponse) ([]byte, bool, error) {
	if response.StatusCode >= 400 && response.StatusCode < 500 {
		return nil, false, io.EOF
	}
	var frames []byte
	var done bool
	var cursor string
	var err error
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		frames, done, cursor, err = eventFrames(response.Body, s.cursor)
	} else {
		err = errors.New("event poll failed")
	}
	if err != nil {
		s.failures++
		if s.failures >= 3 {
			return nil, false, err
		}
		return nil, false, nil
	}
	s.failures = 0
	s.cursor = cursor
	return frames, done, nil
}
