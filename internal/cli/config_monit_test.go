package cli

import (
	"strings"
	"testing"

	"mailio/internal/config"
)

func TestRenderMonit(t *testing.T) {
	cfg := &config.Config{
		Hostname: "mail.example-a.com",
		Domains: []config.Domain{
			{Name: "example-a.com", Accounts: []config.Account{{LocalPart: "user", Password: `se"cret`}}},
			{Name: "example-b.com", Accounts: []config.Account{
				{LocalPart: "user", Password: "pw2"},
				{LocalPart: "admin", Password: "pw3"},
			}},
		},
	}
	var sb strings.Builder
	if err := renderMonit(&sb, cfg); err != nil {
		t.Fatal(err)
	}
	out := sb.String()

	// First account is active: full-address username, with quotes escaped.
	if !strings.Contains(out, `username "user@example-a.com" password "se\"cret"`) {
		t.Error("missing/incorrect active mailserver line")
	}
	// from: must equal the login (sender/login binding).
	if !strings.Contains(out, "set mail-format { from: Monit <user@example-a.com> }") {
		t.Error("missing active mail-format from")
	}
	// Other accounts are present but commented out.
	if !strings.Contains(out, `#     username "admin@example-b.com" password "pw3"`) {
		t.Error("missing commented admin account")
	}
	// The second account must not appear as an active (uncommented) mailserver.
	if strings.Contains(out, "\nset mailserver localhost port 587\n    username \"user@example-b.com\"") {
		t.Error("second account should not be active")
	}
}

func TestRenderMonitNoAccounts(t *testing.T) {
	if err := renderMonit(&strings.Builder{}, &config.Config{}); err == nil {
		t.Error("expected error when config has no accounts")
	}
}
