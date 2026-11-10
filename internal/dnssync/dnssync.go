// Package dnssync publishes what each domain needs to send: its DKIM key, its SPF and its DMARC,
// rewritten to mailio's on every sync, since managing them is mailio's job. Other records are
// left alone, MX among them: it is set outside mailio, pointing at MAILIO_MAIL_HOST.
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
}

// Wanted is every record mailio keeps for a domain, names absolute, with the domain's own
// policies or the defaults.
func Wanted(d *store.Domain, set *store.Settings, mailHost string) []Record {
	dmarc, spf := d.Policies(set)
	out := []Record{
		{Type: "TXT", Name: d.Name, Data: "v=spf1 a:" + mailHost + " " + string(spf)},
		{Type: "TXT", Name: "_dmarc." + d.Name, Data: "v=DMARC1; p=" + string(dmarc)},
	}
	if d.DKIMKey != "" {
		if rec, err := dkimkey.Record(d.DKIMKey); err == nil {
			out = append(out, Record{Type: "TXT", Name: dkimkey.RecordName(d.Name, d.DKIMSelector), Data: rec})
		}
	}
	return out
}

// Sync publishes every domain's records through the API, and says how many it changed. A domain
// the API does not manage is left to whoever does, with the records shown in the admin interface.
func Sync(ctx context.Context, st *store.Store, api *dnsapi.Client, mailHost string, log *slog.Logger) (int, error) {
	domains, err := st.Domains(ctx)
	if err != nil {
		return 0, err
	}
	set, err := st.Settings(ctx)
	if err != nil {
		return 0, err
	}
	zones, err := api.Zones(ctx)
	if err != nil {
		return 0, err
	}
	changed := 0
	for i := range domains {
		d := &domains[i]
		zone, _, ok := dnsapi.ZoneFor(zones, d.Name)
		if !ok {
			continue
		}
		for _, want := range Wanted(d, set, mailHost) {
			did, err := apply(ctx, api, zone, want, log)
			if err != nil {
				return changed, fmt.Errorf("%s %s: %w", want.Type, want.Name, err)
			}
			if did {
				changed++
			}
		}
	}
	return changed, nil
}

func apply(ctx context.Context, api *dnsapi.Client, zone *dnsapi.Zone, want Record, log *slog.Logger) (bool, error) {
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
	if len(same) == 1 && strings.EqualFold(same[0].Data, want.Data) {
		return false, nil
	}
	for _, r := range same {
		if err := api.Delete(ctx, zone.Name, r.ID); err != nil {
			return false, err
		}
	}
	if err := api.Create(ctx, zone.Name, want.Type, rel, want.Data, 0); err != nil {
		return false, err
	}
	log.Info("dns record published", "type", want.Type, "name", want.Name)
	return true, nil
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
