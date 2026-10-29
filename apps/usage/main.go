// Command usage runs the usage pipeline:
//
//	usage writeapi   UsageWriteAPI: accepts usage events and produces them to Kafka
//	usage writer     Usage Writer: consumes Kafka and stores the events once
//	usage migrate    applies the migrations to the usage database
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

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/core"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/health"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/kafka"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/store"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/writeapi"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/writer"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := core.LoadConfig()
	cmd := ""
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var err error
	switch cmd {
	case "writeapi":
		err = runWriteAPI(ctx, cfg, log)
	case "writer":
		err = runWriter(ctx, cfg, log)
	case "migrate":
		err = migrate(ctx, cfg, log)
	default:
		err = fmt.Errorf("unknown command %q: want writeapi, writer or migrate", cmd)
	}
	if err != nil {
		log.Error("exit", "cmd", cmd, "err", err)
		os.Exit(1)
	}
}

func runWriteAPI(ctx context.Context, cfg core.Config, log *slog.Logger) error {
	writeAPIService, healthService, closeAll, err := bootstrapWriteAPI(cfg, log)
	if err != nil {
		return err
	}
	defer closeAll()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/events", writeAPIService.EventsHandler)
	mux.HandleFunc("GET /healthz", healthService.LiveHandler)
	mux.HandleFunc("GET /readyz", healthService.ReadyHandler)

	return core.Serve(ctx, log, cfg.ListenAddr, mux)
}

func bootstrapWriteAPI(cfg core.Config, log *slog.Logger) (*writeapi.Service, *health.Service, func(), error) {
	brokers, err := cfg.Brokers()
	if err != nil {
		return nil, nil, nil, err
	}
	producer, err := kafka.NewProducer(brokers, cfg.KafkaTopic)
	if err != nil {
		return nil, nil, nil, err
	}

	writeAPIService := writeapi.NewService(producer, log)
	healthService := health.NewService(producer.Ping)

	return writeAPIService, healthService, producer.Close, nil
}

func runWriter(ctx context.Context, cfg core.Config, log *slog.Logger) error {
	writerService, healthService, closeAll, err := bootstrapWriter(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer closeAll()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthService.LiveHandler)
	go func() {
		if err := core.Serve(ctx, log, cfg.ListenAddr, mux); err != nil {
			log.Error("health endpoint", "err", err)
		}
	}()

	log.Info("consuming")
	return writerService.Run(ctx)
}

func bootstrapWriter(ctx context.Context, cfg core.Config, log *slog.Logger) (*writer.Service, *health.Service, func(), error) {
	if err := core.Required("DATABASE_URL", cfg.DatabaseURL); err != nil {
		return nil, nil, nil, err
	}
	brokers, err := cfg.Brokers()
	if err != nil {
		return nil, nil, nil, err
	}
	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, nil, nil, err
	}
	consumer, err := kafka.NewConsumer(brokers, cfg.KafkaTopic, cfg.KafkaGroup)
	if err != nil {
		st.Close()
		return nil, nil, nil, err
	}

	writerService := writer.NewService(consumer, st, log)
	healthService := health.NewService(nil)

	closeAll := func() {
		consumer.Close()
		st.Close()
	}
	return writerService, healthService, closeAll, nil
}

// migrate waits up to 5 minutes for the database, then applies the migrations
// and fails if any relation with tenant_id is left open to another tenant.
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
		return fmt.Errorf("relations with tenant_id open to application roles without tenant isolation: %v", unprotected)
	}
	log.Info("migrations applied")
	return nil
}
