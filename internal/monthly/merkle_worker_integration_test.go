package monthly

import (
	"context"
	"math/big"
	"os"
	"testing"
	"time"

	"resolver-network/internal/database"
)

func TestMerkleWorkerWritesRewardsToPostgres(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	if err := database.ValidateTestDatabaseURL(url); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := database.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	month := time.Date(2099, 12, 1, 0, 0, 0, 0, time.UTC)
	if err = db.EnsureMonthlyPartition(ctx, month); err != nil {
		t.Fatal(err)
	}
	resolver := "0x00000000000000000000000000000000000000f1"
	distributor := "0x00000000000000000000000000000000000000f2"
	token := "0x00000000000000000000000000000000000000f3"
	delegator := "0x00000000000000000000000000000000000000f4"
	cleanup := func() {
		_, _ = db.Pool.Exec(ctx, "DELETE FROM delegation_monthly WHERE month_start=$1", month)
		_, _ = db.Pool.Exec(ctx, "DELETE FROM monthly_root_submissions WHERE month_start=$1", month)
		_, _ = db.Pool.Exec(ctx, "DELETE FROM resolver_monthly_reward_rates WHERE month_start=$1", month)
		_, _ = db.Pool.Exec(ctx, "DELETE FROM monthly_workflows WHERE month_start=$1", month)
	}
	cleanup()
	defer cleanup()
	if _, err = db.Pool.Exec(ctx, "INSERT INTO monthly_workflows(month_start,status) VALUES($1,'settled')", month); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, `INSERT INTO resolver_monthly_reward_rates(month_start,resolver_address,distributor_address,reward_token_address,reward_rate,rewards_updater) VALUES($1,$2,$3,$4,$5,$6)`, month, resolver, distributor, token, "1000000000000000000000000", "0x00000000000000000000000000000000000000f5"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, `INSERT INTO delegation_monthly(month_start,month_end,resolver_address,delegator_address,delegation_weight) VALUES($1,$2,$3,$4,$5)`, month, month.AddDate(0, 1, 0), resolver, delegator, "86400"); err != nil {
		t.Fatal(err)
	}
	w := MerkleWorker{Store: db, Build: func(ctx context.Context, m time.Time) ([]database.MonthlyAllocation, error) {
		weights, err := db.LoadWeights(ctx, m)
		if err != nil {
			return nil, err
		}
		rates, err := db.RateSnapshots(ctx, m)
		if err != nil {
			return nil, err
		}
		return BuildMerkleAllocations(weights, rates, big.NewInt(11155111), m)
	}}
	if err = w.RunOnce(ctx, month); err != nil {
		t.Fatal(err)
	}
	var amount, root, proof string
	if err = db.Pool.QueryRow(ctx, "SELECT reward_amount::text,merkle_root,merkle_proof::text FROM delegation_monthly WHERE month_start=$1 AND resolver_address=$2 AND delegator_address=$3", month, resolver, delegator).Scan(&amount, &root, &proof); err != nil {
		t.Fatal(err)
	}
	if amount == "" || root == "" || proof == "" {
		t.Fatalf("missing merkle output amount=%q root=%q proof=%q", amount, root, proof)
	}
	if amount != "2739" { t.Fatalf("reward amount=%q, want 2739", amount) }
	var total string
	if err = db.Pool.QueryRow(ctx, "SELECT resolver_total_reward::text FROM delegation_monthly WHERE month_start=$1 AND resolver_address=$2 AND delegator_address=$3", month, resolver, delegator).Scan(&total); err != nil { t.Fatal(err) }
	if total != amount { t.Fatalf("total reward=%q, want %q", total, amount) }
	var status string
	if err = db.Pool.QueryRow(ctx, "SELECT status FROM monthly_workflows WHERE month_start=$1", month).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "root_pending" {
		t.Fatalf("workflow status=%q", status)
	}
}
