package core

import (
	"fmt"
	"os"
	"strings"
)

type Config struct {
	ListenAddr   string // writeapi, writer health
	KafkaBrokers string // host:port,... (writeapi, writer)
	KafkaTopic   string
	KafkaGroup   string // writer
	// DatabaseURL is Usage Writer's role, through PgBouncer (writer).
	DatabaseURL string
	// MigrateDatabaseURL is the owner role of the usage database (migrate).
	// CNPG's Database resource creates the database and the timescaledb
	// extension (ADR-0008).
	MigrateDatabaseURL string
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
		KafkaBrokers:       GetEnv("KAFKA_BROKERS", ""),
		KafkaTopic:         GetEnv("KAFKA_TOPIC", "usage-events"),
		KafkaGroup:         GetEnv("KAFKA_GROUP", "usage-writer"),
		DatabaseURL:        GetEnv("DATABASE_URL", ""),
		MigrateDatabaseURL: GetEnv("MIGRATE_DATABASE_URL", ""),
	}
}

// Required fails when the setting of env var key is empty.
func Required(key, val string) error {
	if val == "" {
		return fmt.Errorf("%s is required", key)
	}
	return nil
}

func (c Config) Brokers() ([]string, error) {
	if err := Required("KAFKA_BROKERS", c.KafkaBrokers); err != nil {
		return nil, err
	}
	return strings.Split(c.KafkaBrokers, ","), nil
}
