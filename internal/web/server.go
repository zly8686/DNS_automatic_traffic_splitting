package web

import (
	"context"
	"doh-autoproxy/internal/client"
	"doh-autoproxy/internal/config"
	"doh-autoproxy/internal/manager"
	"doh-autoproxy/internal/resolver"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/miekg/dns"
)

var uiFS embed.FS

var (
	sessions  = make(map[string]time.Time)
	sessionMu sync.Mutex
)

type DashboardStats struct {
	UptimeSeconds    int64            `json:"uptime_seconds"`
	MemoryUsageMB    float64          `json:"memory_usage_mb"`
	NumGoroutines    int              `json:"num_goroutines"`
	TotalQueries     int64            `json:"total_queries"`
	TotalCN          int64            `json:"total_cn"`
	TotalOverseas    int64            `json:"total_overseas"`
	ListenDNSUDP     string           `json:"listen_dns_udp"`
	ListenDNSTCP     string           `json:"listen_dns_tcp"`
	ListenDOH        string           `json:"listen_doh"`
	ListenDOT        string           `json:"listen_dot"`
	ListenDOQ        string           `json:"listen_doq"`
	UpstreamCN       int              `json:"upstream_cn_count"`
	UpstreamOverseas int              `json:"upstream_overseas_count"`
	UpstreamStats    []interface{}    `json:"upstream_stats,omitempty"`
	TopClients       map[string]int64 `json:"top_clients"`
	TopDomains       map[string]int64 `json:"top_domains"`
}

type TestResult struct {
	Address  string `json:"address"`
	Protocol string `json:"protocol"`
	Group    string `json:"group"`
	Status   string `json:"status"`
	Latency  string `json:"latency"`
	Error    string `json:"error,omitempty"`
}

