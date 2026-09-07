package statserver

import (
	"testing"
	"time"
)

func TestConfigValidation(t *testing.T) {
	if err := (Config{ListenAddr: "127.0.0.1:1", AdminListenAddr: "127.0.0.1:2", DatabaseURL: "postgres://test", RefreshInterval: time.Hour}).Validate(); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []Config{{}, {DatabaseURL: "postgres://test", ListenAddr: "", AdminListenAddr: "x", RefreshInterval: time.Hour}, {DatabaseURL: "postgres://test", ListenAddr: "x", AdminListenAddr: "x", RefreshInterval: 0}} {
		if err := invalid.Validate(); err == nil {
			t.Fatalf("expected invalid config: %+v", invalid)
		}
	}
}
