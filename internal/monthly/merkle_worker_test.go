package monthly

import (
	"context"
	"errors"
	"resolver-network/internal/database"
	"resolver-network/internal/metrics"
	"resolver-network/internal/workflow"
	"testing"
	"time"
)

func TestMerkleWorkerHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background()); cancel()
	f := &merkleStoreFake{status: string(workflow.SettlementDone)}
	w := MerkleWorker{Store: f, Build: func(context.Context, time.Time) ([]database.MonthlyAllocation, error) { t.Fatal("build must not run"); return nil, nil }}
	if err := w.RunOnce(ctx, time.Now()); !errors.Is(err, context.Canceled) { t.Fatalf("err=%v", err) }
}

type merkleStoreFake struct {
	status string
	saved  int
}

type merkleFailStore struct{ merkleStoreFake }
func (f *merkleFailStore) SaveMerkleAllocations(context.Context, time.Time, []database.MonthlyAllocation) error { return context.DeadlineExceeded }

func TestMerkleWorkerFailureMetric(t *testing.T) {
	before := metrics.MerkleFailed.Value()
	w := MerkleWorker{Store: &merkleFailStore{merkleStoreFake{status: string(workflow.MerklePending)}}, Build: func(context.Context, time.Time) ([]database.MonthlyAllocation, error) { return []database.MonthlyAllocation{{Resolver: "r"}}, nil }}
	if err := w.RunOnce(context.Background(), time.Now()); err == nil { t.Fatal("expected save failure") }
	if metrics.MerkleFailed.Value() != before+1 { t.Fatal("failure metric not incremented") }
}

func (f *merkleStoreFake) WorkflowStatus(context.Context, time.Time) (string, error) {
	return f.status, nil
}
func (f *merkleStoreFake) TransitionWorkflow(_ context.Context, _ time.Time, stage workflow.Stage) error {
	f.status = string(stage)
	return nil
}
func (f *merkleStoreFake) SaveMerkleAllocations(context.Context, time.Time, []database.MonthlyAllocation) error {
	f.saved++
	return nil
}

func TestMerkleWorkerRequiresSettledStage(t *testing.T) {
	f := &merkleStoreFake{status: "settlement_processing"}
	w := MerkleWorker{Store: f, Build: func(context.Context, time.Time) ([]database.MonthlyAllocation, error) {
		t.Fatal("build must not run")
		return nil, nil
	}}
	if err := w.RunOnce(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if f.saved != 0 {
		t.Fatal("unexpected save")
	}
}

func TestMerkleWorkerAdvancesSettledStage(t *testing.T) {
	started, succeeded := metrics.MerkleStarted.Value(), metrics.MerkleSucceeded.Value()
	f := &merkleStoreFake{status: "settled"}
	w := MerkleWorker{Store: f, Build: func(context.Context, time.Time) ([]database.MonthlyAllocation, error) {
		return []database.MonthlyAllocation{{Resolver: "r"}}, nil
	}}
	if err := w.RunOnce(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if f.saved != 1 {
		t.Fatalf("saved=%d", f.saved)
	}
	if metrics.MerkleStarted.Value() != started+1 || metrics.MerkleSucceeded.Value() != succeeded+1 { t.Fatal("merkle success metrics not incremented") }
}
