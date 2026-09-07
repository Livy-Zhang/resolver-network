package main

// run settlement worker for resolver-network
import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"resolver-network/internal/config"
	"resolver-network/internal/database"
	"resolver-network/internal/ethereum"
	"resolver-network/internal/graph"
	"resolver-network/internal/monthly"
	"syscall"
	"time"
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
		slog.Error("settlement worker failed", "error", err)
		os.Exit(1)
	}
}

func Run(ctx context.Context, args []string, cfg config.Config) error {
	fs := flag.NewFlagSet("settlement-worker", flag.ContinueOnError)
	monthArg := fs.String("month", "", "YYYYMM")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	m := time.Now().UTC()
	var err error
	if *monthArg != "" {
		m, err = time.Parse("200601", *monthArg)
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
	service := monthly.Service{DB: db, Graph: graph.New(cfg.GraphEndpoint), Ethereum: chain}
	if _, err = service.Aggregate(ctx, m); err != nil {
		return fmt.Errorf("settlement failed: %w", err)
	}
	slog.Info("settlement worker completed", "month", m.Format("200601"), "status", "settled")
	return nil
}
