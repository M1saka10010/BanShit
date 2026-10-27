package app

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

func proxyPrefix(raw string) (netip.Prefix, error) {
	if ip, err := netip.ParseAddr(raw); err == nil && ip.Zone() == "" {
		ip = ip.Unmap()
		return netip.PrefixFrom(ip, ip.BitLen()), nil
	}
	p, err := netip.ParsePrefix(raw)
	if err != nil || p.Bits() == 0 || p.Addr().Is4In6() {
		return netip.Prefix{}, fmt.Errorf("trustedProxies 必须为明确的 IP 或 CIDR，不能使用 /0: %q", raw)
	}
	return p.Masked(), nil
}

func validateTrustedProxies(values []string) error {
	if len(values) > 64 {
		return fmt.Errorf("trustedProxies 最多64项")
	}
	for _, raw := range values {
		if _, err := proxyPrefix(raw); err != nil {
			return err
		}
	}
	return nil
}

// Only trusted peers may supply X-Forwarded-For. Walk right to left,
// stopping at the first untrusted hop. Invalid chains fall back to the peer.
func (a *App) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	peer = peer.Unmap()
	proxies := a.snapshot().TrustedProxies
	trusted := func(ip netip.Addr) bool {
		for _, raw := range proxies {
			p, err := proxyPrefix(raw)
			if err == nil && p.Contains(ip) {
				return true
			}
		}
		return false
	}
	if !trusted(peer) {
		return peer.String()
	}
	forwarded := strings.Join(r.Header.Values("X-Forwarded-For"), ",")
	if len(forwarded) > 4096 {
		return peer.String()
	}
	parts := strings.Split(forwarded, ",")
	if len(parts) > 64 {
		return peer.String()
	}
	current := peer
	for i := len(parts) - 1; i >= 0 && trusted(current); i-- {
		ip, err := netip.ParseAddr(strings.TrimSpace(parts[i]))
		if err != nil || ip.Zone() != "" {
			return peer.String()
		}
		current = ip.Unmap()
	}
	return current.String()
}
