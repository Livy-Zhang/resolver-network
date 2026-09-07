package ethereum

// interact with an Ethereum node using ethclient
import (
	"context"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
)

type Client struct {
	client   *ethclient.Client
	endpoint string
	timeout  time.Duration
	maxLookups int
}

type Options struct {
	RequestTimeout time.Duration
	MaxBlockLookups int
}

const (
	rpcRequestTimeout = 30 * time.Second
	maxBlockLookups   = 128
)

type ChainClient interface {
	Finalized(context.Context) (Block, error)
	ChainID(context.Context) (*big.Int, error)
	SendTransaction(context.Context, *types.Transaction) error
}

type Block struct {
	Number    int64
	Timestamp time.Time
}

func New(ctx context.Context, endpoint string) (*Client, error) {
	return NewWithOptions(ctx, endpoint, Options{})
}

func NewWithOptions(ctx context.Context, endpoint string, options Options) (*Client, error) {
	if endpoint == "" {
		return nil, fmt.Errorf("RPC endpoint is required")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid RPC endpoint %q", endpoint)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "ws", "wss":
	default:
		return nil, fmt.Errorf("unsupported RPC endpoint scheme %q", parsed.Scheme)
	}
	client, err := ethclient.DialContext(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	if options.RequestTimeout <= 0 { options.RequestTimeout = rpcRequestTimeout }
	if options.MaxBlockLookups <= 0 { options.MaxBlockLookups = maxBlockLookups }
	return &Client{client: client, endpoint: endpoint, timeout: options.RequestTimeout, maxLookups: options.MaxBlockLookups}, nil
}

func (c *Client) Close() {
	if c != nil && c.client != nil {
		c.client.Close()
	}
}

func (c *Client) FinalizedBlockNumber(ctx context.Context) (int64, error) {
	b, err := c.Finalized(ctx)
	return b.Number, err
}


func (c *Client) ChainID(ctx context.Context) (*big.Int, error) {
	if c == nil || c.client == nil {
		return nil, fmt.Errorf("chain id: ethereum client is nil")
	}
	requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	id, err := c.client.ChainID(requestCtx)
	if err != nil {
		return nil, fmt.Errorf("chain id (%s): %w", c.endpoint, err)
	}
	if id == nil || id.Sign() <= 0 {
		return nil, fmt.Errorf("chain id (%s): RPC returned invalid chain id", c.endpoint)
	}
	return id, nil
}

func (c *Client) SendTransaction(ctx context.Context, tx *types.Transaction) error {
	if c == nil || c.client == nil {
		return fmt.Errorf("send transaction: ethereum client is nil")
	}
	if tx == nil {
		return fmt.Errorf("send transaction (%s): transaction is nil", c.endpoint)
	}
	requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	if err := c.client.SendTransaction(requestCtx, tx); err != nil {
		return fmt.Errorf("send transaction (%s): %w", c.endpoint, err)
	}
	return nil
}

func (c *Client) Finalized(ctx context.Context) (Block, error) {
	if c == nil || c.client == nil {
		return Block{}, fmt.Errorf("finalized header: ethereum client is nil")
	}
	requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	header, err := c.client.HeaderByNumber(requestCtx, big.NewInt(int64(rpc.FinalizedBlockNumber)))
	if err != nil {
		return Block{}, fmt.Errorf("finalized header (%s): %w", c.endpoint, err)
	}
	if header == nil || header.Number == nil {
		return Block{}, fmt.Errorf("RPC returned no finalized block")
	}
	if !header.Number.IsInt64() {
		return Block{}, fmt.Errorf("finalized block number exceeds int64")
	}
	if header.Time == 0 {
		return Block{}, fmt.Errorf("finalized header (%s): RPC returned invalid timestamp", c.endpoint)
	}
	return Block{Number: header.Number.Int64(), Timestamp: time.Unix(int64(header.Time), 0).UTC()}, nil
}

func (c *Client) BlockBefore(ctx context.Context, before time.Time, maximum int64) (Block, error) {
	if c == nil || c.client == nil {
		return Block{}, fmt.Errorf("block before: ethereum client is nil")
	}
	if maximum < 0 {
		return Block{}, fmt.Errorf("maximum block number must be non-negative")
	}
	low, high := int64(0), maximum
	var result *Block
	lookups := 0
	for low <= high {
		lookups++
		if lookups > c.maxLookups {
			return Block{}, fmt.Errorf("block before (%s): lookup limit exceeded", c.endpoint)
		}
		mid := low + (high-low)/2
		requestCtx, cancel := context.WithTimeout(ctx, c.timeout)
		header, err := c.client.HeaderByNumber(requestCtx, big.NewInt(mid))
		cancel()
		if err != nil {
			return Block{}, fmt.Errorf("block before (%s), block %d: %w", c.endpoint, mid, err)
		}
		if header == nil || header.Number == nil || !header.Number.IsInt64() {
			return Block{}, fmt.Errorf("RPC returned invalid block %d", mid)
		}
		if header.Number.Int64() != mid {
			return Block{}, fmt.Errorf("block before (%s): RPC returned block %d for request %d", c.endpoint, header.Number.Int64(), mid)
		}
		block := Block{Number: header.Number.Int64(), Timestamp: time.Unix(int64(header.Time), 0).UTC()}
		if block.Timestamp.Before(before.UTC()) {
			result = &block
			low = mid + 1
		} else {
			high = mid - 1
		}
	}
	if result == nil {
		return Block{}, fmt.Errorf("no block before %s", before.UTC().Format(time.RFC3339))
	}
	return *result, nil
}
