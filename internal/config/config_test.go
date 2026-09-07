package config

import "testing"

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"DATABASE_URL",
		"DATABASE_READ_URL",
		"GRAPH_ENDPOINT",
		"RPC_URL",
		"REWARDS_UPDATER_PRIVATE_KEY",
	} {
		t.Setenv(key, "")
	}
}

func TestLoadRequiresEveryConnectionValue(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("GRAPH_ENDPOINT", "https://graph.example")
	t.Setenv("RPC_URL", "https://rpc.example")
	t.Setenv("REWARDS_UPDATER_PRIVATE_KEY", "key")
	if _, err := Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	t.Setenv("GRAPH_ENDPOINT", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected missing GRAPH_ENDPOINT error")
	}
}

func TestLoadRejectsInvalidEndpoints(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("GRAPH_ENDPOINT", "not-a-url")
	t.Setenv("RPC_URL", "https://rpc.example")
	t.Setenv("REWARDS_UPDATER_PRIVATE_KEY", "key")
	if _, err := Load(); err == nil {
		t.Fatal("expected invalid GRAPH_ENDPOINT error")
	}
}

func TestLoadAPIOnlyRequiresDatabase(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("DATABASE_URL", "postgres://example")
	c, err := LoadAPI()
	if err != nil || c.DatabaseReadURL != c.DatabaseURL {
		t.Fatalf("config=%+v err=%v", c, err)
	}
}

func TestLoadMigration(t *testing.T) {
	clearConfigEnv(t)
	if _, err := LoadMigration(); err == nil {
		t.Fatal("expected missing database error")
	}
	t.Setenv("DATABASE_URL", "postgres://example")
	c, err := LoadMigration()
	if err != nil || c.DatabaseURL == "" {
		t.Fatalf("config=%+v err=%v", c, err)
	}
}
