package storage

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/djangbahevans/goerp/internal/engine/config"
)

var (
	ErrNotPublic         = errors.New("storage object is not public")
	ErrPublicUnsupported = errors.New("r2 does not support per-object public access; use signed URLs")
)

type S3CompatBackend struct {
	client  *s3.Client
	bucket  string
	backend string
}

func newS3CompatBackend(ctx context.Context, cfg config.Config) (*S3CompatBackend, error) {
	region := cmp.Or(cfg.StorageS3Region, "us-east-1")
	endpoint := cfg.StorageS3Endpoint
	pathStyle := cfg.StorageS3UsePathStyle

	switch cfg.StorageBackend {
	case "seaweedfs":
		pathStyle = true
	case "r2":
		region = "auto"
	case "gcs":
		region = "auto"
		endpoint = cmp.Or(endpoint, "https://storage.googleapis.com")
		pathStyle = false
	}

	opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(region)}
	if cfg.StorageS3AccessKey != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.StorageS3AccessKey, cfg.StorageS3SecretKey, "")))
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("load storage credentials: %w", err)
	}

	if _, err := awsCfg.Credentials.Retrieve(ctx); err != nil {
		return nil, fmt.Errorf("resolve storage credentials (GOERP_STORAGE_S3_ACCESS_KEY and GOERP_STORAGE_S3_SECRET_KEY, or AWS credential chain): %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = pathStyle
		if endpoint != "" {
			o.BaseEndpoint = new(endpoint)
		}

		// Optional streaming checksums are not supported by every S3-compatible service.
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	})

	return &S3CompatBackend{
		client:  client,
		bucket:  cfg.StorageBucket,
		backend: cfg.StorageBackend,
	}, nil
}

func (b *S3CompatBackend) Upload(ctx context.Context, key string, r io.Reader, opts UploadOptions) (string, error) {
	if opts.Public && b.backend == "r2" {
		return "", ErrPublicUnsupported
	}

	input := &transfermanager.UploadObjectInput{
		Bucket:      new(b.bucket),
		Key:         new(key),
		Body:        r,
		ContentType: new(cmp.Or(opts.ContentType, "application/octet-stream")),
		Metadata:    map[string]string{"goerp-public": "false"},
	}
	if opts.Public {
		input.ACL = "public-read"
		input.Metadata["goerp-public"] = "true"
	}

	if _, err := transfermanager.New(b.client, func(o *transfermanager.Options) {
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.FailTimeout = 10 * time.Second
	}).UploadObject(ctx, input); err != nil {
		return "", fmt.Errorf("upload storage object %q: %w", key, err)
	}

	return key, nil
}

func (b *S3CompatBackend) Download(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	out, err := b.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: new(b.bucket),
		Key:    new(key),
	})
	if err != nil {
		return nil, 0, fmt.Errorf("download storage object %q: %w", key, err)
	}

	return out.Body, aws.ToInt64(out.ContentLength), nil
}

func (b *S3CompatBackend) Delete(ctx context.Context, key string) error {
	if _, err := b.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: new(b.bucket),
		Key:    new(key),
	}); err != nil {
		return fmt.Errorf("delete storage object %q: %w", key, err)
	}

	return nil
}

func (b *S3CompatBackend) DeleteByPrefix(ctx context.Context, prefix string) error {
	pages := s3.NewListObjectsV2Paginator(b.client, &s3.ListObjectsV2Input{
		Bucket: new(b.bucket),
		Prefix: new(prefix),
	})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("list storage prefix %q: %w", prefix, err)
		}

		objects := make([]types.ObjectIdentifier, 0, len(page.Contents))
		for _, object := range page.Contents {
			objects = append(objects, types.ObjectIdentifier{Key: object.Key})
		}

		if len(objects) == 0 {
			continue
		}

		out, err := b.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: new(b.bucket),
			Delete: &types.Delete{
				Objects: objects,
				Quiet:   new(true),
			},
		})
		if err != nil {
			return fmt.Errorf("delete storage prefix %q: %w", prefix, err)
		}

		if len(out.Errors) > 0 {
			failure := out.Errors[0]

			return fmt.Errorf("delete storage prefix %q: %d objects failed; %q: %s: %s", prefix, len(out.Errors), aws.ToString(failure.Key), aws.ToString(failure.Code), aws.ToString(failure.Message))
		}
	}

	return nil
}

func (b *S3CompatBackend) SignedURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	if expiry < time.Second || expiry > 7*24*time.Hour {
		return "", fmt.Errorf("storage signed URL expiry must be between 1s and 168h")
	}

	out, err := s3.NewPresignClient(b.client).PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: new(b.bucket),
		Key:    new(key),
	}, func(o *s3.PresignOptions) { o.Expires = expiry })
	if err != nil {
		return "", fmt.Errorf("sign storage object URL %q: %w", key, err)
	}

	return out.URL, nil
}

func (b *S3CompatBackend) PublicURL(ctx context.Context, key string) (string, error) {
	if b.backend == "r2" {
		return "", ErrPublicUnsupported
	}
	head, err := b.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: new(b.bucket),
		Key:    new(key),
	})
	if err != nil {
		return "", fmt.Errorf("check public storage object %q: %w", key, err)
	}

	if head.Metadata["goerp-public"] != "true" {
		return "", ErrNotPublic
	}

	signed, err := b.SignedURL(ctx, key, time.Minute)
	if err != nil {
		return "", err
	}

	objectURL, err := url.Parse(signed)
	if err != nil {
		return "", fmt.Errorf("parse storage object URL: %w", err)
	}

	objectURL.RawQuery = ""

	return objectURL.String(), nil
}

func (b *S3CompatBackend) Exists(ctx context.Context, key string) (bool, error) {
	_, err := b.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: new(b.bucket),
		Key:    new(key),
	})
	if err == nil {
		return true, nil
	}
	if response, ok := errors.AsType[*smithyhttp.ResponseError](err); ok && response.HTTPStatusCode() == http.StatusNotFound {
		return false, nil
	}

	return false, fmt.Errorf("check storage object %q: %w", key, err)
}
