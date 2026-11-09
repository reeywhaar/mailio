// Package dnsapi is a client for doapi, or any DNS API with the same /dns endpoint: GET lists
// every zone's records, POST creates one, DELETE removes one by id.
package dnsapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Record is one record in a zone. Name is relative: "@" for the zone itself.
type Record struct {
	ID   int    `json:"id"`
	Type string `json:"type"`
	Name string `json:"name"`
	Data string `json:"data"`
}

// Zone is a domain the API manages.
type Zone struct {
	Name    string   `json:"name"`
	Records []Record `json:"records"`
}

// Client talks to one API.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// New is a client for baseURL, e.g. http://doapi:8433.
func New(baseURL string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// Zones lists every zone with its records.
func (c *Client) Zones(ctx context.Context) ([]Zone, error) {
	var zones []Zone
	if err := c.do(ctx, http.MethodGet, nil, &zones); err != nil {
		return nil, err
	}
	return zones, nil
}

// Create adds a record. ttl 0 is the provider's default.
func (c *Client) Create(ctx context.Context, zone, typ, name, data string, ttl int) error {
	body := map[string]any{"domain": zone, "type": typ, "name": name, "data": data}
	if ttl > 0 {
		body["ttl"] = ttl
	}
	return c.do(ctx, http.MethodPost, body, nil)
}

// Delete removes a record.
func (c *Client) Delete(ctx context.Context, zone string, id int) error {
	return c.do(ctx, http.MethodDelete, map[string]any{"domain": zone, "id": id}, nil)
}

func (c *Client) do(ctx context.Context, method string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+"/dns", r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("dns api: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("dns api %s → %s: %s", method, resp.Status, bytes.TrimSpace(msg))
	}
	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ZoneFor finds the zone a host belongs to, the longest that contains it, and the host's name
// relative to it.
func ZoneFor(zones []Zone, host string) (*Zone, string, bool) {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	var best *Zone
	for i := range zones {
		z := strings.ToLower(zones[i].Name)
		if (host == z || strings.HasSuffix(host, "."+z)) && (best == nil || len(z) > len(best.Name)) {
			best = &zones[i]
		}
	}
	if best == nil {
		return nil, "", false
	}
	rel := strings.TrimSuffix(strings.TrimSuffix(host, strings.ToLower(best.Name)), ".")
	if rel == "" {
		rel = "@"
	}
	return best, rel, true
}
