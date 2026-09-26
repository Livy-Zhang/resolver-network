package rewards

// Root Submitter signs and submits transactions to RewardsDistributor contracts to submit Merkle roots for each epoch
import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

const distributorABI = `[
{"type":"function","name":"submitRoot","stateMutability":"nonpayable","inputs":[{"name":"epochId","type":"uint256"},{"name":"merkleRoot","type":"bytes32"},{"name":"totalReward","type":"uint256"}],"outputs":[]},
{"type":"function","name":"confirmRoot","stateMutability":"nonpayable","inputs":[{"name":"epochId","type":"uint256"}],"outputs":[]},
{"type":"function","name":"rewardsUpdater","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"address"}]},
{"type":"function","name":"epochs","stateMutability":"view","inputs":[{"name":"epochId","type":"uint256"}],"outputs":[{"name":"merkleRoot","type":"bytes32"},{"name":"totalReward","type":"uint256"},{"name":"status","type":"uint8"}]}
]`

const (
	EpochStatusNone uint8 = iota
	EpochStatusPending
	EpochStatusClaimable
)

// Submitter signs transactions as the account configured as rewardsUpdater in
// each RewardsDistributor clone.
type Submitter struct {
	client  submitterClient
	key     *ecdsa.PrivateKey
	from    common.Address
	chainID *big.Int
	abi     abi.ABI
	lastTx  TxMetadata
}

// submitterClient is limited to the RPC methods used to prepare, sign, send, and inspect
// root-submission transactions. It permits using go-ethereum's simulated chain in tests.
type submitterClient interface {
	ethereum.ChainIDReader
	ethereum.ChainReader
	ethereum.ContractCaller
	ethereum.GasEstimator
	ethereum.GasPricer1559
	ethereum.PendingStateReader
	ethereum.TransactionReader
	ethereum.TransactionSender
}

type TxMetadata struct {
	Nonce     uint64
	GasLimit  uint64
	GasTipCap *big.Int
	GasFeeCap *big.Int
}

func NewSubmitter(ctx context.Context, rpcURL, privateKeyHex string) (*Submitter, error) {
	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		return nil, err
	}
	key, err := crypto.HexToECDSA(strings.TrimPrefix(privateKeyHex, "0x"))
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("rewards updater private key: %w", err)
	}
	chainID, err := client.ChainID(ctx)
	if err != nil {
		client.Close()
		return nil, err
	}
	parsed, err := abi.JSON(strings.NewReader(distributorABI))
	if err != nil {
		client.Close()
		return nil, err
	}
	return &Submitter{client: client, key: key, from: crypto.PubkeyToAddress(key.PublicKey), chainID: chainID, abi: parsed}, nil
}

func (s *Submitter) Close() {
	if client, ok := s.client.(interface{ Close() }); ok {
		client.Close()
	}
}
func (s *Submitter) From() common.Address { return s.from }
func (s *Submitter) Receipt(ctx context.Context, hash common.Hash) (*types.Receipt, error) {
	receipt, err := s.client.TransactionReceipt(ctx, hash)
	if err == ethereum.NotFound {
		return nil, nil
	}
	return receipt, err
}

func (s *Submitter) ValidateRewardsUpdater(_ context.Context, expected string) error {
	if !common.IsHexAddress(expected) {
		return fmt.Errorf("invalid database rewards updater address")
	}
	if common.HexToAddress(expected) != s.from {
		return fmt.Errorf("rewards updater mismatch: database=%s signer=%s", common.HexToAddress(expected).Hex(), s.from.Hex())
	}
	return nil
}

// SubmitRoot sends RewardsDistributor.submitRoot(epochId, root, totalReward).
// The root must be produced from allocations for this exact distributor and epoch.
func (s *Submitter) SubmitRoot(ctx context.Context, distributor, rewardsUpdater string, epochID *big.Int, root [32]byte, totalReward *big.Int) (common.Hash, error) {
	if !common.IsHexAddress(distributor) {
		return common.Hash{}, fmt.Errorf("invalid distributor address")
	}
	if epochID == nil || epochID.Sign() <= 0 || epochID.BitLen() > 256 {
		return common.Hash{}, fmt.Errorf("epoch ID must be a positive uint256")
	}
	if totalReward == nil || totalReward.Sign() <= 0 || totalReward.BitLen() > 256 {
		return common.Hash{}, fmt.Errorf("total reward must be a positive uint256")
	}
	if root == ([32]byte{}) {
		return common.Hash{}, fmt.Errorf("merkle root cannot be zero")
	}
	if err := s.ValidateRewardsUpdater(ctx, rewardsUpdater); err != nil {
		return common.Hash{}, err
	}

	to := common.HexToAddress(distributor)
	data, err := s.abi.Pack("submitRoot", epochID, root, totalReward)
	if err != nil {
		return common.Hash{}, err
	}
	nonce, err := s.client.PendingNonceAt(ctx, s.from)
	if err != nil {
		return common.Hash{}, err
	}
	tip, err := s.client.SuggestGasTipCap(ctx)
	if err != nil {
		return common.Hash{}, err
	}
	header, err := s.client.HeaderByNumber(ctx, nil)
	if err != nil {
		return common.Hash{}, err
	}
	if header.BaseFee == nil {
		return common.Hash{}, fmt.Errorf("RPC did not return an EIP-1559 base fee")
	}
	feeCap := new(big.Int).Add(new(big.Int).Mul(header.BaseFee, big.NewInt(2)), tip)
	gas, err := s.client.EstimateGas(ctx, ethereum.CallMsg{From: s.from, To: &to, GasTipCap: tip, GasFeeCap: feeCap, Data: data})
	if err != nil {
		return common.Hash{}, fmt.Errorf("submitRoot simulation failed: %w", err)
	}
	tx := types.NewTx(&types.DynamicFeeTx{ChainID: s.chainID, Nonce: nonce, To: &to, Gas: gas, GasTipCap: tip, GasFeeCap: feeCap, Data: data})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(s.chainID), s.key)
	if err != nil {
		return common.Hash{}, err
	}
	if err = s.client.SendTransaction(ctx, signed); err != nil {
		return common.Hash{}, err
	}
	s.lastTx = TxMetadata{Nonce: nonce, GasLimit: gas, GasTipCap: new(big.Int).Set(tip), GasFeeCap: new(big.Int).Set(feeCap)}
	return signed.Hash(), nil
}

