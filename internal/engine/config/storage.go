package config

import (
	"fmt"
	"net/url"
	"strings"
)

func (cfg Config) ValidateStorage() error {
	if cfg.StorageBackend != "seaweedfs" && cfg.StorageBackend != "s3" && cfg.StorageBackend != "r2" && cfg.StorageBackend != "gcs" {
		return nil
	}

	if strings.TrimSpace(cfg.StorageBucket) == "" {
		return fmt.Errorf("GOERP_STORAGE_BUCKET is required")
	}

	if cfg.StorageBackend == "seaweedfs" || cfg.StorageBackend == "r2" {
		if cfg.StorageS3Endpoint == "" {
			return fmt.Errorf("GOERP_STORAGE_S3_ENDPOINT is required for %s", cfg.StorageBackend)
		}
	}

	if cfg.StorageS3Endpoint != "" {
		endpoint, err := url.Parse(cfg.StorageS3Endpoint)
		if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
			return fmt.Errorf("GOERP_STORAGE_S3_ENDPOINT must be an HTTP or HTTPS URL without credentials, query or fragment")
		}
	}

	if cfg.StorageBackend == "s3" && cfg.StorageS3Region == "" {
		return fmt.Errorf("GOERP_STORAGE_S3_REGION is required for s3")
	}

	if cfg.StorageBackend != "s3" || cfg.StorageS3AccessKey != "" || cfg.StorageS3SecretKey != "" {
		if cfg.StorageS3AccessKey == "" {
			return fmt.Errorf("GOERP_STORAGE_S3_ACCESS_KEY is required")
		}

		if cfg.StorageS3SecretKey == "" {
			return fmt.Errorf("GOERP_STORAGE_S3_SECRET_KEY is required")
		}
	}

	return nil
}
