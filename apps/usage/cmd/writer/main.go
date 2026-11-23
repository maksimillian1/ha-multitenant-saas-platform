// Command writer must not link the HTTP ingestion code or the migrations.
// `writer replay` needs no database and runs as the KafkaUser usage-replay.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

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

	cmd := run
	if len(os.Args) > 1 && os.Args[1] == "replay" {
		cmd = func(ctx context.Context, cfg core.Config, log *slog.Logger) error {
			return replay(ctx, cfg, log, os.Args[2:])
		}
	}
	if err := cmd(ctx, core.LoadConfig(), log); err != nil {
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
	auth, err := kafka.ClientAuth(cfg.KafkaCAFile, cfg.KafkaUsername, cfg.KafkaPassword)
	if err != nil {
		return err
	}
	consumer, err := kafka.NewConsumer(brokers, cfg.KafkaTopic, cfg.KafkaGroup, auth...)
	if err != nil {
		return err
	}
	defer consumer.Close()
	dlq, err := kafka.NewDLQ(brokers, cfg.KafkaDLQTopic, auth...)
	if err != nil {
		return err
	}
	defer dlq.Close()

	writerService := writer.NewService(consumer, st, dlq, log)
	healthService := health.NewService(nil)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthService.LiveHandler)
	mux.HandleFunc("GET /metrics", writerService.MetricsHandler)
	go func() {
		if err := httpx.Serve(ctx, log, cfg.ListenAddr, mux); err != nil {
			log.Error("health endpoint", "err", err)
		}
	}()

	log.Info("consuming")
	return writerService.Run(ctx)
}

func replay(ctx context.Context, cfg core.Config, log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	partition := fs.Int("partition", -1, "partition of the DLQ topic")
	from := fs.Int64("from", -1, "first offset to replay")
	to := fs.Int64("to", -1, "last offset to replay, included")
	timeout := fs.Duration("timeout", 2*time.Minute, "give up after this long")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *partition < 0 || *from < 0 || *to < 0 {
		return fmt.Errorf("-partition, -from and -to are required")
	}
	brokers, err := cfg.Brokers()
	if err != nil {
		return err
	}
	auth, err := kafka.ClientAuth(cfg.KafkaCAFile, cfg.KafkaUsername, cfg.KafkaPassword)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	spec := writer.ReplaySpec{Partition: int32(*partition), From: *from, To: *to} //nolint:gosec // G115: a Kafka partition number fits in int32
	n, err := writer.Replay(ctx, brokers, cfg.KafkaDLQTopic, cfg.KafkaTopic, spec, auth...)
	if err != nil {
		return err
	}
	log.Info("replayed", "records", n, "dlq", cfg.KafkaDLQTopic, "partition", spec.Partition, "from", spec.From, "to", spec.To)
	return nil
}
