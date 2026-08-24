package config

import "testing"

func TestParseAndValidate(t *testing.T) {
	c, err := Parse([]string{"-data-dir", "/tmp/payless-test", "-listen", "127.0.0.1:1234"})
	if err != nil {
		t.Fatal(err)
	}
	if c.DataDir != "/tmp/payless-test" || c.ListenAddr != "127.0.0.1:1234" {
		t.Fatalf("unexpected config: %#v", c)
	}
}

func TestValidateRejectsInvalidValues(t *testing.T) {
	tests := []Config{{DataDir: "", ListenAddr: "127.0.0.1:1"}, {DataDir: "/tmp", ListenAddr: "bad"}, {DataDir: "/tmp", ListenAddr: "127.0.0.1:1", ReadHeaderTimeout: -1}, {DataDir: "/tmp", ListenAddr: "127.0.0.1:1", OpenRouterBaseURL: "", SurplusBaseURL: ""}}
	for _, test := range tests {
		if err := test.Validate(); err == nil {
			t.Fatalf("expected error for %#v", test)
		}
	}
}
