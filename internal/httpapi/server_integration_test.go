package httpapi

import (
	"context"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"resolver-network/internal/database"
)

func integrationDB(t *testing.T) (*database.DB, time.Time) {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL API integration tests")
	}
	if err := database.ValidateTestDatabaseURL(raw); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Migrate(context.Background()); err != nil {
		db.Close()
		t.Fatal(err)
	}
	month := time.Date(2099, 8, 1, 0, 0, 0, 0, time.UTC)
	if err = db.EnsureMonthlyPartition(context.Background(), month); err != nil {
		db.Close()
		t.Fatal(err)
	}
	return db, month
}

func TestAPIQueriesRealPostgres(t *testing.T) {
	db, month := integrationDB(t)
	defer db.Close()
	ctx := context.Background()
	_, _ = db.Pool.Exec(ctx, "DELETE FROM delegation_monthly WHERE month_start=$1", month)
	_, _ = db.Pool.Exec(ctx, "DELETE FROM monthly_root_submissions WHERE month_start=$1", month)
	_, _ = db.Pool.Exec(ctx, "DELETE FROM resolver_monthly_reward_rates WHERE month_start=$1", month)
	defer func() {
		_, _ = db.Pool.Exec(ctx, "DELETE FROM delegation_monthly WHERE month_start=$1", month)
		_, _ = db.Pool.Exec(ctx, "DELETE FROM monthly_root_submissions WHERE month_start=$1", month)
		_, _ = db.Pool.Exec(ctx, "DELETE FROM resolver_monthly_reward_rates WHERE month_start=$1", month)
	}()
	resolvers := []string{"0x00000000000000000000000000000000000000a1", "0x00000000000000000000000000000000000000a2"}
	for i, resolver := range resolvers {
		distributor := "0x00000000000000000000000000000000000000b" + string(rune('1'+i))
		_, err := db.Pool.Exec(ctx, "INSERT INTO resolver_monthly_reward_rates(month_start,resolver_address,distributor_address,reward_token_address,reward_rate,rewards_updater) VALUES($1,$2,$3,$4,$5,$6)", month, resolver, distributor, "0x00000000000000000000000000000000000000c1", "10", "0x00000000000000000000000000000000000000d1")
		if err != nil {
			t.Fatal(err)
		}
		_, err = db.Pool.Exec(ctx, "INSERT INTO delegation_monthly(month_start,month_end,resolver_address,distributor_address,delegator_address,reward_rate,delegation_weight,reward_amount,resolver_total_reward,epoch_id,merkle_root,merkle_proof,indexed_block_number) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)", month, month.AddDate(0, 1, 0), resolver, distributor, "0x00000000000000000000000000000000000000e1", "10", "100", "1000", "1000", "209908", "0x"+strings.Repeat("1", 64), []string{}, 123)
		if err != nil {
			t.Fatal(err)
		}
		status := "confirmed"
		if i == 1 {
			status = "failed"
		}
		_, err = db.Pool.Exec(ctx, `INSERT INTO monthly_root_submissions(month_start,resolver_address,distributor_address,reward_token_address,rewards_updater,reward_rate,total_reward,epoch_id,merkle_root,root_submission_status) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT DO NOTHING`, month, resolver, distributor, "0x00000000000000000000000000000000000000c1", "0x00000000000000000000000000000000000000d1", "10", "1000", "209908", "0x"+strings.Repeat("1", 64), status)
		if err != nil {
			t.Fatal(err)
		}
	}
	server := New(db)
	for _, path := range []string{"/v1/monthly-rewards-details?month=209908", "/v1/merkle-proof-submission?month=209908"} {
		req := httptest.NewRequest("GET", path, nil)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("%s status=%d", path, rec.Code)
		}
		body, _ := io.ReadAll(rec.Result().Body)
		if !strings.Contains(string(body), resolvers[0]) {
			t.Fatalf("%s missing resolver: %s", path, body)
		}
		text := string(body)
		for _, want := range []string{"209908", "0x00000000000000000000000000000000000000b1", "1000", "209908"} {
			if !strings.Contains(text, want) { t.Fatalf("%s missing %q: %s", path, want, text) }
		}
		if strings.Contains(path, "monthly-rewards-details") {
			for _, want := range []string{"delegationWeight", "rewardAmount", "10"} {
				if !strings.Contains(text, want) { t.Fatalf("details missing %q: %s", want, text) }
			}
		} else {
			for _, want := range []string{"confirmed", "failed", "totalReward", "epochId"} {
				if !strings.Contains(text, want) { t.Fatalf("submissions missing %q: %s", want, text) }
			}
		}
	}
}

func TestAPIEmptyMonthRealPostgres(t *testing.T) {
	db, month := integrationDB(t)
	defer db.Close()
	ctx := context.Background()
	_, _ = db.Pool.Exec(ctx, "DELETE FROM delegation_monthly WHERE month_start=$1", month)
	req := httptest.NewRequest("GET", "/v1/monthly-rewards-details?month=209908", nil)
	rec := httptest.NewRecorder()
	New(db).ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAPIRealPostgresConnectionInterrupted(t *testing.T) {
	db, _ := integrationDB(t)
	server := New(db)
	db.Close()
	req := httptest.NewRequest("GET", "/v1/monthly-rewards-details?month=209908", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != 500 {
		t.Fatalf("status=%d want 500", rec.Code)
	}
}
