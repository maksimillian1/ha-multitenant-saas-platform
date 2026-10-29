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
	}
}

func (c Config) Validate() error {
	for _, setting := range [][2]string{
		{"ISSUER", c.Issuer}, {"CLIENT_ID", c.ClientID}, {"CLIENT_SECRET", c.ClientSecret}, {"SESSION_KEY", c.SessionKey},
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
