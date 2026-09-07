package monthly

import (
	"context"
	"errors"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"math/big"
	"strings"
	"resolver-network/internal/database"
	"testing"
	"time"
)

type workerStore struct {
	rows            []database.PendingRootSubmission
	statuses        []string
	failedPermanent int
	confirmed       int
	hash            int
	replacementHash string
}

func (s *workerStore) ListPendingRootSubmissions(context.Context, int) ([]database.PendingRootSubmission, error) {
	rows := make([]database.PendingRootSubmission, 0, len(s.rows))
	for _, row := range s.rows {
		if row.Status != rootStatusFailedPermanent { rows = append(rows, row) }
	}
	return rows, nil
}
func (s *workerStore) MarkRootProcessing(context.Context, time.Time, string) error {
	s.statuses = append(s.statuses, "processing")
	return nil
}
func (s *workerStore) SetRootSubmissionHash(context.Context, time.Time, string, string) error {
	s.hash++
	return nil
}
func (s *workerStore) MarkRootConfirmed(context.Context, time.Time, string) error {
	s.confirmed++
	return nil
}
func (s *workerStore) MarkRootFailed(context.Context, time.Time, string, string) error {
	if len(s.rows) > 0 { s.rows[0].Status = rootStatusFailed }
	return nil
}
func (s *workerStore) MarkRootPermanentFailure(context.Context, time.Time, string, string) error {
	s.failedPermanent++
	if len(s.rows) > 0 { s.rows[0].Status = rootStatusFailedPermanent }
	return nil
}
func (s *workerStore) RecordRootReplacement(_ context.Context, _ time.Time, _ string, hash, _, _ string) error {
	s.replacementHash = hash
	if len(s.rows) > 0 { s.rows[0].Attempts++ }
	return nil
}

type workerSubmitter struct {
	receipt                *types.Receipt
	err                    error
	calls                  int
	replaceNonce           uint64
	replaceTip, replaceFee *big.Int
	replacementErr         error
}

func (s *workerSubmitter) EnsureRoot(context.Context, string, string, *big.Int, [32]byte, *big.Int) (common.Hash, bool, error) {
	s.calls++
	return common.HexToHash("0x1"), true, s.err
}
func (s *workerSubmitter) Receipt(context.Context, common.Hash) (*types.Receipt, error) {
	return s.receipt, nil
}
func (s *workerSubmitter) ReplaceRoot(_ context.Context, _ string, _ string, _ *big.Int, _ [32]byte, _ *big.Int, nonce, _ uint64, tip, fee *big.Int) (common.Hash, error) {
	s.replaceNonce, s.replaceTip, s.replaceFee = nonce, new(big.Int).Set(tip), new(big.Int).Set(fee)
	if s.replacementErr != nil { return common.Hash{}, s.replacementErr }
	return common.HexToHash("0x2"), nil
}

func workerRow(status, hash string) database.PendingRootSubmission {
	return database.PendingRootSubmission{Month: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC), Resolver: "r", Distributor: "0x0000000000000000000000000000000000000001", RewardsUpdater: "0x0000000000000000000000000000000000000002", EpochID: "1", MerkleRoot: "0x" + "11" + string(make([]byte, 0)), TotalReward: "1", Status: status, TxHash: hash}
}

func TestRootWorkerReceiptConfirmed(t *testing.T) {
	s := &workerStore{rows: []database.PendingRootSubmission{workerRow("submitted", "0x1")}}
	w := RootWorker{DB: s, Submitter: &workerSubmitter{receipt: &types.Receipt{Status: 1}}, BatchSize: 10}
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.confirmed != 1 {
		t.Fatalf("confirmed=%d", s.confirmed)
	}
}

func TestRootWorkerHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background()); cancel()
	if err := (RootWorker{DB: &workerStore{}, Submitter: &workerSubmitter{}}).RunOnce(ctx); !errors.Is(err, context.Canceled) { t.Fatalf("err=%v", err) }
}
func TestRootWorkerReceiptReverted(t *testing.T) {
	s := &workerStore{rows: []database.PendingRootSubmission{workerRow("submitted", "0x1")}}
	w := RootWorker{DB: s, Submitter: &workerSubmitter{receipt: &types.Receipt{Status: 0}}, BatchSize: 10}
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.failedPermanent != 1 {
		t.Fatalf("permanent=%d", s.failedPermanent)
	}
}
func TestRootWorkerPendingDoesNotExitEarly(t *testing.T) {
	s := &workerStore{rows: []database.PendingRootSubmission{workerRow("submitted", "0x1")}}
	w := RootWorker{DB: s, Submitter: &workerSubmitter{}, BatchSize: 10}
	ctx, c := context.WithCancel(context.Background())
	c()
	if err := w.RunUntilIdle(ctx, time.Millisecond); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestPermanentRootErrorClassification(t *testing.T) {
	for _, message := range []string{"rewards updater mismatch", "invalid merkle root"} {
		if !permanentRootError(errors.New(message)) {
			t.Fatalf("%q should be permanent", message)
		}
	}
	if permanentRootError(errors.New("rpc timeout")) {
		t.Fatal("rpc timeout should be retryable")
	}
}

func TestRootWorkerReplacementPreservesNonceAndRaisesFees(t *testing.T) {
	old := time.Now().Add(-21 * time.Minute)
	nonce, gas := int64(7), int64(21000)
	tip, fee := "10", "100"
	s := &workerStore{rows: []database.PendingRootSubmission{{Month: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC), Resolver: "r", Distributor: "0x0000000000000000000000000000000000000001", RewardsUpdater: "0x0000000000000000000000000000000000000002", EpochID: "1", MerkleRoot: "0x1111111111111111111111111111111111111111111111111111111111111111", TotalReward: "1", Status: "submitted", TxHash: "0x1", Nonce: &nonce, GasLimit: &gas, GasTipCap: &tip, GasFeeCap: &fee, SubmittedAt: &old, Attempts: 1}}}
	sub := &workerSubmitter{}
	if err := (RootWorker{DB: s, Submitter: sub, BatchSize: 1}).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sub.replaceNonce != 7 || sub.replaceTip.Cmp(big.NewInt(10)) != 0 || sub.replaceFee.Cmp(big.NewInt(100)) != 0 {
		t.Fatalf("replacement metadata nonce=%d tip=%v fee=%v", sub.replaceNonce, sub.replaceTip, sub.replaceFee)
	}
	if s.replacementHash == "" {
		t.Fatal("replacement hash was not recorded")
	}
}

func TestRootWorkerStopsAfterReplacementLimit(t *testing.T) {
	old := time.Now().Add(-21 * time.Minute)
	nonce, gas := int64(7), int64(21000); tip, fee := "10", "100"
	s := &workerStore{rows: []database.PendingRootSubmission{{Month: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC), Resolver: "r", Distributor: "0x0000000000000000000000000000000000000001", RewardsUpdater: "0x0000000000000000000000000000000000000002", EpochID: "1", MerkleRoot: "0x1111111111111111111111111111111111111111111111111111111111111111", TotalReward: "1", Status: "submitted", TxHash: "0x1", Nonce: &nonce, GasLimit: &gas, GasTipCap: &tip, GasFeeCap: &fee, SubmittedAt: &old, Attempts: 3}}}
	if err := (RootWorker{DB: s, Submitter: &workerSubmitter{}, BatchSize: 1}).RunOnce(context.Background()); err != nil { t.Fatal(err) }
	if s.failedPermanent != 1 { t.Fatalf("failed permanent=%d", s.failedPermanent) }
}

