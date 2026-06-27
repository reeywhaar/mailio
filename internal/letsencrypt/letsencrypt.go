// Package letsencrypt obtains and renews TLS certificates from Let's Encrypt
// using the ACME v2 DNS-01 challenge, completed via the configured DNS provider.
package letsencrypt

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/acme"

	"mailio/internal/config"
	"mailio/internal/dns"
)

const (
	accountKeyPath = "/etc/letsencrypt/account.key"
	stagingURL     = "https://acme-staging-v02.api.letsencrypt.org/directory"

	// settleDelay is an extra wait after the challenge TXT looks fully propagated,
	// to give the provider's anycast fleet a final moment to converge before
	// Let's Encrypt validates from its multiple global vantage points.
	settleDelay = 20 * time.Second

	// propagationTimeout bounds how long we wait for the new TXT value to appear
	// (and stale values to disappear) across the authoritative nameservers.
	// DNS propagation is eventually-consistent and can take minutes.
	propagationTimeout = 1 * time.Minute

	// requiredCleanRounds is how many consecutive polls must show ONLY the
	// expected value on every nameserver. Providers often serve multi-record TXT
	// sets in rotation (one value per response), so a single clean poll can hide
	// a stale value that rotates in next time; requiring several consecutive
	// clean rounds guards against being fooled by that rotation.
	requiredCleanRounds = 4

	// challengeTTL is the TTL (seconds) requested for the challenge TXT record.
	// It must be low: the record is written and validated within seconds, so a
	// provider's high default (e.g. 1800s) keeps stale values cached for up to
	// half an hour. 30s is a common provider minimum.
	challengeTTL = 30
)

func loadOrGenerateAccountKey() (*ecdsa.PrivateKey, error) {
	data, err := os.ReadFile(accountKeyPath)
	if err == nil {
		if block, _ := pem.Decode(data); block != nil {
			if key, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
				return key, nil
			}
		}
	}
	if err := os.MkdirAll("/etc/letsencrypt", 0700); err != nil {
		return nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	block := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(accountKeyPath, block, 0600); err != nil {
		return nil, err
	}
	fmt.Println("[acme] generated new account key")
	return key, nil
}

func certNeedsRenewal(certFile string) bool {
	data, err := os.ReadFile(certFile)
	if err != nil {
		return true
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return true
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return true
	}
	// A self-signed placeholder cert (written by the startup fallback when ACME
	// is unavailable) reports a far-future expiry, so the time check alone would
	// never replace it. Force renewal so the real Let's Encrypt cert is obtained
	// as soon as ACME succeeds again.
	if cert.Issuer.String() == cert.Subject.String() {
		return true
	}
	return time.Until(cert.NotAfter) < 30*24*time.Hour
}

