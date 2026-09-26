package monthly

// RootWorker processes persisted root submissions
import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"resolver-network/internal/database"
	"resolver-network/internal/metrics"
	"resolver-network/internal/rewards"
)

// RootWorker processes persisted root submissions independently of settlement.
type RootSubmissionStore interface {
	ListPendingRootSubmissions(context.Context, time.Time, int) ([]database.PendingRootSubmission, error)
	MarkRootProcessing(context.Context, time.Time, string) error
	RecordRootSubmission(context.Context, time.Time, string, string, int64, int64, string, string) error
	MarkRootConfirmed(context.Context, time.Time, string) error
	MarkRootFailed(context.Context, time.Time, string, string) error
	MarkRootPermanentFailure(context.Context, time.Time, string, string) error
}

// RootSubmitter is the narrow transaction interface required by RootWorker.
type RootSubmitter interface {
	EnsureRoot(context.Context, string, string, *big.Int, [32]byte, *big.Int) (common.Hash, bool, error)
	LastTxMetadata() rewards.TxMetadata
}

type epochStatusReader interface {
	EpochStatus(context.Context, string, *big.Int) (uint8, error)
}

const epochStatusClaimable uint8 = 2

type RootWorker struct {
	DB        RootSubmissionStore
	Submitter RootSubmitter
	Month     time.Time
	BatchSize int
}

const maxReplacementAttempts = 3

const (
	rootStatusProcessing      = "processing"
	rootStatusSubmitted       = "submitted"
	rootStatusConfirmed       = "confirmed"
	rootStatusFailed          = "failed"
	rootStatusFailedPermanent = "failed_permanent"
)

type receiptReader interface {
	Receipt(context.Context, common.Hash) (*types.Receipt, error)
}

func replacementEligible(row database.PendingRootSubmission, now time.Time) bool {
	return row.Status == rootStatusSubmitted && row.TxHash != "" && row.SubmittedAt != nil &&
		now.Sub(*row.SubmittedAt) >= 20*time.Minute && row.Nonce != nil && row.GasLimit != nil &&
		*row.GasLimit > 0 && row.GasTipCap != nil && row.GasFeeCap != nil && row.Attempts < maxReplacementAttempts
}

func queryReceipt(ctx context.Context, submitter RootSubmitter, txHash string) (*types.Receipt, error) {
	reader, ok := submitter.(receiptReader)
	if !ok {
		return nil, fmt.Errorf("root submitter does not support receipt queries")
	}
	receipt, err := reader.Receipt(ctx, common.HexToHash(txHash))
	if err != nil {
		return nil, fmt.Errorf("query receipt %s: %w", txHash, err)
	}
	return receipt, nil
}

func queryEpochStatus(ctx context.Context, submitter RootSubmitter, distributor string, epoch *big.Int) (uint8, error) {
	reader, ok := submitter.(epochStatusReader)
	if !ok {
		return 0, fmt.Errorf("root submitter does not support epoch status queries")
	}
	return reader.EpochStatus(ctx, distributor, epoch)
}

