package api

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// clientIP is who sent the request: the address fail2ban bans.
//
// Behind Caddy that is the last X-Forwarded-For hop, the one Caddy wrote; anything before it is
// whatever the caller sent. The header is believed only from a private or loopback peer, which is
// where a proxy on the same host connects from. From anywhere else it is the caller's own claim,
// and believing it would let a stranger name the address that gets banned. See docs/deploy.md.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	peer = peer.Unmap()
	if !peer.IsPrivate() && !peer.IsLoopback() {
		return peer.String()
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	if last, err := netip.ParseAddr(strings.TrimSpace(hops[len(hops)-1])); err == nil {
		return last.Unmap().String()
	}
	return peer.String()
}
