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
	// ZitadelKeyFile is the JWT profile key file of the admin app's service
	// account admin-app (instance roles IAM_ORG_MANAGER and
	// IAM_BRANDING_MANAGER, HAMSP-36). The app gets short-lived access tokens
	// with it.
	ZitadelKeyFile string
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
	if len(c.SessionKey) < 32 {
		return fmt.Errorf("SESSION_KEY must be at least 32 bytes")
	}
	return nil
}
