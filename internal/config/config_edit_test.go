package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleConfig = `# mailio config
hostname: mail.example-a.com  # the FQDN
mynetworks:
  - 127.0.0.0/8

# the domains we serve
domains:
  - name: example-a.com
    selector: mail
    accounts:
      - local_part: user
        password: "changeme"
        local_user: root
  - name: example-b.com
    selector: mail
    accounts:
      - local_part: user
        password: "pw"
  - name: example-c.com   # no accounts yet
    selector: mail
`

// withConfig writes sampleConfig to a temp file, points Path at it, and returns
// a restore func.
func withConfig(t *testing.T) func() {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(p, []byte(sampleConfig), 0644); err != nil {
		t.Fatal(err)
	}
	orig := Path
	Path = p
	return func() { Path = orig }
}

func TestAddAccount(t *testing.T) {
	defer withConfig(t)()

	if err := AddAccount("example-b.com", "user2", "s3cret", "vmail"); err != nil {
		t.Fatalf("AddAccount: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	var found *Account
	for i := range cfg.Domains {
		if cfg.Domains[i].Name != "example-b.com" {
			continue
		}
		for j := range cfg.Domains[i].Accounts {
			if cfg.Domains[i].Accounts[j].LocalPart == "user2" {
				found = &cfg.Domains[i].Accounts[j]
			}
		}
	}
	if found == nil {
		t.Fatal("user2 not added to example-b.com")
	}
	if found.Password != "s3cret" || found.LocalUser != "vmail" {
		t.Errorf("unexpected account: %+v", *found)
	}

	// Comments must survive the round-trip.
	raw, _ := os.ReadFile(Path)
	for _, want := range []string{"# mailio config", "# the FQDN", "# the domains we serve"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("comment %q lost after edit", want)
		}
	}
}

func TestAddAccountCreatesAccountsList(t *testing.T) {
	defer withConfig(t)()

	if err := AddAccount("example-c.com", "first", "pw", ""); err != nil {
		t.Fatalf("AddAccount: %v", err)
	}
	cfg, _ := Load()
	for _, d := range cfg.Domains {
		if d.Name == "example-c.com" {
			if len(d.Accounts) != 1 || d.Accounts[0].LocalPart != "first" {
				t.Errorf("accounts not created correctly: %+v", d.Accounts)
			}
			return
		}
	}
	t.Fatal("example-c.com missing")
}

func TestAddAccountErrors(t *testing.T) {
	defer withConfig(t)()

	if err := AddAccount("nope.com", "x", "pw", ""); err == nil {
		t.Error("expected error for unknown domain")
	}
	if err := AddAccount("example-a.com", "user", "pw", ""); err == nil {
		t.Error("expected error for duplicate account")
	}
}

func TestDeleteAccount(t *testing.T) {
	defer withConfig(t)()

	if err := DeleteAccount("example-a.com", "user"); err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}
	cfg, _ := Load()
	for _, d := range cfg.Domains {
		if d.Name == "example-a.com" && len(d.Accounts) != 0 {
			t.Errorf("account not removed: %+v", d.Accounts)
		}
	}
	raw, _ := os.ReadFile(Path)
	if !strings.Contains(string(raw), "# the domains we serve") {
		t.Error("comment lost after delete")
	}
}

func TestDeleteAccountErrors(t *testing.T) {
	defer withConfig(t)()

	if err := DeleteAccount("nope.com", "user"); err == nil {
		t.Error("expected error for unknown domain")
	}
	if err := DeleteAccount("example-a.com", "ghost"); err == nil {
		t.Error("expected error for unknown account")
	}
}
