package api

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// clientIP is who sent the request: the address fail2ban bans.
//
// X-Forwarded-For is read only from a trusted peer, right to left: each proxy appends who it
// accepted, so the first hop that is not a trusted proxy is the caller, and everything left of it
// is whatever the caller sent. From any other peer it is the caller's own claim, and believing it
// would let a stranger name the address that gets banned. X-Real-IP is never read, since a proxy
// that does not set it passes the caller's along. See docs/deploy.md.
func (s *Server) clientIP(r *http.Request) string {
	peer, ok := peerOf(r)
	if !ok {
		return r.RemoteAddr
	}
	trusted := s.cfg.TrustedProxies
	if !trusted.Trusts(peer) {
		return peer.String()
	}
	client := peer
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		hop, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break
		}
		client = hop.Unmap()
		if !trusted.Trusts(client) {
			break
		}
	}
	return client.String()
}

// peerOf is the address the connection itself came from.
func peerOf(r *http.Request) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}
