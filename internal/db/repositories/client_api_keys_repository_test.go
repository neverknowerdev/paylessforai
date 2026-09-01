package repositories_test

import "testing"

func TestClientAPIKeysRepositoryIntegration(t *testing.T) {
	i := newIntegrationDB(t)
	key, secret, err := i.repos.ClientAPIKeys.Create(i.ctx, "integration")
	if err != nil {
		t.Fatal(err)
	}
	if authenticated, ok, err := i.repos.ClientAPIKeys.Authenticate(i.ctx, secret); err != nil || !ok || authenticated.ID != key.ID {
		t.Fatalf("authenticate: %+v, %v, %v", authenticated, ok, err)
	}
	keys, err := i.repos.ClientAPIKeys.List(i.ctx)
	if err != nil || len(keys) != 1 {
		t.Fatalf("list: %d, %v", len(keys), err)
	}
	if err := i.repos.ClientAPIKeys.Revoke(i.ctx, key.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := i.repos.ClientAPIKeys.Authenticate(i.ctx, secret); err != nil || ok {
		t.Fatalf("revoked key authenticated: %v, %v", ok, err)
	}
}
