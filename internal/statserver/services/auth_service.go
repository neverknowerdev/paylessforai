package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"

	"github.com/neverknowerdev/paylessforai/internal/statserver/repositories"
)

type AuthService struct{ repositories *repositories.Repositories }

func NewAuth(repos *repositories.Repositories) *AuthService { return &AuthService{repositories: repos} }

func (s *AuthService) Bootstrap(ctx context.Context, email, password string) error {
	if email == "" || password == "" {
		return nil
	}
	return s.repositories.Users.BootstrapAdmin(ctx, email, hashPassword(password))
}

func (s *AuthService) SignIn(ctx context.Context, email, password string) (string, bool, error) {
	userID, storedHash, err := s.repositories.Users.AuthenticateAdmin(ctx, email)
	if err != nil {
		return "", false, nil
	}
	if !constantEqual(storedHash, hashPassword(password)) {
		return "", false, nil
	}
	token := randomToken()
	if err := s.repositories.Users.CreateSession(ctx, userID, hashString(token)); err != nil {
		return "", false, err
	}
	return token, true, nil
}

func (s *AuthService) IsAdmin(ctx context.Context, token string) bool {
	ok, err := s.repositories.Users.SessionIsAdmin(ctx, hashString(token))
	return err == nil && ok
}

func randomToken() string {
	bytes := make([]byte, 32)
	_, _ = rand.Read(bytes)
	return hex.EncodeToString(bytes)
}
func hashString(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}
func hashPassword(value string) string { return "sha256$" + hashString(value) }
func constantEqual(left, right string) bool {
	return len(left) == len(right) && subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}
