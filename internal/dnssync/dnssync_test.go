package dnssync

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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

func TestDKIMIsKeptCurrentAndSPFAndDMARCOnlyCreated(t *testing.T) {
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
		// Somebody's SPF, including the relay: left alone.
		{ID: 1, Type: "TXT", Name: "@", Data: "v=spf1 include:relay.example ~all"},
		// The Postfix-era key under its own selector: left alone.
		{ID: 2, Type: "TXT", Name: "mail._domainkey", Data: "v=DKIM1; k=rsa; p=OLD"},
		// A stale key under mailio's selector: replaced.
		{ID: 3, Type: "TXT", Name: dkimkey.Selector + "._domainkey", Data: "v=DKIM1; k=rsa; p=STALE"},
	}}}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	if err := Sync(ctx, st, dnsapi.New(srv.URL), "mail.vyrtsev.com", slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	if len(fake.deleted) != 1 || fake.deleted[0] != 3 {
		t.Errorf("deleted %v, want only the stale key", fake.deleted)
	}
	want, _ := dkimkey.Record(d.DKIMKey)
	got := map[string]string{}
	for _, r := range fake.zone.Records {
		got[r.Name+" "+strings.SplitN(r.Data, ";", 2)[0]] = r.Data
	}
	if got[dkimkey.Selector+"._domainkey v=DKIM1"] != want {
		t.Error("the key was not published")
	}
	if got["@ v=spf1 include:relay.example ~all"] == "" {
		t.Error("the existing SPF was touched")
	}
	if got["_dmarc v=DMARC1"] != "v=DMARC1; p=none" {
		t.Errorf("no DMARC was created: %v", got)
	}

	// A second run changes nothing.
	fake.created, fake.deleted = nil, nil
	if err := Sync(ctx, st, dnsapi.New(srv.URL), "mail.vyrtsev.com", slog.New(slog.DiscardHandler)); err != nil {
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
	if err := Sync(ctx, st, dnsapi.New(srv.URL), "mail.vyrtsev.com", slog.New(slog.DiscardHandler)); err != nil {
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
