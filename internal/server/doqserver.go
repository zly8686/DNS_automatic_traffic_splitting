package server

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"io"
	"log"
	"net"
	"strings"
	"time"

	"doh-autoproxy/internal/config"
	"doh-autoproxy/internal/router"
	"doh-autoproxy/internal/util"

	"github.com/miekg/dns"
	"github.com/quic-go/quic-go"
)

type DoQServer struct {
	addr   string
	router *router.Router
	cfg    *config.Config
	cm     *util.CertManager
}

func NewDoQServer(cfg *config.Config, r *router.Router, cm *util.CertManager) *DoQServer {
	return &DoQServer{
		addr:   cfg.Listen.DOQ,
		router: r,
		cfg:    cfg,
		cm:     cm,
	}
}

func (s *DoQServer) Start() {
	var tlsConfig *tls.Config

	if s.cm != nil && s.cm.GetCertificateFunc() != nil {
		log.Println("DoQ: Using AutoCert for TLS")
		tlsConfig = &tls.Config{
			GetCertificate: s.cm.GetCertificateFunc(),
			NextProtos:     []string{"doq"},
		}
	} else {
		certs, err := util.LoadServerCertificate("server.crt", "server.key")
		if err != nil {
			log.Printf("Warning: DoQ 服务器无法加载证书: %v", err)
			return
		}
		tlsConfig = &tls.Config{
			Certificates: certs,
			NextProtos:   []string{"doq"},
		}
	}

	quicConfig := &quic.Config{
		MaxIdleTimeout: 30 * time.Second,
	}

	go func() {
		log.Printf("Starting DoQ server on %s", s.addr)
		listener, err := quic.ListenAddr(s.addr, tlsConfig, quicConfig)
		if err != nil {
			log.Printf("无法启动DoQ服务器: %v", err)
			return
		}
		defer listener.Close()

		for {
			conn, err := listener.Accept(context.Background())
			if err != nil {
				log.Printf("接受QUIC连接失败: %v", err)
				continue
			}
			go s.handleQuicConnection(conn)
		}
	}()
}

func (s *DoQServer) handleQuicConnection(conn *quic.Conn) {
	log.Printf("DoQ: New connection from %s", conn.RemoteAddr())
	defer conn.CloseWithError(quic.ApplicationErrorCode(quic.NoError), "Connection closed")

	for {
		stream, err := conn.AcceptStream(context.Background())
		if err != nil {
			log.Printf("DoQ: 接受流失败: %v", err)
			return
		}
		go s.handleQuicStream(stream, conn.RemoteAddr())
	}
}

func (s *DoQServer) handleQuicStream(stream *quic.Stream, remoteAddr net.Addr) {
	defer stream.Close()

	lengthBytes := make([]byte, 2)
	if _, err := io.ReadFull(stream, lengthBytes); err != nil {
		if err != io.EOF {
			log.Printf("DoQ: 读取DNS消息长度失败: %v", err)
		}
		return
	}
	dnsMsgLen := binary.BigEndian.Uint16(lengthBytes)

	msgBuf := make([]byte, dnsMsgLen)
	if _, err := io.ReadFull(stream, msgBuf); err != nil {
		log.Printf("DoQ: 读取DNS消息失败: %v", err)
		return
	}

	req := new(dns.Msg)
	if err := req.Unpack(msgBuf); err != nil {
		log.Printf("DoQ: 解包DNS消息失败: %v", err)
		return
	}

	if len(req.Question) == 0 {
		log.Printf("DoQ: 收到空问题查询 from %s", remoteAddr)
		return
	}

	qName := strings.ToLower(strings.TrimSuffix(req.Question[0].Name, "."))
	log.Printf("Received DoQ query for %s (Type: %s, From: %s)", qName, dns.Type(req.Question[0].Qtype).String(), remoteAddr.String())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := s.router.Route(ctx, req)
	if err != nil {
		log.Printf("DoQ: Error routing DNS query for %s: %v", qName, err)
		resp = new(dns.Msg)
		resp.SetRcode(req, dns.RcodeServerFailure)
	}

	packedResp, err := resp.Pack()
	if err != nil {
		log.Printf("DoQ: 打包响应消息失败: %v", err)
		return
	}

	responseLength := make([]byte, 2)
	binary.BigEndian.PutUint16(responseLength, uint16(len(packedResp)))

	if _, err := stream.Write(responseLength); err != nil {
		log.Printf("DoQ: 写入响应长度失败: %v", err)
		return
	}
	if _, err := stream.Write(packedResp); err != nil {
		log.Printf("DoQ: 写入响应体失败: %v", err)
		return
	}
}
