// Package storagetest provides isolated local or SeaweedFS storage for consumer tests.
package storagetest

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
	"uuid"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/djangbahevans/goerp/internal/engine/config"
	"github.com/djangbahevans/goerp/internal/engine/storage"
)

// New uses a private bucket when GOERP_TEST_S3_ENDPOINT is set, otherwise a temporary directory.
func New(t *testing.T) (storage.Backend, error) {
	t.Helper()

	endpoint := os.Getenv("GOERP_TEST_S3_ENDPOINT")
	if endpoint == "" {
		return storage.NewLocal(t.TempDir())
	}

	bucket := "goerp-test-" + uuid.New().String()
	client := s3.New(s3.Options{
		Region:       "us-east-1",
		BaseEndpoint: new(endpoint),
		UsePathStyle: true,
		Credentials:  aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider("goerp-dev", "goerp-dev-secret", "")),
	})
	if _, err := client.CreateBucket(t.Context(), &s3.CreateBucketInput{Bucket: new(bucket)}); err != nil {
		return nil, fmt.Errorf("create test storage bucket: %w", err)
	}

	backend, err := storage.New(t.Context(), config.Config{
		StorageBackend:     "seaweedfs",
		StorageBucket:      bucket,
		StorageS3Endpoint:  endpoint,
		StorageS3AccessKey: "goerp-dev",
		StorageS3SecretKey: "goerp-dev-secret",
	})

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()

		if backend != nil {
			if err := backend.DeleteByPrefix(ctx, ""); err != nil {
				t.Errorf("empty test storage bucket: %v", err)
				return
			}
		}

		if _, err := client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: new(bucket)}); err != nil {
			t.Errorf("delete test storage bucket: %v", err)
		}
	})

	return backend, err
}
