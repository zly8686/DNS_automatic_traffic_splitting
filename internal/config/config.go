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
	Listen       ListenConfig      `yaml:"listen"`
	BootstrapDNS []string          `yaml:"bootstrap_dns"`
	Upstreams    UpstreamsConfig   `yaml:"upstreams"`
	Hosts        map[string]string `yaml:"-"`
	Rules        map[string]string `yaml:"-"`
	GeoData      GeoDataConfig     `yaml:"geo_data"`
	AutoCert     AutoCertConfig    `yaml:"auto_cert"`
}

type AutoCertConfig struct {
	Enabled bool     `yaml:"enabled"`
	Email   string   `yaml:"email"`
	Domains []string `yaml:"domains"`
	CertDir string   `yaml:"cert_dir"`
}

type ListenConfig struct {
	DNSUDP string `yaml:"dns_udp"`
	DNSTCP string `yaml:"dns_tcp"`
	DOH    string `yaml:"doh"`
	DOT    string `yaml:"dot"`
	DOQ    string `yaml:"doq"`
}

type UpstreamsConfig struct {
	CN       []UpstreamServer `yaml:"cn"`
	Overseas []UpstreamServer `yaml:"overseas"`
}

type UpstreamServer struct {
	Address            string `yaml:"address"`
	Protocol           string `yaml:"protocol"`
	ECSIP              string `yaml:"ecs_ip"`
	EnablePipeline     bool   `yaml:"pipeline"`
	EnableH3           bool   `yaml:"http3"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
}

type GeoDataConfig struct {
	GeoIPDat           string `yaml:"geoip_dat"`
	GeoSiteDat         string `yaml:"geosite_dat"`
	GeoIPDownloadURL   string `yaml:"geoip_download_url"`
	GeoSiteDownloadURL string `yaml:"geosite_download_url"`
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