// Obtain obtains or renews a certificate for cfg.Hostname and writes the chain
// and key to certFile/keyFile. Unless force is set, renewal is skipped while the
// existing certificate has more than 30 days of validity remaining. The returned
// bool reports whether a new certificate was actually written.
func Obtain(cfg *config.Config, certFile, keyFile string, force bool) (renewed bool, err error) {
	if !force && !certNeedsRenewal(certFile) {
		fmt.Println("[acme] certificate still valid, skipping renewal")
		return false, nil
	}
	if cfg.LetsEncrypt.Email == "" {
		return false, fmt.Errorf("letsencrypt.email is required")
	}

	// Find which configured domain owns the hostname for the DNS challenge
	parentDomain := cfg.Hostname
	if idx := strings.Index(cfg.Hostname, "."); idx >= 0 {
		parentDomain = cfg.Hostname[idx+1:]
	}
	var challDomain string
	for _, d := range cfg.Domains {
		if d.Name == parentDomain {
			challDomain = d.Name
			break
		}
	}
	if challDomain == "" {
		return false, fmt.Errorf("no configured domain matches %q (needed for DNS-01 challenge)", parentDomain)
	}

	fmt.Printf("[acme] obtaining certificate for %s\n", cfg.Hostname)

	accountKey, err := loadOrGenerateAccountKey()
	if err != nil {
		return false, err
	}

	dirURL := acme.LetsEncryptURL
	if cfg.LetsEncrypt.Staging {
		dirURL = stagingURL
	}

	client := &acme.Client{
		Key:          accountKey,
		DirectoryURL: dirURL,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	// Register or retrieve existing account. When the account key is persisted
	// across restarts, Register returns ErrAccountAlreadyExists on every run after
	// the first; this is not an error — the client caches the account URL
	// internally, so we just fetch the existing account record and continue.
	account, err := client.Register(ctx, &acme.Account{
		Contact: []string{"mailto:" + cfg.LetsEncrypt.Email},
	}, acme.AcceptTOS)
	if errors.Is(err, acme.ErrAccountAlreadyExists) {
		account, err = client.GetReg(ctx, "")
	}
	if err != nil {
		return false, fmt.Errorf("register account: %w", err)
	}
	fmt.Printf("[acme] account: %s\n", account.URI)

	// Create order for the hostname
	order, err := client.AuthorizeOrder(ctx, acme.DomainIDs(cfg.Hostname))
	if err != nil {
		return false, fmt.Errorf("create order: %w", err)
	}
	fmt.Printf("[acme] order %s (status: %s)\n", order.URI, order.Status)

	// Clean up _acme-challenge TXT records when done
	defer func() {
		records, err := dns.GetAll()
		if err != nil {
			fmt.Printf("[acme] warn: could not fetch DNS records for cleanup: %v\n", err)
			return
		}
		for _, r := range records[challDomain] {
			if r.Type == "TXT" && strings.HasPrefix(r.Name, "_acme-challenge") {
				if err := dns.DeleteRecord(challDomain, r.ID); err != nil {
					fmt.Printf("[acme] warn: cleanup %s.%s failed: %v\n", r.Name, challDomain, err)
				} else {
					fmt.Printf("[acme] cleaned up TXT %s.%s\n", r.Name, challDomain)
				}
			}
		}
	}()

	// Complete each authorization via DNS-01 challenge
	for _, authzURL := range order.AuthzURLs {
		authz, err := client.GetAuthorization(ctx, authzURL)
		if err != nil {
			return false, fmt.Errorf("get authz: %w", err)
		}

		if authz.Status == acme.StatusValid {
			fmt.Printf("[acme] authz for %s already valid\n", authz.Identifier.Value)
			continue
		}

		var challenge *acme.Challenge
		for _, ch := range authz.Challenges {
			if ch.Type == "dns-01" {
				challenge = ch
				break
			}
		}
		if challenge == nil {
			return false, fmt.Errorf("no dns-01 challenge for %s", authz.Identifier.Value)
		}

		txtValue, err := client.DNS01ChallengeRecord(challenge.Token)
		if err != nil {
			return false, fmt.Errorf("compute dns-01 value: %w", err)
		}

		challengeHost := authz.Identifier.Value
		var recordName string
		if challengeHost == challDomain {
			recordName = "_acme-challenge"
		} else {
			sub := strings.TrimSuffix(challengeHost, "."+challDomain)
			recordName = "_acme-challenge." + sub
		}

		// Remove any stale challenge records first
		allRecords, err := dns.GetAll()
		if err != nil {
			return false, fmt.Errorf("get dns records: %w", err)
		}
		for _, r := range allRecords[challDomain] {
			if r.Type == "TXT" && r.Name == recordName {
				if err := dns.DeleteRecord(challDomain, r.ID); err != nil {
					return false, fmt.Errorf("delete stale challenge record: %w", err)
				}
			}
		}

		// A low TTL is essential for challenge records: they are written and
		// validated within seconds, so a high TTL (e.g. the provider's 1800s
		// default) keeps stale values from previous attempts cached for up to half
		// an hour, which is the main cause of "Incorrect TXT record" failures.
		fmt.Printf("[acme] creating TXT %s.%s (ttl=%d)\n", recordName, challDomain, challengeTTL)
		if err := dns.CreateRecord(challDomain, "TXT", recordName, txtValue, challengeTTL); err != nil {
			return false, fmt.Errorf("create challenge record: %w", err)
		}

		// Wait until the new value is actually served by the zone's authoritative
		// nameservers before asking Let's Encrypt to validate. Without this, LE may
		// read a stale value from a previous attempt (the DNS provider is
		// eventually-consistent) and reject the challenge as "Incorrect TXT record".
		fqdn := recordName + "." + challDomain
		fmt.Printf("[acme] waiting for TXT %s to propagate...\n", fqdn)
		propCtx, propCancel := context.WithTimeout(ctx, propagationTimeout)
		waitForTXTPropagation(propCtx, challDomain, fqdn, txtValue)
		propCancel()

		if _, err := client.Accept(ctx, challenge); err != nil {
			return false, fmt.Errorf("accept challenge: %w", err)
		}
		fmt.Printf("[acme] waiting for authorization for %s...\n", challengeHost)
		if _, err := client.WaitAuthorization(ctx, authz.URI); err != nil {
			return false, fmt.Errorf("authorization failed for %s: %w", challengeHost, err)
		}
		fmt.Printf("[acme] authorization valid for %s\n", challengeHost)
	}

	// Generate certificate private key and CSR
	certKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return false, fmt.Errorf("generate cert key: %w", err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: cfg.Hostname},
		DNSNames: []string{cfg.Hostname},
	}, certKey)
	if err != nil {
		return false, fmt.Errorf("create csr: %w", err)
	}

	// Finalize order and download cert chain (bundle=true includes intermediates)
	der, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, csrDER, true)
	if err != nil {
		return false, fmt.Errorf("create cert: %w", err)
	}

	var certPEM []byte
	for _, b := range der {
		certPEM = append(certPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: b})...)
	}

	if err := os.MkdirAll(certDir(certFile), 0755); err != nil {
		return false, fmt.Errorf("mkdir tls: %w", err)
	}
	if err := os.WriteFile(certFile, certPEM, 0644); err != nil {
		return false, fmt.Errorf("write cert: %w", err)
	}
	certKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(certKey),
	})
	if err := os.WriteFile(keyFile, certKeyPEM, 0600); err != nil {
		return false, fmt.Errorf("write key: %w", err)
	}

	fmt.Printf("[acme] certificate obtained for %s\n", cfg.Hostname)
	return true, nil
}

