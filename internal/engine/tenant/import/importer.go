package tenantimport

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/djangbahevans/goerp/internal/engine/auth/rowcrypt"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

// Importer satisfies adminapi.TenantImporter — the POST
// /admin/tenants/import handler's entry point into this package.
type Importer struct {
	tenantStore *tenant.Store
	jobClient   *river.Client[pgx.Tx]
	// jobQueue mirrors tenantexport.Exporter's own field of the same name.
	jobQueue string
	// keys encrypts decryptionKey before it's persisted in the job's Args —
	// goerp#450, since River persists job args as-is in river_job.args for
	// the life of the job row, and the archive's own decryption key has no
	// business sitting there in plaintext.
	keys *rowcrypt.RowKeySet
}

func NewImporter(tenantStore *tenant.Store, jobClient *river.Client[pgx.Tx], jobQueue string, keys *rowcrypt.RowKeySet) *Importer {
	return &Importer{tenantStore: tenantStore, jobClient: jobClient, jobQueue: jobQueue, keys: keys}
}

// jobIDPrefix/encodeJobID mirror adminapi's own and tenantexport's —
// duplicated rather than exported across a package boundary for this one
// small helper, same call those packages already made.
const jobIDPrefix = "job_"

func encodeJobID(id int64) string {
	return jobIDPrefix + strconv.FormatInt(id, 10)
}

// StartImport rejects a newSlug already in use up front — a fast usage
// error instead of a job that's doomed to fail once Worker.run gets to
// resolveTenant. A slug that's free right now can still race with another
// create between this check and the job actually running; resolveTenant's
// own status check catches that case instead.
func (im *Importer) StartImport(ctx context.Context, newSlug, inputRef, decryptionKey string) (jobID string, err error) {
	_, err = im.tenantStore.GetBySlug(ctx, newSlug)
	switch {
	case err == nil:
		return "", fmt.Errorf("tenant %q already exists", newSlug)
	case !errors.Is(err, tenant.ErrTenantNotFound):
		return "", fmt.Errorf("look up tenant %q: %w", newSlug, err)
	}

	encryptedKey, err := im.keys.Encrypt([]byte(decryptionKey))
	if err != nil {
		return "", fmt.Errorf("encrypt decryption key for storage: %w", err)
	}

	insertResult, err := im.jobClient.Insert(ctx, Args{
		NewSlug:       newSlug,
		InputRef:      inputRef,
		DecryptionKey: string(encryptedKey),
	}, &river.InsertOpts{Queue: im.jobQueue})
	if err != nil {
		return "", fmt.Errorf("enqueue import job: %w", err)
	}
	return encodeJobID(insertResult.Job.ID), nil
}
