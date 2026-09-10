package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/stephenafamo/bob"
)

const (
	correlationRetention  = 30 * 24 * time.Hour
	cleanupInterval       = 5 * time.Minute
	maxRecentFingerprints = 8
)

// SessionRepository owns the short transactions used by session detection.
// SQL is kept here so the session package remains independent from the
// database implementation and contains only detection policy.
type SessionRepository struct {
	database    *sql.DB
	cleanup     sync.Mutex
	lastCleanup time.Time
}

func (r *SessionRepository) UpsertSession(ctx context.Context, clientKeyID, sessionID string, now time.Time) error {
	if r == nil || r.database == nil || clientKeyID == "" || sessionID == "" {
		return fmt.Errorf("session store unavailable")
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	_, err := r.database.ExecContext(ctx, `
		INSERT INTO sessions(client_key_id, session_id, created_at, last_seen_at)
		VALUES(?, ?, ?, ?)
		ON CONFLICT(client_key_id, session_id) DO UPDATE SET last_seen_at = excluded.last_seen_at`, clientKeyID, sessionID, stamp, stamp)
	return err
}

// ResolveAndRegister performs lookup, selection, registration and bounded
// retention atomically. hashes must be ordered longest prefix first.
func (r *SessionRepository) ResolveAndRegister(ctx context.Context, clientKeyID string, hashes []string, anchor, fingerprint, fresh string, now time.Time) (string, error) {
	if r == nil || r.database == nil || clientKeyID == "" || len(hashes) == 0 || fresh == "" {
		return "", fmt.Errorf("session store unavailable")
	}
	cleanup := r.cleanupDue(now)
	db := bob.NewDB(r.database)
	selected := ""
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bob.Transaction) error {
		if cleanup {
			cutoff := now.UTC().Add(-correlationRetention).Format(time.RFC3339Nano)
			if _, err := tx.ExecContext(ctx, `DELETE FROM session_keys WHERE EXISTS (
				SELECT 1 FROM sessions
				WHERE sessions.client_key_id = session_keys.client_key_id
				  AND sessions.session_id = session_keys.session_id
				  AND sessions.last_seen_at < ?
			)`, cutoff); err != nil {
				return err
			}
		}
		existing, err := r.lookup(ctx, tx, clientKeyID, hashes, now)
		if err != nil {
			return err
		}
		for _, hash := range hashes {
			matches := existing[hash]
			if len(matches) == 1 {
				selected = matches[0]
				break
			}
			if len(matches) > 1 {
				selected = fresh
				break
			}
		}
		if selected == "" {
			selected = fresh
		}
		stamp := now.UTC().Format(time.RFC3339Nano)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO sessions(client_key_id, session_id, created_at, last_seen_at)
			VALUES(?, ?, ?, ?)
			ON CONFLICT(client_key_id, session_id) DO UPDATE SET last_seen_at = excluded.last_seen_at`, clientKeyID, selected, stamp, stamp); err != nil {
			return err
		}
		register := []struct {
			hash   string
			anchor bool
		}{{hash: anchor, anchor: true}, {hash: fingerprint}}
		seen := map[string]bool{}
		for _, item := range register {
			if item.hash == "" || seen[item.hash] {
				continue
			}
			seen[item.hash] = true
			if selected == fresh && len(existing[item.hash]) > 0 {
				// An ambiguous/new session may only add evidence that does not
				// already belong to another retained session.
				continue
			}
			anchorValue := 0
			if item.anchor {
				anchorValue = 1
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO session_keys(client_key_id, session_id, key_hash, is_anchor, last_seen_at)
				VALUES(?, ?, ?, ?, ?)
				ON CONFLICT(client_key_id, session_id, key_hash) DO UPDATE SET
					is_anchor = CASE WHEN session_keys.is_anchor = 1 OR excluded.is_anchor = 1 THEN 1 ELSE 0 END,
					last_seen_at = excluded.last_seen_at`, clientKeyID, selected, item.hash, anchorValue, stamp); err != nil {
				return err
			}
		}
		return r.prune(ctx, tx, clientKeyID, selected)
	})
	if err != nil {
		return "", err
	}
	return selected, nil
}

func (r *SessionRepository) lookup(ctx context.Context, tx bob.Executor, clientKeyID string, hashes []string, now time.Time) (map[string][]string, error) {
	placeholders := strings.TrimRight(strings.Repeat("?,", len(hashes)), ",")
	args := make([]any, 0, len(hashes)+2)
	args = append(args, clientKeyID)
	for _, hash := range hashes {
		args = append(args, hash)
	}
	args = append(args, now.UTC().Add(-correlationRetention).Format(time.RFC3339Nano))
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`
		SELECT sk.key_hash, sk.session_id
		FROM session_keys sk
		JOIN sessions s ON s.client_key_id = sk.client_key_id AND s.session_id = sk.session_id
		WHERE sk.client_key_id = ? AND sk.key_hash IN (%s) AND s.last_seen_at >= ?`, placeholders), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string][]string, len(hashes))
	for rows.Next() {
		var hash, sessionID string
		if err := rows.Scan(&hash, &sessionID); err != nil {
			return nil, err
		}
		matches := result[hash]
		found := false
		for _, value := range matches {
			if value == sessionID {
				found = true
				break
			}
		}
		if !found && len(matches) < 2 {
			result[hash] = append(matches, sessionID)
		}
	}
	return result, rows.Err()
}

func (r *SessionRepository) prune(ctx context.Context, tx bob.Executor, clientKeyID, sessionID string) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT key_hash, is_anchor FROM session_keys
		WHERE client_key_id = ? AND session_id = ?
		ORDER BY is_anchor DESC, last_seen_at DESC, key_hash ASC`, clientKeyID, sessionID)
	if err != nil {
		return err
	}
	defer rows.Close()
	type key struct {
		hash   string
		anchor int
	}
	keys := make([]key, 0, maxRecentFingerprints+1)
	for rows.Next() {
		var item key
		if err := rows.Scan(&item.hash, &item.anchor); err != nil {
			return err
		}
		keys = append(keys, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	keep := make(map[string]bool, maxRecentFingerprints+1)
	nonAnchor := 0
	for _, item := range keys {
		if item.anchor == 1 || nonAnchor < maxRecentFingerprints {
			keep[item.hash] = true
			if item.anchor == 0 {
				nonAnchor++
			}
		}
	}
	for _, item := range keys {
		if keep[item.hash] {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM session_keys WHERE client_key_id = ? AND session_id = ? AND key_hash = ?`, clientKeyID, sessionID, item.hash); err != nil {
			return err
		}
	}
	return nil
}

func (r *SessionRepository) cleanupDue(now time.Time) bool {
	r.cleanup.Lock()
	defer r.cleanup.Unlock()
	if !r.lastCleanup.IsZero() && now.Sub(r.lastCleanup) < cleanupInterval {
		return false
	}
	r.lastCleanup = now
	return true
}
