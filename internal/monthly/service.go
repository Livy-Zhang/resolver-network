package monthly
// aggregation and reward settlement service
import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"resolver-network/internal/database"
	"resolver-network/internal/delegation"
	"resolver-network/internal/ethereum"
	"resolver-network/internal/graph"
	"resolver-network/internal/metrics"
	"resolver-network/internal/workflow"
	"strings"
	"time"
)

type Service struct {
	DB       Store
	Graph    GraphSource
	Ethereum ChainSource
}

type Store interface {
	EnsureMonthlyPartition(context.Context, time.Time) error
	LoadSnapshot(context.Context, time.Time) (delegation.State, bool, error)
	SaveSnapshot(context.Context, time.Time, delegation.State) error
	RateSnapshots(context.Context, time.Time) (map[string]database.RateSnapshot, error)
	FreezeNextRates(context.Context, time.Time, []database.RateChange) error
	SaveSettlement(context.Context, time.Time, int64, []delegation.Weight, delegation.State, []database.MonthlyAllocation) error
}

type GraphSource interface {
	Meta(context.Context) (graph.Meta, error)
	Delegations(context.Context, int64) ([]graph.DelegationSnapshot, error)
	Events(context.Context, *time.Time, time.Time, int64) ([]delegation.Event, error)
	RewardRateEvents(context.Context, time.Time, time.Time, int64) ([]graph.RewardRateEvent, error)
}

type ChainSource interface {
	Finalized(context.Context) (ethereum.Block, error)
	BlockBefore(context.Context, time.Time, int64) (ethereum.Block, error)
	ChainID(context.Context) (*big.Int, error)
}

func (s Service) Aggregate(ctx context.Context, month time.Time) (weights []delegation.Weight, err error) {
	metrics.SettlementsStarted.Add(1)
	monthID := month.Format("200601")
	slog.Info("settlement started", "month", monthID, "status", string(workflow.SettlementPending))
	defer func() {
		if err != nil {
			metrics.SettlementsFailed.Add(1)
			slog.Error("settlement failed", "month", monthID, "status", "failed", "error", err)
		} else {
			metrics.SettlementsSucceeded.Add(1)
			slog.Info("settlement completed", "month", monthID, "status", string(workflow.SettlementDone), "weights", len(weights))
		}
	}()
	start := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, month.Location())
	end := start.AddDate(0, 1, 0)
	finalized, e := s.validateFinality(ctx, end)
	if e != nil { return nil, e }
	if e = s.DB.EnsureMonthlyPartition(ctx, start); e != nil {
		return nil, fmt.Errorf("ensure monthly partition: %w", e)
	}
	opening, e := s.ensureOpeningSnapshot(ctx, start, finalized.Number)
	if e != nil { return nil, e }
	_, e = s.loadRates(ctx, start, finalized.Number)
	if e != nil { return nil, fmt.Errorf("load rates for %s: %w", start.Format("200601"), e) }
	weights, ending, e := s.calculateSettlement(ctx, opening, start, end, finalized.Number)
	if e != nil { return nil, e }
	if e = s.DB.SaveSettlement(ctx, start, finalized.Number, weights, ending, nil); e != nil {
		return nil, e
	}
	rateEvents, e := s.Graph.RewardRateEvents(ctx, start, end, finalized.Number)
	if e != nil {
		return nil, e
	}
	if e = s.freezeNextRates(ctx, start, rateEvents); e != nil {
		return nil, e
	}
	return weights, nil
}

func (s Service) calculateSettlement(ctx context.Context, opening delegation.State, start, end time.Time, atBlock int64) ([]delegation.Weight, delegation.State, error) {
	events, err := s.Graph.Events(ctx, &start, end, atBlock)
	if err != nil { return nil, nil, fmt.Errorf("events for %s at block %d: %w", start.Format("200601"), atBlock, err) }
	weights, ending, err := delegation.Calculate(opening, events, start, end)
	if err != nil { return nil, nil, fmt.Errorf("calculate settlement for %s: %w", start.Format("200601"), err) }
	return weights, ending, nil
}

func (s Service) validateFinality(ctx context.Context, monthEnd time.Time) (ethereum.Block, error) {
	finalized, err := s.Ethereum.Finalized(ctx)
	if err != nil { return ethereum.Block{}, fmt.Errorf("finalized block: %w", err) }
	if finalized.Timestamp.Before(monthEnd.UTC()) { return ethereum.Block{}, fmt.Errorf("month boundary %s is not finalized yet", monthEnd.UTC().Format(time.RFC3339)) }
	meta, err := s.Graph.Meta(ctx)
	if err != nil { return ethereum.Block{}, fmt.Errorf("subgraph metadata: %w", err) }
	if meta.BlockNumber < finalized.Number { return ethereum.Block{}, fmt.Errorf("subgraph is behind finalized block: %d < %d", meta.BlockNumber, finalized.Number) }
	return finalized, nil
}

func (s Service) ensureOpeningSnapshot(ctx context.Context, month time.Time, atBlock int64) (delegation.State, error) {
	snapshotMonth := month.AddDate(0, -1, 0)
	opening, found, err := s.DB.LoadSnapshot(ctx, snapshotMonth)
	if err != nil { return nil, err }
	if found { return opening, nil }
	boundary, err := s.Ethereum.BlockBefore(ctx, month, atBlock)
	if err != nil { return nil, fmt.Errorf("opening snapshot block: %w", err) }
	entities, err := s.Graph.Delegations(ctx, boundary.Number)
	if err != nil { return nil, fmt.Errorf("opening snapshot: %w", err) }
	opening = delegation.State{}
	for _, entity := range entities { opening[delegation.Key{Resolver: strings.ToLower(entity.Resolver), Delegator: strings.ToLower(entity.Delegator)}] = entity.Amount }
	if err = s.DB.SaveSnapshot(ctx, snapshotMonth, opening); err != nil { return nil, fmt.Errorf("save opening snapshot: %w", err) }
	return opening, nil
}

func (s Service) loadRates(ctx context.Context, month time.Time, atBlock int64) (map[string]database.RateSnapshot, error) {
	rates, err := s.DB.RateSnapshots(ctx, month)
	if err != nil || len(rates) != 0 { return rates, err }
	history, err := s.Graph.RewardRateEvents(ctx, time.Unix(0, 0).UTC(), month, atBlock)
	if err != nil { return nil, fmt.Errorf("bootstrap reward rates at block %d: %w", atBlock, err) }
	if err = s.freezeNextRates(ctx, month.AddDate(0, -1, 0), history); err != nil { return nil, err }
	return s.DB.RateSnapshots(ctx, month)
}

func (s Service) freezeNextRates(ctx context.Context, month time.Time, events []graph.RewardRateEvent) error {
	changes := make([]database.RateChange, 0, len(events))
	for _, event := range events {
		if event.Resolver == "" || event.Distributor == "" || event.RewardAddress == "" { return fmt.Errorf("invalid rate event %s", event.ID) }
		changes = append(changes, database.RateChange{Resolver: strings.ToLower(event.Resolver), Distributor: strings.ToLower(event.Distributor), RewardToken: strings.ToLower(event.RewardAddress), RewardRate: event.RewardRate, RewardsUpdater: strings.ToLower(event.RewardsUpdater)})
	}
	return s.DB.FreezeNextRates(ctx, month, changes)
}
