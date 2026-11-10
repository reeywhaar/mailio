// Package dnscheck looks up what DNS serves for each record a domain should have, and says how it
// differs from what mailio wants. It asks the domain's own nameservers, so a cached old answer
// neither hides a fix nor a breakage.
package dnscheck

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"mailio/internal/dnssync"
	"mailio/internal/store"
)

// Status is how a record compares with what DNS serves.
type Status string

const (
	OK Status = "ok"
	// Missing is no record of the kind at the name.
	Missing Status = "missing"
	// Different is one record of the kind, not the one wanted.
	Different Status = "different"
	// Several is more than one SPF or DMARC record at the name, which receivers read as none.
	Several Status = "several"
	// Failed is a lookup that got no answer.
	Failed Status = "failed"
)

// Result is one record's status, what DNS served when it is not OK, and why a Failed lookup
// failed.
type Result struct {
	Status Status
	Found  []string
	Error  string
}

// Lookup answers queries. A name with no such records is nil and no error.
type Lookup interface {
	TXT(ctx context.Context, name string) ([]string, error)
	MX(ctx context.Context, name string) ([]string, error)
}

// Check compares one wanted record with what DNS serves.
func Check(ctx context.Context, look Lookup, want dnssync.Record) Result {
	if want.Type == "MX" {
		hosts, err := look.MX(ctx, want.Name)
		if err != nil {
			return Result{Status: Failed, Error: err.Error()}
		}
		return compareMX(want.Data, hosts)
	}
	txt, err := look.TXT(ctx, want.Name)
	if err != nil {
		return Result{Status: Failed, Error: err.Error()}
	}
	return compareTXT(want.Data, txt)
}

func compareMX(host string, found []string) Result {
	if len(found) == 0 {
		return Result{Status: Missing}
	}
	for i, h := range found {
		found[i] = strings.ToLower(strings.TrimSuffix(h, "."))
	}
	for _, h := range found {
		// Every MX is tried by senders, so one pointing elsewhere is mail going elsewhere.
		if h != strings.ToLower(host) {
			return Result{Status: Different, Found: found}
		}
	}
	return Result{Status: OK}
}

func compareTXT(want string, txt []string) Result {
	k := kind(want)
	var same []string
	for _, t := range txt {
		// A DKIM key needs no v=DKIM1, and its name holds nothing else.
		if k == "dkim" || kind(t) == k {
			same = append(same, t)
		}
	}
	switch {
	case len(same) == 0:
		return Result{Status: Missing}
	case len(same) > 1:
		return Result{Status: Several, Found: same}
	case equal(k, want, same[0]):
		return Result{Status: OK}
	}
	return Result{Status: Different, Found: same}
}

func kind(txt string) string {
	t := strings.ToLower(strings.TrimSpace(txt))
	switch {
	case strings.HasPrefix(t, "v=spf1"):
		return "spf"
	case strings.HasPrefix(t, "v=dmarc1"):
		return "dmarc"
	case strings.HasPrefix(t, "v=dkim1"):
		return "dkim"
	}
	return ""
}

// equal compares as receivers read them, not as typed: SPF by its terms, DMARC and DKIM by their
// tags, whatever the spacing and order.
func equal(kind, a, b string) bool {
	if kind == "spf" {
		return strings.Join(strings.Fields(strings.ToLower(a)), " ") == strings.Join(strings.Fields(strings.ToLower(b)), " ")
	}
	return strings.Join(tags(kind, a), ";") == strings.Join(tags(kind, b), ";")
}

func tags(kind, record string) []string {
	var out []string
	for _, part := range strings.Split(record, ";") {
		name, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		name = strings.ToLower(strings.TrimSpace(name))
		value = strings.Join(strings.Fields(value), "")
		// A key is base64, where case is the key; everything else here is a keyword.
		if !(kind == "dkim" && name == "p") {
			value = strings.ToLower(value)
		}
		// DKIM's version and RSA are what a key record says when it says nothing.
		if kind == "dkim" && (name == "v" && value == "dkim1" || name == "k" && value == "rsa") {
			continue
		}
		out = append(out, name+"="+value)
	}
	sort.Strings(out)
	return out
}

// Authoritative asks the nameservers a name is delegated to.
type Authoritative struct{}

func (Authoritative) TXT(ctx context.Context, name string) ([]string, error) {
	return ask(ctx, name, func(r *net.Resolver) ([]string, error) { return r.LookupTXT(ctx, name+".") })
}

