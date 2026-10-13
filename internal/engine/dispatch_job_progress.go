package engine

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/jobprogress"
	"github.com/djangbahevans/goerp/internal/engine/jobqueue"
	"github.com/djangbahevans/goerp/internal/engine/route"
)

func (e *Engine) dispatchJobProgressRoute(w http.ResponseWriter, r *http.Request) {
	auth, tenant := authFromContext(r.Context()), tenantFromContext(r.Context())
	if auth == nil || tenant == nil || e.jobProgress == nil || e.streams == nil {
		httperr.Write(r.Context(), w, http.StatusServiceUnavailable, "not_ready", "engine has not finished starting")
		return
	}

	if !auth.IsAuthenticated || auth.AuthMethod != "jwt" || auth.UserID == "" {
		httperr.Write(r.Context(), w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}

	rawID := route.ParamsFromContext(r.Context())["job_id"]
	id, err := jobqueue.DecodeJobID(rawID)
	if err != nil || id <= 0 || jobqueue.EncodeJobID(id) != rawID {
		httperr.Write(r.Context(), w, http.StatusNotFound, "jobs.not_found", "Job not found")
		return
	}

	locale := "en"
	if e.wasmRuntime != nil {
		info, err := e.wasmRuntime.ResolveLocale(r.Context(), tenant.TenantID, auth.UserID)
		if err != nil {
			writeDispatchError(r.Context(), w, err, 0)
			return
		}
		locale = info.Locale
	}

	read := func(ctx context.Context) ([]byte, bool, error) {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		snapshot, err := e.jobProgress.Read(ctx, id, tenant.TenantID, auth.UserID)
		if err != nil {
			return nil, false, err
		}

		snapshot.JobID = rawID
		if snapshot.Error != nil {
			snapshot.Error.Message = jobFailureMessage(locale, snapshot.Error.Code)
		}

		data, err := json.Marshal(snapshot)
		return data, snapshot.Status == "complete" || snapshot.Status == "failed", err
	}

	data, done, err := read(r.Context())
	if errors.Is(err, jobprogress.ErrNotFound) {
		httperr.Write(r.Context(), w, http.StatusNotFound, "jobs.not_found", "Job not found")
		return
	}
	if err != nil {
		writeDispatchError(r.Context(), w, err, 0)
		return
	}

	connection, ok := e.streams.Open(w, r, tenant.TenantID, auth.UserID, auth.AccessTokenExpiresAt)
	if !ok {
		return
	}
	defer connection.Close()

	last := data
	connection.Run(data, done, time.Second, func(ctx context.Context) ([]byte, bool, error) {
		data, done, err := read(ctx)
		if bytes.Equal(data, last) {
			return nil, done, err
		}
		last = data
		return data, done, err
	})
}

func jobFailureMessage(locale, code string) string {
	language, _, _ := strings.Cut(locale, "-")
	cancelled := code == "jobs.cancelled"
	switch language {
	case "fr":
		if cancelled {
			return "Le travail a été annulé"
		}
		return "Le travail a échoué"
	case "ar":
		if cancelled {
			return "تم إلغاء المهمة"
		}
		return "فشلت المهمة"
	default:
		if cancelled {
			return "The job was cancelled"
		}
		return "The job failed"
	}
}
