// Command migrate applies the migrations to the usage database and exits. It
// runs as the owner role, in a Job before writer rolls out
// (prototype/k8s/apps/usage/migrate-job.yaml).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/core"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/migrations"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, core.LoadConfig(), log); err != nil {
		log.Error("exit", "err", err)
		os.Exit(1)
	}
}

// run waits up to 5 minutes for the database, then applies the migrations and
// fails if any relation with tenant_id is left open to another tenant.
func run(ctx context.Context, cfg core.Config, log *slog.Logger) error {
	if err := core.Required("MIGRATE_DATABASE_URL", cfg.MigrateDatabaseURL); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	conn, err := migrations.Connect(ctx, log, cfg.MigrateDatabaseURL)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())

	if err := migrations.Migrate(ctx, conn); err != nil {
		return err
	}
	unprotected, err := migrations.UnprotectedTables(ctx, conn)
	if err != nil {
		return err
	}
	if len(unprotected) > 0 {
		return fmt.Errorf("relations with tenant_id open to application roles without tenant isolation: %v", unprotected)
	}
	log.Info("migrations applied")
	return nil
}