func (w RootWorker) RunOnce(ctx context.Context) error {
	if w.DB == nil || w.Submitter == nil {
		return fmt.Errorf("root worker dependencies are required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	rows, err := w.DB.ListPendingRootSubmissions(ctx, w.Month, w.BatchSize)
	if err != nil {
		return err
	}
	for _, row := range rows {
		logAttrs := []any{"month", row.Month.Format("200601"), "resolver", row.Resolver, "epoch_id", row.EpochID, "tx_hash", row.TxHash, "attempt", row.Attempts, "status", row.Status}
		if row.Status == rootStatusSubmitted && row.TxHash != "" {
			metrics.RootReceiptChecks.Add(1)
			slog.Info("root transaction receipt check", logAttrs...)
			receipt, err := queryReceipt(ctx, w.Submitter, row.TxHash)
			if err != nil {
				return err
			}
			if receipt == nil {
				slog.Info("root transaction pending", logAttrs...)
				if row.Nonce != nil && row.GasLimit != nil && row.GasTipCap != nil && row.GasFeeCap != nil && row.Attempts >= maxReplacementAttempts {
					if err = w.DB.MarkRootPermanentFailure(ctx, row.Month, row.Resolver, "maximum replacement attempts exceeded"); err != nil {
						return err
					}
					continue
				}
				if !replacementEligible(row, time.Now()) {
					continue
				}
				replacer, ok := w.Submitter.(interface {
					ReplaceRoot(context.Context, string, string, *big.Int, [32]byte, *big.Int, uint64, uint64, *big.Int, *big.Int) (common.Hash, error)
				})
				recorder, canRecord := w.DB.(interface {
					RecordRootReplacement(context.Context, time.Time, string, string, string, string) error
				})
				if !ok || !canRecord {
					continue
				}
				epoch, ok := new(big.Int).SetString(row.EpochID, 10)
				if !ok {
					continue
				}
				total, ok := new(big.Int).SetString(row.TotalReward, 10)
				if !ok {
					continue
				}
				raw, err := hex.DecodeString(strings.TrimPrefix(row.MerkleRoot, "0x"))
				if err != nil || len(raw) != 32 {
					continue
				}
				var root [32]byte
				copy(root[:], raw)
				tip, ok := new(big.Int).SetString(*row.GasTipCap, 10)
				if !ok {
					continue
				}
				fee, ok := new(big.Int).SetString(*row.GasFeeCap, 10)
				if !ok {
					continue
				}
				if row.GasLimit == nil || *row.GasLimit <= 0 {
					continue
				}
				hash, err := replacer.ReplaceRoot(ctx, row.Distributor, row.RewardsUpdater, epoch, root, total, uint64(*row.Nonce), uint64(*row.GasLimit), tip, fee)
				if err != nil {
					slog.Error("root replacement failed", append(logAttrs, "error", err)...)
					_ = w.DB.MarkRootFailed(ctx, row.Month, row.Resolver, err.Error())
					metrics.RootFailed.Add(1)
					metrics.RootRetryableFailures.Add(1)
					continue
				}
				if err = recorder.RecordRootReplacement(ctx, row.Month, row.Resolver, hash.Hex(), new(big.Int).Mul(tip, big.NewInt(2)).String(), new(big.Int).Mul(fee, big.NewInt(2)).String()); err != nil {
					return err
				}
				slog.Info("root replacement submitted", append(logAttrs, "tx_hash", hash.Hex(), "attempt", row.Attempts+1, "status", rootStatusSubmitted)...)
				metrics.RootReplacements.Add(1)
				continue
			}
			if receipt.Status == 1 {
				epoch, ok := new(big.Int).SetString(row.EpochID, 10)
				if !ok {
					return fmt.Errorf("invalid epoch id %q", row.EpochID)
				}
				status, err := queryEpochStatus(ctx, w.Submitter, row.Distributor, epoch)
				if err != nil {
					return fmt.Errorf("query epoch status: %w", err)
				}
				if status != epochStatusClaimable {
					slog.Info("root submitted but awaiting resolver confirmation", append(logAttrs, "on_chain_epoch_status", status)...)
					continue
				}
				if err = w.DB.MarkRootConfirmed(ctx, row.Month, row.Resolver); err != nil {
					return err
				}
				slog.Info("root epoch claimable", append(logAttrs, "status", rootStatusConfirmed)...)
				metrics.RootConfirmed.Add(1)
			} else {
				_ = w.DB.MarkRootPermanentFailure(ctx, row.Month, row.Resolver, "transaction reverted")
				metrics.RootFailed.Add(1)
				slog.Error("root transaction reverted", append(logAttrs, "status", rootStatusFailedPermanent)...)
			}
			continue
		}
		if err = w.DB.MarkRootProcessing(ctx, row.Month, row.Resolver); err != nil {
			return err
		}
		slog.Info("root transaction processing", append(logAttrs, "status", rootStatusProcessing)...)
		epoch, ok := new(big.Int).SetString(row.EpochID, 10)
		if !ok {
			_ = w.DB.MarkRootPermanentFailure(ctx, row.Month, row.Resolver, "invalid epoch id")
			metrics.RootFailed.Add(1)
			continue
		}
		total, ok := new(big.Int).SetString(row.TotalReward, 10)
		if !ok {
			_ = w.DB.MarkRootPermanentFailure(ctx, row.Month, row.Resolver, "invalid total reward")
			metrics.RootFailed.Add(1)
			continue
		}
		raw, err := hex.DecodeString(strings.TrimPrefix(row.MerkleRoot, "0x"))
		if err != nil || len(raw) != 32 {
			_ = w.DB.MarkRootPermanentFailure(ctx, row.Month, row.Resolver, "invalid merkle root")
			metrics.RootFailed.Add(1)
			continue
		}
		var root [32]byte
		copy(root[:], raw)
		hash, submitted, err := w.Submitter.EnsureRoot(ctx, row.Distributor, row.RewardsUpdater, epoch, root, total)
		if err != nil {
			if permanentRootError(err) {
				_ = w.DB.MarkRootPermanentFailure(ctx, row.Month, row.Resolver, err.Error())
				metrics.RootFailed.Add(1)
			} else {
				_ = w.DB.MarkRootFailed(ctx, row.Month, row.Resolver, err.Error())
			}
			continue
		}
		if submitted {
			metadata := w.Submitter.LastTxMetadata()
			if metadata.GasTipCap == nil || metadata.GasFeeCap == nil {
				return fmt.Errorf("submitted root transaction is missing gas metadata")
			}
			if metadata.Nonce > uint64(^uint64(0)>>1) || metadata.GasLimit > uint64(^uint64(0)>>1) {
				return fmt.Errorf("submitted root transaction metadata exceeds database integer range")
			}
			if err = w.DB.RecordRootSubmission(ctx, row.Month, row.Resolver, hash.Hex(), int64(metadata.Nonce), int64(metadata.GasLimit), metadata.GasTipCap.String(), metadata.GasFeeCap.String()); err != nil {
				return err
			}
			slog.Info("root transaction submitted", append(logAttrs, "tx_hash", hash.Hex(), "attempt", row.Attempts+1, "status", rootStatusSubmitted)...)
			metrics.RootSubmitted.Add(1)
		}
	}
	return nil
}

func permanentRootError(err error) bool {
	m := strings.ToLower(err.Error())
	for _, s := range []string{"rewards updater mismatch", "invalid distributor", "invalid database", "invalid epoch", "invalid total reward", "merkle root cannot be zero", "invalid merkle root", "epoch already exists with different"} {
		if strings.Contains(m, s) {
			return true
		}
	}
	return false
}

func (w RootWorker) RunUntilIdle(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = time.Minute
	}
	for {
		rows, err := w.DB.ListPendingRootSubmissions(ctx, w.Month, w.BatchSize)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		if err = w.RunOnce(ctx); err != nil {
			return err
		}
		t := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

var _ = common.Hash{}
