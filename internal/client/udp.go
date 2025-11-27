package client

import (
	"context"
	"fmt"
	"net"
	"time"

	"doh-autoproxy/internal/config"
	"doh-autoproxy/internal/resolver"

	"github.com/miekg/dns"
)

type UDPClient struct {
	cfg          config.UpstreamServer
	bootstrapper *resolver.Bootstrapper
}

func NewUDPClient(cfg config.UpstreamServer, b *resolver.Bootstrapper) *UDPClient {
	return &UDPClient{
		cfg:          cfg,
		bootstrapper: b,
	}
}

func (c *UDPClient) Resolve(ctx context.Context, req *dns.Msg) (*dns.Msg, error) {
	host, port, err := net.SplitHostPort(c.cfg.Address)
	if err != nil {
		return nil, fmt.Errorf("invalid address %s: %w", c.cfg.Address, err)
	}

	ip, err := c.bootstrapper.LookupIP(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("bootstrap failed for %s: %w", host, err)
	}

	addr := net.JoinHostPort(ip, port)

	cli := &dns.Client{
		Net:     "udp",
		Timeout: 5 * time.Second,
	}

	ensureECS(req, c.cfg.ECSIP)

	resp, _, err := cli.ExchangeContext(ctx, req, addr)
	if err != nil {
		return nil, fmt.Errorf("UDP查询失败: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("UDP查询无响应")
	}

	return resp, nil
}
