package client

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"doh-autoproxy/internal/config"
	"doh-autoproxy/internal/resolver"

	"github.com/miekg/dns"
	"github.com/quic-go/quic-go"
)

type DoQClient struct {
	cfg          config.UpstreamServer
	bootstrapper *resolver.Bootstrapper
}

func NewDoQClient(cfg config.UpstreamServer, b *resolver.Bootstrapper) *DoQClient {
	return &DoQClient{
		cfg:          cfg,
		bootstrapper: b,
	}
}

func (c *DoQClient) Resolve(ctx context.Context, req *dns.Msg) (*dns.Msg, error) {
	ensureECS(req, c.cfg.ECSIP)

	msgBuf, err := req.Pack()
	if err != nil {
		return nil, fmt.Errorf("打包DNS消息失败: %w", err)
	}

	addrStr := strings.TrimPrefix(c.cfg.Address, "quic://")
	if !strings.Contains(addrStr, ":") {
		addrStr = net.JoinHostPort(addrStr, "853")
	}

	host, port, err := net.SplitHostPort(addrStr)
	if err != nil {
		return nil, err
	}

	ip, err := c.bootstrapper.LookupIP(ctx, host)
	if err != nil {
		return nil, err
	}

	targetAddr := net.JoinHostPort(ip, port)

	tlsConfig := &tls.Config{
		ServerName:         host,
		InsecureSkipVerify: c.cfg.InsecureSkipVerify,
		NextProtos:         []string{"doq"},
	}

	quicConfig := &quic.Config{
		MaxIdleTimeout: 10 * time.Second,
	}

	conn, err := quic.DialAddr(ctx, targetAddr, tlsConfig, quicConfig)
	if err != nil {
		return nil, fmt.Errorf("建立QUIC连接失败: %w", err)
	}
	defer conn.CloseWithError(quic.ApplicationErrorCode(quic.NoError), "Connection closed")

	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return nil, fmt.Errorf("打开QUIC流失败: %w", err)
	}
	defer stream.Close()

	length := make([]byte, 2)
	binary.BigEndian.PutUint16(length, uint16(len(msgBuf)))
	if _, err := stream.Write(length); err != nil {
		return nil, fmt.Errorf("写入DNS消息长度失败: %w", err)
	}
	if _, err := stream.Write(msgBuf); err != nil {
		return nil, fmt.Errorf("写入DNS消息失败: %w", err)
	}

	responseLengthBytes := make([]byte, 2)
	if _, err := io.ReadFull(stream, responseLengthBytes); err != nil {
		return nil, fmt.Errorf("读取DoQ响应长度失败: %w", err)
	}
	responseLength := binary.BigEndian.Uint16(responseLengthBytes)

	respBuf := make([]byte, responseLength)
	if _, err := io.ReadFull(stream, respBuf); err != nil {
		return nil, fmt.Errorf("读取DoQ响应体失败: %w", err)
	}

	responseMsg := new(dns.Msg)
	err = responseMsg.Unpack(respBuf)
	if err != nil {
		return nil, fmt.Errorf("解包DoQ响应消息失败: %w", err)
	}

	return responseMsg, nil
}
