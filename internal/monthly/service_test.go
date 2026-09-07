package monthly

import (
	"context"
	"math/big"
	"strings"
	"testing"
	"time"

	"resolver-network/internal/database"
	"resolver-network/internal/delegation"
	"resolver-network/internal/ethereum"
	"resolver-network/internal/graph"
	"resolver-network/internal/metrics"
)

type fakeStore struct {
	rates   map[string]database.RateSnapshot
	saved   []database.MonthlyAllocation
	frozen  []database.RateChange
	saves   int
	freezes int
	root    database.RootSubmission
	hash    string
	saveErr error
	freezeErr error
}

func (f *fakeStore) EnsureMonthlyPartition(context.Context, time.Time) error { return nil }
func (f *fakeStore) LoadSnapshot(context.Context, time.Time) (delegation.State, bool, error) {
	return delegation.State{}, true, nil
}
func (f *fakeStore) SaveSnapshot(context.Context, time.Time, delegation.State) error { return nil }
func (f *fakeStore) RateSnapshots(context.Context, time.Time) (map[string]database.RateSnapshot, error) {
	return f.rates, nil
}
func (f *fakeStore) FreezeNextRates(_ context.Context, _ time.Time, c []database.RateChange) error {
	f.freezes++
	f.frozen = c
	return f.freezeErr
}
func (f *fakeStore) SaveSettlement(_ context.Context, _ time.Time, _ int64, _ []delegation.Weight, _ delegation.State, a []database.MonthlyAllocation) error {
	f.saves++
	f.saved = a
	return f.saveErr
}
func (f *fakeStore) RootSubmission(context.Context, time.Time, string) (database.RootSubmission, error) {
	return f.root, nil
}
func (f *fakeStore) MarkRootConfirmed(context.Context, time.Time, string) error      { return nil }
func (f *fakeStore) MarkRootFailed(context.Context, time.Time, string, string) error { return nil }
func (f *fakeStore) SetRootSubmissionHash(_ context.Context, _ time.Time, _ string, h string) error {
	f.hash = h
	return nil
}

type fakeGraph struct {
	events  []delegation.Event
	changes []graph.RewardRateEvent
	meta    graph.Meta
}

func (f fakeGraph) Meta(context.Context) (graph.Meta, error) { return f.meta, nil }
func (fakeGraph) Delegations(context.Context, int64) ([]graph.DelegationSnapshot, error) {
	return nil, nil
}
func (f fakeGraph) Events(context.Context, *time.Time, time.Time, int64) ([]delegation.Event, error) {
	return f.events, nil
}
func (f fakeGraph) RewardRateEvents(context.Context, time.Time, time.Time, int64) ([]graph.RewardRateEvent, error) {
	return f.changes, nil
}

type fakeChain struct{ finalized ethereum.Block }

func (f fakeChain) Finalized(context.Context) (ethereum.Block, error) {
	return f.finalized, nil
}
func (fakeChain) BlockBefore(context.Context, time.Time, int64) (ethereum.Block, error) {
	return ethereum.Block{}, nil
}
func (fakeChain) ChainID(context.Context) (*big.Int, error) { return big.NewInt(11155111), nil }

func TestAggregateCalculatesAndSavesWithoutSubmitting(t *testing.T) {
	month := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	resolver := "0x0000000000000000000000000000000000000001"
	store := &fakeStore{rates: map[string]database.RateSnapshot{resolver: {Resolver: resolver, Distributor: "0x0000000000000000000000000000000000000002", RewardToken: "0x0000000000000000000000000000000000000003", RewardRate: "1000000000000000000"}}}
	service := Service{DB: store, Graph: fakeGraph{meta: graph.Meta{BlockNumber: 10}, events: []delegation.Event{{Kind: delegation.Delegated, ID: "e", Resolver: resolver, Delegator: "0x0000000000000000000000000000000000000004", Amount: big.NewInt(100), Timestamp: month.Add(time.Hour)}}}, Ethereum: fakeChain{finalized: ethereum.Block{Number: 10, Timestamp: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)}}}
	weights, err := service.Aggregate(context.Background(), month)
	if err != nil {
		t.Fatal(err)
	}
	if len(weights) != 1 || len(store.saved) != 0 || store.hash != "" {
		t.Fatalf("weights=%d saved=%d hash=%q", len(weights), len(store.saved), store.hash)
	}
}

