package server

import (
	"context"
	"log"
	"strings"
	"time"

	"doh-autoproxy/internal/config"
	"doh-autoproxy/internal/router"

	"github.com/miekg/dns"
)

type DNSServer struct {
	udpServer *dns.Server
	tcpServer *dns.Server
	router    *router.Router
}

func NewDNSServer(cfg *config.Config, r *router.Router) *DNSServer {
	handler := &DNSRequestHandler{router: r}

	var udpServer, tcpServer *dns.Server

	if cfg.Listen.DNSUDP != "" {
		udpServer = &dns.Server{Addr: cfg.Listen.DNSUDP, Net: "udp", Handler: handler, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second}
	}

	if cfg.Listen.DNSTCP != "" {
		tcpServer = &dns.Server{Addr: cfg.Listen.DNSTCP, Net: "tcp", Handler: handler, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second}
	}

	return &DNSServer{
		udpServer: udpServer,
		tcpServer: tcpServer,
		router:    r,
	}
}

func (s *DNSServer) Start() {
	if s.udpServer != nil {
		go func() {
			log.Printf("Starting UDP DNS server on %s", s.udpServer.Addr)
			err := s.udpServer.ListenAndServe()
			if err != nil {
				log.Printf("无法启动UDP DNS服务器: %v", err)
			}
		}()
	}

	if s.tcpServer != nil {
		go func() {
			log.Printf("Starting TCP DNS server on %s", s.tcpServer.Addr)
			err := s.tcpServer.ListenAndServe()
			if err != nil {
				log.Printf("无法启动TCP DNS服务器: %v", err)
			}
		}()
	}
}

type DNSRequestHandler struct {
	router *router.Router
}

func (h *DNSRequestHandler) ServeDNS(w dns.ResponseWriter, req *dns.Msg) {
	if len(req.Question) == 0 {
		dns.HandleFailed(w, req)
		return
	}

	qName := strings.ToLower(strings.TrimSuffix(req.Question[0].Name, "."))
	log.Printf("Received DNS query for %s (Type: %s, From: %s)", qName, dns.Type(req.Question[0].Qtype).String(), w.RemoteAddr().String())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := h.router.Route(ctx, req)
	if err != nil {
		log.Printf("Error routing DNS query for %s: %v", qName, err)
		dns.HandleFailed(w, req)
		return
	}

	resp.SetRcode(req, resp.Rcode)
	w.WriteMsg(resp)
}
