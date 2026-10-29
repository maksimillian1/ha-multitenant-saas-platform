// Command exampleapp is the minimal per-tenant service from ADR-0001. It exists
// to test tenant isolation, failover and load; it is not part of the product.
//
//	exampleapp serve     serve the HTTP API as the application role (DATABASE_URL)
//	exampleapp migrate   apply migrations as the owner role (MIGRATE_DATABASE_URL)
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/exampleapp/core"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/exampleapp/health"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/exampleapp/items"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/exampleapp/store"
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
	itemsService, healthService, closeAll, err := bootstrapServices(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer closeAll()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthService.LiveHandler)
	mux.HandleFunc("GET /readyz", healthService.ReadyHandler)
	mux.HandleFunc("POST /items", itemsService.CreateHandler)
	mux.HandleFunc("GET /items", itemsService.ListHandler)
	mux.HandleFunc("GET /items/{id}", itemsService.GetHandler)
	mux.HandleFunc("PUT /items/{id}", itemsService.UpdateHandler)
	mux.HandleFunc("DELETE /items/{id}", itemsService.DeleteHandler)

	return core.Serve(ctx, log, cfg.ListenAddr, core.AccessLog(mux, log))
}

func bootstrapServices(ctx context.Context, cfg core.Config, log *slog.Logger) (*items.Service, *health.Service, func(), error) {
	if err := core.Required("DATABASE_URL", cfg.DatabaseURL); err != nil {
		return nil, nil, nil, err
	}
	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, nil, nil, err
	}

	itemsService := items.NewService(st, log)
	healthService := health.NewService(st.Ping)

	return itemsService, healthService, st.Close, nil
}

// migrate waits up to 5 minutes for the database, then applies the migrations
// and fails if any multi-tenant table is left without RLS.
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
	defer conn.Close(context.Background())

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
