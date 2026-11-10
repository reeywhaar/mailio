package dnscheck

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"mailio/internal/dnssync"
	"mailio/internal/store"
)

// fake is DNS as a map; a name in fail does not answer.
type fake struct {
	txt  map[string][]string
	mx   map[string][]string
	fail map[string]bool
}

func (f fake) TXT(_ context.Context, name string) ([]string, error) {
	if f.fail[name] {
		return nil, errors.New("timeout")
	}
	return f.txt[name], nil
}

func (f fake) MX(_ context.Context, name string) ([]string, error) {
	if f.fail[name] {
		return nil, errors.New("timeout")
	}
	return f.mx[name], nil
}

func TestEachRecordIsComparedAsReceiversReadIt(t *testing.T) {
	dkim := "v=DKIM1; k=rsa; p=MIIBIjANBgkqh+AbC/dEf"
	dns := fake{
		txt: map[string][]string{
			// A site verification beside the SPF is not an SPF.
			"ok.ru":                    {"google-site-verification=x", "v=spf1  a:mail.test ~all"},
			"_dmarc.ok.ru":             {"v=DMARC1;p=REJECT"},
			"mailio._domainkey.ok.ru":  {"k=rsa;p=MIIBIjANBgkqh+AbC/dEf"},
			"wrong.ru":                 {"v=spf1 include:_spf.google.com ~all"},
			"_dmarc.wrong.ru":          {"v=DMARC1; p=none"},
			"mailio._domainkey.two.ru": {"v=DKIM1; p=old"},
			"two.ru":                   {"v=spf1 a:mail.test ~all", "v=spf1 mx -all"},
		},
		mx: map[string][]string{
			"ok.ru":    {"MAIL.test."},
			"wrong.ru": {"mail.test.", "mx.elsewhere.com."},
		},
		fail: map[string]bool{"_dmarc.two.ru": true},
	}
	for _, c := range []struct {
		want  dnssync.Record
		got   Status
		found []string
	}{
		{dnssync.Record{Type: "MX", Name: "ok.ru", Data: "mail.test"}, OK, nil},
		{dnssync.Record{Type: "TXT", Name: "ok.ru", Data: "v=spf1 a:mail.test ~all"}, OK, nil},
		{dnssync.Record{Type: "TXT", Name: "_dmarc.ok.ru", Data: "v=DMARC1; p=reject"}, OK, nil},
		{dnssync.Record{Type: "TXT", Name: "mailio._domainkey.ok.ru", Data: dkim}, OK, nil},
		{dnssync.Record{Type: "MX", Name: "wrong.ru", Data: "mail.test"}, Different, []string{"mail.test", "mx.elsewhere.com"}},
		{dnssync.Record{Type: "TXT", Name: "wrong.ru", Data: "v=spf1 a:mail.test ~all"}, Different, []string{"v=spf1 include:_spf.google.com ~all"}},
		{dnssync.Record{Type: "TXT", Name: "_dmarc.wrong.ru", Data: "v=DMARC1; p=reject"}, Different, []string{"v=DMARC1; p=none"}},
		{dnssync.Record{Type: "MX", Name: "two.ru", Data: "mail.test"}, Missing, nil},
		{dnssync.Record{Type: "TXT", Name: "two.ru", Data: "v=spf1 a:mail.test ~all"}, Several, []string{"v=spf1 a:mail.test ~all", "v=spf1 mx -all"}},
		{dnssync.Record{Type: "TXT", Name: "_dmarc.two.ru", Data: "v=DMARC1; p=reject"}, Failed, nil},
		{dnssync.Record{Type: "TXT", Name: "mailio._domainkey.two.ru", Data: dkim}, Different, []string{"v=DKIM1; p=old"}},
		{dnssync.Record{Type: "TXT", Name: "mailio._domainkey.none.ru", Data: dkim}, Missing, nil},
	} {
		r := Check(context.Background(), dns, c.want)
		if r.Status != c.got || !reflect.DeepEqual(r.Found, c.found) || (r.Status == Failed) != (r.Error != "") {
			t.Errorf("%s %s: %s %q, want %s %q", c.want.Type, c.want.Name, r.Status, r.Found, c.got, c.found)
		}
	}
}

// A record changed since the last check has no result until the next: the old one's would be
// about a value no longer wanted.
func TestALastCheckIsOfTheRecordAsItWas(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	d, err := st.CreateDomain(ctx, "ok.ru")
	if err != nil {
		t.Fatal(err)
	}
	dns := fake{txt: map[string][]string{"_dmarc.ok.ru": {"v=DMARC1; p=reject"}}}
	c := NewChecker(st, dns, "mail.test")
	if err := c.CheckAll(ctx); err != nil {
		t.Fatal(err)
	}
	dmarc := func() dnssync.Record {
		d, _ := st.DomainByID(ctx, d.ID)
		set, _ := st.Settings(ctx)
		return Wanted(d, set, "mail.test")[2]
	}
	if r, _, ok := c.Last(d.ID, dmarc()); !ok || r.Status != OK {
		t.Errorf("checked: %v %v", r, ok)
	}
	st.SetDMARC(ctx, d.ID, store.DMARCNone)
	if r, _, ok := c.Last(d.ID, dmarc()); ok {
		t.Errorf("a changed record still has the old result: %v", r)
	}
	if err := c.CheckDomain(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if r, _, ok := c.Last(d.ID, dmarc()); !ok || r.Status != Different {
		t.Errorf("checked again: %v %v", r, ok)
	}
}
