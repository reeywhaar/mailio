package dnssync

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"

	"mailio/internal/dkimkey"
	"mailio/internal/dnsapi"
	"mailio/internal/store"
)

// doapi is a fake DNS API holding one zone.
type doapi struct {
	mu      sync.Mutex
	zone    dnsapi.Zone
	next    int
	created []string
	deleted []int
}

func (f *doapi) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.Method {
	case http.MethodGet:
		json.NewEncoder(w).Encode([]dnsapi.Zone{f.zone})
	case http.MethodPost:
		var rec struct{ Type, Name, Data string }
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &rec)
		f.next++
		f.zone.Records = append(f.zone.Records, dnsapi.Record{ID: f.next, Type: rec.Type, Name: rec.Name, Data: rec.Data})
		f.created = append(f.created, rec.Type+" "+rec.Name)
	case http.MethodDelete:
		var del struct{ ID int }
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &del)
		f.deleted = append(f.deleted, del.ID)
		for i, rec := range f.zone.Records {
			if rec.ID == del.ID {
				f.zone.Records = append(f.zone.Records[:i], f.zone.Records[i+1:]...)
				break
			}
		}
	}
}

func TestTheDomainsMailRecordsAreMailiosAndTheRestAreLeftAlone(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	d, err := st.CreateDomain(ctx, "reey.ru")
	if err != nil {
		t.Fatal(err)
	}

	fake := &doapi{next: 10, zone: dnsapi.Zone{Name: "reey.ru", Records: []dnsapi.Record{
		// An SPF and a DMARC of somebody else's: replaced by mailio's.
		{ID: 1, Type: "TXT", Name: "@", Data: "v=spf1 include:elsewhere.example ~all"},
		{ID: 2, Type: "TXT", Name: "_dmarc", Data: "v=DMARC1; p=none"},
		// A stale key under mailio's selector: replaced.
		{ID: 3, Type: "TXT", Name: dkimkey.Selector + "._domainkey", Data: "v=DKIM1; k=rsa; p=STALE"},
		// The Postfix-era key under its own selector, a site verification, and MX: left alone.
		{ID: 4, Type: "TXT", Name: "mail._domainkey", Data: "v=DKIM1; k=rsa; p=OLD"},
		{ID: 5, Type: "TXT", Name: "@", Data: "google-site-verification=abc"},
		{ID: 6, Type: "MX", Name: "@", Data: "mx.elsewhere.example."},
	}}}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	if _, err := Sync(ctx, st, dnsapi.New(srv.URL), "mail.vyrtsev.com", slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	sort.Ints(fake.deleted)
	if fmt.Sprint(fake.deleted) != "[1 2 3]" {
		t.Errorf("deleted %v, want the SPF, the DMARC and the stale key", fake.deleted)
	}
	key, _ := dkimkey.Record(d.DKIMKey)
	got := map[string]string{}
	for _, r := range fake.zone.Records {
		got[r.Type+" "+r.Name+" "+strings.SplitN(r.Data, "=", 2)[0]] = r.Data
	}
	for k, want := range map[string]string{
		"TXT @ v":                        "v=spf1 a:mail.vyrtsev.com ~all",
		"TXT _dmarc v":                   "v=DMARC1; p=reject",
		"TXT mailio._domainkey v":        key,
		"TXT mail._domainkey v":          "v=DKIM1; k=rsa; p=OLD",
		"TXT @ google-site-verification": "google-site-verification=abc",
		"MX @ mx.elsewhere.example.":     "mx.elsewhere.example.",
	} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}

	// A second run changes nothing.
	fake.created, fake.deleted = nil, nil
	if _, err := Sync(ctx, st, dnsapi.New(srv.URL), "mail.vyrtsev.com", slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	if len(fake.created)+len(fake.deleted) != 0 {
		t.Errorf("a second run created %v and deleted %v", fake.created, fake.deleted)
	}
}

func TestADomainTheAPIDoesNotManageIsLeftAlone(t *testing.T) {
	ctx := context.Background()
	st, _ := store.Open(t.TempDir())
	defer st.Close()
	st.CreateDomain(ctx, "elsewhere.org")
	fake := &doapi{zone: dnsapi.Zone{Name: "reey.ru"}}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	if _, err := Sync(ctx, st, dnsapi.New(srv.URL), "mail.vyrtsev.com", slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	if len(fake.created) != 0 {
		t.Errorf("created %v in a zone that is not the domain's", fake.created)
	}
}

func TestAHostIsFoundInItsLongestZone(t *testing.T) {
	zones := []dnsapi.Zone{{Name: "vyrtsev.com"}, {Name: "mail.vyrtsev.com"}, {Name: "reey.ru"}}
	for host, want := range map[string]string{
		"mail.vyrtsev.com":          "mail.vyrtsev.com @",
		"x.mail.vyrtsev.com":        "mail.vyrtsev.com x",
		"vyrtsev.com":               "vyrtsev.com @",
		"_dmarc.vyrtsev.com":        "vyrtsev.com _dmarc",
		"mailio._domainkey.reey.ru": "reey.ru mailio._domainkey",
	} {
		z, rel, ok := dnsapi.ZoneFor(zones, host)
		if !ok || z.Name+" "+rel != want {
			t.Errorf("%s: got %v %q", host, z, rel)
		}
	}
	if _, _, ok := dnsapi.ZoneFor(zones, "example.org"); ok {
		t.Error("found a zone for a foreign host")
	}
}

// The DMARC published is the domain's setting, and changing it changes the record.
func TestDMARCFollowsTheDomainsSetting(t *testing.T) {
	ctx := context.Background()
	st, _ := store.Open(t.TempDir())
	defer st.Close()
	d, _ := st.CreateDomain(ctx, "reey.ru")
	fake := &doapi{zone: dnsapi.Zone{Name: "reey.ru"}}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	sync := func() string {
		t.Helper()
		if _, err := Sync(ctx, st, dnsapi.New(srv.URL), "mail.vyrtsev.com", slog.New(slog.DiscardHandler)); err != nil {
			t.Fatal(err)
		}
		for _, r := range fake.zone.Records {
			if r.Name == "_dmarc" {
				return r.Data
			}
		}
		return ""
	}
	if got := sync(); got != "v=DMARC1; p=reject" {
		t.Errorf("a new domain publishes %q", got)
	}
	if err := st.SetDMARC(ctx, d.ID, store.DMARCQuarantine); err != nil {
		t.Fatal(err)
	}
	if got := sync(); got != "v=DMARC1; p=quarantine" {
		t.Errorf("after the change: %q", got)
	}
	if err := st.SetDMARC(ctx, d.ID, "bounce"); err == nil {
		t.Error("a made-up policy was taken")
	}
}

// A domain without its own policies publishes the defaults, and follows them when they change.
func TestADomainPublishesTheDefaultsItFollows(t *testing.T) {
	ctx := context.Background()
	st, _ := store.Open(t.TempDir())
	defer st.Close()
	st.CreateDomain(ctx, "reey.ru")
	fake := &doapi{zone: dnsapi.Zone{Name: "reey.ru"}}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	st.SetSettings(ctx, store.Settings{Unknown: store.UnknownReject, DMARC: store.DMARCQuarantine, SPF: store.SPFFail})
	if _, err := Sync(ctx, st, dnsapi.New(srv.URL), "mail.vyrtsev.com", slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range fake.zone.Records {
		got[r.Name] = r.Data
	}
	if got["@"] != "v=spf1 a:mail.vyrtsev.com -all" || got["_dmarc"] != "v=DMARC1; p=quarantine" {
		t.Errorf("published %v", got)
	}
}
