package monthly

import (
	"context"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"resolver-network/internal/database"
)

type postgresRootSubmitter struct{}
func (postgresRootSubmitter) EnsureRoot(context.Context, string, string, *big.Int, [32]byte, *big.Int) (common.Hash, bool, error) { return common.HexToHash("0x1234"), true, nil }

func TestRootWorkerSubmitsPostgresPendingRoot(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL"); if url == "" { t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests") }
	if err := database.ValidateTestDatabaseURL(url); err != nil { t.Fatal(err) }
	ctx := context.Background(); db, err := database.Open(ctx, url); if err != nil { t.Fatal(err) }; defer db.Close(); if err = db.Migrate(ctx); err != nil { t.Fatal(err) }
	month := time.Date(2099, 12, 1, 0, 0, 0, 0, time.UTC); resolver := "0x00000000000000000000000000000000000000f6"; distributor := "0x00000000000000000000000000000000000000f7"; token := "0x00000000000000000000000000000000000000f8"; root := "0x0000000000000000000000000000000000000000000000000000000000000001"
	cleanup := func(){ _, _ = db.Pool.Exec(ctx,"DELETE FROM monthly_root_submissions WHERE month_start=$1",month); _, _ = db.Pool.Exec(ctx,"DELETE FROM monthly_workflows WHERE month_start=$1",month) }; cleanup(); defer cleanup()
	if _, err = db.Pool.Exec(ctx,"INSERT INTO monthly_workflows(month_start,status) VALUES($1,'root_pending')",month); err != nil { t.Fatal(err) }
	_, err = db.Pool.Exec(ctx,`INSERT INTO monthly_root_submissions(month_start,resolver_address,distributor_address,reward_token_address,reward_rate,total_reward,epoch_id,merkle_root) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,month,resolver,distributor,token,"1","10","209912",root); if err != nil { t.Fatal(err) }
	w := RootWorker{DB: db, Submitter: postgresRootSubmitter{}, BatchSize: 10}; if err = w.RunOnce(ctx); err != nil { t.Fatal(err) }
	var status, hash string; if err = db.Pool.QueryRow(ctx,"SELECT root_submission_status,root_submission_tx_hash FROM monthly_root_submissions WHERE month_start=$1 AND resolver_address=$2",month,resolver).Scan(&status,&hash); err != nil { t.Fatal(err) }; if status != "submitted" || hash == "" { t.Fatalf("status=%q hash=%q",status,hash) }
}
