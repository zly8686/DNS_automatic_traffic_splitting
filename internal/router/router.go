package router

import (
	"context"
	"fmt"
	"log"
	"net"
	"regexp"
	"strings"
	"time"

	"doh-autoproxy/internal/client"
	"doh-autoproxy/internal/config"
	"doh-autoproxy/internal/querylog"
	"doh-autoproxy/internal/resolver"

	"github.com/miekg/dns"
)

type RegexRule struct {
	Pattern *regexp.Regexp
	Target  string
}

type Router struct {
	config          *config.Config
	geo             *GeoDataManager
	logger          *querylog.QueryLogger
	cnClients       []client.DNSClient
	overseasClients []client.DNSClient

	cnStats       []*client.StatsClient
	overseasStats []*client.StatsClient

	regexRules []RegexRule
}

func NewRouter(cfg *config.Config, geoManager *GeoDataManager, logger *querylog.QueryLogger) *Router {
	r := &Router{
		config: cfg,
		geo:    geoManager,
		logger: logger,
	}

	for domain, target := range cfg.Rules {
		if strings.HasPrefix(domain, "regexp:") {
			pattern := strings.TrimPrefix(domain, "regexp:")
			re, err := regexp.Compile(pattern)
			if err != nil {
				log.Printf("忽略无效的正则规则: %s -> %v", domain, err)
				continue
			}
			r.regexRules = append(r.regexRules, RegexRule{
				Pattern: re,
				Target:  target,
			})
		}
	}

	bootstrapper := resolver.NewBootstrapper(cfg.BootstrapDNS)

	for _, upstreamCfg := range cfg.Upstreams.CN {
		c, err := client.NewDNSClient(upstreamCfg, bootstrapper)
		if err != nil {
			log.Printf("Failed to initialize CN upstream %s: %v", upstreamCfg.Address, err)
			continue
		}
		sc := client.NewStatsClient(c, upstreamCfg.Address, upstreamCfg.Protocol, "CN")
		r.cnClients = append(r.cnClients, sc)
		r.cnStats = append(r.cnStats, sc)
	}

	for _, upstreamCfg := range cfg.Upstreams.Overseas {
		c, err := client.NewDNSClient(upstreamCfg, bootstrapper)
		if err != nil {
			log.Printf("Failed to initialize Overseas upstream %s: %v", upstreamCfg.Address, err)
			continue
		}
		sc := client.NewStatsClient(c, upstreamCfg.Address, upstreamCfg.Protocol, "Overseas")
		r.overseasClients = append(r.overseasClients, sc)
		r.overseasStats = append(r.overseasStats, sc)
	}

	return r
}

func (r *Router) GetUpstreamStats() []interface{} {
	var stats []interface{}
	for _, s := range r.cnStats {
		stats = append(stats, s.GetStats())
	}
	for _, s := range r.overseasStats {
		stats = append(stats, s.GetStats())
	}
	return stats
}

func (r *Router) Route(ctx context.Context, req *dns.Msg, clientIP string) (*dns.Msg, error) {
	start := time.Now()
	if len(req.Question) == 0 {
		return nil, fmt.Errorf("no question")
	}

	resp, upstream, err := r.routeInternal(ctx, req)

	duration := time.Since(start).Milliseconds()

	qName := req.Question[0].Name
	qType := dns.Type(req.Question[0].Qtype).String()

	status := "ERROR"
	answer := ""
	var answerRecords []querylog.AnswerRecord

	if err == nil && resp != nil {
		status = dns.RcodeToString[resp.Rcode]
		if len(resp.Answer) > 0 {
			parts := strings.Fields(resp.Answer[0].String())
			if len(parts) > 4 {
				answer = strings.Join(parts[4:], " ")
			} else {
				answer = resp.Answer[0].String()
			}
			if len(resp.Answer) > 1 {
				answer += fmt.Sprintf(" (+%d more)", len(resp.Answer)-1)
			}

			for _, ans := range resp.Answer {
				parts := strings.Fields(ans.String())
				data := ""
				if len(parts) > 4 {
					data = strings.Join(parts[4:], " ")
				} else {
					data = ans.String()
				}
				answerRecords = append(answerRecords, querylog.AnswerRecord{
					Name: ans.Header().Name,
					Type: dns.Type(ans.Header().Rrtype).String(),
					TTL:  ans.Header().Ttl,
					Data: data,
				})
			}
		}
	}

	if r.logger != nil {
		r.logger.AddLog(&querylog.LogEntry{
			ClientIP:      clientIP,
			Domain:        qName,
			Type:          qType,
			Upstream:      upstream,
			Answer:        answer,
			AnswerRecords: answerRecords,
			DurationMs:    duration,
			Status:        status,
		})
	}

	return resp, err
}

func (r *Router) routeInternal(ctx context.Context, req *dns.Msg) (*dns.Msg, string, error) {
	qName := strings.ToLower(strings.TrimSuffix(req.Question[0].Name, "."))

	if ipStr, ok := r.config.Hosts[qName]; ok {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			return nil, "Hosts", fmt.Errorf("自定义Hosts中存在无效IP地址: %s for %s", ipStr, qName)
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
		return m, "Hosts", nil
	}

	if rule, ok := r.config.Rules[qName]; ok {
		switch strings.ToLower(rule) {
		case "cn":
			resp, err := client.RaceResolve(ctx, req, r.cnClients)
			return resp, "Rule(CN)", err
		case "overseas":
			resp, err := client.RaceResolve(ctx, req, r.overseasClients)
			return resp, "Rule(Overseas)", err
		default:
		}
	}

	for _, rr := range r.regexRules {
		if rr.Pattern.MatchString(qName) {
			switch strings.ToLower(rr.Target) {
			case "cn":
				resp, err := client.RaceResolve(ctx, req, r.cnClients)
				return resp, "Rule(Regex/CN)", err
			case "overseas":
				resp, err := client.RaceResolve(ctx, req, r.overseasClients)
				return resp, "Rule(Regex/Overseas)", err
			}
		}
	}

	if geoSiteRule := r.geo.LookupGeoSite(qName); geoSiteRule != "" {
		switch strings.ToLower(geoSiteRule) {
		case "cn":
			resp, err := client.RaceResolve(ctx, req, r.cnClients)
			return resp, "GeoSite(CN)", err
		default:
			resp, err := client.RaceResolve(ctx, req, r.overseasClients)
			return resp, "GeoSite(Overseas)", err
		}
	}

	resp, err := client.RaceResolve(ctx, req, r.overseasClients)
	if err != nil {
		return nil, "GeoIP(Fail)", fmt.Errorf("GeoIP分流时首次海外解析失败: %w", err)
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
		resp, err := client.RaceResolve(ctx, req, r.cnClients)
		return resp, "GeoIP(CN)", err
	}

	return resp, "GeoIP(Overseas)", nil
}
