package tenantimport

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/djangbahevans/goerp/internal/engine/tenant/transfer"
	"github.com/riverqueue/river"
)

type accountMapping struct {
	IDs     map[string]string `json:"ids"`
	Matched int               `json:"matched"`
	Created int               `json:"created"`
}

// Account creation and its mapping checkpoint commit together so a retry neither creates
// duplicate accounts nor counts accounts created by this import as matches.
func (w *Worker) mapAccounts(ctx context.Context, job *river.Job[Args], t *tenant.Tenant, data []byte) (accountMapping, error) {
	var accounts []transfer.Account
	if err := json.Unmarshal(data, &accounts); err != nil {
		return accountMapping{}, fmt.Errorf("decode accounts: %w", err)
	}

	tx, err := w.RawDB.BeginTx(ctx, nil)
	if err != nil {
		return accountMapping{}, err
	}
	defer func() { _ = tx.Rollback() }()
	jobID := strconv.FormatInt(job.ID, 10)
	if _, err := tx.ExecContext(ctx, `INSERT INTO system.job_checkpoints (job_id, module, tenant_id) VALUES ($1, 'engine:accounts', $2) ON CONFLICT DO NOTHING`, jobID, t.ID); err != nil {
		return accountMapping{}, err
	}
	var saved string
	if err := tx.QueryRowContext(ctx, `SELECT last_id FROM system.job_checkpoints WHERE job_id = $1 AND module = 'engine:accounts' FOR UPDATE`, jobID).Scan(&saved); err != nil {
		return accountMapping{}, err
	}
	if saved != "" {
		var mapping accountMapping
		if err := json.Unmarshal([]byte(saved), &mapping); err != nil {
			return accountMapping{}, err
		}
		return mapping, nil
	}

	mapping := accountMapping{IDs: make(map[string]string, len(accounts))}
	for _, account := range accounts {
		id, err := uuid.Parse(account.ID)
		if err != nil {
			return accountMapping{}, fmt.Errorf("invalid account ID %q: %w", account.ID, err)
		}
		email := strings.ToLower(strings.TrimSpace(account.Email))
		if email == "" || mapping.IDs[id.String()] != "" {
			return accountMapping{}, fmt.Errorf("duplicate or empty account identity %q", account.Email)
		}

		var targetID string
		err = tx.QueryRowContext(ctx, `INSERT INTO system.users (email, status) VALUES ($1, 'invited') ON CONFLICT (email) WHERE deleted_at IS NULL DO NOTHING RETURNING id`, email).Scan(&targetID)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if err := tx.QueryRowContext(ctx, `SELECT id FROM system.users WHERE email = $1 AND deleted_at IS NULL`, email).Scan(&targetID); err != nil {
				return accountMapping{}, err
			}
			mapping.Matched++
		case err != nil:
			return accountMapping{}, fmt.Errorf("create imported account: %w", err)
		default:
			mapping.Created++
			if _, err := tx.ExecContext(ctx, `INSERT INTO system.user_profiles (user_id, name) VALUES ($1, $2)`, targetID, account.Name); err != nil {
				return accountMapping{}, err
			}
		}
		mapping.IDs[id.String()] = targetID
	}

	encoded, err := json.Marshal(mapping)
	if err != nil {
		return accountMapping{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE system.job_checkpoints SET last_id = $2, status = 'complete', completed_at = NOW() WHERE job_id = $1 AND module = 'engine:accounts'`, jobID, string(encoded)); err != nil {
		return accountMapping{}, err
	}
	if err := tx.Commit(); err != nil {
		return accountMapping{}, err
	}
	return mapping, nil
}

func rewriteIdentities(record map[string]any, cols []transfer.Column, mapping map[string]string, tenantID string) error {
	for _, col := range cols {
		value, ok := record[col.Name]
		if !ok || value == nil {
			continue
		}
		if col.Name == "tenant_id" && tenantID != "" {
			record[col.Name] = tenantID
			continue
		}
		if !col.User {
			continue
		}
		oldID, ok := value.(string)
		if !ok {
			return fmt.Errorf("user column %q is not a UUID string", col.Name)
		}
		id, err := uuid.Parse(oldID)
		if err != nil {
			return err
		}
		targetID, ok := mapping[id.String()]
		if !ok {
			return fmt.Errorf("user column %q references account %s missing from accounts.json", col.Name, oldID)
		}
		record[col.Name] = targetID
	}
	return nil
}