func StartWebServer(mgr *manager.ServiceManager) {
	cfg := mgr.Config

	if !cfg.WebUI.Enabled {
		return
	}

	addr := cfg.WebUI.Address
	if addr == "" {
		addr = ":8080"
	}

	mux := http.NewServeMux()

	checkAuth := func(r *http.Request) bool {
		if mgr.Config.WebUI.Username == "" || mgr.Config.WebUI.Password == "" {
			return true
		}
		cookie, err := r.Cookie("session_token")
		if err != nil {
			return false
		}
		sessionMu.Lock()
		defer sessionMu.Unlock()
		expiry, ok := sessions[cookie.Value]
		return ok && time.Now().Before(expiry)
	}

	mux.HandleFunc("/api/auth/status", func(w http.ResponseWriter, r *http.Request) {
		enabled := mgr.Config.WebUI.Username != "" && mgr.Config.WebUI.Password != ""
		authenticated := checkAuth(r)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"enabled":       enabled,
			"authenticated": authenticated,
		})
	})

	mux.HandleFunc("/api/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var creds struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		if creds.Username == mgr.Config.WebUI.Username && creds.Password == mgr.Config.WebUI.Password {
			token := fmt.Sprintf("%d", time.Now().UnixNano())
			expiry := time.Now().Add(24 * time.Hour)

			sessionMu.Lock()
			sessions[token] = expiry
			sessionMu.Unlock()

			http.SetCookie(w, &http.Cookie{
				Name:     "session_token",
				Value:    token,
				Expires:  expiry,
				HttpOnly: true,
				Path:     "/",
			})
			w.WriteHeader(http.StatusOK)
		} else {
			http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		}
	})

	mux.HandleFunc("/api/logout", func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie("session_token"); err == nil {
			sessionMu.Lock()
			delete(sessions, cookie.Value)
			sessionMu.Unlock()
		}
		http.SetCookie(w, &http.Cookie{
			Name:     "session_token",
			Value:    "",
			Expires:  time.Now().Add(-1 * time.Hour),
			HttpOnly: true,
			Path:     "/",
		})
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		currentCfg := mgr.Config

		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(currentCfg)
			return
		}

		if r.Method == http.MethodPost {
			if !checkAuth(r) {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			var newCfg config.Config
			if err := json.NewDecoder(r.Body).Decode(&newCfg); err != nil {
				http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
				return
			}

			configPath := config.GetDefaultConfigPath()
			if err := newCfg.Save(configPath); err != nil {
				http.Error(w, "Failed to save config: "+err.Error(), http.StatusInternalServerError)
				return
			}

			if err := mgr.Reload(&newCfg); err != nil {
				http.Error(w, "Config saved but reload failed: "+err.Error(), http.StatusInternalServerError)
				return
			}

			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Config saved and service reloaded."))
			return
		}

		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	})

	mux.HandleFunc("/api/test-upstreams", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if !checkAuth(r) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		var tempCfg config.Config
		if err := json.NewDecoder(r.Body).Decode(&tempCfg); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		bootstrapper := resolver.NewBootstrapper(tempCfg.BootstrapDNS)
		var results []TestResult
		var mu sync.Mutex
		var wg sync.WaitGroup

		testServer := func(srv config.UpstreamServer, group, target string) {
			defer wg.Done()

			start := time.Now()
			res := TestResult{Address: srv.Address, Protocol: srv.Protocol, Group: group}

			c, err := client.NewDNSClient(srv, bootstrapper)
			if err != nil {
				res.Status = "Error"
				res.Error = err.Error()
				mu.Lock()
				results = append(results, res)
				mu.Unlock()
				return
			}

			req := new(dns.Msg)
			req.SetQuestion(dns.Fqdn(target), dns.TypeA)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			_, err = c.Resolve(ctx, req)
			duration := time.Since(start)
			res.Latency = duration.String()

			if err != nil {
				res.Status = "Fail"
				res.Error = err.Error()
			} else {
				res.Status = "OK"
			}

			mu.Lock()
			results = append(results, res)
			mu.Unlock()
		}

		for _, s := range tempCfg.Upstreams.CN {
			wg.Add(1)
			go testServer(s, "CN", "www.baidu.com")
		}
		for _, s := range tempCfg.Upstreams.Overseas {
			wg.Add(1)
			go testServer(s, "Overseas", "www.google.com")
		}

		wg.Wait()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(results)
	})

	mux.HandleFunc("/api/logs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		limit := 20
		page := 1

		if p := r.URL.Query().Get("page"); p != "" {
			fmt.Sscanf(p, "%d", &page)
			if page < 1 {
				page = 1
			}
		}

		offset := (page - 1) * limit
		query := r.URL.Query().Get("q")
		if query == "" {
			query = r.URL.Query().Get("ip")
		}

		logs, total := mgr.QueryLog.GetLogs(offset, limit, query)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data":  logs,
			"total": total,
			"page":  page,
			"limit": limit,
		})
	})

	mux.HandleFunc("/api/stats", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var m runtime.MemStats
		runtime.ReadMemStats(&m)

		stats := mgr.QueryLog.GetStats()
		currentCfg := mgr.Config

		resp := DashboardStats{
			UptimeSeconds:    int64(time.Since(stats.StartTime).Seconds()),
			MemoryUsageMB:    float64(m.Alloc) / 1024 / 1024,
			NumGoroutines:    runtime.NumGoroutine(),
			TotalQueries:     stats.TotalQueries,
			TotalCN:          stats.TotalCN,
			TotalOverseas:    stats.TotalOverseas,
			ListenDNSUDP:     currentCfg.Listen.DNSUDP,
			ListenDNSTCP:     currentCfg.Listen.DNSTCP,
			ListenDOH:        currentCfg.Listen.DOH,
			ListenDOT:        currentCfg.Listen.DOT,
			ListenDOQ:        currentCfg.Listen.DOQ,
			UpstreamCN:       len(currentCfg.Upstreams.CN),
			UpstreamOverseas: len(currentCfg.Upstreams.Overseas),
			TopClients:       stats.TopClients,
			TopDomains:       stats.TopDomains,
		}

		if mgr.Router != nil {
			resp.UpstreamStats = mgr.Router.GetUpstreamStats()
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	uiAssets, err := fs.Sub(uiFS, "ui")
	if err != nil {
		log.Fatalf("Failed to embed UI: %v", err)
	}
	mux.Handle("/", http.FileServer(http.FS(uiAssets)))

	go func() {
		certManager := mgr.GetCertManager()

		if cfg.WebUI.CertFile != "" && cfg.WebUI.KeyFile != "" {
			log.Printf("WebUI HTTPS started on https://%s (manual cert)", addr)
			if err := http.ListenAndServeTLS(addr, cfg.WebUI.CertFile, cfg.WebUI.KeyFile, mux); err != nil {
				log.Printf("WebUI HTTPS server failed: %v", err)
			}
			return
		}

		if cfg.AutoCert.Enabled && certManager != nil {
			server := &http.Server{
				Addr:      addr,
				Handler:   mux,
				TLSConfig: certManager.TLSConfig(),
			}
			log.Printf("WebUI HTTPS started on https://%s (auto cert)", addr)
			if err := server.ListenAndServeTLS("", ""); err != nil {
				log.Printf("WebUI HTTPS server failed: %v", err)
			}
			return
		}

		log.Printf("WebUI HTTP started on http://%s", addr)
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Printf("WebUI HTTP server failed: %v", err)
		}
	}()
}
