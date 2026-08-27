package repositories_test

import "testing"

func TestUserRepositorySQL(t *testing.T) {
	f := newIntegrationFixture(t)
	if err := f.repos.Users.BootstrapAdmin(f.ctx, "admin@example.test", "sha256$hash"); err != nil {
		t.Fatal(err)
	}
	if err := f.repos.Users.BootstrapAdmin(f.ctx, "other@example.test", "sha256$other"); err != nil {
		t.Fatal(err)
	}
	id, hash, err := f.repos.Users.AuthenticateAdmin(f.ctx, "admin@example.test")
	if err != nil || id == 0 || hash != "sha256$hash" {
		t.Fatalf("id=%d hash=%q err=%v", id, hash, err)
	}
	if err := f.repos.Users.CreateSession(f.ctx, id, "session-hash"); err != nil {
		t.Fatal(err)
	}
	ok, err := f.repos.Users.SessionIsAdmin(f.ctx, "session-hash")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	ok, err = f.repos.Users.SessionIsAdmin(f.ctx, "missing")
	if err != nil || ok {
		t.Fatalf("missing ok=%v err=%v", ok, err)
	}
}
