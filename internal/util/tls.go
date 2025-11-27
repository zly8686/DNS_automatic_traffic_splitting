package util

import (
	"crypto/tls"
	"fmt"
)

func LoadServerCertificate(certFile, keyFile string) ([]tls.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("无法加载证书和密钥 (%s, %s): %w", certFile, keyFile, err)
	}
	return []tls.Certificate{cert}, nil
}
