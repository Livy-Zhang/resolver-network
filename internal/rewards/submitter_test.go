package rewards

import (
	"context"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
)

type receiptRPC struct{ receipt *types.Receipt }

func (r receiptRPC) GetTransactionReceipt(common.Hash) (*types.Receipt, error) { return r.receipt, nil }

func TestSubmitRootValidatesArgumentsBeforeRPC(t *testing.T) {
	s := &Submitter{}
	if _, err := s.SubmitRoot(context.Background(), "bad", "", big.NewInt(1), [32]byte{1}, big.NewInt(1)); err == nil {
		t.Fatal("expected address error")
	}
	if _, err := s.SubmitRoot(context.Background(), "0x0000000000000000000000000000000000000001", "", big.NewInt(0), [32]byte{1}, big.NewInt(1)); err == nil {
		t.Fatal("expected epoch error")
	}
	if _, err := s.SubmitRoot(context.Background(), "0x0000000000000000000000000000000000000001", "", big.NewInt(1), [32]byte{}, big.NewInt(1)); err == nil {
		t.Fatal("expected root error")
	}
}

func TestReceiptReturnsNilForPendingTransaction(t *testing.T) {
	server := rpc.NewServer()
	if err := server.RegisterName("eth", receiptRPC{}); err != nil {
		t.Fatal(err)
	}
	s := &Submitter{client: ethclient.NewClient(rpc.DialInProc(server))}
	receipt, err := s.Receipt(context.Background(), common.HexToHash("0x01"))
	if err != nil || receipt != nil {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
}

func TestReceiptReturnsMinedTransaction(t *testing.T) {
	server := rpc.NewServer()
	want := &types.Receipt{Status: types.ReceiptStatusSuccessful, TxHash: common.HexToHash("0x01"), Logs: []*types.Log{}}
	if err := server.RegisterName("eth", receiptRPC{receipt: want}); err != nil {
		t.Fatal(err)
	}
	s := &Submitter{client: ethclient.NewClient(rpc.DialInProc(server))}
	receipt, err := s.Receipt(context.Background(), want.TxHash)
	if err != nil || receipt == nil || receipt.Status != types.ReceiptStatusSuccessful {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
}

func TestReplaceRootRejectsInvalidGasParameters(t *testing.T) {
	s := &Submitter{from: common.Address{}}
	if _, err := s.ReplaceRoot(context.Background(), "0x0000000000000000000000000000000000000001", "0x0000000000000000000000000000000000000000", big.NewInt(1), [32]byte{1}, big.NewInt(1), 1, 21000, nil, big.NewInt(1)); err == nil {
		t.Fatal("expected replacement gas validation error")
	}
}
