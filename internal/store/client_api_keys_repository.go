package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"github.com/neverknowerdev/paylessforai/internal/ids"
	"strings"
	"time"
)

type ClientAPIKeysRepository struct{ db DBTX }

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
	id := ids.New()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	prefix := secret[:13]
	_, err := r.db.ExecContext(ctx, `INSERT INTO client_api_keys(id,label,key_hash,key_prefix,created_at) VALUES(?,?,?,?,?)`, id, label, hex.EncodeToString(h[:]), prefix, now)
	return ClientKey{ID: id, Label: label, Prefix: prefix, CreatedAt: now}, secret, err
}

func (r *ClientAPIKeysRepository) Authenticate(ctx context.Context, secret string) (ClientKey, bool, error) {
	h := sha256.Sum256([]byte(secret))
	var key ClientKey
	var revoked sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT id,label,key_prefix,created_at,last_used_at,revoked_at FROM client_api_keys WHERE key_hash=?`, hex.EncodeToString(h[:])).Scan(&key.ID, &key.Label, &key.Prefix, &key.CreatedAt, &key.LastUsedAt, &revoked)
	if err == sql.ErrNoRows {
		return ClientKey{}, false, nil
	}
	if err != nil {
		return ClientKey{}, false, err
	}
	if revoked.Valid {
		return key, false, nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := r.db.ExecContext(ctx, `UPDATE client_api_keys SET last_used_at=? WHERE id=?`, now, key.ID); err != nil {
		return ClientKey{}, false, err
	}
	key.LastUsedAt = &now
	return key, true, nil
}

func (r *ClientAPIKeysRepository) List(ctx context.Context) ([]ClientKey, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,label,key_prefix,created_at,last_used_at,revoked_at FROM client_api_keys ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ClientKey{}
	for rows.Next() {
		var k ClientKey
		if err := rows.Scan(&k.ID, &k.Label, &k.Prefix, &k.CreatedAt, &k.LastUsedAt, &k.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
func (r *ClientAPIKeysRepository) Revoke(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE client_api_keys SET revoked_at=? WHERE id=? AND revoked_at IS NULL`, time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}
