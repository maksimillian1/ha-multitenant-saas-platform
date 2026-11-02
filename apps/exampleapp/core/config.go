package core

import (
	"fmt"
	"os"
)

type Config struct {
	ListenAddr         string
	DatabaseURL        string // application role (serve)
	MigrateDatabaseURL string // owner role (migrate)
}

func GetEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func LoadConfig() Config {
	return Config{
		ListenAddr:         GetEnv("LISTEN_ADDR", ":8080"),
		DatabaseURL:        GetEnv("DATABASE_URL", ""),
		MigrateDatabaseURL: GetEnv("MIGRATE_DATABASE_URL", ""),
	}
}

func Required(key, val string) error {
	if val == "" {
		return fmt.Errorf("%s is required", key)
	}
	return nil
}
