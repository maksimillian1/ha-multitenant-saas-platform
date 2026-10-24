// Command usage runs the usage pipeline of ADR-0001 item 4 (HAMSP-26):
//
//	usage writeapi   UsageWriteAPI: accepts usage events and produces them to Kafka
//	usage writer     Usage Writer: consumes Kafka and stores the events once
//	usage migrate    creates the usage database and applies its migrations
//
// Configuration (environment):
//
//	KAFKA_BROKERS          host:port,... (writeapi, writer)
//	KAFKA_TOPIC            default usage-events
//	KAFKA_GROUP            default usage-writer (writer)
//	DATABASE_URL           Usage Writer's role, through PgBouncer (writer)
//	ADMIN_DATABASE_URL     superuser, creates the database (migrate)
//	MIGRATE_DATABASE_URL   owner role of the usage database (migrate)
//	DATABASE_NAME, DATABASE_OWNER   default usage, usage_owner (migrate)
//	LISTEN_ADDR            default :8080 (writeapi, writer health)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/store"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/writeapi"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/writer"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cmd := ""
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var err error
	switch cmd {
	case "writeapi":
		err = runWriteAPI(ctx, log)
	case "writer":
		err = runWriter(ctx, log)
	case "migrate":
		err = migrate(ctx, log)
	default:
		err = fmt.Errorf("unknown command %q: want writeapi, writer or migrate", cmd)
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

func required(key string) (string, error) {
	v := os.Getenv(key)
	if v == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return v, nil
}

func brokers() ([]string, error) {
	v, err := required("KAFKA_BROKERS")
	if err != nil {
		return nil, err
	}
	return strings.Split(v, ","), nil
}

// connect retries for up to 5 minutes, because on a fresh cluster the database
// may still be starting when the job runs.
func connect(ctx context.Context, log *slog.Logger, dsn string) (*pgx.Conn, error) {
	for {
		conn, err := pgx.Connect(ctx, dsn)
		if err == nil {
			return conn, nil
		}
		log.Warn("database not reachable, retrying", "err", err)
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(5 * time.Second):
		}
	}
}

func migrate(ctx context.Context, log *slog.Logger) error {
	adminDSN, err := required("ADMIN_DATABASE_URL")
	if err != nil {
		return err
	}
	ownerDSN, err := required("MIGRATE_DATABASE_URL")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	admin, err := connect(ctx, log, adminDSN)
	if err != nil {
		return err
	}
	name, owner := env("DATABASE_NAME", "usage"), env("DATABASE_OWNER", "usage_owner")
	created, err := store.CreateDatabase(ctx, admin, name, owner)
	admin.Close(context.Background())
	if err != nil {
		return fmt.Errorf("create database %s: %w", name, err)
	}
	log.Info("database", "name", name, "created", created)

	conn, err := connect(ctx, log, ownerDSN)
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

// serve runs h on LISTEN_ADDR until ctx is done.
func serve(ctx context.Context, log *slog.Logger, h http.Handler) error {
	srv := &http.Server{Addr: env("LISTEN_ADDR", ":8080"), Handler: h, ReadHeaderTimeout: 5 * time.Second}
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

func runWriteAPI(ctx context.Context, log *slog.Logger) error {
	seeds, err := brokers()
	if err != nil {
		return err
	}
	k, err := writeapi.NewKafka(seeds, env("KAFKA_TOPIC", "usage-events"))
	if err != nil {
		return err
	}
	defer k.Close()
	return serve(ctx, log, writeapi.New(k, log))
}

func runWriter(ctx context.Context, log *slog.Logger) error {
	dsn, err := required("DATABASE_URL")
	if err != nil {
		return err
	}
	seeds, err := brokers()
	if err != nil {
		return err
	}
	st, err := store.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer st.Close()
	client, err := writer.NewClient(seeds, env("KAFKA_TOPIC", "usage-events"), env("KAFKA_GROUP", "usage-writer"))
	if err != nil {
		return err
	}
	defer client.Close()

	health := http.NewServeMux()
	health.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	go func() {
		if err := serve(ctx, log, health); err != nil {
			log.Error("health endpoint", "err", err)
		}
	}()
	log.Info("consuming")
	return writer.New(client, st, log).Run(ctx)
}
