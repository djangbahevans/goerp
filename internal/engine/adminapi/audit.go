package adminapi

import (
	"encoding/json"
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
	body, _ := r.Context().Value(ctxKeyBody).([]byte)
	var v struct {
		Slug string `json:"slug"`
	}
	_ = json.Unmarshal(body, &v)
	return v.Slug
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
