package core

import (
	"fmt"
	"os"
)

type Config struct {
	ListenAddr string
	// MetricsAddr is not routed by the public Gateway.
	MetricsAddr string
	// Issuer is the token issuer, e.g. http://zitadel.localtest.me:30080.
	Issuer string
	// Audience a token must carry: the Zitadel project ID of the platform API.
	Audience string
	JWKSURL  string
	// IssuerDialAddr is the host:port to connect to for the issuer's URLs, e.g.
	// zitadel.zitadel.svc:8080; the Host header stays the issuer's.
	IssuerDialAddr string
	// Routes is /prefix=http://upstream,...
	Routes string
	// UsageURL is UsageWriteAPI, e.g. http://usage-writeapi.usage.svc:8080/v1/events;
	// empty disables usage reporting (HAMSP-26).
	UsageURL string
	// UsageMaxPending is the number of unsent usage events at most.
	UsageMaxPending string
}

func GetEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func LoadConfig() Config {
	issuer := GetEnv("ISSUER", "")
	return Config{
		ListenAddr:      GetEnv("LISTEN_ADDR", ":8080"),
		MetricsAddr:     GetEnv("METRICS_ADDR", ":9090"),
		Issuer:          issuer,
		Audience:        GetEnv("AUDIENCE", ""),
		JWKSURL:         GetEnv("JWKS_URL", issuer+"/oauth/v2/keys"),
		IssuerDialAddr:  GetEnv("ISSUER_DIAL_ADDR", ""),
		Routes:          GetEnv("ROUTES", ""),
		UsageURL:        GetEnv("USAGE_URL", ""),
		UsageMaxPending: GetEnv("USAGE_MAX_PENDING", "10000"),
	}
}

// Required fails when the setting of env var key is empty.
func Required(key, val string) error {
	if val == "" {
		return fmt.Errorf("%s is required", key)
	}
	return nil
}
