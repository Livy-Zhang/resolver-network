package config

// reads environment variables and provides a structured configuration object
import (
	"fmt"
	"net"
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
	databaseURL, err := databaseURLFromEnv()
	if err != nil {
		return Config{}, err
	}
	c := Config{
		HTTPAddr:                 envOrDefault("HTTP_ADDR", "127.0.0.1:8080"),
		DatabaseURL:              databaseURL,
		DatabaseReadURL:          envOrDefault("DATABASE_READ_URL", databaseURL),
		GraphEndpoint:            os.Getenv("GRAPH_ENDPOINT"),
		RPCURL:                   os.Getenv("RPC_URL"),
		RewardsUpdaterPrivateKey: os.Getenv("REWARDS_UPDATER_PRIVATE_KEY"),
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
	databaseURL, err := databaseURLFromEnv()
	if err != nil {
		return Config{}, err
	}
	return Config{DatabaseURL: databaseURL}, nil
}

// databaseURLFromEnv supports both a conventional DATABASE_URL and the
// separate fields exposed by the RDS-managed Secrets Manager secret. The
// latter lets ECS inject the generated password without duplicating it in a
// second secret or requiring callers to URL-escape it.
func databaseURLFromEnv() (string, error) {
	if value := os.Getenv("DATABASE_URL"); value != "" {
		return value, nil
	}

	host := os.Getenv("DATABASE_HOST")
	user := os.Getenv("DATABASE_USER")
	password := os.Getenv("DATABASE_PASSWORD")
	if host == "" || user == "" || password == "" {
		return "", fmt.Errorf("DATABASE_URL or DATABASE_HOST, DATABASE_USER, and DATABASE_PASSWORD are required")
	}

	databaseName := envOrDefault("DATABASE_NAME", "resolver_network")
	port := envOrDefault("DATABASE_PORT", "5432")
	sslMode := envOrDefault("DATABASE_SSLMODE", "require")
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(user, password),
		Host:   net.JoinHostPort(host, port),
		Path:   "/" + databaseName,
	}
	q := u.Query()
	q.Set("sslmode", sslMode)
	u.RawQuery = q.Encode()
	return u.String(), nil
}
