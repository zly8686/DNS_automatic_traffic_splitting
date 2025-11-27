package client

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"doh-autoproxy/internal/config"
	"doh-autoproxy/internal/resolver"

	"github.com/miekg/dns"
)

type TCPClient struct {
	cfg          config.UpstreamServer
	bootstrapper *resolver.Bootstrapper
	conn         *dns.Conn
	lock         sync.Mutex
}

func NewTCPClient(cfg config.UpstreamServer, b *resolver.Bootstrapper) *TCPClient {
	return &TCPClient{
		cfg:          cfg,
		bootstrapper: b,
	}
}

func (c *TCPClient) Resolve(ctx context.Context, req *dns.Msg) (*dns.Msg, error) {
	ensureECS(req, c.cfg.ECSIP)

	if c.cfg.EnablePipeline {
		return c.resolvePipeline(ctx, req)
	}
	return c.resolveOneshot(ctx, req)
}

func (c *TCPClient) resolveOneshot(ctx context.Context, req *dns.Msg) (*dns.Msg, error) {
	addr, err := c.resolveAddr(ctx)
	if err != nil {
		return nil, err
	}

	cli := &dns.Client{
		Net:     "tcp",
		Timeout: 5 * time.Second,
	}

	resp, _, err := cli.ExchangeContext(ctx, req, addr)
	if err != nil {
		return nil, fmt.Errorf("TCP查询失败: %w", err)
	}
	return resp, nil
}

func (c *TCPClient) resolvePipeline(ctx context.Context, req *dns.Msg) (*dns.Msg, error) {
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

func (c *TCPClient) dial(ctx context.Context) error {
	addr, err := c.resolveAddr(ctx)
	if err != nil {
		return err
	}

	cli := &dns.Client{Net: "tcp", Timeout: 5 * time.Second}
	conn, err := cli.Dial(addr)
	if err != nil {
		return err
	}
	c.conn = conn
	return nil
}

func (c *TCPClient) resolveAddr(ctx context.Context) (string, error) {
	host, port, err := net.SplitHostPort(c.cfg.Address)
	if err != nil {
		return "", fmt.Errorf("invalid address %s: %w", c.cfg.Address, err)
	}
	ip, err := c.bootstrapper.LookupIP(ctx, host)
	if err != nil {
		return "", fmt.Errorf("bootstrap failed: %w", err)
	}
	return net.JoinHostPort(ip, port), nil
}
