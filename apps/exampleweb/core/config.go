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
	// Zitadel project ID of the platform API: the API Gateway accepts only tokens with it in aud.
	Audience   string
	GatewayURL string
	ConsoleURL string
	ZitadelURL string
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func LoadConfig() Config {
	return Config{
		ListenAddr:     getEnv("LISTEN_ADDR", ":8080"),
		Issuer:         getEnv("ISSUER", ""),
		IssuerDialAddr: getEnv("ISSUER_DIAL_ADDR", ""),
		ClientID:       getEnv("CLIENT_ID", ""),
		ClientSecret:   getEnv("CLIENT_SECRET", ""),
		PublicURL:      getEnv("PUBLIC_URL", "http://exampleweb.localtest.me:30080"),
		Audience:       getEnv("AUDIENCE", ""),
		GatewayURL:     getEnv("GATEWAY_URL", ""),
		ConsoleURL:     getEnv("CONSOLE_URL", getEnv("ISSUER", "")+"/ui/console"),
		ZitadelURL:     getEnv("ZITADEL_API_URL", getEnv("ISSUER", "")),
	}
}

func (c Config) Validate() error {
	for _, setting := range [][2]string{
		{"ISSUER", c.Issuer}, {"CLIENT_ID", c.ClientID}, {"CLIENT_SECRET", c.ClientSecret},
		{"AUDIENCE", c.Audience}, {"GATEWAY_URL", c.GatewayURL},
	} {
		if setting[1] == "" {
			return fmt.Errorf("%s is required", setting[0])
		}
	}
	return nil
}
