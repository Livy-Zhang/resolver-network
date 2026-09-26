package database

// PostgreSQL database access layer
import (
	"context"
	"embed"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"math/big"
	"resolver-network/internal/delegation"
	"resolver-network/internal/retry"
	"resolver-network/internal/workflow"
	"sort"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var files embed.FS

type DB struct{ Pool *pgxpool.Pool }

// WorkflowStatus returns the persisted stage for a month.
func (d *DB) WorkflowStatus(ctx context.Context, month time.Time) (string, error) {
	var status string
	err := d.Pool.QueryRow(ctx, "SELECT status FROM monthly_workflows WHERE month_start=$1", month).Scan(&status)
	return status, err
}

func (d *DB) TransitionWorkflow(ctx context.Context, month time.Time, next workflow.Stage) error {
	var current string
	if err := d.Pool.QueryRow(ctx, "SELECT status FROM monthly_workflows WHERE month_start=$1 FOR UPDATE", month).Scan(&current); err != nil {
		return err
	}
	if err := workflow.Transition(workflow.Stage(current), next); err != nil {
		return err
	}
	_, err := d.Pool.Exec(ctx, "UPDATE monthly_workflows SET status=$1,updated_at=now(),started_at=COALESCE(started_at,now()) WHERE month_start=$2", string(next), month)
	return err
}

func (d *DB) LoadWeights(ctx context.Context, month time.Time) ([]delegation.Weight, error) {
	rows, err := d.Pool.Query(ctx, "SELECT resolver_address,delegator_address,delegation_weight::text FROM delegation_monthly WHERE month_start=$1 ORDER BY resolver_address,delegator_address", month)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []delegation.Weight{}
	for rows.Next() {
		var resolver, delegator, value string
		if err = rows.Scan(&resolver, &delegator, &value); err != nil {
			return nil, err
		}
		v, ok := new(big.Int).SetString(value, 10)
		if !ok {
			return nil, fmt.Errorf("invalid delegation weight")
		}
		out = append(out, delegation.Weight{Resolver: resolver, Delegator: delegator, Value: v})
	}
	return out, rows.Err()
}

// SaveMerkleAllocations persists Merkle output and advances the workflow atomically.
func (d *DB) SaveMerkleAllocations(ctx context.Context, month time.Time, allocations []MonthlyAllocation) error {
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	resolvers := map[string]MonthlyAllocation{}
	for _, a := range allocations {
		resolvers[strings.ToLower(a.Resolver)] = a
		if _, err = tx.Exec(ctx, `UPDATE delegation_monthly SET reward_amount=$1,resolver_total_reward=$2,epoch_id=$3,merkle_root=$4,merkle_proof=$5 WHERE month_start=$6 AND resolver_address=$7 AND delegator_address=$8`, a.Amount, a.TotalReward, a.EpochID, a.MerkleRoot, a.Proof, month, strings.ToLower(a.Resolver), strings.ToLower(a.Delegator)); err != nil {
			return err
		}
	}
	for resolver, a := range resolvers {
		if _, err = tx.Exec(ctx, `INSERT INTO monthly_root_submissions(month_start,resolver_address,distributor_address,reward_token_address,rewards_updater,reward_rate,total_reward,epoch_id,merkle_root,root_submission_status,indexed_block_number)
SELECT $1,$2,$3,reward_token_address,rewards_updater,$4,$5,$6,$7,'not_submitted',0
FROM resolver_monthly_reward_rates WHERE month_start=$1 AND resolver_address=$2
ON CONFLICT (month_start,resolver_address) DO UPDATE SET distributor_address=EXCLUDED.distributor_address,reward_rate=EXCLUDED.reward_rate,total_reward=EXCLUDED.total_reward,epoch_id=EXCLUDED.epoch_id,merkle_root=EXCLUDED.merkle_root`, month, resolver, a.Distributor, a.RewardRate, a.TotalReward, a.EpochID, a.MerkleRoot); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE monthly_workflows SET status='root_pending',completed_at=now(),updated_at=now() WHERE month_start=$1 AND status IN ('settled','merkle_pending','merkle_processing')`, month); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (d *DB) Ready(ctx context.Context) error {
	if err := d.Pool.Ping(ctx); err != nil {
		return fmt.Errorf("database ping: %w", err)
	}
	statuses, err := d.MigrationStatus(ctx)
	if err != nil {
		return fmt.Errorf("migration status: %w", err)
	}
	for _, status := range statuses {
		if !status.Applied {
			return fmt.Errorf("migration %s is pending", status.Version)
		}
	}
	return nil
}

type RateSnapshot struct {
	Resolver, Distributor, RewardToken, RewardRate, RewardsUpdater string
}

type RateChange struct {
	Resolver, Distributor, RewardToken, RewardRate, RewardsUpdater string
}

type MonthlyAllocation struct {
	Resolver, Distributor, Delegator, RewardRate, RewardsUpdater, Weight, Amount, TotalReward, EpochID, MerkleRoot string
	Proof                                                                                                          []string
}

type MonthlyRewardDetail struct {
	Resolver         string   `json:"resolver"`
	Distributor      string   `json:"distributor"`
	RewardToken      string   `json:"rewardToken"`
	RewardRate       string   `json:"rewardRate"`
	Delegator        string   `json:"delegator"`
	DelegationWeight string   `json:"delegationWeight"`
	RewardAmount     string   `json:"rewardAmount"`
	TotalReward      string   `json:"totalReward"`
	EpochID          string   `json:"epochId"`
	MerkleRoot       string   `json:"merkleRoot"`
	Proof            []string `json:"merkleProof"`
}

type MerkleProofSubmission struct {
	Resolver    string `json:"resolver"`
	Distributor string `json:"distributor"`
	RewardToken string `json:"rewardToken"`
	RewardRate  string `json:"rewardRate"`
	TotalReward string `json:"totalReward"`
	EpochID     string `json:"epochId"`
	MerkleRoot  string `json:"merkleRoot"`
	Status      string `json:"status"`
	TxHash      string `json:"txHash"`
}

type RootSubmission struct {
	Hash        *string
	Status      string
	Nonce       *int64
	GasLimit    *int64
	GasTipCap   *string
	GasFeeCap   *string
	Attempts    int
	SubmittedAt *time.Time
	ConfirmedAt *time.Time
	Error       *string
}

type PendingRootSubmission struct {
	Month                                                                   time.Time
	Resolver, Distributor, RewardsUpdater, EpochID, MerkleRoot, TotalReward string
	TxHash, Status                                                          string
	Nonce                                                                   *int64
	GasLimit                                                                *int64
	GasTipCap, GasFeeCap                                                    *string
	SubmittedAt                                                             *time.Time
	Attempts                                                                int
}

func (d *DB) ListPendingRootSubmissions(ctx context.Context, month time.Time, limit int) ([]PendingRootSubmission, error) {
	if limit < 1 || limit > 1000 {
		limit = 100
	}
	rows, err := d.Pool.Query(ctx, `SELECT r.month_start,r.resolver_address,r.distributor_address,r.rewards_updater,r.epoch_id::text,r.merkle_root,r.total_reward::text,COALESCE(r.root_submission_tx_hash,''),r.root_submission_status,r.root_submission_nonce,r.root_submission_gas_limit,r.root_submission_gas_tip_cap::text,r.root_submission_gas_fee_cap::text,r.root_submission_submitted_at,r.root_submission_attempts FROM monthly_root_submissions r JOIN monthly_workflows w ON w.month_start=r.month_start AND w.status IN ('root_pending','root_processing','confirmed') WHERE r.month_start=$1 AND (r.root_submission_status IN ('not_submitted','submitted') OR (r.root_submission_status='processing' AND r.updated_at < now() - interval '20 minutes') OR (r.root_submission_status='failed' AND (r.next_retry_at IS NULL OR r.next_retry_at <= now()))) ORDER BY r.month_start,r.resolver_address LIMIT $2`, month, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PendingRootSubmission{}
	for rows.Next() {
		var x PendingRootSubmission
		if err = rows.Scan(&x.Month, &x.Resolver, &x.Distributor, &x.RewardsUpdater, &x.EpochID, &x.MerkleRoot, &x.TotalReward, &x.TxHash, &x.Status, &x.Nonce, &x.GasLimit, &x.GasTipCap, &x.GasFeeCap, &x.SubmittedAt, &x.Attempts); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (d *DB) MarkRootProcessing(ctx context.Context, month time.Time, resolver string) error {
	result, err := d.Pool.Exec(ctx, `UPDATE monthly_root_submissions SET root_submission_status='processing',updated_at=now() WHERE month_start=$1 AND resolver_address=$2 AND (root_submission_status IN ('not_submitted','failed') OR (root_submission_status='processing' AND updated_at < now() - interval '20 minutes'))`, month, strings.ToLower(resolver))
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("root submission cannot be marked processing for %s/%s", month.Format("2006-01"), resolver)
	}
	return nil
}

func (d *DB) MonthlyRewardDetails(ctx context.Context, month time.Time) ([]MonthlyRewardDetail, error) {
	rows, err := d.Pool.Query(ctx, `SELECT dm.resolver_address,dm.distributor_address,rm.reward_token_address,dm.reward_rate,dm.delegator_address,dm.delegation_weight,dm.reward_amount,dm.resolver_total_reward,dm.epoch_id,dm.merkle_root,dm.merkle_proof FROM delegation_monthly dm JOIN resolver_monthly_reward_rates rm ON rm.month_start=dm.month_start AND rm.resolver_address=dm.resolver_address WHERE dm.month_start=$1 ORDER BY dm.resolver_address,dm.delegator_address`, month)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MonthlyRewardDetail{}
	for rows.Next() {
		var x MonthlyRewardDetail
		if err = rows.Scan(&x.Resolver, &x.Distributor, &x.RewardToken, &x.RewardRate, &x.Delegator, &x.DelegationWeight, &x.RewardAmount, &x.TotalReward, &x.EpochID, &x.MerkleRoot, &x.Proof); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (d *DB) MerkleProofSubmissions(ctx context.Context, month time.Time) ([]MerkleProofSubmission, error) {
	rows, err := d.Pool.Query(ctx, `SELECT resolver_address,distributor_address,reward_token_address,reward_rate::text,total_reward::text,epoch_id::text,merkle_root,root_submission_status,COALESCE(root_submission_tx_hash,'') FROM monthly_root_submissions WHERE month_start=$1 ORDER BY resolver_address`, month)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MerkleProofSubmission{}
	for rows.Next() {
		var x MerkleProofSubmission
		if err = rows.Scan(&x.Resolver, &x.Distributor, &x.RewardToken, &x.RewardRate, &x.TotalReward, &x.EpochID, &x.MerkleRoot, &x.Status, &x.TxHash); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func Open(ctx context.Context, url string) (*DB, error) {
	p, e := pgxpool.New(ctx, url)
	if e != nil {
		return nil, e
	}
	if e = p.Ping(ctx); e != nil {
		p.Close()
		return nil, e
	}
	return &DB{p}, nil
}
func (d *DB) Close() { d.Pool.Close() }

func (d *DB) Migrate(ctx context.Context) error {
	conn, e := d.Pool.Acquire(ctx)
	if e != nil {
		return e
	}
	defer conn.Release()
	if _, e = conn.Exec(ctx, "SELECT pg_advisory_lock(hashtext('resolver-network:migrations'))"); e != nil {
		return e
	}
	defer conn.Exec(context.Background(), "SELECT pg_advisory_unlock(hashtext('resolver-network:migrations'))")
	if _, e = conn.Exec(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())"); e != nil {
		return e
	}
	es, e := files.ReadDir("migrations")
	if e != nil {
		return e
	}
	sort.Slice(es, func(i, j int) bool { return es[i].Name() < es[j].Name() })
	for _, x := range es {
		var applied bool
		e = conn.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)", x.Name()).Scan(&applied)
		if e != nil {
			return e
		}
		if applied {
			continue
		}
		sql, e := files.ReadFile("migrations/" + x.Name())
		if e != nil {
			return e
		}
		tx, e := conn.Begin(ctx)
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, string(sql))
		if e == nil {
			_, e = tx.Exec(ctx, "INSERT INTO schema_migrations(version) VALUES($1)", x.Name())
		}
		if e != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("migration %s: %w", x.Name(), e)
		}
		if e = tx.Commit(ctx); e != nil {
			return e
		}
	}
	return nil
}

type MigrationStatus struct {
	Version string
	Applied bool
}

func (d *DB) MigrationStatus(ctx context.Context) ([]MigrationStatus, error) {
	entries, err := files.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	rows, err := d.Pool.Query(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	applied := map[string]bool{}
	for rows.Next() {
		var version string
		if err = rows.Scan(&version); err != nil {
			return nil, err
		}
		applied[version] = true
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	result := make([]MigrationStatus, 0, len(entries))
	for _, entry := range entries {
		result = append(result, MigrationStatus{Version: entry.Name(), Applied: applied[entry.Name()]})
	}
	return result, nil
}

func (d *DB) EnsureMonthlyPartition(ctx context.Context, month time.Time) error {
	start := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	name := fmt.Sprintf("delegation_monthly_%04d%02d", start.Year(), start.Month())
	query := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s PARTITION OF delegation_monthly FOR VALUES FROM ('%s') TO ('%s')", name, start.Format("2006-01-02"), end.Format("2006-01-02"))
	_, err := d.Pool.Exec(ctx, query)
	return err
}

func (d *DB) RateSnapshots(ctx context.Context, month time.Time) (map[string]RateSnapshot, error) {
	rows, err := d.Pool.Query(ctx, "SELECT resolver_address,distributor_address,reward_token_address,reward_rate::text,rewards_updater FROM resolver_monthly_reward_rates WHERE month_start=$1", month)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]RateSnapshot{}
	for rows.Next() {
		var r RateSnapshot
		if err = rows.Scan(&r.Resolver, &r.Distributor, &r.RewardToken, &r.RewardRate, &r.RewardsUpdater); err != nil {
			return nil, err
		}
		out[strings.ToLower(r.Resolver)] = r
	}
	return out, rows.Err()
}

// FreezeNextRates carries current-month rates forward, then applies all
// finalized configuration changes from the current month.
func (d *DB) FreezeNextRates(ctx context.Context, currentMonth time.Time, changes []RateChange) error {
	next := currentMonth.AddDate(0, 1, 0)
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO resolver_monthly_reward_rates(month_start,resolver_address,distributor_address,reward_token_address,reward_rate,rewards_updater)
SELECT $1,resolver_address,distributor_address,reward_token_address,reward_rate,rewards_updater FROM resolver_monthly_reward_rates WHERE month_start=$2
ON CONFLICT (month_start,resolver_address) DO NOTHING`, next, currentMonth)
	if err != nil {
		return err
	}
	for _, c := range changes {
		_, err = tx.Exec(ctx, `INSERT INTO resolver_monthly_reward_rates(month_start,resolver_address,distributor_address,reward_token_address,reward_rate,rewards_updater)
VALUES($1,$2,$3,$4,$5,$6)
ON CONFLICT (month_start,resolver_address) DO UPDATE SET distributor_address=EXCLUDED.distributor_address,reward_token_address=EXCLUDED.reward_token_address,reward_rate=EXCLUDED.reward_rate,rewards_updater=CASE WHEN EXCLUDED.rewards_updater='' THEN resolver_monthly_reward_rates.rewards_updater ELSE EXCLUDED.rewards_updater END`, next, strings.ToLower(c.Resolver), strings.ToLower(c.Distributor), strings.ToLower(c.RewardToken), c.RewardRate, strings.ToLower(c.RewardsUpdater))
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (d *DB) SaveSettlement(ctx context.Context, month time.Time, indexedBlockNumber int64, weights []delegation.Weight, ending delegation.State, allocations []MonthlyAllocation) error {
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO monthly_workflows(month_start,status,completed_at,updated_at) VALUES($1,'settled',now(),now()) ON CONFLICT (month_start) DO UPDATE SET status='settled',last_error=NULL,updated_at=now()`, month); err != nil {
		return err
	}
	existingSubmissions := map[string]RootSubmission{}
	rows, err := tx.Query(ctx, "SELECT resolver_address,root_submission_tx_hash,root_submission_status,root_submission_submitted_at,root_submission_confirmed_at,root_submission_error FROM monthly_root_submissions WHERE month_start=$1", month)
	if err != nil {
		return err
	}
	for rows.Next() {
		var resolver string
		var submission RootSubmission
		if err = rows.Scan(&resolver, &submission.Hash, &submission.Status, &submission.SubmittedAt, &submission.ConfirmedAt, &submission.Error); err != nil {
			rows.Close()
			return err
		}
		existingSubmissions[strings.ToLower(resolver)] = submission
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, table := range []string{"delegation_month_end_states", "delegation_monthly"} {
		if _, err = tx.Exec(ctx, "DELETE FROM "+table+" WHERE month_start=$1", month); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO delegation_snapshot_checkpoints(month_start,completed_at)
VALUES($1,now())
ON CONFLICT (month_start) DO UPDATE SET completed_at=EXCLUDED.completed_at`, month); err != nil {
		return err
	}
	for k, a := range ending {
		if a.Sign() > 0 {
			if _, err = tx.Exec(ctx, "INSERT INTO delegation_month_end_states(month_start,resolver,delegator,delegated_amount) VALUES($1,$2,$3,$4)", month, k.Resolver, k.Delegator, a.String()); err != nil {
				return err
			}
		}
	}
	if len(allocations) == 0 {
		for _, w := range weights {
			if w.Value.Sign() <= 0 {
				continue
			}
			if _, err = tx.Exec(ctx, `INSERT INTO delegation_monthly(month_start,month_end,resolver_address,delegator_address,delegation_weight,indexed_block_number)
VALUES($1,$2,$3,$4,$5,$6)`, month, month.AddDate(0, 1, 0), strings.ToLower(w.Resolver), strings.ToLower(w.Delegator), w.Value.String(), indexedBlockNumber); err != nil {
				return err
			}
		}
	}
	end := month.AddDate(0, 1, 0)
	rootRows := map[string]MonthlyAllocation{}
	for _, a := range allocations {
		rootRows[strings.ToLower(a.Resolver)] = a
		if _, err = tx.Exec(ctx, `INSERT INTO delegation_monthly(month_start,month_end,resolver_address,distributor_address,delegator_address,reward_rate,delegation_weight,reward_amount,resolver_total_reward,epoch_id,merkle_root,merkle_proof,indexed_block_number) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, month, end, a.Resolver, a.Distributor, a.Delegator, a.RewardRate, a.Weight, a.Amount, a.TotalReward, a.EpochID, a.MerkleRoot, a.Proof, indexedBlockNumber); err != nil {
			return err
		}
	}
	for resolver, a := range rootRows {
		submission := existingSubmissions[resolver]
		status := submission.Status
		if status == "" {
			status = "not_submitted"
		}
		_, err = tx.Exec(ctx, `INSERT INTO monthly_root_submissions(month_start,resolver_address,distributor_address,reward_token_address,rewards_updater,reward_rate,total_reward,epoch_id,merkle_root,root_submission_tx_hash,root_submission_status,root_submission_submitted_at,root_submission_confirmed_at,root_submission_error,indexed_block_number)
SELECT $1,$2,$3,reward_token_address,rewards_updater,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13
FROM resolver_monthly_reward_rates WHERE month_start=$1 AND resolver_address=$2
ON CONFLICT (month_start,resolver_address) DO UPDATE SET distributor_address=EXCLUDED.distributor_address,reward_token_address=EXCLUDED.reward_token_address,rewards_updater=EXCLUDED.rewards_updater,reward_rate=EXCLUDED.reward_rate,total_reward=EXCLUDED.total_reward,epoch_id=EXCLUDED.epoch_id,merkle_root=EXCLUDED.merkle_root,indexed_block_number=EXCLUDED.indexed_block_number`, month, resolver, a.Distributor, a.RewardRate, a.TotalReward, a.EpochID, a.MerkleRoot, submission.Hash, status, submission.SubmittedAt, submission.ConfirmedAt, submission.Error, indexedBlockNumber)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (d *DB) SaveSnapshot(ctx context.Context, month time.Time, state delegation.State) error {
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "DELETE FROM delegation_month_end_states WHERE month_start=$1", month); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO delegation_snapshot_checkpoints(month_start,completed_at)
VALUES($1,now())
ON CONFLICT (month_start) DO UPDATE SET completed_at=EXCLUDED.completed_at`, month); err != nil {
		return err
	}
	for key, amount := range state {
		if amount.Sign() <= 0 {
			continue
		}
		if _, err = tx.Exec(ctx, "INSERT INTO delegation_month_end_states(month_start,resolver,delegator,delegated_amount) VALUES($1,$2,$3,$4)", month, strings.ToLower(key.Resolver), strings.ToLower(key.Delegator), amount.String()); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (d *DB) SetRootSubmissionHash(ctx context.Context, month time.Time, resolver, txHash string) error {
	_, err := d.Pool.Exec(ctx, "UPDATE monthly_root_submissions SET root_submission_tx_hash=$1,root_submission_status='submitted',root_submission_submitted_at=now(),root_submission_attempts=root_submission_attempts+1,root_submission_error=NULL,updated_at=now() WHERE month_start=$2 AND resolver_address=$3", txHash, month, strings.ToLower(resolver))
	return err
}

func (d *DB) RecordRootSubmission(ctx context.Context, month time.Time, resolver, txHash string, nonce, gasLimit int64, gasTipCap, gasFeeCap string) error {
	result, err := d.Pool.Exec(ctx, `UPDATE monthly_root_submissions
SET root_submission_tx_hash=$1, root_submission_status='submitted',
    root_submission_nonce=$2, root_submission_gas_limit=$3,
    root_submission_gas_tip_cap=$4, root_submission_gas_fee_cap=$5,
    root_submission_submitted_at=now(), root_submission_attempts=root_submission_attempts+1,
    root_submission_error=NULL, updated_at=now()
WHERE month_start=$6 AND resolver_address=$7`, txHash, nonce, gasLimit, gasTipCap, gasFeeCap, month, strings.ToLower(resolver))
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("root submission not found for %s/%s", month.Format("2006-01"), resolver)
	}
	return nil
}

func (d *DB) RecordRootReplacement(ctx context.Context, month time.Time, resolver, txHash, gasTipCap, gasFeeCap string) error {
	_, err := d.Pool.Exec(ctx, `UPDATE monthly_root_submissions SET root_submission_tx_hash=$1,root_submission_status='submitted',root_submission_gas_tip_cap=$2,root_submission_gas_fee_cap=$3,root_submission_attempts=root_submission_attempts+1,root_submission_submitted_at=now(),updated_at=now() WHERE month_start=$4 AND resolver_address=$5`, txHash, gasTipCap, gasFeeCap, month, strings.ToLower(resolver))
	return err
}

func (d *DB) RootSubmission(ctx context.Context, month time.Time, resolver string) (RootSubmission, error) {
	var s RootSubmission
	err := d.Pool.QueryRow(ctx, "SELECT root_submission_tx_hash,root_submission_status,root_submission_nonce,root_submission_gas_limit,root_submission_gas_tip_cap::text,root_submission_gas_fee_cap::text,root_submission_attempts,root_submission_submitted_at,root_submission_confirmed_at,root_submission_error FROM monthly_root_submissions WHERE month_start=$1 AND resolver_address=$2", month, strings.ToLower(resolver)).Scan(&s.Hash, &s.Status, &s.Nonce, &s.GasLimit, &s.GasTipCap, &s.GasFeeCap, &s.Attempts, &s.SubmittedAt, &s.ConfirmedAt, &s.Error)
	return s, err
}
func (d *DB) MarkRootConfirmed(ctx context.Context, month time.Time, resolver string) error {
	_, err := d.Pool.Exec(ctx, "UPDATE monthly_root_submissions SET root_submission_status='confirmed',root_submission_confirmed_at=now(),root_submission_error=NULL,updated_at=now() WHERE month_start=$1 AND resolver_address=$2", month, strings.ToLower(resolver))
	return err
}
func (d *DB) MarkRootFailed(ctx context.Context, month time.Time, resolver, message string) error {
	// The retry schedule is deliberately computed in Go so it is unit-testable and
	// independent of PostgreSQL-specific interval expressions.
	var attempts, maxAttempts int
	if err := d.Pool.QueryRow(ctx, "SELECT root_submission_attempts,max_attempts FROM monthly_root_submissions WHERE month_start=$1 AND resolver_address=$2", month, strings.ToLower(resolver)).Scan(&attempts, &maxAttempts); err != nil {
		return err
	}
	if attempts >= maxAttempts {
		_, err := d.Pool.Exec(ctx, "UPDATE monthly_root_submissions SET root_submission_status='failed_permanent',root_submission_error=$1,next_retry_at=NULL,updated_at=now() WHERE month_start=$2 AND resolver_address=$3", message, month, strings.ToLower(resolver))
		return err
	}
	delay := retry.Delay(attempts)
	_, err := d.Pool.Exec(ctx, "UPDATE monthly_root_submissions SET root_submission_status='failed',root_submission_error=$1,next_retry_at=now()+$2::interval,updated_at=now() WHERE month_start=$3 AND resolver_address=$4", message, fmt.Sprintf("%d seconds", int64(delay/time.Second)), month, strings.ToLower(resolver))
	return err
}

func (d *DB) MarkRootPermanentFailure(ctx context.Context, month time.Time, resolver, message string) error {
	_, err := d.Pool.Exec(ctx, "UPDATE monthly_root_submissions SET root_submission_status='failed_permanent',root_submission_error=$1,updated_at=now() WHERE month_start=$2 AND resolver_address=$3", message, month, strings.ToLower(resolver))
	return err
}

func (d *DB) LoadSnapshot(ctx context.Context, m time.Time) (delegation.State, bool, error) {
	var found bool
	if e := d.Pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM delegation_snapshot_checkpoints WHERE month_start=$1)", m).Scan(&found); e != nil {
		return nil, false, e
	}
	if !found {
		return delegation.State{}, false, nil
	}
	rs, e := d.Pool.Query(ctx, "SELECT resolver,delegator,delegated_amount::text FROM delegation_month_end_states WHERE month_start=$1", m)
	if e != nil {
		return nil, false, e
	}
	defer rs.Close()
	s := delegation.State{}
	for rs.Next() {
		var r, g, a string
		if e = rs.Scan(&r, &g, &a); e != nil {
			return nil, false, e
		}
		v, ok := new(big.Int).SetString(a, 10)
		if !ok {
			return nil, false, fmt.Errorf("invalid amount")
		}
		s[delegation.Key{Resolver: r, Delegator: g}] = v
	}
	return s, found, rs.Err()
}
