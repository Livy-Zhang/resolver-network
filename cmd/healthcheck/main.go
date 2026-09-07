package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"
)

func main() {
	target := "http://127.0.0.1:8080/readyz"
	if len(os.Args) == 2 {
		target = os.Args[1]
	}
	if len(os.Args) > 2 {
		fmt.Fprintln(os.Stderr, "usage: healthcheck [url]")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := check(ctx, http.DefaultClient, target); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func check(ctx context.Context, client *http.Client, target string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return fmt.Errorf("create readiness request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("readiness request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("readiness returned %s", response.Status)
	}
	return nil
}
