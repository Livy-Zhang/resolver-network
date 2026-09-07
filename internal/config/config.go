package config

// reads environment variables and provides a structured configuration object
import (
	"fmt"
	"net/url"
	"os"
)

type Config struct {
	HTTPAddr                 string
	DatabaseURL              string
	DatabaseReadURL          string
	GraphEndpoint            string
	RPCURL                   string
	RewardsUpdaterPrivateKey string
}

func Load() (Config, error) {
	return load(true)
}

func LoadAPI() (Config, error) {
	return load(false)
}

func load(requireChain bool) (Config, error) {
	c := Config{
		HTTPAddr:                 envOrDefault("HTTP_ADDR", "127.0.0.1:8080"),
		DatabaseURL:              os.Getenv("DATABASE_URL"),
		DatabaseReadURL:          envOrDefault("DATABASE_READ_URL", os.Getenv("DATABASE_URL")),
		GraphEndpoint:            os.Getenv("GRAPH_ENDPOINT"),
		RPCURL:                   os.Getenv("RPC_URL"),
		RewardsUpdaterPrivateKey: os.Getenv("REWARDS_UPDATER_PRIVATE_KEY"),
	}
	if c.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	if requireChain && c.GraphEndpoint == "" {
		return Config{}, fmt.Errorf("GRAPH_ENDPOINT is required")
	}
	if requireChain && c.RPCURL == "" {
		return Config{}, fmt.Errorf("RPC_URL is required")
	}
	if requireChain && c.RewardsUpdaterPrivateKey == "" {
		return Config{}, fmt.Errorf("REWARDS_UPDATER_PRIVATE_KEY is required")
	}
	if requireChain {
		for name, value := range map[string]string{"GRAPH_ENDPOINT": c.GraphEndpoint, "RPC_URL": c.RPCURL} {
			u, err := url.ParseRequestURI(value)
			if err != nil || u.Scheme == "" || u.Host == "" {
				return Config{}, fmt.Errorf("invalid %s", name)
			}
		}
	}
	return c, nil
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func LoadMigration() (Config, error) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	return Config{DatabaseURL: url}, nil
}
