package core

import (
	"errors"
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	ListenAddr      string
	MetricsAddr     string
	Issuer          string
	Audience        string
	JWKSURL         string
	IssuerDialAddr  string
	Routes          string
	UsageURL        string
	UsageMaxPending int
}

func GetEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func LoadConfig() Config {
	issuer := GetEnv("ISSUER", "")
	maxPending, _ := strconv.Atoi(GetEnv("USAGE_MAX_PENDING", "10000"))
	return Config{
		ListenAddr:      GetEnv("LISTEN_ADDR", ":8080"),
		MetricsAddr:     GetEnv("METRICS_ADDR", ":9090"),
		Issuer:          issuer,
		Audience:        GetEnv("AUDIENCE", ""),
		JWKSURL:         GetEnv("JWKS_URL", issuer+"/oauth/v2/keys"),
		IssuerDialAddr:  GetEnv("ISSUER_DIAL_ADDR", ""),
		Routes:          GetEnv("ROUTES", ""),
		UsageURL:        GetEnv("USAGE_URL", ""),
		UsageMaxPending: maxPending,
	}
}

func (c Config) Validate() error {
	for _, setting := range [][2]string{{"ISSUER", c.Issuer}, {"AUDIENCE", c.Audience}} {
		if setting[1] == "" {
			return fmt.Errorf("%s is required", setting[0])
		}
	}
	if c.UsageURL != "" && c.UsageMaxPending <= 0 {
		return errors.New("USAGE_MAX_PENDING must be a positive integer")
	}
	return nil
}
