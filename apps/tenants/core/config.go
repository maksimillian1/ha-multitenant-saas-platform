package core

import (
	"fmt"
	"os"
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
	// ZitadelToken is the personal access token of the service's own
	// Zitadel service account (instance role IAM_ORG_MANAGER).
	ZitadelToken string

	// KubeAPI is the Kubernetes API server; empty means the in-cluster one.
	KubeAPI string
	// Quota is the ResourceQuota of every tenant namespace.
	QuotaCPU    string
	QuotaMemory string
	QuotaPods   string

	// StepTimeout bounds one call to Zitadel or Kubernetes.
	StepTimeout time.Duration
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
		ZitadelToken:       GetEnv("ZITADEL_TOKEN", ""),
		KubeAPI:            GetEnv("KUBE_API", ""),
		QuotaCPU:           GetEnv("TENANT_QUOTA_CPU", "4"),
		QuotaMemory:        GetEnv("TENANT_QUOTA_MEMORY", "8Gi"),
		QuotaPods:          GetEnv("TENANT_QUOTA_PODS", "20"),
		StepTimeout:        timeout,
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