func TestAggregateRejectsSubgraphBehindFinalizedBlock(t *testing.T) {
	month := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	store := &fakeStore{}
	service := Service{
		DB:       store,
		Graph:    fakeGraph{meta: graph.Meta{BlockNumber: 99}},
		Ethereum: fakeChain{finalized: ethereum.Block{Number: 100, Timestamp: month.AddDate(0, 1, 1)}},
	}
	_, err := service.Aggregate(context.Background(), month)
	if err == nil || !strings.Contains(err.Error(), "subgraph is behind finalized block: 99 < 100") {
		t.Fatalf("err = %v", err)
	}
	if len(store.saved) != 0 {
		t.Fatalf("settlement was saved despite an unsafe subgraph")
	}
}

func TestAggregateRejectsWhenFinalizedTimestampDoesNotReachMonthEnd(t *testing.T) {
	month := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	store := &fakeStore{}
	service := Service{
		DB:       store,
		Graph:    fakeGraph{meta: graph.Meta{BlockNumber: 100}},
		Ethereum: fakeChain{finalized: ethereum.Block{Number: 100, Timestamp: month.AddDate(0, 1, 0).Add(-time.Second)}},
	}
	_, err := service.Aggregate(context.Background(), month)
	if err == nil || !strings.Contains(err.Error(), "month boundary") {
		t.Fatalf("err = %v", err)
	}
	if len(store.saved) != 0 {
		t.Fatalf("settlement was saved before the month boundary finalized")
	}
}

func TestAggregateCompletesWithoutRewards(t *testing.T) {
	month := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	store := &fakeStore{}
	service := Service{
		DB:       store,
		Graph:    fakeGraph{meta: graph.Meta{BlockNumber: 100}},
		Ethereum: fakeChain{finalized: ethereum.Block{Number: 100, Timestamp: month.AddDate(0, 1, 1)}},
	}
	weights, err := service.Aggregate(context.Background(), month)
	if err != nil {
		t.Fatal(err)
	}
	if len(weights) != 0 || len(store.saved) != 0 {
		t.Fatalf("weights=%d allocations=%d, want no rewards", len(weights), len(store.saved))
	}
	if store.saves != 1 {
		t.Fatalf("settlement saves = %d, want 1", store.saves)
	}
	if store.freezes != 2 {
		t.Fatalf("rate freezes = %d, want 2", store.freezes)
	}
}

func TestAggregateStopsWhenSettlementSaveFails(t *testing.T) {
	month := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	store := &fakeStore{saveErr: context.DeadlineExceeded}
	service := Service{DB: store, Graph: fakeGraph{meta: graph.Meta{BlockNumber: 100}}, Ethereum: fakeChain{finalized: ethereum.Block{Number: 100, Timestamp: month.AddDate(0, 1, 1)}}}
	if _, err := service.Aggregate(context.Background(), month); err == nil || !strings.Contains(err.Error(), "deadline") { t.Fatalf("err=%v", err) }
	if store.freezes != 1 { t.Fatalf("freeze calls=%d, expected bootstrap only", store.freezes) }
}

func TestAggregateReturnsRateFreezeFailure(t *testing.T) {
	month := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	resolver := "0x0000000000000000000000000000000000000001"
	store := &fakeStore{rates: map[string]database.RateSnapshot{resolver: {RewardRate: "1", Distributor: "0x0000000000000000000000000000000000000002"}}, freezeErr: context.Canceled}
	service := Service{DB: store, Graph: fakeGraph{meta: graph.Meta{BlockNumber: 100}}, Ethereum: fakeChain{finalized: ethereum.Block{Number: 100, Timestamp: month.AddDate(0, 1, 1)}}}
	if _, err := service.Aggregate(context.Background(), month); err == nil || !strings.Contains(err.Error(), "canceled") { t.Fatalf("err=%v", err) }
}

func TestAggregateFailureMetrics(t *testing.T) {
	started, failed := metrics.SettlementsStarted.Value(), metrics.SettlementsFailed.Value()
	month := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	s := Service{DB: &fakeStore{}, Graph: fakeGraph{meta: graph.Meta{BlockNumber: 1}}, Ethereum: fakeChain{finalized: ethereum.Block{Number: 2, Timestamp: month.AddDate(0, 1, 1)}}}
	if _, err := s.Aggregate(context.Background(), month); err == nil { t.Fatal("expected finality error") }
	if metrics.SettlementsStarted.Value() != started+1 || metrics.SettlementsFailed.Value() != failed+1 { t.Fatal("settlement failure metrics not incremented") }
}

func TestFreezeNextRatesRejectsIncompleteEvent(t *testing.T) {
	s := Service{DB: &fakeStore{}}
	if err := s.freezeNextRates(context.Background(), time.Now(), []graph.RewardRateEvent{{ID: "bad"}}); err == nil { t.Fatal("expected invalid rate event") }
}
