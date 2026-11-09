// Package dnssync publishes what each domain needs to send: its DKIM key always, and SPF and DMARC
// when it has none. An existing SPF or DMARC record is somebody's decision, such as an SPF that
// includes the relay, and is never rewritten.
package dnssync

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"mailio/internal/dkimkey"
	"mailio/internal/dnsapi"
	"mailio/internal/store"
)

// Record is one record a domain should have, as the admin interface shows it.
type Record struct {
	Type string `json:"type"`
	Name string `json:"name"`
	Data string `json:"data"`
	// Kept: mailio keeps it current. Otherwise it only creates the record when absent.
	Kept bool `json:"kept"`
}

// Wanted is every record a domain should publish, names absolute.
func Wanted(d *store.Domain, mailHost string) []Record {
	out := []Record{
		{Type: "MX", Name: d.Name, Data: "10 " + mailHost + "."},
		{Type: "TXT", Name: d.Name, Data: "v=spf1 a:" + mailHost + " ~all"},
		{Type: "TXT", Name: "_dmarc." + d.Name, Data: "v=DMARC1; p=none"},
	}
	if d.DKIMKey != "" {
		if rec, err := dkimkey.Record(d.DKIMKey); err == nil {
			out = append(out, Record{Type: "TXT", Name: dkimkey.RecordName(d.Name, d.DKIMSelector), Data: rec, Kept: true})
		}
	}
	return out
}

// Sync publishes every domain's records through the API. A domain the API does not manage is left
// to whoever does, with the records shown in the admin interface.
func Sync(ctx context.Context, st *store.Store, api *dnsapi.Client, mailHost string, log *slog.Logger) error {
	domains, err := st.Domains(ctx)
	if err != nil {
		return err
	}
	zones, err := api.Zones(ctx)
	if err != nil {
		return err
	}
	for i := range domains {
		d := &domains[i]
		zone, _, ok := dnsapi.ZoneFor(zones, d.Name)
		if !ok {
			continue
		}
		for _, want := range Wanted(d, mailHost) {
			if err := apply(ctx, api, zone, want, log); err != nil {
				return fmt.Errorf("%s %s: %w", want.Type, want.Name, err)
			}
		}
	}
	return nil
}

func apply(ctx context.Context, api *dnsapi.Client, zone *dnsapi.Zone, want Record, log *slog.Logger) error {
	// MX is set once, by hand, when a domain starts receiving: the API has no field for its
	// priority, and a domain's mail is not for a sync to redirect.
	if want.Type == "MX" {
		return nil
	}
	_, rel, _ := dnsapi.ZoneFor([]dnsapi.Zone{*zone}, want.Name)
	var same []dnsapi.Record
	for _, r := range zone.Records {
		if r.Type != want.Type || r.Name != rel {
			continue
		}
		// One name holds many TXT records; only the kind being published counts.
		if want.Type == "TXT" && kind(r.Data) != kind(want.Data) {
			continue
		}
		same = append(same, r)
	}
	switch {
	case len(same) == 1 && strings.EqualFold(same[0].Data, want.Data):
		return nil
	case len(same) > 0 && !want.Kept:
		return nil
	}
	for _, r := range same {
		if err := api.Delete(ctx, zone.Name, r.ID); err != nil {
			return err
		}
	}
	if err := api.Create(ctx, zone.Name, want.Type, rel, want.Data, 0); err != nil {
		return err
	}
	log.Info("dns record published", "type", want.Type, "name", want.Name)
	return nil
}

func kind(txt string) string {
	switch {
	case strings.HasPrefix(txt, "v=spf1"):
		return "spf"
	case strings.HasPrefix(txt, "v=DMARC1"):
		return "dmarc"
	case strings.HasPrefix(txt, "v=DKIM1"):
		return "dkim"
	}
	return txt
}
