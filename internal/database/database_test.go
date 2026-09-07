package database

import (
	"context"
	"math/big"
	"os"
	"resolver-network/internal/delegation"
	"resolver-network/internal/workflow"
	"testing"
	"time"
)

func TestMigrateAndEnsureMonthlyPartition(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	if err := ValidateTestDatabaseURL(url); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	month := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	if err = db.EnsureMonthlyPartition(ctx, month); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err = db.Pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version='001_initial_schema.sql')").Scan(&exists); err != nil || !exists {
		t.Fatalf("migration exists=%v err=%v", exists, err)
	}
}

func TestWorkflowTransitionsAreAcceptedByDatabase(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	if err := ValidateTestDatabaseURL(url); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	month := time.Date(2100, 12, 1, 0, 0, 0, 0, time.UTC)
	if _, err = db.Pool.Exec(ctx, "DELETE FROM monthly_workflows WHERE month_start=$1", month); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, "INSERT INTO monthly_workflows(month_start,status) VALUES($1,$2)", month, workflow.SettlementPending); err != nil {
		t.Fatal(err)
	}
	for _, next := range []workflow.Stage{
		workflow.SettlementDone,
		workflow.MerklePending,
		workflow.MerkleProcessing,
		workflow.RootPending,
		workflow.RootProcessing,
		workflow.Confirmed,
	} {
		if err = db.TransitionWorkflow(ctx, month, next); err != nil {
			t.Fatalf("transition to %q: %v", next, err)
		}
		current, err := db.WorkflowStatus(ctx, month)
		if err != nil {
			t.Fatal(err)
		}
		if current != string(next) {
			t.Fatalf("workflow status = %q, want %q", current, next)
		}
	}
}

func TestMigrationTransactionRollsBackOnFailure(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	if err := ValidateTestDatabaseURL(url); err != nil {
		t.Fatal(err)
	}
	db, err := Open(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, "CREATE TABLE migration_rollback_probe (id integer)")
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, "INSERT INTO migration_rollback_probe (missing_column) VALUES (1)")
	if err == nil {
		t.Fatal("expected deliberate migration failure")
	}
	var exists bool
	if err = db.Pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name='migration_rollback_probe')").Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("failed migration left schema changes behind")
	}
}

func TestEmptySnapshotIsDifferentFromMissingSnapshot(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	if err := ValidateTestDatabaseURL(url); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	month := time.Date(2099, 2, 1, 0, 0, 0, 0, time.UTC)
	if err = db.EnsureMonthlyPartition(ctx, month); err != nil {
		t.Fatal(err)
	}
	if err = db.SaveSettlement(ctx, month, 10, nil, delegation.State{}, nil); err != nil {
		t.Fatal(err)
	}
	state, found, err := db.LoadSnapshot(ctx, month)
	if err != nil || !found || len(state) != 0 {
		t.Fatalf("empty snapshot state=%v found=%v err=%v", state, found, err)
	}
	_, found, err = db.LoadSnapshot(ctx, month.AddDate(0, 1, 0))
	if err != nil || found {
		t.Fatalf("missing snapshot found=%v err=%v", found, err)
	}
}

