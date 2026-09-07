package monthly
// MerkleWorker owns the transition from a successfully settled month to a persisted Merkle tree for that month
import (
	"context"
	"fmt"
	"resolver-network/internal/database"
	"resolver-network/internal/workflow"
	"resolver-network/internal/metrics"
	"time"
)


type MerkleWorker struct {
	Store MerkleStore
	Build func(context.Context, time.Time) ([]database.MonthlyAllocation, error)
}

type MerkleStore interface {
	WorkflowStatus(context.Context, time.Time) (string, error)
	TransitionWorkflow(context.Context, time.Time, workflow.Stage) error
	SaveMerkleAllocations(context.Context, time.Time, []database.MonthlyAllocation) error
}

func (w MerkleWorker) RunOnce(ctx context.Context, month time.Time) error {
	if w.Store == nil || w.Build == nil {
		return fmt.Errorf("merkle worker dependencies are required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	status, err := w.Store.WorkflowStatus(ctx, month)
	if err != nil {
		return err
	}
	if status != string(workflow.SettlementDone) && status != string(workflow.MerklePending) {
		return nil
	}
	metrics.MerkleStarted.Add(1)
	if status == string(workflow.SettlementDone) {
		if err = w.Store.TransitionWorkflow(ctx, month, workflow.MerklePending); err != nil {
			metrics.MerkleFailed.Add(1)
			return err
		}
	}
	if err = w.Store.TransitionWorkflow(ctx, month, workflow.MerkleProcessing); err != nil {
		metrics.MerkleFailed.Add(1)
		return err
	}
	allocations, err := w.Build(ctx, month)
	if err != nil {
		metrics.MerkleFailed.Add(1)
		return err
	}
	if err = w.Store.SaveMerkleAllocations(ctx, month, allocations); err != nil {
		metrics.MerkleFailed.Add(1)
		return err
	}
	metrics.MerkleSucceeded.Add(1)
	return nil
}
