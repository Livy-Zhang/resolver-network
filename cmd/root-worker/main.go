package main

// run root submission worker for resolver-network
import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

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
	if err := Run(ctx, os.Args[1:], cfg); err != nil {
		slog.Error("root worker failed", "error", err)
		os.Exit(1)
	}
}

func Run(ctx context.Context, args []string, cfg config.Config) error {
	fs := flag.NewFlagSet("root-worker", flag.ContinueOnError)
	month := fs.String("month", "", "YYYYMM; reserved for workflow compatibility")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if *month != "" {
		if _, err := time.Parse("200601", *month); err != nil {
			return fmt.Errorf("invalid month: %w", err)
		}
	}
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
	worker := monthly.RootWorker{DB: db, Submitter: submitter, BatchSize: 50}
	slog.Info("root transaction worker started", "status", "started")
	if err = worker.RunUntilIdle(ctx, time.Minute); err != nil && ctx.Err() == nil {
		return err
	}
	return ctx.Err()
}
