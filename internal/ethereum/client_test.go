package ethereum

import (
	"context"
	"errors"
	"math/big"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rpc"
)

type ethRPC struct{}

type emptyHeaderRPC struct{}
type zeroTimestampRPC struct{}
type mismatchRPC struct{}
type failingRPC struct{}
type invalidChainRPC struct{}
type sendFailRPC struct{}

func (emptyHeaderRPC) GetBlockByNumber(context.Context, rpc.BlockNumber, bool) (*types.Header, error) { return nil, nil }
func (zeroTimestampRPC) GetBlockByNumber(_ context.Context, n rpc.BlockNumber, _ bool) (*types.Header, error) { return &types.Header{Number: big.NewInt(int64(n)), Difficulty: big.NewInt(0)}, nil }
func (mismatchRPC) GetBlockByNumber(_ context.Context, n rpc.BlockNumber, _ bool) (*types.Header, error) { return &types.Header{Number: big.NewInt(int64(n)+1), Time: 1, Difficulty: big.NewInt(0)}, nil }
func (failingRPC) GetBlockByNumber(context.Context, rpc.BlockNumber, bool) (*types.Header, error) { return nil, errors.New("rpc unavailable") }
func (invalidChainRPC) ChainId() *hexutil.Big { return nil }
func (sendFailRPC) SendRawTransaction(context.Context, hexutil.Bytes) (common.Hash, error) { return common.Hash{}, errors.New("send failed") }

func (ethRPC) SendRawTransaction(_ context.Context, _ hexutil.Bytes) (common.Hash, error) {
	return common.HexToHash("0x1234"), nil
}

func (ethRPC) GetBlockByNumber(_ context.Context, number rpc.BlockNumber, _ bool) (*types.Header, error) {
	if number == rpc.FinalizedBlockNumber {
		return &types.Header{Number: big.NewInt(16), Time: 100, Difficulty: big.NewInt(0)}, nil
	}
	if number < 0 {
		return nil, errors.New("unexpected block tag")
	}
	return &types.Header{Number: big.NewInt(int64(number)), Time: uint64(number) * 10, Difficulty: big.NewInt(0)}, nil
}

func (ethRPC) ChainId() *hexutil.Big {
	v := hexutil.Big(*big.NewInt(11155111))
	return &v
}

