// Package dkimkey makes, publishes and signs with each domain's DKIM key.
package dkimkey

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/emersion/go-msgauth/dkim"
)

// Selector is the name every key is published under: <selector>._domainkey.<domain>. Not "mail",
// which the Postfix-based mailio used, so both can be published while one replaces the other.
const Selector = "mailio"

// Generate makes an RSA-2048 key as PEM. RSA rather than Ed25519, which a large share of
// receivers still cannot verify.
func Generate() (string, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), nil
}

func parse(keyPEM string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(keyPEM))
	if block == nil {
		return nil, errors.New("dkim key: not PEM")
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rk, ok := k.(*rsa.PrivateKey); ok {
			return rk, nil
		}
		return nil, errors.New("dkim key: not RSA")
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}

// Record is the TXT value to publish for a key.
func Record(keyPEM string) (string, error) {
	key, err := parse(keyPEM)
	if err != nil {
		return "", err
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return "", err
	}
	return "v=DKIM1; k=rsa; p=" + base64.StdEncoding.EncodeToString(der), nil
}

// Sign writes the message with a DKIM-Signature for domain prepended.
func Sign(w io.Writer, r io.Reader, domain, selector, keyPEM string) error {
	key, err := parse(keyPEM)
	if err != nil {
		return err
	}
	return dkim.Sign(w, r, &dkim.SignOptions{
		Domain:   domain,
		Selector: selector,
		Signer:   crypto.Signer(key),
		// What a forwarder or list rewrites least, and what a forger most wants to change.
		HeaderKeys: []string{"From", "To", "Cc", "Subject", "Date", "Message-ID", "Reply-To",
			"In-Reply-To", "References", "MIME-Version", "Content-Type", "Content-Transfer-Encoding"},
		HeaderCanonicalization: dkim.CanonicalizationRelaxed,
		BodyCanonicalization:   dkim.CanonicalizationRelaxed,
	})
}

// RecordName is where Record is published.
func RecordName(domain, selector string) string {
	return fmt.Sprintf("%s._domainkey.%s", selector, strings.TrimSuffix(domain, "."))
}
