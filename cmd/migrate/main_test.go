package main

import (
	"context"
	"strings"
	"testing"

	"resolver-network/internal/config"
)

func TestRunRejectsInvalidArgumentsBeforeConnecting(t *testing.T) {
	for _, args := range [][]string{{}, {"bad"}, {"up", "status"}} {
		err := Run(context.Background(), args, config.Config{DatabaseURL: "postgres://invalid"})
		if err == nil || !strings.Contains(err.Error(), "usage:") {
			t.Fatalf("args=%v err=%v", args, err)
		}
	}
}
