package core

import (
	"fmt"
	"os"
)

type Config struct {
	ListenAddr     string
	Issuer         string
	IssuerDialAddr string
	ClientID       string
	ClientSecret   string
	PublicURL      string
	SessionKey     string
	AdminRole      string
	// The issuer by default.
	ZitadelURL string
	// Instance roles IAM_ORG_MANAGER and IAM_BRANDING_MANAGER.
	ZitadelKeyFile string

	// Without TENANTS_URL the tenant routes answer 503.
	TenantsURL          string
	TenantsAudience     string
	TenantsClientID     string
	TenantsClientSecret string
}

func GetEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func LoadConfig() Config {
	return Config{
		ListenAddr:     GetEnv("LISTEN_ADDR", ":8080"),
		Issuer:         GetEnv("ISSUER", ""),
		IssuerDialAddr: GetEnv("ISSUER_DIAL_ADDR", ""),
		ClientID:       GetEnv("CLIENT_ID", ""),
		ClientSecret:   GetEnv("CLIENT_SECRET", ""),
		PublicURL:      GetEnv("PUBLIC_URL", "http://admin.localtest.me:30080"),
		SessionKey:     GetEnv("SESSION_KEY", ""),
		AdminRole:      GetEnv("ADMIN_ROLE", "super-admin"),
		ZitadelURL:     GetEnv("ZITADEL_API_URL", GetEnv("ISSUER", "")),
		ZitadelKeyFile: GetEnv("ZITADEL_KEY_FILE", ""),

		TenantsURL:          GetEnv("TENANTS_URL", ""),
		TenantsAudience:     GetEnv("TENANTS_AUDIENCE", ""),
		TenantsClientID:     GetEnv("TENANTS_CLIENT_ID", ""),
		TenantsClientSecret: GetEnv("TENANTS_CLIENT_SECRET", ""),
	}
}

func (c Config) Validate() error {
	for _, setting := range [][2]string{
		{"ISSUER", c.Issuer}, {"CLIENT_ID", c.ClientID}, {"CLIENT_SECRET", c.ClientSecret}, {"SESSION_KEY", c.SessionKey},
		{"ZITADEL_KEY_FILE", c.ZitadelKeyFile},
	} {
		if setting[1] == "" {
			return fmt.Errorf("%s is required", setting[0])
		}
	}
	if c.TenantsURL != "" {
		for _, setting := range [][2]string{
			{"TENANTS_AUDIENCE", c.TenantsAudience}, {"TENANTS_CLIENT_ID", c.TenantsClientID}, {"TENANTS_CLIENT_SECRET", c.TenantsClientSecret},
		} {
			if setting[1] == "" {
				return fmt.Errorf("%s is required with TENANTS_URL", setting[0])
			}
		}
	}
	if len(c.SessionKey) < 32 {
		return fmt.Errorf("SESSION_KEY must be at least 32 bytes")
	}
	return nil
}
