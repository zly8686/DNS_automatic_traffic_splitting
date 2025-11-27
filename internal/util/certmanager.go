package util

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"os"

	"doh-autoproxy/internal/config"

	"golang.org/x/crypto/acme/autocert"
)

type CertManager struct {
	manager *autocert.Manager
	enabled bool
}

func NewCertManager(cfg *config.Config) (*CertManager, error) {
	if !cfg.AutoCert.Enabled {
		return &CertManager{enabled: false}, nil
	}

	if len(cfg.AutoCert.Domains) == 0 {
		return nil, fmt.Errorf("auto_cert enabled but no domains specified")
	}

	if cfg.AutoCert.Email == "" {
		return nil, fmt.Errorf("auto_cert enabled but email not specified")
	}

	certDir := cfg.AutoCert.CertDir
	if certDir == "" {
		certDir = "certs"
	}
	if err := os.MkdirAll(certDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cert dir: %w", err)
	}

	m := &autocert.Manager{
		Cache:      autocert.DirCache(certDir),
		Prompt:     autocert.AcceptTOS,
		Email:      cfg.AutoCert.Email,
		HostPolicy: autocert.HostWhitelist(cfg.AutoCert.Domains...),
	}

	return &CertManager{
		manager: m,
		enabled: true,
	}, nil
}

func (cm *CertManager) GetCertificateFunc() func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	if !cm.enabled {
		return nil
	}
	return cm.manager.GetCertificate
}

func (cm *CertManager) HTTPHandler(fallback http.Handler) http.Handler {
	if !cm.enabled {
		return fallback
	}
	return cm.manager.HTTPHandler(fallback)
}

func (cm *CertManager) TLSConfig() *tls.Config {
	if !cm.enabled {
		return nil
	}
	return cm.manager.TLSConfig()
}
