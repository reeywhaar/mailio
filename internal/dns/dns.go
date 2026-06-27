// Package dns reads and upserts DNS records through a pluggable Provider.
// The default Provider is a REST client (pointed at doapi by default); swapping
// it lets mailio target a different DNS backend without touching the rest of
// the code.
package dns

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"mailio/internal/config"
)

type Domain struct {
	Name    string   `json:"name"`
	Records []Record `json:"records"`
}

type Record struct {
	ID   int    `json:"id"`
	Type string `json:"type"`
	Name string `json:"name"`
	Data string `json:"data"`
}

// Provider is a DNS backend that lists, creates, and deletes records for the
// domains it manages. ttl is the record TTL in seconds; pass 0 to use the
// provider's default.
type Provider interface {
	List() (map[string][]Record, error)
	Create(domain, recordType, name, data string, ttl int) error
	Delete(domain string, id int) error
}

// Default is the Provider used by the package-level helpers. Replace it to
// target a different DNS backend.
var Default Provider = newRESTProvider()

// GetAll fetches every DNS record, keyed by domain name.
func GetAll() (map[string][]Record, error) { return Default.List() }

// CreateRecord adds a DNS record. ttl of 0 means the provider's default.
func CreateRecord(domain, recordType, name, data string, ttl int) error {
	return Default.Create(domain, recordType, name, data, ttl)
}

// DeleteRecord removes the record with the given id from domain.
func DeleteRecord(domain string, id int) error { return Default.Delete(domain, id) }

// restProvider implements Provider against any REST DNS API that exposes a
// /dns endpoint (GET to list, POST to create, DELETE to remove). It is not tied
// to any particular service; doapi is just the default target.
type restProvider struct {
	baseURL string
}

func newRESTProvider() *restProvider {
	// Resolved lazily in request() so commands that don't touch DNS still run
	// without DNS_API_URL set.
	return &restProvider{baseURL: os.Getenv("DNS_API_URL")}
}

func (p *restProvider) List() (map[string][]Record, error) {
	resp, err := p.request("GET", "/dns", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var domains []Domain
	if err := json.NewDecoder(resp.Body).Decode(&domains); err != nil {
		return nil, err
	}
	result := make(map[string][]Record)
	for _, d := range domains {
		result[d.Name] = d.Records
	}
	return result, nil
}

func (p *restProvider) Create(domain, recordType, name, data string, ttl int) error {
	body := map[string]any{"domain": domain, "type": recordType, "name": name, "data": data}
	if ttl > 0 {
		body["ttl"] = ttl
	}
	resp, err := p.request("POST", "/dns", body)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (p *restProvider) Delete(domain string, id int) error {
	resp, err := p.request("DELETE", "/dns", map[string]any{"domain": domain, "id": id})
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// request sends an HTTP request to the DNS API. The caller must close the
// response body.
func (p *restProvider) request(method, path string, body any) (*http.Response, error) {
	if p.baseURL == "" {
		return nil, fmt.Errorf("DNS_API_URL is not set (point it at the DNS API base URL, e.g. http://doapi:8433)")
	}
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, p.baseURL+path, r)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("dns api %s %s → HTTP %d: %s", method, path, resp.StatusCode, body)
	}
	return resp, nil
}

func UpsertDKIM(domain, selector, pubkey string, allRecords map[string][]Record) error {
	recordName := selector + "._domainkey"
	var existing []Record
	for _, r := range allRecords[domain] {
		if r.Type == "TXT" && r.Name == recordName {
			existing = append(existing, r)
		}
	}

	if len(existing) == 1 && existing[0].Data == pubkey {
		fmt.Printf("[dns] TXT %s.%s already correct, skipping\n", recordName, domain)
		return nil
	}

	for _, r := range existing {
		fmt.Printf("[dns] deleting stale TXT %s.%s (id=%d)\n", recordName, domain, r.ID)
		if err := DeleteRecord(domain, r.ID); err != nil {
			return err
		}
	}

	fmt.Printf("[dns] creating TXT %s.%s\n", recordName, domain)
	return CreateRecord(domain, "TXT", recordName, pubkey, 0)
}

func UpsertDMARC(domain string, dmarc *config.DMARC, allRecords map[string][]Record) error {
	if dmarc == nil {
		return nil
	}

	value := "v=DMARC1; p=" + dmarc.Policy
	if dmarc.RUA != "" {
		value += "; rua=mailto:" + dmarc.RUA
	}

	const recordName = "_dmarc"
	var existing []Record
	for _, r := range allRecords[domain] {
		if r.Type == "TXT" && r.Name == recordName {
			existing = append(existing, r)
		}
	}

	if len(existing) == 1 && existing[0].Data == value {
		fmt.Printf("[dns] TXT _dmarc.%s already correct, skipping\n", domain)
		return nil
	}

	for _, r := range existing {
		fmt.Printf("[dns] deleting stale TXT _dmarc.%s (id=%d)\n", domain, r.ID)
		if err := DeleteRecord(domain, r.ID); err != nil {
			return err
		}
	}

	fmt.Printf("[dns] creating TXT _dmarc.%s → %s\n", domain, value)
	return CreateRecord(domain, "TXT", recordName, value, 0)
}

func UpsertSPF(domain, hostname, policy string, allRecords map[string][]Record) error {
	if policy == "" {
		return nil
	}

	value := fmt.Sprintf("v=spf1 a:%s %s", hostname, policy)

	var existing []Record
	for _, r := range allRecords[domain] {
		if r.Type == "TXT" && r.Name == "@" && strings.HasPrefix(r.Data, "v=spf1") {
			existing = append(existing, r)
		}
	}

	if len(existing) == 1 && existing[0].Data == value {
		fmt.Printf("[dns] TXT SPF %s already correct, skipping\n", domain)
		return nil
	}

	for _, r := range existing {
		fmt.Printf("[dns] deleting stale TXT SPF %s (id=%d)\n", domain, r.ID)
		if err := DeleteRecord(domain, r.ID); err != nil {
			return err
		}
	}

	fmt.Printf("[dns] creating TXT SPF %s → %s\n", domain, value)
	return CreateRecord(domain, "TXT", "@", value, 0)
}
