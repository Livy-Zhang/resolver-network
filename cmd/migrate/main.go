package main

// run database migrations for resolver-network
import (
	"context"
	"fmt"
	"log"
	"os"

	"resolver-network/internal/config"
	"resolver-network/internal/database"
)

func main() {
	cfg, err := config.LoadMigration()
	if err != nil {
		log.Fatal(err)
	}
	if err := Run(context.Background(), os.Args[1:], cfg); err != nil {
		log.Fatal(err)
	}
}

// Run executes one migration CLI command using the supplied configuration.
// Keeping command dispatch outside main makes argument validation testable.
func Run(ctx context.Context, args []string, cfg config.Config) error {
	if len(args) != 1 || (args[0] != "up" && args[0] != "status") {
		return fmt.Errorf("usage: resolver-network-migrate <up|status>")
	}
	db, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	if args[0] == "up" {
		if err = db.Migrate(ctx); err != nil {
			return err
		}
		log.Print("database migrations applied")
		return nil
	}
	statuses, err := db.MigrationStatus(ctx)
	if err != nil {
		return err
	}
	for _, status := range statuses {
		fmt.Printf("%s\t%s\n", status.Version, map[bool]string{true: "applied", false: "pending"}[status.Applied])
	}
	return nil
}
