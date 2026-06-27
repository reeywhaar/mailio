// Package config defines the mailio configuration schema and loader.
package config

import (
	"bytes"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Path is the location of the config file inside the container. It is a var so
// tests can point it at a temporary file.
var Path = "/etc/mailio/config.yml"

type Config struct {
	Hostname    string       `yaml:"hostname"`
	MyNetworks  []string     `yaml:"mynetworks"`
	Domains     []Domain     `yaml:"domains"`
	LetsEncrypt *LetsEncrypt `yaml:"letsencrypt"`
	Relay       *Relay       `yaml:"relay"`
}

// Relay configures an outbound smarthost: instead of delivering mail directly,
// Postfix forwards all outgoing mail to this server using SMTP AUTH over TLS.
// Useful when the host's port 25 is blocked (most cloud/residential networks).
type Relay struct {
	Host     string `yaml:"host"`     // e.g. smtp.gmail.com
	Port     int    `yaml:"port"`     // e.g. 587 (STARTTLS) or 465 (implicit TLS); defaults to 587
	Username string `yaml:"username"` // SMTP AUTH username, e.g. you@gmail.com
	Password string `yaml:"password"` // SMTP AUTH password, e.g. a Gmail App Password
}

// EffectivePort returns the configured port, defaulting to 587 (submission).
func (r *Relay) EffectivePort() int {
	if r.Port == 0 {
		return 587
	}
	return r.Port
}

type LetsEncrypt struct {
	Email   string `yaml:"email"`
	Staging bool   `yaml:"staging"`
}

type Domain struct {
	Name     string    `yaml:"name"`
	Selector string    `yaml:"selector"`
	SPF      string    `yaml:"spf"` // ~all | -all | ?all; empty = skip
	DMARC    *DMARC    `yaml:"dmarc"`
	Accounts []Account `yaml:"accounts"`
	// Relay overrides the top-level relay for all mail sent from this domain.
	// A per-account relay takes precedence over this. Omit to use the global relay.
	Relay *Relay `yaml:"relay"`
}

type DMARC struct {
	Policy string `yaml:"policy"` // none | quarantine | reject
	RUA    string `yaml:"rua"`    // mailto: address for aggregate reports (optional)
}

type Account struct {
	LocalPart string `yaml:"local_part"`
	Password  string `yaml:"password"`
	LocalUser string `yaml:"local_user"`
	// Relay overrides the top-level relay for mail sent from this account
	// (envelope sender <local_part>@<domain>). Omit to use the global relay.
	Relay *Relay `yaml:"relay"`
}

// Load reads and parses the config file from Path.
func Load() (*Config, error) {
	data, err := os.ReadFile(Path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	return &cfg, yaml.Unmarshal(data, &cfg)
}

// AddAccount appends an account to an existing domain in the config file at
// Path and saves it. The file is edited through a yaml.Node round-trip so
// existing comments are preserved (only whitespace may be normalized). It
// errors if the domain is absent or the account already exists. localUser is
// written only when non-empty.
func AddAccount(domain, localPart, password, localUser string) error {
	doc, top, err := loadDoc()
	if err != nil {
		return err
	}
	domNode, err := findDomain(top, domain)
	if err != nil {
		return err
	}

	accounts := mapValue(domNode, "accounts")
	if accounts == nil {
		accounts = &yaml.Node{Kind: yaml.SequenceNode}
		domNode.Content = append(domNode.Content, scalarNode("accounts"), accounts)
	}
	if accounts.Kind != yaml.SequenceNode {
		return fmt.Errorf("accounts for %q is not a list", domain)
	}
	for _, an := range accounts.Content {
		if lp := mapValue(an, "local_part"); lp != nil && lp.Value == localPart {
			return fmt.Errorf("account %s@%s already exists", localPart, domain)
		}
	}

	acct := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		scalarNode("local_part"), scalarNode(localPart),
		scalarNode("password"), quotedScalarNode(password),
	}}
	if localUser != "" {
		acct.Content = append(acct.Content, scalarNode("local_user"), scalarNode(localUser))
	}
	accounts.Content = append(accounts.Content, acct)

	return saveDoc(doc)
}

// DeleteAccount removes an account from a domain in the config file at Path and
// saves it (preserving comments). It errors if the domain or account is absent.
//
// Note: this only edits the config. The account's SASL credential persists in
// the sasl_db volume until it is removed there, so the login is not fully
// revoked by a config edit + restart alone.
func DeleteAccount(domain, localPart string) error {
	doc, top, err := loadDoc()
	if err != nil {
		return err
	}
	domNode, err := findDomain(top, domain)
	if err != nil {
		return err
	}
	accounts := mapValue(domNode, "accounts")
	if accounts != nil && accounts.Kind == yaml.SequenceNode {
		for i, an := range accounts.Content {
			if lp := mapValue(an, "local_part"); lp != nil && lp.Value == localPart {
				accounts.Content = append(accounts.Content[:i], accounts.Content[i+1:]...)
				return saveDoc(doc)
			}
		}
	}
	return fmt.Errorf("account %s@%s not found", localPart, domain)
}

// loadDoc reads and parses the config file at Path into a yaml.Node, returning
// the document node and the top-level mapping node.
func loadDoc() (*yaml.Node, *yaml.Node, error) {
	data, err := os.ReadFile(Path)
	if err != nil {
		return nil, nil, err
	}
	doc := &yaml.Node{}
	if err := yaml.Unmarshal(data, doc); err != nil {
		return nil, nil, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("unexpected config structure")
	}
	return doc, doc.Content[0], nil
}

// saveDoc marshals a yaml.Node document back to the config file at Path with
// 2-space indentation.
func saveDoc(doc *yaml.Node) error {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return os.WriteFile(Path, buf.Bytes(), 0644)
}

// findDomain returns the mapping node for the named domain under top.domains.
func findDomain(top *yaml.Node, domain string) (*yaml.Node, error) {
	domains := mapValue(top, "domains")
	if domains == nil || domains.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("no domains defined in config")
	}
	for _, dn := range domains.Content {
		if name := mapValue(dn, "name"); name != nil && name.Value == domain {
			return dn, nil
		}
	}
	return nil, fmt.Errorf("domain %q not found in config", domain)
}

func mapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func scalarNode(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Value: v}
}

func quotedScalarNode(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Value: v, Style: yaml.DoubleQuotedStyle}
}
