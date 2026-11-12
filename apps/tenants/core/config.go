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

	// Issuer is Zitadel's public issuer URL; IssuerDialAddr, when set, is
	// where connections to it go inside the cluster.
	Issuer         string
	IssuerDialAddr string
	// Audience is the ID of the tenant-management project: callers' tokens
	// must be issued for it and carry its role tenant-manager.
	Audience string
	// ZitadelKeyFile is the path of the JWT profile key of the service's own
	// Zitadel machine user (instance role IAM_ORG_MANAGER). The service
	// exchanges it for short-lived access tokens; it has no long-lived token.
	ZitadelKeyFile string

	// KubeAPI is the Kubernetes API server; empty means the in-cluster one.
	KubeAPI string
	// Quota is the ResourceQuota of every tenant namespace.
	QuotaCPU    string
	QuotaMemory string
	QuotaPods   string

	// StepTimeout bounds one call to Zitadel or Kubernetes.
	StepTimeout time.Duration

	// KafkaBrokers and TenantStates: where every tenant's state is published
	// for the API Gateway (ADR-0012, item 5).
	KafkaBrokers []string
	TenantStates string
	// KafkaUsername and KafkaPassword: the SCRAM login of the tenants KafkaUser (HAMSP-31).
	KafkaUsername string
	KafkaPassword string
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
