// Command tenants is Tenant Management (ADR-0012): it creates, suspends,
// resumes and deletes tenants for platform admins.
//
//	tenants serve     serve the HTTP API as the application role (DATABASE_URL)
//	tenants migrate   apply migrations as the owner role (MIGRATE_DATABASE_URL)
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/tenants/api"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/tenants/auth"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/tenants/core"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/tenants/events"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/tenants/health"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/tenants/kube"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/tenants/lifecycle"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/tenants/store"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/tenants/zitadel"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := core.LoadConfig()
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var err error
	switch cmd {
	case "serve":
		err = serve(ctx, cfg, log)
	case "migrate":
		err = migrate(ctx, cfg, log)
	default:
		err = fmt.Errorf("unknown command %q", cmd)
	}
	if err != nil {
		log.Error("exit", "cmd", cmd, "err", err)
		os.Exit(1)
	}
}

func serve(ctx context.Context, cfg core.Config, log *slog.Logger) error {
	if err := core.Required(
		"DATABASE_URL", cfg.DatabaseURL,
		"ZITADEL_ISSUER", cfg.Issuer,
		"TENANT_MANAGEMENT_AUDIENCE", cfg.Audience,
		"KAFKA_BROKERS", strings.Join(cfg.KafkaBrokers, ","),
	); err != nil {
		return err
	}
	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()

	issuerClient, err := auth.NewIssuerClient(cfg.Issuer, cfg.IssuerDialAddr, cfg.StepTimeout)
	if err != nil {
		return err
	}
	verifier := auth.NewVerifier(cfg.Issuer, cfg.Audience, strings.TrimSuffix(cfg.Issuer, "/")+"/oauth/v2/keys", issuerClient)
	key, err := zitadel.LoadKey("ZITADEL_KEY_FILE", cfg.ZitadelKeyFile)
	if err != nil {
		return err
	}
	tokens, err := zitadel.NewKeyTokens(ctx, cfg.Issuer, key, issuerClient)
	if err != nil {
		return err
	}
	zitadelClient := zitadel.NewClient(cfg.Issuer, tokens, issuerClient)
	kubeClient, err := kube.InCluster(cfg.KubeAPI, cfg.StepTimeout, kube.Quota{CPU: cfg.QuotaCPU, Memory: cfg.QuotaMemory, Pods: cfg.QuotaPods})
	if err != nil {
		return err
	}

	tenants := lifecycle.NewService(st, zitadelClient, kubeClient, log, core.NewID)
	tenants.StepTimeout = cfg.StepTimeout

	publisher, err := events.NewPublisher(cfg.KafkaBrokers, cfg.TenantStates, log)
	if err != nil {
		return err
	}
	defer publisher.Close()
	tenants.Published = publisher.Publish
	go publisher.Run(ctx, time.Minute, func(ctx context.Context) ([]core.Tenant, error) { return st.List(ctx, true) })

	mux := http.NewServeMux()
	healthService := health.NewService(st.Ping)
	mux.HandleFunc("GET /healthz", healthService.LiveHandler)
	mux.HandleFunc("GET /readyz", healthService.ReadyHandler)
	mux.Handle("/", api.NewServer(tenants, log).Routes(verifier))
	return core.Serve(ctx, log, cfg.ListenAddr, mux)
}

// migrate waits up to 5 minutes for the database, then applies the migrations
// and fails if a table with tenant_id, other than the platform tables, is
// left without RLS.
func migrate(ctx context.Context, cfg core.Config, log *slog.Logger) error {
	if err := core.Required("MIGRATE_DATABASE_URL", cfg.MigrateDatabaseURL); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	conn, err := store.Connect(ctx, log, cfg.MigrateDatabaseURL)
	if err != nil {
		return err
	}
	defer conn.Close(context.WithoutCancel(ctx))

	if err := store.Migrate(ctx, conn); err != nil {
		return err
	}
	unprotected, err := store.UnprotectedTables(ctx, conn)
	if err != nil {
		return err
	}
	if len(unprotected) > 0 {
		return fmt.Errorf("tables with tenant_id but without enabled and forced RLS: %v", unprotected)
	}
	log.Info("migrations applied")
	return nil
}
