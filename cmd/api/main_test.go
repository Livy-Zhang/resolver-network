package main

import (
	"context"
	"os"
	"os/exec"
	"resolver-network/internal/config"
	"strings"
	"testing"
)

func TestRunRejectsUnexpectedArguments(t *testing.T) {
	err := Run(context.Background(), []string{"extra"}, config.Config{DatabaseReadURL: "postgres://invalid"})
	if err == nil || !strings.Contains(err.Error(), "unexpected arguments") {
		t.Fatalf("err=%v", err)
	}
}

func TestMainExitsNonZeroForInvalidConfig(t *testing.T) {
	const helperEnv = "RESOLVER_NETWORK_API_INVALID_CONFIG_HELPER"
	if os.Getenv(helperEnv) == "1" {
		os.Clearenv()
		main()
		os.Exit(0)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestMainExitsNonZeroForInvalidConfig$")
	cmd.Env = append(os.Environ(), helperEnv+"=1")
	err := cmd.Run()
	if err == nil {
		t.Fatal("main exited successfully with an invalid configuration")
	}
	if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() == 0 {
		t.Fatalf("expected a non-zero exit code, got %v", err)
	}
}
