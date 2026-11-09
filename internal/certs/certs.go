// Package certs holds the mail ports' certificate: loaded from the store, swapped in place when it
// is renewed, so no listener restarts.
package certs

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"sync/atomic"
	"time"

	"mailio/internal/store"
)

// Manager serves the current certificate for one host.
type Manager struct {
	st   *store.Store
	host string
	log  *slog.Logger
	cur  atomic.Pointer[tls.Certificate]
	meta atomic.Pointer[store.Certificate]
}

// New loads the stored certificate, or makes a self-signed one when there is none, so the mail
// ports always have something to offer: other servers deliver over a self-signed certificate
// rather than not at all.
func New(ctx context.Context, st *store.Store, host string, log *slog.Logger) (*Manager, error) {
	m := &Manager{st: st, host: host, log: log}
	c, err := st.CertificateFor(ctx, host)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if c == nil || time.Now().After(c.NotAfter) {
		if c, err = SelfSigned(host); err != nil {
			return nil, err
		}
		if err := st.SaveCertificate(ctx, c); err != nil {
			return nil, err
		}
		log.Warn("the mail ports have a self-signed certificate until a real one is issued", "host", host)
	}
	return m, m.Use(c)
}

// Use swaps a certificate in for every new connection.
func (m *Manager) Use(c *store.Certificate) error {
	pair, err := tls.X509KeyPair([]byte(c.Chain), []byte(c.Key))
	if err != nil {
		return fmt.Errorf("certificate for %s: %w", c.Host, err)
	}
	m.cur.Store(&pair)
	m.meta.Store(c)
	return nil
}

// Current is what is being served.
func (m *Manager) Current() *store.Certificate { return m.meta.Load() }

// Host is the name the certificate is for.
func (m *Manager) Host() string { return m.host }

// TLSConfig is the configuration every mail listener uses.
func (m *Manager) TLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return m.cur.Load(), nil
		},
	}
}

// SelfSigned makes a year's certificate for host, signed by itself.
func SelfSigned(host string) (*store.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	return &store.Certificate{
		Host:     host,
		Chain:    string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		Key:      string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})),
		Issuer:   "self",
		NotAfter: tmpl.NotAfter,
	}, nil
}