func (s *Submitter) LastTxMetadata() TxMetadata { return s.lastTx }

// EpochStatus returns the distributor's on-chain status for an epoch.
// A root is safe to advertise as confirmed only once this is Claimable.
func (s *Submitter) EpochStatus(ctx context.Context, distributor string, epochID *big.Int) (uint8, error) {
	if !common.IsHexAddress(distributor) {
		return 0, fmt.Errorf("invalid distributor address")
	}
	if epochID == nil || epochID.Sign() <= 0 || epochID.BitLen() > 256 {
		return 0, fmt.Errorf("epoch ID must be a positive uint256")
	}
	to := common.HexToAddress(distributor)
	data, err := s.abi.Pack("epochs", epochID)
	if err != nil {
		return 0, err
	}
	raw, err := s.client.CallContract(ctx, ethereum.CallMsg{To: &to, Data: data}, nil)
	if err != nil {
		return 0, err
	}
	values, err := s.abi.Unpack("epochs", raw)
	if err != nil {
		return 0, err
	}
	return values[2].(uint8), nil
}

// ReplaceRoot resends the same root using the original nonce and higher fees.
func (s *Submitter) ReplaceRoot(ctx context.Context, distributor, rewardsUpdater string, epochID *big.Int, root [32]byte, totalReward *big.Int, nonce, gasLimit uint64, oldTip, oldFee *big.Int) (common.Hash, error) {
	if err := s.ValidateRewardsUpdater(ctx, rewardsUpdater); err != nil {
		return common.Hash{}, err
	}
	if oldTip == nil || oldFee == nil || oldTip.Sign() < 0 || oldFee.Sign() < 0 {
		return common.Hash{}, fmt.Errorf("invalid replacement gas parameters")
	}
	data, err := s.abi.Pack("submitRoot", epochID, root, totalReward)
	if err != nil {
		return common.Hash{}, err
	}
	tip := new(big.Int).Mul(oldTip, big.NewInt(2))
	if tip.Cmp(oldTip) <= 0 {
		tip.Add(oldTip, big.NewInt(1))
	}
	fee := new(big.Int).Mul(oldFee, big.NewInt(2))
	if fee.Cmp(oldFee) <= 0 {
		fee.Add(oldFee, big.NewInt(1))
	}
	to := common.HexToAddress(distributor)
	if gasLimit == 0 {
		return common.Hash{}, fmt.Errorf("replacement gas limit must be positive")
	}
	tx := types.NewTx(&types.DynamicFeeTx{ChainID: s.chainID, Nonce: nonce, To: &to, Gas: gasLimit, GasTipCap: tip, GasFeeCap: fee, Data: data})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(s.chainID), s.key)
	if err != nil {
		return common.Hash{}, err
	}
	if err = s.client.SendTransaction(ctx, signed); err != nil {
		return common.Hash{}, err
	}
	s.lastTx = TxMetadata{Nonce: nonce, GasLimit: gasLimit, GasTipCap: tip, GasFeeCap: fee}
	return signed.Hash(), nil
}

// EnsureRoot submits a root only when the epoch does not yet exist. A retry is
// accepted only if the existing on-chain epoch has exactly the same root and total.
func (s *Submitter) EnsureRoot(ctx context.Context, distributor, rewardsUpdater string, epochID *big.Int, root [32]byte, totalReward *big.Int) (common.Hash, bool, error) {
	if !common.IsHexAddress(distributor) {
		return common.Hash{}, false, fmt.Errorf("invalid distributor address")
	}
	to := common.HexToAddress(distributor)
	data, err := s.abi.Pack("epochs", epochID)
	if err != nil {
		return common.Hash{}, false, err
	}
	raw, err := s.client.CallContract(ctx, ethereum.CallMsg{To: &to, Data: data}, nil)
	if err != nil {
		return common.Hash{}, false, err
	}
	values, err := s.abi.Unpack("epochs", raw)
	if err != nil {
		return common.Hash{}, false, err
	}
	onChainRoot := values[0].([32]byte)
	onChainTotal := values[1].(*big.Int)
	status := values[2].(uint8)
	if status != 0 {
		if onChainRoot != root || onChainTotal.Cmp(totalReward) != 0 {
			return common.Hash{}, false, fmt.Errorf("epoch %s already exists with different root or total", epochID)
		}
		return common.Hash{}, false, nil
	}
	hash, err := s.SubmitRoot(ctx, distributor, rewardsUpdater, epochID, root, totalReward)
	return hash, err == nil, err
}
