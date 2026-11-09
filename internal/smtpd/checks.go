package smtpd

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"net/mail"
	"strings"
	"time"

	"blitiri.com.ar/go/spf"
	"github.com/emersion/go-msgauth/authres"
	"github.com/emersion/go-msgauth/dkim"
	"github.com/emersion/go-msgauth/dmarc"
	"golang.org/x/net/publicsuffix"
)

// Verdict is what the checks found, and what mailio does about it.
type Verdict struct {
	SPF, DKIM, DMARC string
	// AuthResults is the value of the Authentication-Results header to add.
	AuthResults string
	// Reject is the reason to refuse the message, when its sender's DMARC policy says reject.
	Reject string
	// Junk is a DMARC failure whose policy says quarantine.
	Junk bool
}

// Checker judges one message.
type Checker interface {
	Check(ctx context.Context, ip net.IP, helo, from string, raw []byte) Verdict
}

// Checks is SPF, DKIM and DMARC against live DNS.
//
// No content filter: DMARC is the sender saying what to do with mail that is not theirs, and
// mailio does it. Everything else is the reader's to file, into Junk or out of it.
type Checks struct {
	// Host names the Authentication-Results header.
	Host string
}

// Check runs the three checks, each bounded so a slow DNS answer delays a delivery rather than
// holding a connection forever.
func (c Checks) Check(ctx context.Context, ip net.IP, helo, from string, raw []byte) Verdict {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	var results []authres.Result
	v := Verdict{}

	mailFrom := domainOf(from)
	spfDomain := mailFrom
	if spfDomain == "" {
		spfDomain = helo
	}
	spfResult, _ := spf.CheckHostWithSender(ip, helo, from, spf.WithContext(ctx))
	v.SPF = string(spfResult)
	results = append(results, &authres.SPFResult{Value: authres.ResultValue(spfResult), From: spfDomain, Helo: helo})

	var dkimPass []string
	verifications, err := dkim.VerifyWithOptions(bytes.NewReader(raw), &dkim.VerifyOptions{
		LookupTXT: func(domain string) ([]string, error) { return net.DefaultResolver.LookupTXT(ctx, domain) },
	})
	switch {
	case err != nil:
		v.DKIM = "temperror"
		results = append(results, &authres.DKIMResult{Value: authres.ResultTempError})
	case len(verifications) == 0:
		v.DKIM = "none"
		results = append(results, &authres.DKIMResult{Value: authres.ResultNone})
	default:
		v.DKIM = "fail"
		for _, ver := range verifications {
			r := &authres.DKIMResult{Domain: ver.Domain, Identifier: ver.Identifier}
			switch {
			case ver.Err == nil:
				r.Value = authres.ResultPass
				dkimPass = append(dkimPass, ver.Domain)
				v.DKIM = "pass"
			case dkim.IsTempFail(ver.Err):
				r.Value = authres.ResultTempError
			case dkim.IsPermFail(ver.Err):
				r.Value = authres.ResultPermError
			default:
				r.Value = authres.ResultFail
			}
			results = append(results, r)
		}
	}

	fromDomain := headerFromDomain(raw)
	v.DMARC = "none"
	if fromDomain != "" {
		record, policyDomain := lookupDMARC(ctx, fromDomain)
		if record != nil {
			aligned := false
			if spfResult == spf.Pass && alignedDomains(mailFrom, fromDomain, record.SPFAlignment) {
				aligned = true
			}
			for _, d := range dkimPass {
				if alignedDomains(d, fromDomain, record.DKIMAlignment) {
					aligned = true
				}
			}
			policy := record.Policy
			if policyDomain != fromDomain && record.SubdomainPolicy != "" {
				policy = record.SubdomainPolicy
			}
			if aligned {
				v.DMARC = "pass"
			} else {
				v.DMARC = "fail"
				switch policy {
				case dmarc.PolicyReject:
					v.Reject = "Rejected by the DMARC policy of " + fromDomain
				case dmarc.PolicyQuarantine:
					v.Junk = true
				}
			}
		}
		results = append(results, &authres.DMARCResult{Value: authres.ResultValue(v.DMARC), From: fromDomain})
	}

	v.AuthResults = authres.Format(c.Host, results)
	return v
}

// lookupDMARC finds the policy for a From domain: its own record, else its organizational
// domain's.
func lookupDMARC(ctx context.Context, domain string) (*dmarc.Record, string) {
	opts := &dmarc.LookupOptions{LookupTXT: func(d string) ([]string, error) { return net.DefaultResolver.LookupTXT(ctx, d) }}
	if r, err := dmarc.LookupWithOptions(domain, opts); err == nil {
		return r, domain
	}
	org := orgDomain(domain)
	if org != domain {
		if r, err := dmarc.LookupWithOptions(org, opts); err == nil {
			return r, org
		}
	}
	return nil, ""
}

func alignedDomains(a, b string, mode dmarc.AlignmentMode) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	if a == "" || b == "" {
		return false
	}
	if mode == dmarc.AlignmentStrict {
		return a == b
	}
	return orgDomain(a) == orgDomain(b)
}

func orgDomain(d string) string {
	org, err := publicsuffix.EffectiveTLDPlusOne(d)
	if err != nil {
		return d
	}
	return org
}

func domainOf(addr string) string {
	if i := strings.LastIndexByte(addr, '@'); i >= 0 {
		return strings.ToLower(addr[i+1:])
	}
	return ""
}

func headerFromDomain(raw []byte) string {
	m, err := mail.ReadMessage(bufio.NewReader(bytes.NewReader(raw)))
	if err != nil {
		return ""
	}
	list, err := m.Header.AddressList("From")
	if err != nil || len(list) != 1 {
		return ""
	}
	return domainOf(list[0].Address)
}
