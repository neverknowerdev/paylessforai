package repositories

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strings"
	"time"

	bobmodels "github.com/neverknowerdev/paylessforai/internal/db/bob/models"
	"github.com/neverknowerdev/paylessforai/internal/ids"
	"github.com/stephenafamo/bob/dialect/sqlite"
	"github.com/stephenafamo/bob/dialect/sqlite/sm"
)

type ClientAPIKeysRepository struct{ bobRepository }

func (r *ClientAPIKeysRepository) Create(ctx context.Context, label string) (ClientKey, string, error) {
	if strings.TrimSpace(label) == "" {
		label = "default"
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return ClientKey{}, "", err
	}
	secret := "plai_" + hex.EncodeToString(b)
	h := sha256.Sum256([]byte(secret))
	hash := hex.EncodeToString(h[:])
	id := ids.New()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	prefix := secret[:13]
	_, err := bobmodels.ClientAPIKeys.Insert(&bobmodels.ClientAPIKeySetter{ID: &id, Label: &label, KeyHash: &hash, KeyPrefix: &prefix, CreatedAt: &now}).One(ctx, r.exec)
	return ClientKey{ID: id, Label: label, Prefix: prefix, CreatedAt: now}, secret, err
}

func (r *ClientAPIKeysRepository) Authenticate(ctx context.Context, secret string) (ClientKey, bool, error) {
	h := sha256.Sum256([]byte(secret))
	row, err := bobmodels.ClientAPIKeys.Query(sm.Where(bobmodels.ClientAPIKeys.Columns.KeyHash.EQ(sqlite.Arg(hex.EncodeToString(h[:]))))).One(ctx, r.exec)
	if err == sql.ErrNoRows {
		return ClientKey{}, false, nil
	}
	if err != nil {
		return ClientKey{}, false, err
	}
	key := ClientKey{ID: row.ID, Label: row.Label, Prefix: row.KeyPrefix, CreatedAt: row.CreatedAt, LastUsedAt: stringPointer(row.LastUsedAt), RevokedAt: stringPointer(row.RevokedAt)}
	if row.RevokedAt.Valid {
		return key, false, nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	lastUsed := nullableString(&now)
	if err := row.Update(ctx, r.exec, &bobmodels.ClientAPIKeySetter{LastUsedAt: &lastUsed}); err != nil {
		return ClientKey{}, false, err
	}
	key.LastUsedAt = &now
	return key, true, nil
}

func (r *ClientAPIKeysRepository) List(ctx context.Context) ([]ClientKey, error) {
	rows, err := bobmodels.ClientAPIKeys.Query(sm.OrderBy(bobmodels.ClientAPIKeys.Columns.CreatedAt).Desc()).All(ctx, r.exec)
	if err != nil {
		return nil, err
	}
	out := make([]ClientKey, 0, len(rows))
	for _, row := range rows {
		out = append(out, ClientKey{ID: row.ID, Label: row.Label, Prefix: row.KeyPrefix, CreatedAt: row.CreatedAt, LastUsedAt: stringPointer(row.LastUsedAt), RevokedAt: stringPointer(row.RevokedAt)})
	}
	return out, nil
}

func (r *ClientAPIKeysRepository) Revoke(ctx context.Context, id string) error {
	row, err := bobmodels.FindClientAPIKey(ctx, r.exec, id)
	if err != nil {
		return err
	}
	if row.RevokedAt.Valid {
		return nil
	}
	revoked := nullableString(pointerString(time.Now().UTC().Format(time.RFC3339Nano)))
	return row.Update(ctx, r.exec, &bobmodels.ClientAPIKeySetter{RevokedAt: &revoked})
}

func pointerString(value string) *string { return &value }