func TestFinalizedAndChainID(t *testing.T) {
	server := rpc.NewServer()
	if err := server.RegisterName("eth", ethRPC{}); err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	c, err := New(context.Background(), httpServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	b, err := c.Finalized(context.Background())
	if err != nil || b.Number != 16 || !b.Timestamp.Equal(time.Unix(100, 0).UTC()) {
		t.Fatalf("block=%+v err=%v", b, err)
	}
	id, err := c.ChainID(context.Background())
	if err != nil || id.Int64() != 11155111 {
		t.Fatalf("id=%v err=%v", id, err)
	}
}

func TestNewRejectsEmptyEndpoint(t *testing.T) {
	if _, err := New(context.Background(), ""); err == nil {
		t.Fatal("expected empty endpoint error")
	}
}

func TestNewRejectsInvalidEndpointScheme(t *testing.T) {
	if _, err := New(context.Background(), "ftp://localhost:8545"); err == nil {
		t.Fatal("expected unsupported scheme error")
	}
}

func TestCloseIsNilSafe(t *testing.T) {
	var c *Client
	c.Close()
	(&Client{}).Close()
}

func TestSendTransaction(t *testing.T) {
	server := rpc.NewServer()
	if err := server.RegisterName("eth", ethRPC{}); err != nil { t.Fatal(err) }
	httpServer := httptest.NewServer(server); defer httpServer.Close()
	c, err := New(context.Background(), httpServer.URL); if err != nil { t.Fatal(err) }; defer c.Close()
	if err = c.SendTransaction(context.Background(), types.NewTx(&types.LegacyTx{Nonce: 1, Gas: 21000, GasPrice: big.NewInt(1), To: &common.Address{}})); err != nil { t.Fatal(err) }
}

func TestSendTransactionRejectsNil(t *testing.T) {
	server := rpc.NewServer()
	if err := server.RegisterName("eth", ethRPC{}); err != nil { t.Fatal(err) }
	httpServer := httptest.NewServer(server); defer httpServer.Close()
	c, err := New(context.Background(), httpServer.URL); if err != nil { t.Fatal(err) }; defer c.Close()
	if err = c.SendTransaction(context.Background(), nil); err == nil { t.Fatal("expected nil transaction error") }
}

func TestBlockBeforeRejectsNegativeMaximum(t *testing.T) {
	server := rpc.NewServer(); if err := server.RegisterName("eth", ethRPC{}); err != nil { t.Fatal(err) }
	httpServer := httptest.NewServer(server); defer httpServer.Close()
	c, err := New(context.Background(), httpServer.URL); if err != nil { t.Fatal(err) }; defer c.Close()
	if _, err = c.BlockBefore(context.Background(), time.Now(), -1); err == nil { t.Fatal("expected negative maximum error") }
}

func TestFinalizedRejectsEmptyHeader(t *testing.T) {
	server := rpc.NewServer(); if err := server.RegisterName("eth", emptyHeaderRPC{}); err != nil { t.Fatal(err) }
	httpServer := httptest.NewServer(server); defer httpServer.Close()
	c, err := New(context.Background(), httpServer.URL); if err != nil { t.Fatal(err) }; defer c.Close()
	if _, err = c.Finalized(context.Background()); err == nil { t.Fatal("expected empty header error") }
}

func TestFinalizedRejectsZeroTimestamp(t *testing.T) {
	server := rpc.NewServer(); if err := server.RegisterName("eth", zeroTimestampRPC{}); err != nil { t.Fatal(err) }
	httpServer := httptest.NewServer(server); defer httpServer.Close()
	c, err := New(context.Background(), httpServer.URL); if err != nil { t.Fatal(err) }; defer c.Close()
	if _, err = c.Finalized(context.Background()); err == nil { t.Fatal("expected zero timestamp error") }
}

func TestBlockBeforeRejectsMismatchedBlock(t *testing.T) {
	server := rpc.NewServer(); if err := server.RegisterName("eth", mismatchRPC{}); err != nil { t.Fatal(err) }
	httpServer := httptest.NewServer(server); defer httpServer.Close()
	c, err := New(context.Background(), httpServer.URL); if err != nil { t.Fatal(err) }; defer c.Close()
	if _, err = c.BlockBefore(context.Background(), time.Unix(100, 0), 4); err == nil { t.Fatal("expected mismatched block error") }
}

func TestFinalizedPropagatesRPCFailure(t *testing.T) {
	server := rpc.NewServer(); if err := server.RegisterName("eth", failingRPC{}); err != nil { t.Fatal(err) }
	httpServer := httptest.NewServer(server); defer httpServer.Close()
	c, err := New(context.Background(), httpServer.URL); if err != nil { t.Fatal(err) }; defer c.Close()
	if _, err = c.FinalizedBlockNumber(context.Background()); err == nil { t.Fatal("expected finalized RPC error") }
}

func TestBlockBeforeReturnsErrorWhenNoBlockPrecedesBoundary(t *testing.T) {
	server := rpc.NewServer(); if err := server.RegisterName("eth", ethRPC{}); err != nil { t.Fatal(err) }
	httpServer := httptest.NewServer(server); defer httpServer.Close()
	c, err := New(context.Background(), httpServer.URL); if err != nil { t.Fatal(err) }; defer c.Close()
	if _, err = c.BlockBefore(context.Background(), time.Unix(0, 0), 10); err == nil { t.Fatal("expected no boundary block error") }
}

func TestFinalizedHonorsCancelledContext(t *testing.T) {
	server := rpc.NewServer(); if err := server.RegisterName("eth", failingRPC{}); err != nil { t.Fatal(err) }
	httpServer := httptest.NewServer(server); defer httpServer.Close()
	c, err := New(context.Background(), httpServer.URL); if err != nil { t.Fatal(err) }; defer c.Close()
	ctx, cancel := context.WithCancel(context.Background()); cancel()
	if _, err = c.Finalized(ctx); err == nil { t.Fatal("expected cancelled context error") }
}

func TestBlockBeforeHonorsLookupLimit(t *testing.T) {
	server := rpc.NewServer(); if err := server.RegisterName("eth", ethRPC{}); err != nil { t.Fatal(err) }
	httpServer := httptest.NewServer(server); defer httpServer.Close()
	c, err := NewWithOptions(context.Background(), httpServer.URL, Options{MaxBlockLookups: 1}); if err != nil { t.Fatal(err) }; defer c.Close()
	if _, err = c.BlockBefore(context.Background(), time.Unix(55, 0), 100); err == nil { t.Fatal("expected lookup limit error") }
}

func TestChainIDRejectsInvalidRPCValue(t *testing.T) {
	server := rpc.NewServer(); if err := server.RegisterName("eth", invalidChainRPC{}); err != nil { t.Fatal(err) }
	httpServer := httptest.NewServer(server); defer httpServer.Close()
	c, err := New(context.Background(), httpServer.URL); if err != nil { t.Fatal(err) }; defer c.Close()
	if _, err = c.ChainID(context.Background()); err == nil { t.Fatal("expected invalid chain id error") }
}

func TestSendTransactionPropagatesRPCFailure(t *testing.T) {
	server := rpc.NewServer(); if err := server.RegisterName("eth", sendFailRPC{}); err != nil { t.Fatal(err) }
	httpServer := httptest.NewServer(server); defer httpServer.Close()
	c, err := New(context.Background(), httpServer.URL); if err != nil { t.Fatal(err) }; defer c.Close()
	tx := types.NewTx(&types.LegacyTx{Nonce: 1, Gas: 21000, GasPrice: big.NewInt(1)})
	if err = c.SendTransaction(context.Background(), tx); err == nil { t.Fatal("expected send RPC error") }
}

func TestRPCConnectionInterrupted(t *testing.T) {
	server := rpc.NewServer(); if err := server.RegisterName("eth", ethRPC{}); err != nil { t.Fatal(err) }
	httpServer := httptest.NewServer(server)
	c, err := New(context.Background(), httpServer.URL); if err != nil { t.Fatal(err) }
	httpServer.Close(); defer c.Close()
	if _, err = c.Finalized(context.Background()); err == nil { t.Fatal("expected connection error") }
}

func TestBlockBeforeFindsLastBlockBeforeBoundary(t *testing.T) {
	server := rpc.NewServer()
	if err := server.RegisterName("eth", ethRPC{}); err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	c, err := New(context.Background(), httpServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	block, err := c.BlockBefore(context.Background(), time.Unix(55, 0).UTC(), 10)
	if err != nil || block.Number != 5 || !block.Timestamp.Equal(time.Unix(50, 0).UTC()) {
		t.Fatalf("block=%+v err=%v", block, err)
	}
}
