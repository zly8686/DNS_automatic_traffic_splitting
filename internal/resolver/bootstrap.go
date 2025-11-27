package resolver

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"time"
)

type Bootstrapper struct {
	servers []string
	counter uint64
}

func NewBootstrapper(servers []string) *Bootstrapper {
	normalized := make([]string, len(servers))
	for i, s := range servers {
		if _, _, err := net.SplitHostPort(s); err != nil {
			normalized[i] = net.JoinHostPort(s, "53")
		} else {
			normalized[i] = s
		}
	}
	return &Bootstrapper{servers: normalized}
}

func (b *Bootstrapper) LookupIP(ctx context.Context, host string) (string, error) {
	if ip := net.ParseIP(host); ip != nil {
		return host, nil
	}

	if len(b.servers) == 0 {
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return "", err
		}
		if len(ips) == 0 {
			return "", fmt.Errorf("no IP found for %s", host)
		}
		return ips[0].String(), nil
	}

	idx := atomic.AddUint64(&b.counter, 1)
	server := b.servers[idx%uint64(len(b.servers))]

	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{
				Timeout: 5 * time.Second,
			}
			return d.DialContext(ctx, "udp", server)
		},
	}

	ips, err := r.LookupIPAddr(ctx, host)
	if err != nil {
		return "", err
	}
	if len(ips) == 0 {
		return "", fmt.Errorf("no IP found for %s via bootstrap %s", host, server)
	}

	return ips[0].String(), nil
}
