package core

import (
	"fmt"
	"os"
	"strings"
)

type Config struct {
	ListenAddr         string
	KafkaBrokers       string
	KafkaTopic         string
	KafkaGroup         string
	DatabaseURL        string
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
	brokers := strings.Split(c.KafkaBrokers, ",")
	for i := range brokers {
		brokers[i] = strings.TrimSpace(brokers[i])
	}
	return brokers, nil
}
