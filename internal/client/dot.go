package client

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"time"

	"doh-autoproxy/internal/config"
	"doh-autoproxy/internal/resolver"

	"github.com/miekg/dns"
)

type DoTClient struct {
	cfg          config.UpstreamServer
	bootstrapper *resolver.Bootstrapper
	conn         *dns.Conn
	lock         sync.Mutex
}

func NewDoTClient(cfg config.UpstreamServer, b *resolver.Bootstrapper) *DoTClient {
	return &DoTClient{
		cfg:          cfg,
		bootstrapper: b,
	}
}

func (c *DoTClient) Resolve(ctx context.Context, req *dns.Msg) (*dns.Msg, error) {
	ensureECS(req, c.cfg.ECSIP)

	if c.cfg.EnablePipeline {
		return c.resolvePipeline(ctx, req)
	}
	return c.resolveOneshot(ctx, req)
}

func (c *DoTClient) resolveOneshot(ctx context.Context, req *dns.Msg) (*dns.Msg, error) {
	addr, tlsConfig, err := c.prepare(ctx)
	if err != nil {
		return nil, err
	}

	cli := &dns.Client{
		Net:       "tcp-tls",
		Timeout:   5 * time.Second,
		TLSConfig: tlsConfig,
	}

	resp, _, err := cli.ExchangeContext(ctx, req, addr)
	if err != nil {
		return nil, fmt.Errorf("DoT查询失败: %w", err)
	}
	return resp, nil
}

func (c *DoTClient) resolvePipeline(ctx context.Context, req *dns.Msg) (*dns.Msg, error) {
	c.lock.Lock()
	defer c.lock.Unlock()

	if c.conn == nil {
		if err := c.dial(ctx); err != nil {
			return nil, err
		}
	}

	c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := c.conn.WriteMsg(req); err != nil {
		c.conn.Close()
		c.conn = nil
		if err := c.dial(ctx); err != nil {
			return nil, fmt.Errorf("重连失败: %w", err)
		}
		c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if err := c.conn.WriteMsg(req); err != nil {
			return nil, fmt.Errorf("写入失败: %w", err)
		}
	}

	c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp, err := c.conn.ReadMsg()
	if err != nil {
		c.conn.Close()
		c.conn = nil
		return nil, fmt.Errorf("读取失败: %w", err)
	}

	if resp.Id != req.Id {
		return nil, fmt.Errorf("ID mismatch")
	}

	return resp, nil
}

func (c *DoTClient) prepare(ctx context.Context) (string, *tls.Config, error) {
	rawAddr := c.cfg.Address
	if len(rawAddr) > 6 && rawAddr[:6] == "tls://" {
		rawAddr = rawAddr[6:]
	}

	host, port, err := net.SplitHostPort(rawAddr)
	if err != nil {
		return "", nil, fmt.Errorf("invalid address %s: %w", c.cfg.Address, err)
	}

	ip, err := c.bootstrapper.LookupIP(ctx, host)
	if err != nil {
		return "", nil, fmt.Errorf("bootstrap failed: %w", err)
	}

	addr := net.JoinHostPort(ip, port)
	tlsConfig := &tls.Config{
		ServerName:         host,
		InsecureSkipVerify: c.cfg.InsecureSkipVerify,
	}

	return addr, tlsConfig, nil
}

func (c *DoTClient) dial(ctx context.Context) error {
	addr, tlsConfig, err := c.prepare(ctx)
	if err != nil {
		return err
	}

	cli := &dns.Client{
		Net:       "tcp-tls",
		Timeout:   5 * time.Second,
		TLSConfig: tlsConfig,
	}
	conn, err := cli.Dial(addr)
	if err != nil {
		return err
	}
	c.conn = conn
	return nil
}
