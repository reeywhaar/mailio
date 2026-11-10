// Package app holds what this program is, as distinct from what it was told.
//
// The line against internal/config: whether an operator can change it without rebuilding.
package app

const Name = "mailio"

const ProjectURL = "https://github.com/reeywhaar/mailio"

// The ports, fixed inside the container. Remap with `docker run -p`.
const (
	// ListenAddr is the admin interface, plain HTTP behind a proxy.
	ListenAddr = ":80"
	// SMTPAddr is where other servers deliver: STARTTLS offered, no AUTH.
	SMTPAddr = ":25"
	// SubmissionAddr is where accounts send, STARTTLS required before AUTH.
	SubmissionAddr = ":587"
	// SubmissionsAddr is where accounts send, TLS from the first byte.
	SubmissionsAddr = ":465"
	// IMAPAddr is where accounts read, TLS from the first byte.
	IMAPAddr = ":993"
)

// Version is stamped at link time:
//
//	-ldflags "-X mailio/internal/app.Version=$(git rev-parse --short HEAD)"
var Version = "dev"

// PasswordRefused is the message of the one line logged for every wrong password, with the
// caller's "ip" and "via": admin, imap or submission. fail2ban matches it, so it is a contract:
// see docs/deploy.md.
const PasswordRefused = "password refused"