func TestSaveSettlementIsIdempotentAndPreservesRootSubmissionState(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	if err := ValidateTestDatabaseURL(url); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	month := time.Date(2099, 3, 1, 0, 0, 0, 0, time.UTC)
	if err = db.EnsureMonthlyPartition(ctx, month); err != nil {
		t.Fatal(err)
	}
	_, _ = db.Pool.Exec(ctx, "DELETE FROM delegation_monthly WHERE month_start=$1", month)
	_, _ = db.Pool.Exec(ctx, "DELETE FROM delegation_month_end_states WHERE month_start=$1", month)
	_, _ = db.Pool.Exec(ctx, "DELETE FROM delegation_snapshot_checkpoints WHERE month_start=$1", month)
	defer func() {
		_, _ = db.Pool.Exec(ctx, "DELETE FROM delegation_monthly WHERE month_start=$1", month)
		_, _ = db.Pool.Exec(ctx, "DELETE FROM delegation_month_end_states WHERE month_start=$1", month)
		_, _ = db.Pool.Exec(ctx, "DELETE FROM delegation_snapshot_checkpoints WHERE month_start=$1", month)
	}()
	resolver := "0x00000000000000000000000000000000000000a1"
	_, err = db.Pool.Exec(ctx, `INSERT INTO resolver_monthly_reward_rates(month_start,resolver_address,distributor_address,reward_token_address,reward_rate,rewards_updater) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, month, resolver, "0x00000000000000000000000000000000000000a2", "0x00000000000000000000000000000000000000a3", "1", "0x00000000000000000000000000000000000000a4")
	if err != nil {
		t.Fatal(err)
	}
	allocation := MonthlyAllocation{
		Resolver: resolver, Distributor: "0x00000000000000000000000000000000000000a2", Delegator: "0x00000000000000000000000000000000000000d1",
		RewardRate: "1", Weight: "10", Amount: "1", TotalReward: "1", EpochID: "209903",
		MerkleRoot: "0x0000000000000000000000000000000000000000000000000000000000000001", Proof: []string{},
	}
	state := delegation.State{delegation.Key{Resolver: resolver, Delegator: allocation.Delegator}: big.NewInt(10)}
	if err = db.SaveSettlement(ctx, month, 10, nil, state, []MonthlyAllocation{allocation}); err != nil {
		t.Fatal(err)
	}
	if err = db.SetRootSubmissionHash(ctx, month, resolver, "0xabc"); err != nil {
		t.Fatal(err)
	}
	if err = db.MarkRootConfirmed(ctx, month, resolver); err != nil {
		t.Fatal(err)
	}
	if err = db.SaveSettlement(ctx, month, 10, nil, state, []MonthlyAllocation{allocation}); err != nil {
		t.Fatal(err)
	}
	submission, err := db.RootSubmission(ctx, month, resolver)
	if err != nil || submission.Hash == nil || *submission.Hash != "0xabc" || submission.Status != "confirmed" || submission.ConfirmedAt == nil {
		t.Fatalf("submission=%+v err=%v", submission, err)
	}
	if err = db.MarkRootFailed(ctx, month, resolver, "transaction reverted"); err != nil {
		t.Fatal(err)
	}
	submission, err = db.RootSubmission(ctx, month, resolver)
	if err != nil || submission.Status != "failed" || submission.Error == nil || *submission.Error != "transaction reverted" {
		t.Fatalf("submission=%+v err=%v", submission, err)
	}
}

func TestMonthlyRewardQueries(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	if err := ValidateTestDatabaseURL(url); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	month := time.Date(2099, 4, 1, 0, 0, 0, 0, time.UTC)
	if err = db.EnsureMonthlyPartition(ctx, month); err != nil {
		t.Fatal(err)
	}
	resolver := "0x00000000000000000000000000000000000000b1"
	distributor := "0x00000000000000000000000000000000000000b2"
	token := "0x00000000000000000000000000000000000000b3"
	delegator := "0x00000000000000000000000000000000000000b4"
	cleanup := func() {
		_, _ = db.Pool.Exec(ctx, "DELETE FROM delegation_monthly WHERE month_start=$1", month)
		_, _ = db.Pool.Exec(ctx, "DELETE FROM resolver_monthly_reward_rates WHERE month_start=$1", month)
	}
	cleanup()
	defer cleanup()
	_, err = db.Pool.Exec(ctx, `INSERT INTO resolver_monthly_reward_rates(month_start,resolver_address,distributor_address,reward_token_address,reward_rate) VALUES($1,$2,$3,$4,$5)`, month, resolver, distributor, token, "2000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Pool.Exec(ctx, `INSERT INTO delegation_monthly(month_start,month_end,resolver_address,distributor_address,delegator_address,reward_rate,delegation_weight,reward_amount,resolver_total_reward,epoch_id,merkle_root,merkle_proof) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, month, month.AddDate(0, 1, 0), resolver, distributor, delegator, "2000000000000000000", "86400", "172800", "172800", "209904", "0x00000000000000000000000000000000000000000000000000000000000000b5", []string{"0x01"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Pool.Exec(ctx, `INSERT INTO monthly_root_submissions(month_start,resolver_address,distributor_address,reward_token_address,rewards_updater,reward_rate,total_reward,epoch_id,merkle_root,root_submission_status) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT DO NOTHING`, month, resolver, distributor, token, "0x00000000000000000000000000000000000000d1", "2000000000000000000", "172800", "209904", "0x00000000000000000000000000000000000000000000000000000000000000b5", "confirmed")
	if err != nil {
		t.Fatal(err)
	}
	details, err := db.MonthlyRewardDetails(ctx, month)
	if err != nil || len(details) != 1 {
		t.Fatalf("details=%+v err=%v", details, err)
	}
	if details[0].Resolver != resolver || details[0].RewardToken != token || details[0].RewardRate != "2000000000000000000" || details[0].DelegationWeight != "86400" || details[0].RewardAmount != "172800" || len(details[0].Proof) != 1 {
		t.Fatalf("unexpected details=%+v", details[0])
	}
	submissions, err := db.MerkleProofSubmissions(ctx, month)
	if err != nil || len(submissions) != 1 {
		t.Fatalf("submissions=%+v err=%v", submissions, err)
	}
	if submissions[0].Resolver != resolver || submissions[0].TotalReward != "172800" || submissions[0].EpochID != "209904" || submissions[0].Status != "confirmed" {
		t.Fatalf("unexpected submission=%+v", submissions[0])
	}
	empty, err := db.MonthlyRewardDetails(ctx, month.AddDate(0, 1, 0))
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty details=%+v err=%v", empty, err)
	}
}

func TestListPendingRootSubmissionsHandlesTimeoutAndPermanentFailure(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	if err := ValidateTestDatabaseURL(url); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	month := time.Date(2099, 5, 1, 0, 0, 0, 0, time.UTC)
	resolver := "0x00000000000000000000000000000000000000c1"
	cleanup := func() {
		_, _ = db.Pool.Exec(ctx, "DELETE FROM monthly_root_submissions WHERE month_start=$1", month)
		_, _ = db.Pool.Exec(ctx, "DELETE FROM monthly_workflows WHERE month_start=$1", month)
	}
	cleanup()
	defer cleanup()
	if _, err = db.Pool.Exec(ctx, "INSERT INTO monthly_workflows(month_start,status) VALUES($1,'root_pending')", month); err != nil {
		t.Fatal(err)
	}
	args := []any{month, resolver, "0x00000000000000000000000000000000000000c2", "0x00000000000000000000000000000000000000c3", "1", "10", "209905", "0x0000000000000000000000000000000000000000000000000000000000000001"}
	if _, err = db.Pool.Exec(ctx, `INSERT INTO monthly_root_submissions(month_start,resolver_address,distributor_address,reward_token_address,reward_rate,total_reward,epoch_id,merkle_root,root_submission_status,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'processing',now()-interval '21 minutes')`, args...); err != nil {
		t.Fatal(err)
	}
	permanent := resolver + "d"
	args[1] = permanent
	if _, err = db.Pool.Exec(ctx, `INSERT INTO monthly_root_submissions(month_start,resolver_address,distributor_address,reward_token_address,reward_rate,total_reward,epoch_id,merkle_root,root_submission_status) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'failed_permanent')`, args...); err != nil {
		t.Fatal(err)
	}
	rows, err := db.ListPendingRootSubmissions(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Resolver != resolver || rows[0].Status != "processing" {
		t.Fatalf("pending rows=%+v", rows)
	}
}

func TestSaveMerkleAllocationsRollsBackOnConstraintFailure(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	if err := ValidateTestDatabaseURL(url); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	month := time.Date(2099, 6, 1, 0, 0, 0, 0, time.UTC)
	if err = db.EnsureMonthlyPartition(ctx, month); err != nil {
		t.Fatal(err)
	}
	resolver := "0x00000000000000000000000000000000000000e1"
	delegator := "0x00000000000000000000000000000000000000e2"
	cleanup := func() {
		_, _ = db.Pool.Exec(ctx, "DELETE FROM delegation_monthly WHERE month_start=$1", month)
		_, _ = db.Pool.Exec(ctx, "DELETE FROM resolver_monthly_reward_rates WHERE month_start=$1", month)
		_, _ = db.Pool.Exec(ctx, "DELETE FROM monthly_workflows WHERE month_start=$1", month)
	}
	cleanup()
	defer cleanup()
	if _, err = db.Pool.Exec(ctx, "INSERT INTO monthly_workflows(month_start,status) VALUES($1,'merkle_processing')", month); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, `INSERT INTO resolver_monthly_reward_rates(month_start,resolver_address,distributor_address,reward_token_address,reward_rate) VALUES($1,$2,$3,$4,$5)`, month, resolver, resolver, resolver, "1"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, `INSERT INTO delegation_monthly(month_start,month_end,resolver_address,delegator_address,delegation_weight) VALUES($1,$2,$3,$4,$5)`, month, month.AddDate(0, 1, 0), resolver, delegator, "1"); err != nil {
		t.Fatal(err)
	}
	allocations := []MonthlyAllocation{{Resolver: resolver, Distributor: resolver, Delegator: delegator, RewardRate: "1", Weight: "1", Amount: "1", TotalReward: "1", EpochID: "209906", MerkleRoot: "0x0000000000000000000000000000000000000000000000000000000000000001"}, {Resolver: resolver, Distributor: resolver, Delegator: resolver, RewardRate: "1", Weight: "1", Amount: "1", TotalReward: "1", EpochID: "209906", MerkleRoot: "bad"}}
	if err = db.SaveMerkleAllocations(ctx, month, allocations); err == nil {
		t.Fatal("expected constraint failure")
	}
	var amount *string
	if err = db.Pool.QueryRow(ctx, "SELECT reward_amount::text FROM delegation_monthly WHERE month_start=$1 AND resolver_address=$2 AND delegator_address=$3", month, resolver, delegator).Scan(&amount); err != nil {
		t.Fatal(err)
	}
	if amount != nil {
		t.Fatalf("transaction was not rolled back, amount=%v", *amount)
	}
}
