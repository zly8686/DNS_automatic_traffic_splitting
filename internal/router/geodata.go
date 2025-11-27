package router

import (
	"fmt"
	"net"
	"strings"

	"github.com/metacubex/geo/geoip"
	"github.com/metacubex/geo/geosite"
)

type GeoDataManager struct {
	geoip   *geoip.Database
	geosite *geosite.Database
}

func NewGeoDataManager(geoipPath, geositePath string) (*GeoDataManager, error) {
	geoIPData, err := geoip.FromFile(geoipPath)
	if err != nil {
		return nil, fmt.Errorf("无法加载 GeoIP 数据 %s: %w", geoipPath, err)
	}

	geoSiteData, err := geosite.FromFile(geositePath)
	if err != nil {
		return nil, fmt.Errorf("无法加载 GeoSite 数据 %s: %w", geositePath, err)
	}

	return &GeoDataManager{
		geoip:   geoIPData,
		geosite: geoSiteData,
	}, nil
}

func (g *GeoDataManager) IsCNIP(ip net.IP) bool {
	if g.geoip == nil {
		return false
	}
	codes := g.geoip.LookupCode(ip)
	for _, code := range codes {
		if strings.ToUpper(code) == "CN" {
			return true
		}
	}
	return false
}

func (g *GeoDataManager) LookupGeoSite(domain string) string {
	if g.geosite == nil {
		return ""
	}

	codes := g.geosite.LookupCodes(domain)
	for _, code := range codes {
		if strings.ToLower(code) == "cn" {
			return "cn"
		}
	}

	return ""
}
