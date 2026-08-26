package repositories_test

import "testing"

func TestSettingsRepositoryIntegration(t *testing.T) {
	i := newIntegrationDB(t)
	i.reset(t)
	if value, ok, err := i.repos.Settings.Get(i.ctx, "theme"); err != nil || ok || value != "" {
		t.Fatalf("missing setting: %q, %v, %v", value, ok, err)
	}
	if err := i.repos.Settings.Set(i.ctx, "theme", "dark"); err != nil {
		t.Fatal(err)
	}
	if value, ok, err := i.repos.Settings.Get(i.ctx, "theme"); err != nil || !ok || value != "dark" {
		t.Fatalf("stored setting: %q, %v, %v", value, ok, err)
	}
}
