package router

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"

	"doh-autoproxy/internal/client"
	"doh-autoproxy/internal/config"
	"doh-autoproxy/internal/resolver"

	"github.com/miekg/dns"
)

type Router struct {
	config          *config.Config
	geo             *GeoDataManager
	cnClients       []client.DNSClient
	overseasClients []client.DNSClient
}

func NewRouter(cfg *config.Config, geoManager *GeoDataManager) *Router {
	r := &Router{
		config: cfg,
		geo:    geoManager,
	}

	bootstrapper := resolver.NewBootstrapper(cfg.BootstrapDNS)

	for _, upstreamCfg := range cfg.Upstreams.CN {
		c, err := client.NewDNSClient(upstreamCfg, bootstrapper)
		if err != nil {
			log.Printf("Failed to initialize CN upstream %s: %v", upstreamCfg.Address, err)
			continue
		}
		r.cnClients = append(r.cnClients, c)
	}

	for _, upstreamCfg := range cfg.Upstreams.Overseas {
		c, err := client.NewDNSClient(upstreamCfg, bootstrapper)
		if err != nil {
			log.Printf("Failed to initialize Overseas upstream %s: %v", upstreamCfg.Address, err)
			continue
		}
		r.overseasClients = append(r.overseasClients, c)
	}

	return r
}

func (r *Router) Route(ctx context.Context, req *dns.Msg) (*dns.Msg, error) {
	qName := strings.ToLower(strings.TrimSuffix(req.Question[0].Name, "."))

	if ipStr, ok := r.config.Hosts[qName]; ok {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			return nil, fmt.Errorf("自定义Hosts中存在无效IP地址: %s for %s", ipStr, qName)
		}

		m := new(dns.Msg)
		m.SetReply(req)
		rrHeader := dns.RR_Header{
			Name:   req.Question[0].Name,
			Rrtype: dns.TypeA,
			Class:  dns.ClassINET,
			Ttl:    60,
		}
		if ipv4 := ip.To4(); ipv4 != nil {
			m.Answer = append(m.Answer, &dns.A{Hdr: rrHeader, A: ipv4})
		} else {
			rrHeader.Rrtype = dns.TypeAAAA
			m.Answer = append(m.Answer, &dns.AAAA{Hdr: rrHeader, AAAA: ip})
		}
		return m, nil
	}

	if rule, ok := r.config.Rules[qName]; ok {
		switch strings.ToLower(rule) {
		case "cn":
			return client.RaceResolve(ctx, req, r.cnClients)
		case "overseas":
			return client.RaceResolve(ctx, req, r.overseasClients)
		default:
			return nil, fmt.Errorf("自定义规则中存在未知路由目标: %s for %s", rule, qName)
		}
	}

	if geoSiteRule := r.geo.LookupGeoSite(qName); geoSiteRule != "" {
		switch strings.ToLower(geoSiteRule) {
		case "cn":
			return client.RaceResolve(ctx, req, r.cnClients)
		default:
			return client.RaceResolve(ctx, req, r.overseasClients)
		}
	}

	resp, err := client.RaceResolve(ctx, req, r.overseasClients)
	if err != nil {
		return nil, fmt.Errorf("GeoIP分流时首次海外解析失败: %w", err)
	}

	var resolvedIP net.IP
	for _, ans := range resp.Answer {
		if a, ok := ans.(*dns.A); ok {
			resolvedIP = a.A
			break
		}
		if aaaa, ok := ans.(*dns.AAAA); ok {
			resolvedIP = aaaa.AAAA
			break
		}
	}

	if resolvedIP != nil && r.geo.IsCNIP(resolvedIP) {
		return client.RaceResolve(ctx, req, r.cnClients)
	}

	return resp, nil
}
