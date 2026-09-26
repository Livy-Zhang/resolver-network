// root-poller processes every month that still has a pending root submission once.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"resolver-network/internal/config"
	"resolver-network/internal/database"
	"resolver-network/internal/monthly"
	"resolver-network/internal/rewards"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config failed", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err = Run(ctx, cfg); err != nil {
		slog.Error("root poller failed", "error", err)
		os.Exit(1)
	}
}

func Run(ctx context.Context, cfg config.Config) error {
	db, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("database failed: %w", err)
	}
	defer db.Close()
	submitter, err := rewards.NewSubmitter(ctx, cfg.RPCURL, cfg.RewardsUpdaterPrivateKey)
	if err != nil {
		return fmt.Errorf("submitter failed: %w", err)
	}
	defer submitter.Close()
	months, err := db.PendingRootSubmissionMonths(ctx, 100)
	if err != nil {
		return fmt.Errorf("pending root months: %w", err)
	}
	for _, month := range months {
		if err = (monthly.RootWorker{DB: db, Submitter: submitter, Month: month, BatchSize: 100}).RunOnce(ctx); err != nil {
			return fmt.Errorf("process %s: %w", month.Format("200601"), err)
		}
	}
	return nil
}