func (Authoritative) MX(ctx context.Context, name string) ([]string, error) {
	return ask(ctx, name, func(r *net.Resolver) ([]string, error) {
		mx, err := r.LookupMX(ctx, name+".")
		hosts := make([]string, len(mx))
		for i, m := range mx {
			hosts[i] = m.Host
		}
		return hosts, err
	})
}

func ask(ctx context.Context, name string, q func(*net.Resolver) ([]string, error)) ([]string, error) {
	servers, err := nameservers(ctx, name)
	if err != nil {
		return nil, err
	}
	var failed []string
	for _, ns := range servers {
		out, err := q(at(ns))
		if notFound(err) {
			return nil, nil
		}
		if err == nil {
			return out, nil
		}
		failed = append(failed, strings.TrimSuffix(ns, ".")+": "+reason(err))
	}
	return nil, errors.New("None of its nameservers answered. " + strings.Join(failed, "; "))
}

// reason is what went wrong with a lookup, without the resolver's address in front of it.
func reason(err error) string {
	var dnsErr *net.DNSError
	switch {
	case errors.As(err, &dnsErr) && dnsErr.IsTimeout:
		return "no answer in time"
	case errors.As(err, &dnsErr):
		return dnsErr.Err
	}
	return err.Error()
}

// nameservers walks up from the name to the zone it is in, the first with NS records.
func nameservers(ctx context.Context, name string) ([]string, error) {
	for n := name; strings.Contains(n, "."); n = n[strings.Index(n, ".")+1:] {
		ns, err := net.DefaultResolver.LookupNS(ctx, n+".")
		if err != nil && !notFound(err) {
			return nil, fmt.Errorf("Looking up the nameservers of %s failed: %s.", n, reason(err))
		}
		if len(ns) > 0 {
			hosts := make([]string, len(ns))
			for i, s := range ns {
				hosts[i] = s.Host
			}
			return hosts, nil
		}
	}
	return nil, fmt.Errorf("No nameservers found for %s or any domain above it: it is not in public DNS.", name)
}

func at(ns string) *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, net.JoinHostPort(ns, "53"))
		},
	}
}

func notFound(err error) bool {
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr) && dnsErr.IsNotFound
}

// Wanted is every record a domain should have, with MX, which is set outside mailio but is checked
// like the rest: mail goes nowhere without it.
func Wanted(d *store.Domain, set *store.Settings, mailHost string) []dnssync.Record {
	return append([]dnssync.Record{{Type: "MX", Name: d.Name, Data: mailHost}}, dnssync.Wanted(d, set, mailHost)...)
}

// Checker keeps the last check of every domain, in memory: a restart checks again.
type Checker struct {
	st   *store.Store
	look Lookup
	host string

	mu   sync.Mutex
	last map[string]checked
}

type checked struct {
	at      time.Time
	results map[dnssync.Record]Result
}

func NewChecker(st *store.Store, look Lookup, mailHost string) *Checker {
	return &Checker{st: st, look: look, host: mailHost, last: map[string]checked{}}
}

// CheckAll checks every domain.
func (c *Checker) CheckAll(ctx context.Context) error {
	domains, err := c.st.Domains(ctx)
	if err != nil {
		return err
	}
	set, err := c.st.Settings(ctx)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for i := range domains {
		seen[domains[i].ID] = true
		c.check(ctx, &domains[i], set)
	}
	c.mu.Lock()
	for id := range c.last {
		if !seen[id] {
			delete(c.last, id)
		}
	}
	c.mu.Unlock()
	return nil
}

// CheckDomain checks one domain now.
func (c *Checker) CheckDomain(ctx context.Context, id string) error {
	d, err := c.st.DomainByID(ctx, id)
	if err != nil {
		return err
	}
	set, err := c.st.Settings(ctx)
	if err != nil {
		return err
	}
	c.check(ctx, d, set)
	return nil
}

func (c *Checker) check(ctx context.Context, d *store.Domain, set *store.Settings) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	results := map[dnssync.Record]Result{}
	for _, want := range Wanted(d, set, c.host) {
		results[want] = Check(ctx, c.look, want)
	}
	c.mu.Lock()
	c.last[d.ID] = checked{at: time.Now(), results: results}
	c.mu.Unlock()
}

// Last is the last check of a record, keyed by the record itself, so one changed since is not
// reported as the old one was found; ok is false for no check yet.
func (c *Checker) Last(domainID string, want dnssync.Record) (Result, time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	last, ok := c.last[domainID]
	if !ok {
		return Result{}, time.Time{}, false
	}
	r, ok := last.results[want]
	return r, last.at, ok
}
