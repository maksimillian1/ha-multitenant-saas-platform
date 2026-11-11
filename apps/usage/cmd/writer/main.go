// Command writer is Usage Writer: it consumes the usage topic and stores every
// event once. It must not link the HTTP ingestion code or the migrations
// (apps/usage/README.md).
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/core"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/health"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/httpx"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/kafka"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/store"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/writer"
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

func run(ctx context.Context, cfg core.Config, log *slog.Logger) error {
	if err := core.Required("DATABASE_URL", cfg.DatabaseURL); err != nil {
		return err
	}
	brokers, err := cfg.Brokers()
	if err != nil {
		return err
	}
	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()
	auth, err := kafka.SCRAM(cfg.KafkaUsername, cfg.KafkaPassword)
	if err != nil {
		return err
	}
	consumer, err := kafka.NewConsumer(brokers, cfg.KafkaTopic, cfg.KafkaGroup, auth...)
	if err != nil {
		return err
	}
	defer consumer.Close()

	writerService := writer.NewService(consumer, st, log)
	healthService := health.NewService(nil)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthService.LiveHandler)
	go func() {
		if err := httpx.Serve(ctx, log, cfg.ListenAddr, mux); err != nil {
			log.Error("health endpoint", "err", err)
		}
	}()

	log.Info("consuming")
	return writerService.Run(ctx)
}
