package storage

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/djangbahevans/goerp/internal/engine/config"
)

func remoteConfig(backend string) config.Config {
	return config.Config{
		StorageBackend:     backend,
		StorageBucket:      "files",
		StorageS3AccessKey: "access",
		StorageS3SecretKey: "secret",
	}
}

func TestS3CompatClientSettings(t *testing.T) {
	for _, tc := range []struct {
		backend       string
		endpoint      string
		region        string
		wantEndpoint  string
		wantRegion    string
		pathStyle     bool
		wantPathStyle bool
	}{
		{
			backend:       "seaweedfs",
			endpoint:      "http://localhost:8334",
			wantEndpoint:  "http://localhost:8334",
			wantRegion:    "us-east-1",
			wantPathStyle: true,
		},
		{
			backend:    "s3",
			region:     "af-south-1",
			wantRegion: "af-south-1",
		},
		{
			backend:       "s3",
			endpoint:      "https://custom.example",
			region:        "us-east-2",
			wantEndpoint:  "https://custom.example",
			wantRegion:    "us-east-2",
			pathStyle:     true,
			wantPathStyle: true,
		},
		{
			backend:      "r2",
			endpoint:     "https://account.r2.cloudflarestorage.com",
			region:       "ignored",
			wantEndpoint: "https://account.r2.cloudflarestorage.com",
			wantRegion:   "auto",
		},
		{
			backend:      "gcs",
			pathStyle:    true,
			wantEndpoint: "https://storage.googleapis.com",
			wantRegion:   "auto",
		},
	} {
		t.Run(tc.backend+tc.endpoint, func(t *testing.T) {
			cfg := remoteConfig(tc.backend)
			cfg.StorageS3Endpoint = tc.endpoint
			cfg.StorageS3Region = tc.region
			cfg.StorageS3UsePathStyle = tc.pathStyle

			b, err := newS3CompatBackend(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}

			opts := b.client.Options()
			if aws.ToString(opts.BaseEndpoint) != tc.wantEndpoint || opts.Region != tc.wantRegion || opts.UsePathStyle != tc.wantPathStyle {
				t.Fatalf("client settings = endpoint %s, region %s, path style %v", aws.ToString(opts.BaseEndpoint), opts.Region, opts.UsePathStyle)
			}

			signed, err := b.presignURL(t.Context(), "folder/a +#é.txt", time.Minute)
			if err != nil {
				t.Fatal(err)
			}

			u, err := url.Parse(signed)
			if err != nil {
				t.Fatal(err)
			}

			if u.Query().Get("X-Amz-Expires") != "60" || !strings.HasSuffix(u.Path, "/folder/a +#é.txt") {
				t.Fatalf("unexpected URL: %s", signed)
			}

			if tc.wantPathStyle && !strings.HasPrefix(u.Path, "/files/") {
				t.Fatalf("path style URL: %s", signed)
			}

			if !tc.wantPathStyle && !strings.HasPrefix(u.Host, "files.") {
				t.Fatalf("virtual hosted URL: %s", signed)
			}
		})
	}
}

func TestS3CompatAWSCredentialChain(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "chain-access")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "chain-secret")
	t.Setenv("AWS_SESSION_TOKEN", "chain-token")

	cfg := remoteConfig("s3")
	cfg.StorageS3AccessKey = ""
	cfg.StorageS3SecretKey = ""
	cfg.StorageS3Region = "us-east-1"

	b, err := newS3CompatBackend(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}

	creds, err := b.client.Options().Credentials.Retrieve(t.Context())
	if err != nil || creds.AccessKeyID != "chain-access" || creds.SessionToken != "chain-token" {
		t.Fatalf("credentials: %v, %v", creds, err)
	}
}

func TestS3CompatStartupBucketCheck(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusForbidden, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPut && r.URL.Query().Has("lifecycle") && status == http.StatusOK {
					w.WriteHeader(http.StatusOK)
					return
				}

				calls++
				if r.Method != "HEAD" || r.URL.Path != "/files" {
					t.Errorf("bucket probe = %s %s", r.Method, r.URL.Path)
				}

				w.WriteHeader(status)
			}))
			defer srv.Close()

			cfg := remoteConfig("seaweedfs")
			cfg.StorageS3Endpoint = srv.URL

			b, err := New(t.Context(), cfg)
			if calls != 1 || (err == nil) != (status == http.StatusOK) {
				t.Fatalf("startup = %v, %v; probes %d", b, err, calls)
			}

			if err != nil && !strings.Contains(err.Error(), "GOERP_STORAGE_BUCKET") {
				t.Fatal(err)
			}
		})
	}
}

func TestS3CompatExistsErrors(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusNotFound, http.StatusForbidden} {
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

			exists, err := b.Exists(t.Context(), "file")
			if exists != (status == http.StatusOK) || (err != nil) != (status == http.StatusForbidden) {
				t.Fatalf("exists = %v, %v", exists, err)
			}
		})
	}
}

func TestS3CompatDeleteByPrefixPartialFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		if r.Method == "GET" {
			_, _ = io.WriteString(w, `<ListBucketResult><Contents><Key>prefix/a</Key></Contents></ListBucketResult>`)
			return
		}

		_, _ = io.WriteString(w, `<DeleteResult><Error><Key>prefix/a</Key><Code>AccessDenied</Code><Message>denied</Message></Error></DeleteResult>`)
	}))
	defer srv.Close()

	cfg := remoteConfig("seaweedfs")
	cfg.StorageS3Endpoint = srv.URL

	b, err := newS3CompatBackend(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}

	if err := b.DeleteByPrefix(t.Context(), "prefix/"); err == nil || !strings.Contains(err.Error(), "AccessDenied") {
		t.Fatalf("partial deletion: %v", err)
	}
}

func TestS3CompatR2PublicAccessRejected(t *testing.T) {
	cfg := remoteConfig("r2")
	cfg.StorageS3Endpoint = "https://account.r2.cloudflarestorage.com"

	b, err := newS3CompatBackend(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := b.Upload(t.Context(), "public", strings.NewReader("data"), UploadOptions{Public: true}); !errors.Is(err, ErrPublicUnsupported) {
		t.Fatal(err)
	}

	if _, err := b.PublicURL(t.Context(), "public"); !errors.Is(err, ErrPublicUnsupported) {
		t.Fatal(err)
	}

	for _, expiry := range []time.Duration{0, time.Millisecond, -time.Second, 8 * 24 * time.Hour} {
		if _, err := b.SignedURL(t.Context(), "file", expiry); err == nil {
			t.Errorf("accepted expiry %s", expiry)
		}
	}
}
