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
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/store"
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

var errNotImplemented = errors.New("not implemented")

func runWriteAPI(context.Context, *slog.Logger) error { return errNotImplemented }
func runWriter(context.Context, *slog.Logger) error   { return errNotImplemented }
