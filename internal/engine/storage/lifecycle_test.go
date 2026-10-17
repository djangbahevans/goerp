package storage

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type lifecycleTransport func(*http.Request) (*http.Response, error)

func (f lifecycleTransport) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestLifecycleProviderPolicies(t *testing.T) {
	for _, backend := range []string{"s3", "seaweedfs", "r2", "gcs"} {
		t.Run(backend, func(t *testing.T) {
			cfg := remoteConfig(backend)
			cfg.StorageS3Endpoint = "https://objects.test"
			cfg.StorageS3Region = "us-east-1"
			b, err := newS3CompatBackend(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}

			var bodies []string
			opts := b.client.Options()
			opts.HTTPClient = lifecycleTransport(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodPut || !req.URL.Query().Has("lifecycle") || req.Header.Get("Authorization") == "" {
					t.Errorf("unsigned or invalid lifecycle request: %s %s", req.Method, req.URL)
				}
				if backend == "gcs" && (req.URL.Host != "files.objects.test" || req.Header.Get("X-Amz-Content-Sha256") == "") {
					t.Errorf("invalid GCS lifecycle endpoint or payload signature: %s", req.URL)
				}
				if backend == "r2" && req.Header.Get("X-Amz-Sdk-Checksum-Algorithm") != "" {
					t.Error("R2 lifecycle request includes an unsupported checksum algorithm header")
				}
				body, err := io.ReadAll(req.Body)
				if err != nil {
					t.Fatal(err)
				}
				bodies = append(bodies, string(body))

				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader("")),
				}, nil
			})
			b.client = s3.New(opts)

			for range 2 {
				if err := b.applyLifecycle(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			if len(bodies) != 2 || bodies[0] != bodies[1] {
				t.Fatalf("non-idempotent policy: %v", bodies)
			}

			var policy struct {
				Rules []struct {
					Status      string  `xml:"Status"`
					Prefix      string  `xml:"Filter>Prefix"`
					Days        int     `xml:"Expiration>Days"`
					GCSPrefix   string  `xml:"Condition>MatchesPrefix"`
					GCSDays     int     `xml:"Condition>Age"`
					GCSDelete   *string `xml:"Action>Delete"`
					Transitions []struct {
						Days  int
						Class string `xml:"StorageClass"`
					} `xml:"Transition"`
				} `xml:"Rule"`
			}
			if err := xml.Unmarshal([]byte(bodies[0]), &policy); err != nil {
				t.Fatal(err)
			}

			expirations := make(map[string]int)
			transitions := make(map[int]string)
			for _, rule := range policy.Rules {
				if backend != "gcs" && rule.Status != "Enabled" {
					t.Fatalf("inactive lifecycle rule: %+v", rule)
				}
				if backend == "gcs" {
					if rule.GCSDelete == nil {
						t.Fatal("GCS rule lacks Delete action")
					}
					expirations[rule.GCSPrefix] = rule.GCSDays
				} else if rule.Days > 0 {
					expirations[rule.Prefix] = rule.Days
				}
				for _, transition := range rule.Transitions {
					if rule.Prefix != "attachments/" {
						t.Fatalf("transition on %q", rule.Prefix)
					}
					transitions[transition.Days] = transition.Class
				}
			}
			if !maps.Equal(expirations, map[string]int{"imports/": 30, "exports/": 7, "reports/": 7}) {
				t.Fatalf("expiry policy = %v", expirations)
			}
			wantTransitions := map[int]string{}
			if backend == "s3" {
				wantTransitions = map[int]string{90: "STANDARD_IA", 365: "GLACIER_IR"}
			}
			if !maps.Equal(transitions, wantTransitions) {
				t.Fatalf("transitions = %v, want %v", transitions, wantTransitions)
			}

			opts.HTTPClient = lifecycleTransport(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusForbidden,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader("")),
				}, nil
			})
			b.client = s3.New(opts)
			if err := b.applyLifecycle(t.Context()); err == nil || !strings.Contains(strings.ToLower(err.Error()), backend) {
				t.Fatalf("lifecycle permission error = %v", err)
			}
		})
	}
}

func TestLifecycleFailureDisablesStorage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	cfg := remoteConfig("seaweedfs")
	cfg.StorageS3Endpoint = srv.URL
	b, err := New(t.Context(), cfg)
	if b != nil || err == nil || !strings.Contains(err.Error(), "lifecycle") {
		t.Fatalf("storage with failed lifecycle = %v, %v", b, err)
	}
}

func TestSignedURLRejectsMissingObject(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusForbidden, http.StatusOK} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			}))
			defer srv.Close()

			cfg := remoteConfig("seaweedfs")
			cfg.StorageS3Endpoint = srv.URL
			b, err := newS3CompatBackend(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			u, err := b.SignedURL(t.Context(), "reports/missing.pdf", time.Hour)
			if (err == nil) != (status == http.StatusOK) || (u != "") != (status == http.StatusOK) || errors.Is(err, fs.ErrNotExist) != (status == http.StatusNotFound) {
				t.Fatalf("signed URL = %q, %v", u, err)
			}
		})
	}
}

func TestSeaweedFSLifecycleOnStartup(t *testing.T) {
	endpoint := os.Getenv("GOERP_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("set GOERP_TEST_S3_ENDPOINT to run SeaweedFS lifecycle integration")
	}
	cfg := remoteConfig("seaweedfs")
	cfg.StorageS3Endpoint = endpoint
	cfg.StorageS3AccessKey = "goerp-dev"
	cfg.StorageS3SecretKey = "goerp-dev-secret"
	cfg.StorageBucket = "goerp-lifecycle-" + uuid.New().String()
	b, err := newS3CompatBackend(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.client.CreateBucket(t.Context(), &s3.CreateBucketInput{Bucket: new(cfg.StorageBucket)}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if _, err := b.client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: new(cfg.StorageBucket)}); err != nil {
			t.Error(err)
		}
	})

	for range 2 {
		if _, err := New(t.Context(), cfg); err != nil {
			t.Fatal(err)
		}
		policy, err := b.client.GetBucketLifecycleConfiguration(t.Context(), &s3.GetBucketLifecycleConfigurationInput{Bucket: new(cfg.StorageBucket)})
		if err != nil {
			t.Fatal(err)
		}
		got := make(map[string]int32)
		for _, rule := range policy.Rules {
			got[aws.ToString(rule.Filter.Prefix)] = aws.ToInt32(rule.Expiration.Days)
		}
		if !maps.Equal(got, map[string]int32{"imports/": 30, "exports/": 7, "reports/": 7}) {
			t.Fatalf("stored lifecycle = %v", got)
		}
	}
}
