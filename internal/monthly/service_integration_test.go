package monthly

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"resolver-network/internal/database"
	"resolver-network/internal/ethereum"
	"resolver-network/internal/graph"
)

type integrationChain struct {
	finalized ethereum.Block
	opening   ethereum.Block
}

func (c integrationChain) Finalized(context.Context) (ethereum.Block, error) { return c.finalized, nil }
func (c integrationChain) BlockBefore(context.Context, time.Time, int64) (ethereum.Block, error) {
	return c.opening, nil
}
func (integrationChain) ChainID(context.Context) (*big.Int, error) { return big.NewInt(11155111), nil }

func TestAggregateReadsGraphQLAndWritesSnapshotsAndMonthlyRows(t *testing.T) {
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

	month := time.Date(2098, 11, 1, 0, 0, 0, 0, time.UTC)
	previous := month.AddDate(0, -1, 0)
	next := month.AddDate(0, 1, 0)
	resolverA := "0x00000000000000000000000000000000000000a1"
	resolverB := "0x00000000000000000000000000000000000000b1"
	resolverC := "0x00000000000000000000000000000000000000c2"
	distributorA := "0x00000000000000000000000000000000000000a2"
	distributorB := "0x00000000000000000000000000000000000000b2"
	distributorC := "0x00000000000000000000000000000000000000c3"
	token := "0x00000000000000000000000000000000000000c1"

	cleanup := func() {
		_, _ = db.Pool.Exec(ctx, "DELETE FROM delegation_monthly WHERE month_start=$1", month)
		_, _ = db.Pool.Exec(ctx, "DELETE FROM delegation_month_end_states WHERE month_start=$1 OR month_start=$2", previous, month)
		_, _ = db.Pool.Exec(ctx, "DELETE FROM delegation_snapshot_checkpoints WHERE month_start=$1 OR month_start=$2", previous, month)
		_, _ = db.Pool.Exec(ctx, "DELETE FROM resolver_monthly_reward_rates WHERE month_start=$1 OR month_start=$2", month, next)
	}
	cleanup()
	defer cleanup()
	if err = db.EnsureMonthlyPartition(ctx, month); err != nil {
		t.Fatal(err)
	}
	for _, rate := range []struct{ resolver, distributor string }{{resolverA, distributorA}, {resolverB, distributorB}} {
		if _, err = db.Pool.Exec(ctx, "INSERT INTO resolver_monthly_reward_rates(month_start,resolver_address,distributor_address,reward_token_address,reward_rate) VALUES($1,$2,$3,$4,$5)", month, rate.resolver, rate.distributor, token, "10000000000000000000"); err != nil {
			t.Fatal(err)
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(request.Query, "query Meta"):
			_, _ = w.Write([]byte(`{"data":{"_meta":{"block":{"number":"100"},"hasIndexingErrors":false}}}`))
		case strings.Contains(request.Query, "query Delegations"):
			_, _ = w.Write([]byte(`{"data":{"delegations":[{"id":"snapshot-1","delegator":"0x00000000000000000000000000000000000000d1","amount":"100","resolver":{"id":"0x00000000000000000000000000000000000000a1"}},{"id":"snapshot-2","delegator":"0x00000000000000000000000000000000000000d2","amount":"50","resolver":{"id":"0x00000000000000000000000000000000000000a1"}},{"id":"snapshot-3","delegator":"0x00000000000000000000000000000000000000d3","amount":"80","resolver":{"id":"0x00000000000000000000000000000000000000b1"}}]}}`))
		case strings.Contains(request.Query, "query E"):
			_, _ = w.Write([]byte(`{"data":{"delegatedEvents":[{"id":"d1","delegator":"0x00000000000000000000000000000000000000d1","resolver":"0x00000000000000000000000000000000000000a1","amount":"20","timestamp":"4065724800","blockNumber":"11","logIndex":"0"},{"id":"d2","delegator":"0x00000000000000000000000000000000000000d4","resolver":"0x00000000000000000000000000000000000000b1","amount":"70","timestamp":"4066588800","blockNumber":"15","logIndex":"0"},{"id":"d3","delegator":"0x00000000000000000000000000000000000000d2","resolver":"0x00000000000000000000000000000000000000a1","amount":"10","timestamp":"4067971200","blockNumber":"18","logIndex":"0"}],"undelegatedEvents":[{"id":"u1","delegator":"0x00000000000000000000000000000000000000d2","resolver":"0x00000000000000000000000000000000000000a1","amount":"20","timestamp":"4065811200","blockNumber":"12","logIndex":"0"},{"id":"u2","delegator":"0x00000000000000000000000000000000000000d1","resolver":"0x00000000000000000000000000000000000000a1","amount":"40","timestamp":"4066416000","blockNumber":"14","logIndex":"0"},{"id":"u3","delegator":"0x00000000000000000000000000000000000000d3","resolver":"0x00000000000000000000000000000000000000a1","amount":"80","timestamp":"4067712000","blockNumber":"17","logIndex":"0"}],"redelegatedEvents":[{"id":"r1","delegator":"0x00000000000000000000000000000000000000d3","oldResolver":"0x00000000000000000000000000000000000000b1","newResolver":"0x00000000000000000000000000000000000000a1","amount":"80","timestamp":"4065984000","blockNumber":"13","logIndex":"0"},{"id":"r2","delegator":"0x00000000000000000000000000000000000000d1","oldResolver":"0x00000000000000000000000000000000000000a1","newResolver":"0x00000000000000000000000000000000000000b1","amount":"80","timestamp":"4067280000","blockNumber":"16","logIndex":"0"}]}}`))
		case strings.Contains(request.Query, "query Created"):
			if strings.Contains(request.Query, "rootPublisher") {
				t.Fatal("created-event query must use the current rewardsUpdater field, not rootPublisher")
			}
			if !strings.Contains(request.Query, "rewardsUpdater") {
				t.Fatal("created-event query must request rewardsUpdater")
			}
			_, _ = w.Write([]byte(`{"data":{"distributorCreatedEvents":[{"id":"created-c","resolver":"0x00000000000000000000000000000000000000c2","distributor":"0x00000000000000000000000000000000000000c3","rewardsUpdater":"0x00000000000000000000000000000000000000c2","rewardAddress":"0x00000000000000000000000000000000000000c1","rewardRate":"400","timestamp":"4066243200","blockNumber":"20","logIndex":"0"}]}}`))
		case strings.Contains(request.Query, "query Updated"):
			_, _ = w.Write([]byte(`{"data":{"rewardRateUpdatedEvents":[{"id":"updated-a-1","resolver":"0x00000000000000000000000000000000000000a1","distributor":"0x00000000000000000000000000000000000000a2","rewardAddress":"0x00000000000000000000000000000000000000c1","rewardRate":"200","timestamp":"4066848000","blockNumber":"21","logIndex":"0"},{"id":"updated-a-2","resolver":"0x00000000000000000000000000000000000000a1","distributor":"0x00000000000000000000000000000000000000a2","rewardAddress":"0x00000000000000000000000000000000000000c1","rewardRate":"300","timestamp":"4067452800","blockNumber":"22","logIndex":"0"}]}}`))
		default:
			t.Fatalf("unexpected GraphQL query: %s", request.Query)
		}
	}))
	defer server.Close()

	service := Service{DB: db, Graph: graph.New(server.URL), Ethereum: integrationChain{finalized: ethereum.Block{Number: 100, Timestamp: month.AddDate(0, 1, 1)}, opening: ethereum.Block{Number: 90, Timestamp: month.Add(-time.Second)}}}
	weights, err := service.Aggregate(ctx, month)
	if err != nil {
		t.Fatal(err)
	}
	if len(weights) != 6 {
		t.Fatalf("weights=%d", len(weights))
	}
	var checkpoints, previousStates, endingStates, monthlyRows int
	if err = db.Pool.QueryRow(ctx, "SELECT count(*) FROM delegation_snapshot_checkpoints WHERE month_start=$1 OR month_start=$2", previous, month).Scan(&checkpoints); err != nil {
		t.Fatal(err)
	}
	if err = db.Pool.QueryRow(ctx, "SELECT count(*) FROM delegation_month_end_states WHERE month_start=$1", previous).Scan(&previousStates); err != nil {
		t.Fatal(err)
	}
	if err = db.Pool.QueryRow(ctx, "SELECT count(*) FROM delegation_month_end_states WHERE month_start=$1", month).Scan(&endingStates); err != nil {
		t.Fatal(err)
	}
	if err = db.Pool.QueryRow(ctx, "SELECT count(*) FROM delegation_monthly WHERE month_start=$1", month).Scan(&monthlyRows); err != nil {
		t.Fatal(err)
	}
	if checkpoints != 2 || previousStates != 3 || endingStates != 3 || monthlyRows != 6 {
		t.Fatalf("checkpoints=%d previousStates=%d endingStates=%d monthlyRows=%d", checkpoints, previousStates, endingStates, monthlyRows)
	}
	rows, err := db.Pool.Query(ctx, "SELECT resolver,delegator,delegated_amount::text FROM delegation_month_end_states WHERE month_start=$1", month)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ending := map[string]string{}
	for rows.Next() {
		var resolver, delegator, amount string
		if err = rows.Scan(&resolver, &delegator, &amount); err != nil {
			t.Fatal(err)
		}
		ending[resolver+":"+delegator] = amount
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	wantEnding := map[string]string{
		resolverB + ":0x00000000000000000000000000000000000000d1": "80",
		resolverA + ":0x00000000000000000000000000000000000000d2": "40",
		resolverB + ":0x00000000000000000000000000000000000000d4": "70",
	}
	if len(ending) != len(wantEnding) {
		t.Fatalf("ending=%v", ending)
	}
	for key, amount := range wantEnding {
		if ending[key] != amount {
			t.Fatalf("ending[%s]=%q, want %q", key, ending[key], amount)
		}
	}
	allocationRows, err := db.Pool.Query(ctx, "SELECT resolver_address,delegator_address,delegation_weight::text FROM delegation_monthly WHERE month_start=$1", month)
	if err != nil {
		t.Fatal(err)
	}
	defer allocationRows.Close()
	allocations := map[string]string{}
	for allocationRows.Next() {
		var resolver, delegator, weight string
		if err = allocationRows.Scan(&resolver, &delegator, &weight); err != nil {
			t.Fatal(err)
		}
		allocations[resolver+":"+delegator] = weight
	}
	if err = allocationRows.Err(); err != nil {
		t.Fatal(err)
	}
	wantAllocations := map[string]string{
		resolverA + ":0x00000000000000000000000000000000000000d1": "160704000",
		resolverA + ":0x00000000000000000000000000000000000000d2": "83808000",
		resolverA + ":0x00000000000000000000000000000000000000d3": "138240000",
		resolverB + ":0x00000000000000000000000000000000000000d1": "76032000",
		resolverB + ":0x00000000000000000000000000000000000000d3": "27648000",
		resolverB + ":0x00000000000000000000000000000000000000d4": "114912000",
	}
	if len(allocations) != len(wantAllocations) {
		t.Fatalf("allocations=%v", allocations)
	}
	for key, want := range wantAllocations {
		if allocations[key] != want {
			t.Fatalf("allocation[%s]=%q, want %q", key, allocations[key], want)
		}
	}
	rateRows, err := db.Pool.Query(ctx, "SELECT resolver_address,distributor_address,reward_rate::text FROM resolver_monthly_reward_rates WHERE month_start=$1", next)
	if err != nil {
		t.Fatal(err)
	}
	defer rateRows.Close()
	nextRates := map[string]string{}
	for rateRows.Next() {
		var resolver, distributor, rate string
		if err = rateRows.Scan(&resolver, &distributor, &rate); err != nil {
			t.Fatal(err)
		}
		nextRates[resolver] = distributor + ":" + rate
	}
	if err = rateRows.Err(); err != nil {
		t.Fatal(err)
	}
	wantRates := map[string]string{
		resolverA: distributorA + ":300",
		resolverB: distributorB + ":10000000000000000000",
		resolverC: distributorC + ":400",
	}
	if len(nextRates) != len(wantRates) {
		t.Fatalf("next rates=%v", nextRates)
	}
	for resolver, want := range wantRates {
		if nextRates[resolver] != want {
			t.Fatalf("next rate for %s = %q, want %q", resolver, nextRates[resolver], want)
		}
	}
}
