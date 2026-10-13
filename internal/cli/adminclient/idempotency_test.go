package adminclient

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
	"uuid"
)

func TestMutationRetriesPreserveKeyAndBody(t *testing.T) {
	for _, kind := range []string{http.MethodPost, http.MethodPatch, http.MethodDelete, "binary", "upload"} {
		t.Run(kind, func(t *testing.T) {
			var mu sync.Mutex
			var keys []string
			var bodies [][]byte
			var contentTypes []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				mu.Lock()
				keys = append(keys, r.Header.Get("Idempotency-Key"))
				bodies = append(bodies, body)
				contentTypes = append(contentTypes, r.Header.Get("Content-Type"))
				attempt := len(keys)
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				if attempt == 1 {
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = w.Write([]byte(`{"error":{"code":"admin_api_busy","message":"busy"}}`))
					return
				}
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"data":{"job_id":"original"}}`))
			}))
			defer srv.Close()
			client, err := New(srv.URL, "test", time.Second)
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				var err error
				switch kind {
				case "binary":
					_, err = client.PostBinary(t.Context(), "/admin/modules/install", []byte("package"))
				case "upload":
					_, err = client.UploadFile(t.Context(), "/admin/tenants/import/upload", "archive", "tenant.zip", bytes.NewBufferString("archive"))
				default:
					_, err = client.Do(t.Context(), kind, "/admin/mutation", map[string]string{"value": "original"})
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if len(keys) != 3 || keys[0] != keys[1] || keys[1] == keys[2] {
				t.Fatalf("keys=%v", keys)
			}
			if _, err := uuid.Parse(keys[0]); err != nil {
				t.Fatalf("invalid key: %v", err)
			}
			if !bytes.Equal(bodies[0], bodies[1]) || contentTypes[0] != contentTypes[1] {
				t.Fatalf("retry payload changed: %q / %q", bodies[0], bodies[1])
			}
		})
	}
}

func TestTimedOutMutationReplaysOriginalJob(t *testing.T) {
	var mu sync.Mutex
	responses := map[string]string{}
	attempts, mutations := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Error(err)
		}
		key := r.Header.Get("Idempotency-Key")
		mu.Lock()
		attempts++
		response, found := responses[key]
		if !found {
			mutations++
			response = `{"data":{"job_id":"job_original","status_url":"/admin/jobs/job_original"}}`
			responses[key] = response
		}
		mu.Unlock()
		if !found {
			select {
			case <-r.Context().Done():
			case <-t.Context().Done():
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(response))
	}))
	defer srv.Close()
	client, err := New(srv.URL, "test", 30*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	data, err := client.Post(t.Context(), "/admin/schema/sync", map[string]string{"tenant": "acme"})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts != 2 || mutations != 1 || result.JobID != "job_original" {
		t.Fatalf("attempts=%d mutations=%d result=%+v", attempts, mutations, result)
	}
}

func TestReadRetryBoundAndNoKey(t *testing.T) {
	var mu sync.Mutex
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if key := r.Header.Get("Idempotency-Key"); key != "" {
			t.Errorf("GET key=%q", key)
		}
		mu.Lock()
		attempts++
		mu.Unlock()
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream unavailable"))
	}))
	defer srv.Close()
	client, err := New(srv.URL, "test", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Get(t.Context(), "/admin/jobs")
	if apiErr, ok := errors.AsType[*APIError](err); !ok || apiErr.HTTPStatus != http.StatusBadGateway {
		t.Fatalf("error=%v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts != 3 {
		t.Fatalf("attempts=%d", attempts)
	}
}

func TestMutationConflictRetryPolicy(t *testing.T) {
	for _, code := range []string{"idempotency_in_progress", "tenant_exists"} {
		t.Run(code, func(t *testing.T) {
			attempts := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts++
				if attempts == 1 {
					w.Header().Set("Retry-After", "1")
					w.WriteHeader(http.StatusConflict)
					_, _ = w.Write([]byte(`{"error":{"code":"` + code + `","message":"conflict"}}`))
					return
				}
				_, _ = w.Write([]byte(`{"data":"original"}`))
			}))
			defer srv.Close()
			client, err := New(srv.URL, "test", time.Second)
			if err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			_, err = client.Post(t.Context(), "/admin/tenants", nil)
			if code == "idempotency_in_progress" {
				if err != nil || attempts != 2 || time.Since(started) < time.Second {
					t.Fatalf("attempts=%d elapsed=%s error=%v", attempts, time.Since(started), err)
				}
			} else if err == nil || attempts != 1 {
				t.Fatalf("attempts=%d error=%v", attempts, err)
			}
		})
	}
}
