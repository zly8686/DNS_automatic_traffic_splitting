package config

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Listen          ListenConfig      `yaml:"listen" json:"listen"`
	BootstrapDNS    []string          `yaml:"bootstrap_dns" json:"bootstrap_dns"`
	Upstreams       UpstreamsConfig   `yaml:"upstreams" json:"upstreams"`
	Hosts           map[string]string `yaml:"-" json:"hosts"`
	Rules           map[string]string `yaml:"-" json:"rules"`
	GeoData         GeoDataConfig     `yaml:"geo_data" json:"geo_data"`
	AutoCert        AutoCertConfig    `yaml:"auto_cert" json:"auto_cert"`
	TLSCertificates []TLSCertConfig   `yaml:"tls_certificates" json:"tls_certificates"`
	WebUI           WebUIConfig       `yaml:"web_ui" json:"web_ui"`
	QueryLog        QueryLogConfig    `yaml:"query_log" json:"query_log"`
}

type TLSCertConfig struct {
	CertFile string `yaml:"cert_file" json:"cert_file"`
	KeyFile  string `yaml:"key_file" json:"key_file"`
}

type QueryLogConfig struct {
	Enabled    bool   `yaml:"enabled" json:"enabled"`
	File       string `yaml:"file" json:"file"`
	MaxSizeMB  int    `yaml:"max_size_mb" json:"max_size_mb"`
	SaveToFile bool   `yaml:"save_to_file" json:"save_to_file"`
}

type WebUIConfig struct {
	Enabled   bool   `yaml:"enabled" json:"enabled"`
	Address   string `yaml:"address" json:"address"`
	Username  string `yaml:"username" json:"username"`
	Password  string `yaml:"password" json:"password"`
	CertFile  string `yaml:"cert_file" json:"cert_file"`
	KeyFile   string `yaml:"key_file" json:"key_file"`
	GuestMode bool   `yaml:"guest_mode" json:"guest_mode"`
}

type AutoCertConfig struct {
	Enabled bool     `yaml:"enabled" json:"enabled"`
	Email   string   `yaml:"email" json:"email"`
	Domains []string `yaml:"domains" json:"domains"`
	CertDir string   `yaml:"cert_dir" json:"cert_dir"`
}

type ListenConfig struct {
	DNSUDP  string `yaml:"dns_udp" json:"dns_udp"`
	DNSTCP  string `yaml:"dns_tcp" json:"dns_tcp"`
	DOH     string `yaml:"doh" json:"doh"`
	DoHPath string `yaml:"doh_path" json:"doh_path"`
	DOT     string `yaml:"dot" json:"dot"`
	DOQ     string `yaml:"doq" json:"doq"`
}

type UpstreamsConfig struct {
	CN       []UpstreamServer `yaml:"cn" json:"cn"`
	Overseas []UpstreamServer `yaml:"overseas" json:"overseas"`
}

type UpstreamServer struct {
	Address            string `yaml:"address" json:"address"`
	Protocol           string `yaml:"protocol" json:"protocol"`
	ECSIP              string `yaml:"ecs_ip" json:"ecs_ip"`
	EnablePipeline     bool   `yaml:"pipeline" json:"pipeline"`
	EnableH3           bool   `yaml:"http3" json:"http3"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify" json:"insecure_skip_verify"`
}

type GeoDataConfig struct {
	GeoIPDat           string `yaml:"geoip_dat" json:"geoip_dat"`
	GeoSiteDat         string `yaml:"geosite_dat" json:"geosite_dat"`
	GeoIPDownloadURL   string `yaml:"geoip_download_url" json:"geoip_download_url"`
	GeoSiteDownloadURL string `yaml:"geosite_download_url" json:"geosite_download_url"`
	AutoUpdate         string `yaml:"auto_update" json:"auto_update"` // Format: "15:04" (HH:MM)
}

func LoadConfig(configPath string) (*Config, error) {
	data, err := ioutil.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("无法读取配置文件 %s: %w", configPath, err)
	}

	var cfg Config
	err = yaml.Unmarshal(data, &cfg)
	if err != nil {
		return nil, fmt.Errorf("无法解析配置文件 %s: %w", configPath, err)
	}

	cfg.QueryLog.Enabled = true

	normalizePort := func(p *string) {
		if *p != "" && !strings.Contains(*p, ":") {
			*p = ":" + *p
		}
	}
	normalizePort(&cfg.Listen.DNSUDP)
	normalizePort(&cfg.Listen.DNSTCP)
	normalizePort(&cfg.Listen.DOH)
	normalizePort(&cfg.Listen.DOT)
	normalizePort(&cfg.Listen.DOQ)

	cfg.Hosts = make(map[string]string)
	cfg.Rules = make(map[string]string)

	if err := loadHostsFile("hosts.txt", cfg.Hosts); err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("加载 hosts.txt 失败: %w", err)
		}
	}

	if err := loadRulesFile("rule.txt", cfg.Rules); err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("加载 rule.txt 失败: %w", err)
		}
	}

	return &cfg, nil
}

func (c *Config) Save(configPath string) error {
	normalizePort := func(p *string) {
		if *p != "" && !strings.Contains(*p, ":") {
			*p = ":" + *p
		}
	}
	normalizePort(&c.Listen.DNSUDP)
	normalizePort(&c.Listen.DNSTCP)
	normalizePort(&c.Listen.DOH)
	normalizePort(&c.Listen.DOT)
	normalizePort(&c.Listen.DOQ)

	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("无法序列化配置: %w", err)
	}
	if err := ioutil.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("无法写入配置文件 %s: %w", configPath, err)
	}

	if err := saveHostsFile("hosts.txt", c.Hosts); err != nil {
		return fmt.Errorf("无法写入 hosts.txt: %w", err)
	}

	if err := saveRulesFile("rule.txt", c.Rules); err != nil {
		return fmt.Errorf("无法写入 rule.txt: %w", err)
	}

	return nil
}

func saveHostsFile(path string, hosts map[string]string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	for domain, ip := range hosts {
		if _, err := fmt.Fprintf(w, "%s %s\n", ip, domain); err != nil {
			return err
		}
	}
	return w.Flush()
}

func saveRulesFile(path string, rules map[string]string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	for domain, target := range rules {
		if _, err := fmt.Fprintf(w, "%s %s\n", domain, target); err != nil {
			return err
		}
	}
	return w.Flush()
}

func loadHostsFile(path string, hosts map[string]string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			ip := parts[0]
			for _, domain := range parts[1:] {
				hosts[strings.ToLower(domain)] = ip
			}
		}
	}
	return scanner.Err()
}

func loadRulesFile(path string, rules map[string]string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			domain := strings.ToLower(parts[0])
			target := strings.ToLower(parts[1])
			rules[domain] = target
		}
	}
	return scanner.Err()
}

func GetDefaultConfigPath() string {
	if p := os.Getenv("DOH_AUTOPROXY_CONFIG"); p != "" {
		return p
	}
	return "config.yaml"
}
