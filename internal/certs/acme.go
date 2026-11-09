package certs

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/acme"

	"mailio/internal/dnsapi"
	"mailio/internal/store"
)

// ACME keeps the certificate from Let's Encrypt, proving the host through a DNS record written
// by the DNS API. DNS rather than HTTP: port 80 belongs to the proxy, and the mail ports cannot
// answer a challenge at all.
type ACME struct {
	M         *Manager
	API       *dnsapi.Client
	Directory string
}

// Run renews whenever the certificate is self-signed or has a third of its life left, checking
// twice a day and an hour after a failure.
func (a *ACME) Run(ctx context.Context) {
	for {
		wait := 12 * time.Hour
		if a.due() {
			if err := a.Obtain(ctx); err != nil {
				if ctx.Err() != nil {
					return
				}
				a.M.log.Error("getting a certificate from Let's Encrypt failed", "host", a.M.host, "error", err)
				wait = time.Hour
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func (a *ACME) due() bool {
	c := a.M.Current()
	if c == nil || c.Issuer != "letsencrypt" {
		return true
	}
	block, _ := pem.Decode([]byte(c.Chain))
	if block == nil {
		return true
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return true
	}
	return time.Until(leaf.NotAfter) < leaf.NotAfter.Sub(leaf.NotBefore)/3
}

// Obtain gets a certificate now and puts it in service.
func (a *ACME) Obtain(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	host := a.M.host

	key, err := a.accountKey(ctx)
	if err != nil {
		return err
	}
	client := &acme.Client{Key: key, DirectoryURL: a.Directory}
	// No contact address: Let's Encrypt sends no expiry mail any more, and renewal is this loop's job.
	if _, err := client.Register(ctx, &acme.Account{}, acme.AcceptTOS); err != nil && !errors.Is(err, acme.ErrAccountAlreadyExists) {
		return fmt.Errorf("register: %w", err)
	}
	order, err := client.AuthorizeOrder(ctx, acme.DomainIDs(host))
	if err != nil {
		return fmt.Errorf("order: %w", err)
	}

	zones, err := a.API.Zones(ctx)
	if err != nil {
		return err
	}
	zone, rel, ok := dnsapi.ZoneFor(zones, host)
	if !ok {
		return fmt.Errorf("the DNS API manages no zone containing %s", host)
	}
	name := "_acme-challenge"
	if rel != "@" {
		name += "." + rel
	}
	defer a.clean(zone.Name, name)

	for _, u := range order.AuthzURLs {
		authz, err := client.GetAuthorization(ctx, u)
		if err != nil {
			return fmt.Errorf("authorization: %w", err)
		}
		if authz.Status == acme.StatusValid {
			continue
		}
		var ch *acme.Challenge
		for _, c := range authz.Challenges {
			if c.Type == "dns-01" {
				ch = c
			}
		}
		if ch == nil {
			return errors.New("no dns-01 challenge offered")
		}
		value, err := client.DNS01ChallengeRecord(ch.Token)
		if err != nil {
			return err
		}
		a.clean(zone.Name, name)
		// Low, because a stale value from an earlier attempt cached for the provider's default
		// half hour is what fails a challenge.
		if err := a.API.Create(ctx, zone.Name, "TXT", name, value, 30); err != nil {
			return fmt.Errorf("challenge record: %w", err)
		}
		a.waitForTXT(ctx, zone.Name, name+"."+zone.Name, value)
		if _, err := client.Accept(ctx, ch); err != nil {
			return fmt.Errorf("accept: %w", err)
		}
		if _, err := client.WaitAuthorization(ctx, authz.URI); err != nil {
			return fmt.Errorf("authorization for %s: %w", host, err)
		}
	}

	certKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: host}, DNSNames: []string{host},
	}, certKey)
	if err != nil {
		return err
	}
	ders, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, csr, true)
	if err != nil {
		return fmt.Errorf("certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(ders[0])
	if err != nil {
		return err
	}
	var chain []byte
	for _, d := range ders {
		chain = append(chain, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: d})...)
	}
	keyDER, err := x509.MarshalECPrivateKey(certKey)
	if err != nil {
		return err
	}
	c := &store.Certificate{
		Host:     host,
		Chain:    string(chain),
		Key:      string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})),
		Issuer:   "letsencrypt",
		NotAfter: leaf.NotAfter,
	}
	if err := a.M.st.SaveCertificate(ctx, c); err != nil {
		return err
	}
	if err := a.M.Use(c); err != nil {
		return err
	}
	a.M.log.Info("certificate from Let's Encrypt in service", "host", host, "not_after", leaf.NotAfter.Format(time.RFC3339))
	return nil
}

func (a *ACME) accountKey(ctx context.Context) (*ecdsa.PrivateKey, error) {
	if p, err := a.M.st.ACMEAccountKey(ctx); err == nil {
		block, _ := pem.Decode([]byte(p))
		if block != nil {
			if k, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
				return k, nil
			}
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		return nil, err
	}
	if err := a.M.st.SaveACMEAccountKey(ctx, string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}))); err != nil {
		return nil, err
	}
	return k, nil
}

// clean deletes challenge records left by this or an earlier attempt. Best effort: a leftover
// TXT record costs nothing but clutter.
func (a *ACME) clean(zone, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	zones, err := a.API.Zones(ctx)
	if err != nil {
		return
	}
	for _, z := range zones {
		if z.Name != zone {
			continue
		}
		for _, r := range z.Records {
			if r.Type == "TXT" && r.Name == name {
				a.API.Delete(ctx, zone, r.ID)
			}
		}
	}
}

// waitForTXT polls the zone's own nameservers until every one serves only the value, several
// rounds running: providers serve a stale value in rotation for a while after a delete, and one
// clean answer can hide it. Best effort; on timeout validation is tried anyway.
func (a *ACME) waitForTXT(ctx context.Context, zone, fqdn, want string) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	nss, err := net.DefaultResolver.LookupNS(ctx, zone)
	if err != nil || len(nss) == 0 {
		sleep(ctx, 20*time.Second)
		return
	}
	clean := 0
	for clean < 4 {
		ok := true
		for _, ns := range nss {
			txts, err := txtAt(ctx, ns.Host, fqdn)
			if err != nil || len(txts) != 1 || txts[0] != want {
				ok = false
			}
		}
		if ok {
			clean++
		} else {
			clean = 0
		}
		if !sleep(ctx, 5*time.Second) {
			return
		}
	}
	sleep(ctx, 20*time.Second)
}

func txtAt(ctx context.Context, ns, fqdn string) ([]string, error) {
	r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		d := net.Dialer{Timeout: 5 * time.Second}
		return d.DialContext(ctx, network, net.JoinHostPort(strings.TrimSuffix(ns, "."), "53"))
	}}
	return r.LookupTXT(ctx, fqdn)
}

func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}
