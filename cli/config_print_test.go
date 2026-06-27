package cli

import (
	"strings"
	"testing"

	"mailio/internal/config"
)

func TestRenderConfig(t *testing.T) {
	cfg := &config.Config{
		Hostname: "mail.example-a.com",
		Domains: []config.Domain{
			{Name: "example-a.com", Accounts: []config.Account{{LocalPart: "user", Password: "secret"}}},
		},
	}
	var sb strings.Builder
	if err := renderConfig(&sb, cfg); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	for _, want := range []string{
		"email: user@example-a.com",
		"smtp_url: smtp://user%40example-a.com@mail.example-a.com:587/",
		"password: secret",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestRenderConfigNoAccounts(t *testing.T) {
	if err := renderConfig(&strings.Builder{}, &config.Config{}); err == nil {
		t.Error("expected error when config has no accounts")
	}
}
