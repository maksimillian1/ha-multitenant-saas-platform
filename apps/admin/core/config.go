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
	// ZitadelURL is where the Zitadel APIs are; the issuer by default.
	ZitadelURL string
	// ZitadelToken is the personal access token of the admin app's service
	// account in Zitadel (instance role IAM_ORG_MANAGER).
	ZitadelToken string
	// ZitadelInstanceToken is the token of a second service account
	// (IAM_OWNER), used only to change the instance's branding (HAMSP-37).
	ZitadelInstanceToken string

	// Tenant Management (ADR-0012): its URL, and the client credentials of
	// the machine user whose token has the role tenant-manager in the
	// project whose ID is TenantsAudience. Without TENANTS_URL the tenant
	// routes answer 503.
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
		ZitadelToken:   GetEnv("ZITADEL_TOKEN", ""),

		ZitadelInstanceToken: GetEnv("ZITADEL_INSTANCE_TOKEN", ""),

		TenantsURL:          GetEnv("TENANTS_URL", ""),
		TenantsAudience:     GetEnv("TENANTS_AUDIENCE", ""),
		TenantsClientID:     GetEnv("TENANTS_CLIENT_ID", ""),
		TenantsClientSecret: GetEnv("TENANTS_CLIENT_SECRET", ""),
	}
}

func (c Config) Validate() error {
	for _, setting := range [][2]string{
		{"ISSUER", c.Issuer}, {"CLIENT_ID", c.ClientID}, {"CLIENT_SECRET", c.ClientSecret}, {"SESSION_KEY", c.SessionKey},
		{"ZITADEL_TOKEN", c.ZitadelToken}, {"ZITADEL_INSTANCE_TOKEN", c.ZitadelInstanceToken},
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
