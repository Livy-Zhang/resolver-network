package main

// run merkle worker for resolver-network
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
	"resolver-network/internal/ethereum"
	"resolver-network/internal/monthly"
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
		slog.Error("merkle worker failed", "error", err)
		os.Exit(1)
	}
}

func Run(ctx context.Context, args []string, cfg config.Config) error {
	fs := flag.NewFlagSet("merkle-worker", flag.ContinueOnError)
	monthArg := fs.String("month", "", "YYYYMM")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	month := time.Now().UTC()
	month = time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	if *monthArg != "" {
		var err error
		month, err = time.Parse("200601", *monthArg)
		if err != nil {
			return fmt.Errorf("invalid month: %w", err)
		}
	}
	db, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("database failed: %w", err)
	}
	defer db.Close()
	chain, err := ethereum.New(ctx, cfg.RPCURL)
	if err != nil {
		return fmt.Errorf("rpc failed: %w", err)
	}
	defer chain.Close()
	w := monthly.MerkleWorker{Store: db, Build: func(ctx context.Context, m time.Time) ([]database.MonthlyAllocation, error) {
		weights, err := db.LoadWeights(ctx, m)
		if err != nil {
			return nil, err
		}
		rates, err := db.RateSnapshots(ctx, m)
		if err != nil {
			return nil, err
		}
		chainID, err := chain.ChainID(ctx)
		if err != nil {
			return nil, err
		}
		return monthly.BuildMerkleAllocations(weights, rates, chainID, m)
	}}
	if err = w.RunOnce(ctx, month); err != nil {
		return fmt.Errorf("merkle worker failed: %w", err)
	}
	slog.Info("merkle worker completed", "month", month.Format("200601"), "status", "root_pending")
	return nil
}
