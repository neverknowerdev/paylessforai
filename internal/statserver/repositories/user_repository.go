package repositories

import (
	"context"
	"database/sql"
)

type UserRepository struct{ db *sql.DB }

func (r *UserRepository) BootstrapAdmin(ctx context.Context, email, passwordHash string) error {
	var count int
	if err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO users(email,password_hash,is_admin) VALUES($1,$2,true)`, email, passwordHash)
	return err
}

func (r *UserRepository) AuthenticateAdmin(ctx context.Context, email string) (id int64, passwordHash string, err error) {
	err = r.db.QueryRowContext(ctx, `SELECT id,password_hash FROM users WHERE email=$1 AND disabled_at IS NULL AND is_admin`, email).Scan(&id, &passwordHash)
	return id, passwordHash, err
}

func (r *UserRepository) CreateSession(ctx context.Context, userID int64, tokenHash string) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO admin_sessions(token_hash,user_id,expires_at) VALUES($1,$2,now()+interval '8 hours')`, tokenHash, userID)
	return err
}

func (r *UserRepository) SessionIsAdmin(ctx context.Context, tokenHash string) (bool, error) {
	var ok bool
	err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM admin_sessions session JOIN users user_record ON user_record.id=session.user_id WHERE session.token_hash=$1 AND session.expires_at>now() AND user_record.is_admin AND user_record.disabled_at IS NULL)`, tokenHash).Scan(&ok)
	return ok, err
}
