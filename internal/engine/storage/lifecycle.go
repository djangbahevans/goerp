package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/rs/zerolog/log"
)

func (b *S3CompatBackend) applyLifecycle(ctx context.Context) error {
	if b.backend != "s3" {
		log.Warn().Str("backend", b.backend).Msg("attachment lifecycle transitions to STANDARD_IA at 90 days and GLACIER_IR at 365 days require AWS S3; applying expiry rules only")
	}

	if b.backend == "gcs" {
		return b.applyGCSLifecycle(ctx)
	}

	var rules []types.LifecycleRule
	if b.backend == "s3" {
		rules = append(rules, types.LifecycleRule{
			ID:     new("move-old-attachments-to-ia"),
			Status: types.ExpirationStatusEnabled,
			Filter: &types.LifecycleRuleFilter{Prefix: new("attachments/")},
			Transitions: []types.Transition{
				{Days: new(int32(90)), StorageClass: types.TransitionStorageClassStandardIa},
				{Days: new(int32(365)), StorageClass: types.TransitionStorageClassGlacierIr},
			},
		})
	}

	for _, purpose := range []string{"imports", "exports", "reports"} {
		days := int32(7)
		if purpose == "imports" {
			days = 30
		}

		rules = append(rules, types.LifecycleRule{
			ID:         new("delete-temp-" + purpose),
			Status:     types.ExpirationStatusEnabled,
			Filter:     &types.LifecycleRuleFilter{Prefix: new(purpose + "/")},
			Expiration: &types.LifecycleExpiration{Days: new(days)},
		})
	}

	_, err := b.client.PutBucketLifecycleConfiguration(ctx, &s3.PutBucketLifecycleConfigurationInput{
		Bucket:                 new(b.bucket),
		LifecycleConfiguration: &types.BucketLifecycleConfiguration{Rules: rules},
	})
	if err != nil {
		return fmt.Errorf("apply %s storage lifecycle to bucket %q: %w", b.backend, b.bucket, err)
	}

	return nil
}

type gcsLifecycleRule struct {
	Delete struct{} `xml:"Action>Delete"`
	Age    int      `xml:"Condition>Age"`
	Prefix string   `xml:"Condition>MatchesPrefix"`
}

func (b *S3CompatBackend) applyGCSLifecycle(ctx context.Context) error {
	// GCS accepts S3 authentication but requires its own lifecycle XML schema.
	policy := struct {
		XMLName xml.Name           `xml:"LifecycleConfiguration"`
		Rules   []gcsLifecycleRule `xml:"Rule"`
	}{
		Rules: []gcsLifecycleRule{
			{Age: 30, Prefix: "imports/"},
			{Age: 7, Prefix: "exports/"},
			{Age: 7, Prefix: "reports/"},
		},
	}

	body, err := xml.Marshal(policy)
	if err != nil {
		return fmt.Errorf("encode GCS storage lifecycle: %w", err)
	}

	opts := b.client.Options()
	u, err := url.Parse(aws.ToString(opts.BaseEndpoint))
	if err != nil {
		return fmt.Errorf("parse GCS storage endpoint: %w", err)
	}
	u.Host = b.bucket + "." + u.Host
	u.Path = "/"
	u.RawQuery = "lifecycle="

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create GCS storage lifecycle request: %w", err)
	}
	req.Header.Set("Content-Type", "application/xml")

	credentials, err := opts.Credentials.Retrieve(ctx)
	if err != nil {
		return fmt.Errorf("resolve GCS lifecycle credentials: %w", err)
	}
	hash := sha256.Sum256(body)
	payloadHash := hex.EncodeToString(hash[:])
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	if err := v4.NewSigner().SignHTTP(ctx, credentials, req, payloadHash, "s3", opts.Region, time.Now()); err != nil {
		return fmt.Errorf("sign GCS storage lifecycle request: %w", err)
	}

	resp, err := opts.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("apply GCS storage lifecycle: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("apply GCS storage lifecycle to bucket %q: HTTP %d", b.bucket, resp.StatusCode)
	}

	return nil
}
