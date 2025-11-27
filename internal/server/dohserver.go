package server

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"doh-autoproxy/internal/config"
	"doh-autoproxy/internal/router"
	"doh-autoproxy/internal/util"

	"github.com/miekg/dns"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

type DoHServer struct {
	http2Server *http.Server
	http3Server *http3.Server
	router      *router.Router
	cfg         *config.Config
}

func NewDoHServer(cfg *config.Config, r *router.Router, cm *util.CertManager) *DoHServer {
	dohHandler := &DoHRequestHandler{router: r}

	var tlsConfig *tls.Config

	if cm != nil && cm.GetCertificateFunc() != nil {
		log.Println("DoH: Using AutoCert for TLS")
		tlsConfig = &tls.Config{
			GetCertificate: cm.GetCertificateFunc(),
			NextProtos:     []string{"h3", "h2", "http/1.1"},
		}
	} else {
		certs, err := util.LoadServerCertificate("server.crt", "server.key")
		if err != nil {
			log.Printf("Warning: DoH 服务器无法加载证书: %v", err)
			return nil
		}
		tlsConfig = &tls.Config{
			Certificates: certs,
			NextProtos:   []string{"h3", "h2", "http/1.1"},
		}
	}

	http2Server := &http.Server{
		Addr:         cfg.Listen.DOH,
		Handler:      dohHandler,
		TLSConfig:    tlsConfig,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	http3Server := &http3.Server{
		Addr:      cfg.Listen.DOH,
		TLSConfig: tlsConfig,
		Handler:   dohHandler,
		QUICConfig: &quic.Config{
			MaxIdleTimeout: 30 * time.Second,
		},
	}

	return &DoHServer{
		http2Server: http2Server,
		http3Server: http3Server,
		router:      r,
		cfg:         cfg,
	}
}

func (s *DoHServer) Start() {
	if s.http2Server == nil || s.http3Server == nil {
		log.Println("DoH 服务器未完全初始化，可能因为证书加载失败。")
		return
	}

	go func() {
		log.Printf("Starting DoH (HTTP/1.1, HTTP/2) server on %s", s.http2Server.Addr)
		err := s.http2Server.ListenAndServeTLS("", "")
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("无法启动DoH (HTTP/1.1, HTTP/2) 服务器: %v", err)
		}
	}()

	go func() {
		log.Printf("Starting DoH (HTTP/3) server on %s", s.http3Server.Addr)

		udpPort := util.ParsePort(s.http3Server.Addr)
		udpAddr := &net.UDPAddr{Port: udpPort}
		udpConn, err := net.ListenUDP("udp", udpAddr)
		if err != nil {
			log.Fatalf("无法监听UDP端口用于HTTP/3: %v", err)
		}
		defer udpConn.Close()

		err = s.http3Server.Serve(udpConn)
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("无法启动DoH (HTTP/3) 服务器: %v", err)
		}
	}()
}

type DoHRequestHandler struct {
	router *router.Router
}

func (h *DoHRequestHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/dns-query" {
		http.NotFound(w, r)
		return
	}

	var dnsMsg []byte
	var err error

	switch r.Method {
	case http.MethodGet:
		dnsParam := r.URL.Query().Get("dns")
		if dnsParam == "" {
			http.Error(w, "缺少dns查询参数", http.StatusBadRequest)
			return
		}
		dnsMsg, err = base64.RawURLEncoding.DecodeString(dnsParam)
		if err != nil {
			http.Error(w, "无法解码dns查询参数", http.StatusBadRequest)
			return
		}
	case http.MethodPost:
		if r.Header.Get("Content-Type") != "application/dns-message" {
			http.Error(w, "Content-Type必须是application/dns-message", http.StatusUnsupportedMediaType)
			return
		}
		dnsMsg, err = ioutil.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "无法读取请求体", http.StatusBadRequest)
			return
		}
	default:
		http.Error(w, "不支持的HTTP方法", http.StatusMethodNotAllowed)
		return
	}

	req := new(dns.Msg)
	if err := req.Unpack(dnsMsg); err != nil {
		http.Error(w, fmt.Sprintf("无法解包DNS消息: %v", err), http.StatusBadRequest)
		return
	}

	if len(req.Question) == 0 {
		http.Error(w, "DNS请求中没有问题", http.StatusBadRequest)
		return
	}

	qName := strings.ToLower(strings.TrimSuffix(req.Question[0].Name, "."))
	log.Printf("Received DoH query for %s (Type: %s, From: %s, Proto: %s)", qName, dns.Type(req.Question[0].Qtype).String(), r.RemoteAddr, r.Proto)

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	resp, err := h.router.Route(ctx, req)
	if err != nil {
		log.Printf("Error routing DoH query for %s: %v", qName, err)
		resp = new(dns.Msg)
		resp.SetRcode(req, dns.RcodeServerFailure)
	}

	packedResp, err := resp.Pack()
	if err != nil {
		http.Error(w, fmt.Sprintf("无法打包DNS响应: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/dns-message")
	w.Write(packedResp)
}
