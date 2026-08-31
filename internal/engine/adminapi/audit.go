package adminapi

import (
	"encoding/json/v2"
	"net/http"
)

const operatorIdentityHeader = "X-GoERP-Operator-Identity"

func operatorIdentity(r *http.Request) string {
	if v := r.Header.Get(operatorIdentityHeader); v != "" {
		return v
	}
	return "internal"
}

func targetScope(r *http.Request) string {
	if slug := r.PathValue("slug"); slug != "" {
		return slug
	}
	// {id} covers routes like /admin/jobs/{id}/... whose response isn't a
	// 202 (jobIDFromResponse's own path to recording which job an
	// endpoint touched), so this is the only place that scope is
	// captured for them.
	if id := r.PathValue("id"); id != "" {
		return id
	}
	body, _ := r.Context().Value(ctxKeyBody).([]byte)
	var v struct {
		Slug string `json:"slug"`
	}
	_ = json.Unmarshal(body, &v)
	return v.Slug
}

// requestReason extracts the request body's own "reason" field, when
// present — not endpoint-specific, so any admin mutation that accepts a
// reason (jobs cancel, tenant suspend, ...) gets it audited the same way.
func requestReason(r *http.Request) string {
	body, _ := r.Context().Value(ctxKeyBody).([]byte)
	var v struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(body, &v)
	return v.Reason
}

func jobIDFromResponse(body []byte, status int) string {
	if status != http.StatusAccepted {
		return ""
	}
	var env struct {
		Data struct {
			JobID string `json:"job_id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &env)
	return env.Data.JobID
}

type responseRecorder struct {
	http.ResponseWriter
	status int
	body   []byte
}

func (r *responseRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	r.body = append(r.body, b...)
	return r.ResponseWriter.Write(b)
}
