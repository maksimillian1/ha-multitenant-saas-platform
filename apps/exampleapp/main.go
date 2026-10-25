// Command exampleapp is the minimal per-tenant service from ADR-0001. It exists
// to test tenant isolation, failover and load; it is not part of the product.
//
//	exampleapp serve     serve the HTTP API as the application role (DATABASE_URL)
//	exampleapp migrate   apply migrations as the owner role (MIGRATE_DATABASE_URL)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/exampleapp/internal/api"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/exampleapp/internal/store"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var err error
	switch cmd {
	case "serve":
		err = serve(ctx, log)
	case "migrate":
		err = migrate(ctx, log)
	default:
		err = fmt.Errorf("unknown command %q", cmd)
	}
	if err != nil {
		log.Error("exit", "cmd", cmd, "err", err)
		os.Exit(1)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func serve(ctx context.Context, log *slog.Logger) error {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL is required")
	}
	st, err := store.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer st.Close()

	srv := &http.Server{
		Addr:              env("LISTEN_ADDR", ":8080"),
		Handler:           api.NewHandler(st, st.Ping, log),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	log.Info("listening", "addr", srv.Addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// migrate retries the connection for up to 5 minutes, because on a fresh
// cluster the database may still be starting when the job runs.
func migrate(ctx context.Context, log *slog.Logger) error {
	dsn := os.Getenv("MIGRATE_DATABASE_URL")
	if dsn == "" {
		return errors.New("MIGRATE_DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	var conn *pgx.Conn
	var err error
	for {
		conn, err = pgx.Connect(ctx, dsn)
		if err == nil {
			break
		}
		log.Warn("database not reachable, retrying", "err", err)
		select {
		case <-ctx.Done():
			return err
		case <-time.After(5 * time.Second):
		}
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
