package httpapi

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// TrustedProxies are the networks whose X-Forwarded-For entries are believed.
// The services listen on loopback behind the gateway, so every request reaches
// them from a loopback address; without this, all players would share the
// gateway's rate-limit bucket. Service mains replace it from
// MINIPLAY_TRUSTED_PROXIES.
var TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128")}

// ParseTrustedProxies reads a comma-separated list of CIDRs or single addresses.
func ParseTrustedProxies(value string) ([]netip.Prefix, error) {
	var prefixes []netip.Prefix
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(part); err == nil {
			prefixes = append(prefixes, prefix.Masked())
			continue
		}
		addr, err := netip.ParseAddr(part)
		if err != nil {
			return nil, fmt.Errorf("trusted proxy %q is not an address or CIDR", part)
		}
		prefixes = append(prefixes, netip.PrefixFrom(addr.Unmap(), addr.Unmap().BitLen()))
	}
	return prefixes, nil
}

func trusted(addr netip.Addr) bool {
	for _, prefix := range TrustedProxies {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// clientIP returns the address rate limits should key on. A direct peer that is
// not a trusted proxy is the client, and its X-Forwarded-For is ignored.
// Otherwise the forwarded chain is read from the right, skipping trusted
// proxies; entries further left were written by the client and could be forged.
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
	if !trusted(peer) {
		return peer.String()
	}
	var hops []string
	for _, line := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(line, ",")...)
	}
	client := peer
	for i := len(hops) - 1; i >= 0; i-- {
		addr, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break
		}
		client = addr.Unmap()
		if !trusted(client) {
			break
		}
	}
	return client.String()
}