func TestRootWorkerReplacementFailureIsRetryable(t *testing.T) {
	old := time.Now().Add(-21 * time.Minute)
	nonce, gas := int64(7), int64(21000); tip, fee := "10", "100"
	s := &workerStore{rows: []database.PendingRootSubmission{{Month: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC), Resolver: "r", Distributor: "0x0000000000000000000000000000000000000001", RewardsUpdater: "0x0000000000000000000000000000000000000002", EpochID: "1", MerkleRoot: "0x1111111111111111111111111111111111111111111111111111111111111111", TotalReward: "1", Status: "submitted", TxHash: "0x1", Nonce: &nonce, GasLimit: &gas, GasTipCap: &tip, GasFeeCap: &fee, SubmittedAt: &old, Attempts: 1}}}
	if err := (RootWorker{DB: s, Submitter: &workerSubmitter{replacementErr: errors.New("rpc timeout")}, BatchSize: 1}).RunOnce(context.Background()); err != nil { t.Fatal(err) }
	if s.failedPermanent != 0 { t.Fatal("retryable replacement failure marked permanent") }
	if len(s.rows) == 0 || s.rows[0].Status != rootStatusFailed { t.Fatalf("status=%v, want failed", s.rows[0].Status) }
}

func TestQueryReceiptRequiresReceiptReader(t *testing.T) {
	_, err := queryReceipt(context.Background(), &rootOnlySubmitter{}, "0x1")
	if err == nil { t.Fatal("expected receipt capability error") }
}

type receiptErrorSubmitter struct{ rootOnlySubmitter }
func (*receiptErrorSubmitter) Receipt(context.Context, common.Hash) (*types.Receipt, error) { return nil, errors.New("rpc unavailable") }

func TestQueryReceiptWrapsRPCError(t *testing.T) {
	_, err := queryReceipt(context.Background(), &receiptErrorSubmitter{}, "0x1")
	if err == nil || !strings.Contains(err.Error(), "query receipt") { t.Fatalf("err=%v", err) }
}

func TestReplacementEligibleHonorsTimeoutAndLimit(t *testing.T) {
	now := time.Now()
	old := now.Add(-21 * time.Minute)
	nonce, gas := int64(1), int64(21000); tip, fee := "1", "2"
	row := database.PendingRootSubmission{Status: rootStatusSubmitted, TxHash: "0x1", SubmittedAt: &old, Nonce: &nonce, GasLimit: &gas, GasTipCap: &tip, GasFeeCap: &fee, Attempts: 1}
	if !replacementEligible(row, now) { t.Fatal("expected eligible replacement") }
	row.Attempts = maxReplacementAttempts
	if replacementEligible(row, now) { t.Fatal("replacement limit must disable replacement") }
}

func TestRootWorkerRetriesUntilTerminal(t *testing.T) {
	old := time.Now().Add(-21 * time.Minute)
	nonce, gas := int64(7), int64(21000); tip, fee := "10", "100"
	s := &workerStore{rows: []database.PendingRootSubmission{{Month: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC), Resolver: "r", Distributor: "0x0000000000000000000000000000000000000001", RewardsUpdater: "0x0000000000000000000000000000000000000002", EpochID: "1", MerkleRoot: "0x1111111111111111111111111111111111111111111111111111111111111111", TotalReward: "1", Status: rootStatusSubmitted, TxHash: "0x1", Nonce: &nonce, GasLimit: &gas, GasTipCap: &tip, GasFeeCap: &fee, SubmittedAt: &old, Attempts: 1}}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second); defer cancel()
	if err := (RootWorker{DB: s, Submitter: &workerSubmitter{}, BatchSize: 1}).RunUntilIdle(ctx, time.Millisecond); err != nil { t.Fatal(err) }
	if s.failedPermanent != 1 { t.Fatalf("failed permanent=%d", s.failedPermanent) }
}

type rootOnlySubmitter struct{}
func (*rootOnlySubmitter) EnsureRoot(context.Context, string, string, *big.Int, [32]byte, *big.Int) (common.Hash, bool, error) {
	return common.Hash{}, false, nil
}
