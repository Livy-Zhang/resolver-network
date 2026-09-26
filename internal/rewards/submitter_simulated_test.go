package rewards

import (
	"context"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient/simulated"
)

// This contract accepts transactions and returns an empty Epoch struct (96 zero
// bytes) for calls. It is sufficient to exercise the complete RPC/sign/send/mine
// path without needing a real node or a compiled distributor artifact.
var emptyEpochContract = common.FromHex("0x6005600c60003960056000f360606000f3")

func TestRootSubmissionTransactionsOnSimulatedChain(t *testing.T) {
	for _, test := range []struct {
		name string
		send func(*Submitter, context.Context, string, string, *big.Int, [32]byte, *big.Int) (common.Hash, error)
	}{
		{"submit", func(s *Submitter, ctx context.Context, distributor, updater string, epoch *big.Int, root [32]byte, total *big.Int) (common.Hash, error) {
			return s.SubmitRoot(ctx, distributor, updater, epoch, root, total)
		}},
		{"replace", func(s *Submitter, ctx context.Context, distributor, updater string, epoch *big.Int, root [32]byte, total *big.Int) (common.Hash, error) {
			return s.ReplaceRoot(ctx, distributor, updater, epoch, root, total, 1, 100_000, big.NewInt(1_000_000_000), big.NewInt(2_000_000_000))
		}},
		{"ensure", func(s *Submitter, ctx context.Context, distributor, updater string, epoch *big.Int, root [32]byte, total *big.Int) (common.Hash, error) {
			hash, submitted, err := s.EnsureRoot(ctx, distributor, updater, epoch, root, total)
			if !submitted && err == nil {
				t.Fatal("EnsureRoot did not submit an empty epoch")
			}
			return hash, err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			s, backend, distributor := simulatedSubmitter(t, ctx)
			defer backend.Close()

			epoch, total := big.NewInt(202609), big.NewInt(123456789)
			root := [32]byte{1}
			hash, err := test.send(s, ctx, distributor.Hex(), s.From().Hex(), epoch, root, total)
			if err != nil {
				t.Fatal(err)
			}
			backend.Commit()
			backend.Commit()

			receipt := eventuallyReceipt(t, s.client, hash)
			if got, err := s.Receipt(ctx, hash); err != nil || got == nil {
				t.Fatalf("Submitter.Receipt()=%+v err=%v", got, err)
			}
			if receipt.Status != types.ReceiptStatusSuccessful {
				t.Fatalf("receipt=%+v", receipt)
			}
			tx, _, err := backend.Client().TransactionByHash(ctx, hash)
			if err != nil {
				t.Fatal(err)
			}
			if tx.To() == nil || *tx.To() != distributor {
				t.Fatalf("transaction recipient=%v, want %s", tx.To(), distributor)
			}
			method, err := s.abi.MethodById(tx.Data()[:4])
			if err != nil || method.Name != "submitRoot" {
				t.Fatalf("transaction calldata does not invoke submitRoot: %v", err)
			}
			values, err := method.Inputs.Unpack(tx.Data()[4:])
			if err != nil || values[0].(*big.Int).Cmp(epoch) != 0 || values[1].([32]byte) != root || values[2].(*big.Int).Cmp(total) != 0 {
				t.Fatalf("unexpected submitRoot calldata: values=%v err=%v", values, err)
			}
		})
	}
}

func simulatedSubmitter(t *testing.T, ctx context.Context) (*Submitter, *simulated.Backend, common.Address) {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	from := crypto.PubkeyToAddress(key.PublicKey)
	backend := simulated.NewBackend(types.GenesisAlloc{from: {Balance: new(big.Int).Mul(big.NewInt(10), big.NewInt(1e18))}})
	client := backend.Client()
	chainID, err := client.ChainID(ctx)
	if err != nil {
		backend.Close()
		t.Fatal(err)
	}
	feeCap := big.NewInt(2_000_000_000)
	deploy := types.NewTx(&types.DynamicFeeTx{ChainID: chainID, Nonce: 0, Gas: 100_000, GasTipCap: big.NewInt(1_000_000_000), GasFeeCap: feeCap, Data: emptyEpochContract})
	signed, err := types.SignTx(deploy, types.LatestSignerForChainID(chainID), key)
	if err != nil {
		backend.Close()
		t.Fatal(err)
	}
	if err = client.SendTransaction(ctx, signed); err != nil {
		backend.Close()
		t.Fatal(err)
	}
	backend.Commit()
	receipt := eventuallyReceipt(t, client, signed.Hash())
	if receipt.Status != types.ReceiptStatusSuccessful {
		backend.Close()
		t.Fatalf("deployment receipt=%+v", receipt)
	}
	parsed, err := abi.JSON(strings.NewReader(distributorABI))
	if err != nil {
		backend.Close()
		t.Fatal(err)
	}
	return &Submitter{client: client, key: key, from: from, chainID: chainID, abi: parsed}, backend, receipt.ContractAddress
}

func eventuallyReceipt(t *testing.T, client interface {
	TransactionReceipt(context.Context, common.Hash) (*types.Receipt, error)
}, hash common.Hash) *types.Receipt {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		receipt, err := client.TransactionReceipt(context.Background(), hash)
		if err == nil && receipt != nil {
			return receipt
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("transaction %s was not indexed within one second", hash)
	return nil
}