// waitForTXTPropagation polls the zone's authoritative nameservers until each of
// them serves ONLY the expected value at fqdn for several consecutive rounds, or
// ctx expires. Querying the authoritative servers directly (rather than a
// recursive resolver) reflects exactly what Let's Encrypt will see. Requiring the
// expected value to be the SOLE value guards against stale records from previous
// attempts whose deletes have not yet propagated out of the provider — those get
// served in rotation and would otherwise be handed to Let's Encrypt, failing
// validation. On timeout it logs a warning and returns so the caller still tries
// validation (best effort); a genuine failure is handled upstream.
func waitForTXTPropagation(ctx context.Context, zone, fqdn, expected string) {
	nameservers, err := net.LookupNS(zone)
	if err != nil || len(nameservers) == 0 {
		fmt.Printf("[acme] warn: could not look up nameservers for %s (%v); sleeping %s\n", zone, err, settleDelay)
		sleepCtx(ctx, settleDelay)
		return
	}

	cleanRounds := 0
	for {
		roundClean := true
		for _, ns := range nameservers {
			txts, err := txtRecordsAt(ctx, ns.Host, fqdn)
			switch {
			case err != nil:
				fmt.Printf("[acme]   %s: query error: %v\n", ns.Host, err)
				roundClean = false
			case !contains(txts, expected):
				fmt.Printf("[acme]   %s: expected value not present yet (saw %v)\n", ns.Host, txts)
				roundClean = false
			case len(txts) > 1 || (len(txts) == 1 && txts[0] != expected):
				// Expected is present but a stale value is still being served
				// alongside it (delete not yet propagated).
				fmt.Printf("[acme]   %s: stale values still present (saw %v)\n", ns.Host, txts)
				roundClean = false
			}
		}
		if roundClean {
			cleanRounds++
			if cleanRounds >= requiredCleanRounds {
				fmt.Printf("[acme] TXT %s clean on all %d nameservers (%d rounds); settling %s\n",
					fqdn, len(nameservers), cleanRounds, settleDelay)
				sleepCtx(ctx, settleDelay)
				return
			}
		} else {
			cleanRounds = 0
		}
		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
			fmt.Printf("[acme] warn: %s did not fully propagate before timeout; proceeding anyway\n", fqdn)
			return
		}
	}
}

// sleepCtx sleeps for d unless ctx is cancelled first.
func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-time.After(d):
	case <-ctx.Done():
	}
}

// txtRecordsAt returns the TXT records for fqdn as served by the nameserver at
// host, querying that server directly on port 53.
func txtRecordsAt(ctx context.Context, host, fqdn string) ([]string, error) {
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, network, net.JoinHostPort(strings.TrimSuffix(host, "."), "53"))
		},
	}
	return resolver.LookupTXT(ctx, fqdn)
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func certDir(certFile string) string {
	if idx := strings.LastIndex(certFile, "/"); idx >= 0 {
		return certFile[:idx]
	}
	return "."
}
