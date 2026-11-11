// Command writeapi is UsageWriteAPI: it accepts usage events and produces them
// to Kafka. It must not link the database driver (apps/usage/README.md).
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
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/writeapi"
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
	brokers, err := cfg.Brokers()
	if err != nil {
		return err
	}
	auth, err := kafka.SCRAM(cfg.KafkaUsername, cfg.KafkaPassword)
	if err != nil {
		return err
	}
	producer, err := kafka.NewProducer(brokers, cfg.KafkaTopic, auth...)
	if err != nil {
		return err
	}
	defer producer.Close()

	writeAPIService := writeapi.NewService(producer, log)
	healthService := health.NewService(producer.Ping)

	mux := http.NewServeMux()
	writeAPIService.Register(mux)
	mux.HandleFunc("GET /healthz", healthService.LiveHandler)
	mux.HandleFunc("GET /readyz", healthService.ReadyHandler)

	return httpx.Serve(ctx, log, cfg.ListenAddr, mux)
}
