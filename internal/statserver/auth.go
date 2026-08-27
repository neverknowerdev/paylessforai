package statserver

import "net/http"

func (s *Server) authorized(r *http.Request) bool {
	c, err := r.Cookie("stat_admin")
	if err != nil {
		return false
	}
	var ok bool
	_ = s.db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM admin_sessions x JOIN users u ON u.id=x.user_id WHERE x.token_hash=$1 AND x.expires_at>now() AND u.is_admin AND u.disabled_at IS NULL)`, hashString(c.Value)).Scan(&ok)
	return ok
}
func (s *Server) bootstrapAdmin() error {
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM users`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := s.db.Exec(`INSERT INTO users(email,password_hash,is_admin) VALUES($1,$2,true)`, s.cfg.BootstrapEmail, hashPassword(s.cfg.BootstrapPassword))
	return err
}
