package core

import (
	"fmt"
	"os"
	"strings"
	"time"
)

type Config struct {
	ListenAddr         string
	DatabaseURL        string // application role (serve)
	MigrateDatabaseURL string // owner role (migrate)

	// IssuerDialAddr: the public issuer name does not resolve inside the cluster.
	Issuer         string
	IssuerDialAddr string
	Audience       string
	// Exchanged for short-lived access tokens; the service holds no long-lived token.
	ZitadelKeyFile string

	// Empty means in-cluster.
	KubeAPI     string
	QuotaCPU    string
	QuotaMemory string
	QuotaPods   string

	StepTimeout time.Duration

	// ADR-0012, item 5.
	KafkaBrokers  []string
	TenantStates  string
	KafkaUsername string
	KafkaPassword string
	KafkaCAFile   string
}

func GetEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func LoadConfig() Config {
	timeout, err := time.ParseDuration(GetEnv("STEP_TIMEOUT", "15s"))
	if err != nil {
		timeout = 15 * time.Second
	}
	return Config{
		ListenAddr:         GetEnv("LISTEN_ADDR", ":8080"),
		DatabaseURL:        GetEnv("DATABASE_URL", ""),
		MigrateDatabaseURL: GetEnv("MIGRATE_DATABASE_URL", ""),
		Issuer:             GetEnv("ZITADEL_ISSUER", ""),
		IssuerDialAddr:     GetEnv("ZITADEL_DIAL_ADDR", ""),
		Audience:           GetEnv("TENANT_MANAGEMENT_AUDIENCE", ""),
		ZitadelKeyFile:     GetEnv("ZITADEL_KEY_FILE", ""),
		KubeAPI:            GetEnv("KUBE_API", ""),
		QuotaCPU:           GetEnv("TENANT_QUOTA_CPU", "4"),
		QuotaMemory:        GetEnv("TENANT_QUOTA_MEMORY", "8Gi"),
		QuotaPods:          GetEnv("TENANT_QUOTA_PODS", "20"),
		StepTimeout:        timeout,
		KafkaBrokers:       splitList(GetEnv("KAFKA_BROKERS", "")),
		TenantStates:       GetEnv("TENANT_STATES_TOPIC", "tenant-states"),
		KafkaUsername:      GetEnv("KAFKA_USERNAME", ""),
		KafkaPassword:      GetEnv("KAFKA_PASSWORD", ""),
		KafkaCAFile:        GetEnv("KAFKA_CA_FILE", ""),
	}
}

func Required(pairs ...string) error {
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] == "" {
			return fmt.Errorf("%s is required", pairs[i])
		}
	}
	return nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
