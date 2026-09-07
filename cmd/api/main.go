package main

// run the api server
import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"resolver-network/internal/config"
	"resolver-network/internal/database"
	"resolver-network/internal/httpapi"
	"syscall"
	"time"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	cfg, err := config.LoadAPI()
	if err != nil {
		slog.Error("config failed", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := Run(ctx, nil, cfg); err != nil {
		slog.Error("api failed", "error", err)
		os.Exit(1)
	}
}
func Run(ctx context.Context, args []string, cfg config.Config) error {
	if len(args) != 0 {
		return fmt.Errorf("unexpected arguments: %v", args)
	}
	db, err := database.Open(ctx, cfg.DatabaseReadURL)
	if err != nil {
		return fmt.Errorf("database failed: %w", err)
	}
	defer db.Close()
	server := &http.Server{Addr: cfg.HTTPAddr, Handler: httpapi.New(db), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}
	ch := make(chan error, 1)
	go func() {
		slog.Info("http server listening", "addr", cfg.HTTPAddr, "status", "started")
		if e := server.ListenAndServe(); e != nil && e != http.ErrServerClosed {
			ch <- e
		}
	}()
	select {
	case e := <-ch:
		return fmt.Errorf("http server failed: %w", e)
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}
