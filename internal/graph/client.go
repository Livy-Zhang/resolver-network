package graph

// interact with the resolver subgraph using GraphQL
import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"resolver-network/internal/delegation"
	"sort"
	"strconv"
	"time"
)

const size = 1000
const defaultMaxPages = 10000
const defaultMaxEvents = 1000000
const maxResponseBytes = 16 << 20

type Client struct {
	endpoint  string
	http      *http.Client
	pageSize  int
	maxPages  int
	maxEvents int
}

type Options struct {
	Timeout                       time.Duration
	PageSize, MaxPages, MaxEvents int
	HTTPClient                    *http.Client
}

func New(e string) *Client { return NewWithOptions(e, Options{}) }

func NewWithOptions(e string, o Options) *Client {
	if o.Timeout <= 0 {
		o.Timeout = 30 * time.Second
	}
	if o.PageSize <= 0 || o.PageSize > size {
		o.PageSize = size
	}
	if o.MaxPages <= 0 {
		o.MaxPages = defaultMaxPages
	}
	if o.MaxEvents <= 0 {
		o.MaxEvents = defaultMaxEvents
	}
	hc := o.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: o.Timeout}
	}
	return &Client{endpoint: e, http: hc, pageSize: o.PageSize, maxPages: o.MaxPages, maxEvents: o.MaxEvents}
}

type row struct {
	ID          string `json:"id"`
	Delegator   string `json:"delegator"`
	Resolver    string `json:"resolver"`
	OldResolver string `json:"oldResolver"`
	NewResolver string `json:"newResolver"`
	Amount      string `json:"amount"`
	Timestamp   string `json:"timestamp"`
	BlockNumber string `json:"blockNumber"`
	LogIndex    string `json:"logIndex"`
	Anomaly     string `json:"anomaly"`
}
type result struct {
	Data struct {
		D []row `json:"delegatedEvents"`
		U []row `json:"undelegatedEvents"`
		R []row `json:"redelegatedEvents"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type Meta struct {
	BlockNumber int64
}

type DelegationSnapshot struct {
	Delegator string
	Resolver  string
	Amount    *big.Int
}

type RewardRateEvent struct {
	ID             string
	Resolver       string
	Distributor    string
	RewardAddress  string
	RewardRate     string
	RewardsUpdater string
	Timestamp      int64
	BlockNumber    int64
	LogIndex       int64
}
type configRow struct {
	ID             string `json:"id"`
	Resolver       string `json:"resolver"`
	Distributor    string `json:"distributor"`
	RewardAddress  string `json:"rewardAddress"`
	RewardRate     string `json:"rewardRate"`
	RewardsUpdater string `json:"rewardsUpdater"`
	Timestamp      string `json:"timestamp"`
	BlockNumber    string `json:"blockNumber"`
	LogIndex       string `json:"logIndex"`
	Anomaly        string `json:"anomaly"`
}

type metaResult struct {
	Data struct {
		Meta struct {
			Block struct {
				Number string `json:"number"`
			} `json:"block"`
			HasIndexingErrors bool `json:"hasIndexingErrors"`
		} `json:"_meta"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

const query = `query E($first:Int!,$before:BigInt!,$from:BigInt,$block:Int!,$delegatedAfter:ID!,$undelegatedAfter:ID!,$redelegatedAfter:ID!){delegatedEvents(first:$first,orderBy:id,orderDirection:asc,block:{number:$block},where:{timestamp_lt:$before,timestamp_gte:$from,id_gt:$delegatedAfter}){id delegator resolver amount timestamp blockNumber logIndex} undelegatedEvents(first:$first,orderBy:id,orderDirection:asc,block:{number:$block},where:{timestamp_lt:$before,timestamp_gte:$from,id_gt:$undelegatedAfter}){id delegator resolver amount anomaly timestamp blockNumber logIndex} redelegatedEvents(first:$first,orderBy:id,orderDirection:asc,block:{number:$block},where:{timestamp_lt:$before,timestamp_gte:$from,id_gt:$redelegatedAfter}){id delegator oldResolver newResolver amount anomaly timestamp blockNumber logIndex}}`

func (c *Client) Events(ctx context.Context, from *time.Time, before time.Time, atBlock int64) ([]delegation.Event, error) {
	if c.endpoint == "" {
		return nil, fmt.Errorf("GRAPH_ENDPOINT is required")
	}
	if from != nil && !from.Before(before) {
		return nil, fmt.Errorf("invalid time range")
	}
	if atBlock < 0 {
		return nil, fmt.Errorf("block number must be non-negative")
	}
	all := []delegation.Event{}
	seen := map[string]struct{}{}
	delegatedAfter, undelegatedAfter, redelegatedAfter := "", "", ""
	pages := 0
	for {
		pages++
		if pages > c.maxPages || len(all) >= c.maxEvents {
			return nil, fmt.Errorf("GraphQL events pagination limit exceeded")
		}
		v := map[string]any{"first": c.pageSize, "before": strconv.FormatInt(before.Unix(), 10), "block": atBlock, "delegatedAfter": delegatedAfter, "undelegatedAfter": undelegatedAfter, "redelegatedAfter": redelegatedAfter}
		if from != nil {
			v["from"] = strconv.FormatInt(from.Unix(), 10)
		}
		var r result
		if err := c.do(ctx, v, &r); err != nil {
			return nil, err
		}
		e, err := convert(r)
		if err != nil {
			return nil, err
		}
		for _, event := range e {
			key := string(event.Kind) + ":" + event.ID
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			all = append(all, event)
		}
		if len(r.Data.D) < c.pageSize && len(r.Data.U) < c.pageSize && len(r.Data.R) < c.pageSize {
			return all, nil
		}
		if len(r.Data.D) == c.pageSize {
			next := r.Data.D[len(r.Data.D)-1].ID
			if next <= delegatedAfter {
				return nil, fmt.Errorf("delegated event cursor did not advance")
			}
			delegatedAfter = next
		}
		if len(r.Data.U) == c.pageSize {
			next := r.Data.U[len(r.Data.U)-1].ID
			if next <= undelegatedAfter {
				return nil, fmt.Errorf("undelegated event cursor did not advance")
			}
			undelegatedAfter = next
		}
		if len(r.Data.R) == c.pageSize {
			next := r.Data.R[len(r.Data.R)-1].ID
			if next <= redelegatedAfter {
				return nil, fmt.Errorf("redelegated event cursor did not advance")
			}
			redelegatedAfter = next
		}
	}
}

const metaQuery = `query Meta { _meta { block { number } hasIndexingErrors } }`
const delegationsQuery = `query Delegations($first:Int!,$after:ID!,$block:Int!){delegations(first:$first,orderBy:id,orderDirection:asc,block:{number:$block},where:{id_gt:$after,amount_gt:"0"}){id delegator amount resolver{id}}}`

func (c *Client) Delegations(ctx context.Context, atBlock int64) ([]DelegationSnapshot, error) {
	if atBlock < 0 {
		return nil, fmt.Errorf("block number must be non-negative")
	}
	var out []DelegationSnapshot
	after := ""
	pages := 0
	for {
		pages++
		if pages > c.maxPages || len(out) >= c.maxEvents {
			return nil, fmt.Errorf("GraphQL delegations pagination limit exceeded")
		}
		var response struct {
			Data struct {
				Delegations []struct {
					ID        string `json:"id"`
					Delegator string `json:"delegator"`
					Amount    string `json:"amount"`
					Resolver  struct {
						ID string `json:"id"`
					} `json:"resolver"`
				} `json:"delegations"`
			} `json:"data"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		if err := c.doQuery(ctx, delegationsQuery, map[string]any{"first": size, "after": after, "block": atBlock}, &response); err != nil {
			return nil, err
		}
		if len(response.Errors) > 0 {
			return nil, fmt.Errorf("GraphQL: %s", response.Errors[0].Message)
		}
		for _, row := range response.Data.Delegations {
			amount, ok := new(big.Int).SetString(row.Amount, 10)
			if !ok || amount.Sign() <= 0 {
				return nil, fmt.Errorf("invalid delegation amount for %s", row.ID)
			}
			out = append(out, DelegationSnapshot{Delegator: row.Delegator, Resolver: row.Resolver.ID, Amount: amount})
		}
		if len(response.Data.Delegations) < c.pageSize {
			return out, nil
		}
		next := response.Data.Delegations[len(response.Data.Delegations)-1].ID
		if next <= after {
			return nil, fmt.Errorf("delegation cursor did not advance")
		}
		after = next
	}
}

func (c *Client) Meta(ctx context.Context) (Meta, error) {
	var r metaResult
	if err := c.doQuery(ctx, metaQuery, nil, &r); err != nil {
		return Meta{}, err
	}
	if len(r.Errors) > 0 {
		return Meta{}, fmt.Errorf("GraphQL: %s", r.Errors[0].Message)
	}
	if r.Data.Meta.HasIndexingErrors {
		return Meta{}, fmt.Errorf("subgraph has indexing errors")
	}
	n, err := strconv.ParseInt(r.Data.Meta.Block.Number, 10, 64)
	if err != nil {
		return Meta{}, fmt.Errorf("invalid _meta block number: %w", err)
	}
	if n < 0 {
		return Meta{}, fmt.Errorf("invalid _meta block number: negative value")
	}
	return Meta{BlockNumber: n}, nil
}

const createdForMonthQuery = `query Created($first:Int!,$from:BigInt!,$to:BigInt!,$after:ID!,$block:Int!){ distributorCreatedEvents(first:$first,orderBy:id,orderDirection:asc,block:{number:$block},where:{timestamp_gte:$from,timestamp_lt:$to,id_gt:$after}){id resolver distributor rewardsUpdater rewardAddress rewardRate timestamp blockNumber logIndex}}`
const updatedForMonthQuery = `query Updated($first:Int!,$from:BigInt!,$to:BigInt!,$after:ID!,$block:Int!){ rewardRateUpdatedEvents(first:$first,orderBy:id,orderDirection:asc,block:{number:$block},where:{timestamp_gte:$from,timestamp_lt:$to,id_gt:$after}){id resolver distributor rewardAddress rewardRate:newRewardRate anomaly timestamp blockNumber logIndex}}`

// RewardRateEvents returns creation and rate-update actions in one natural month.
func (c *Client) RewardRateEvents(ctx context.Context, from, to time.Time, atBlock int64) ([]RewardRateEvent, error) {
	if !from.Before(to) {
		return nil, fmt.Errorf("invalid time range")
	}
	if atBlock < 0 {
		return nil, fmt.Errorf("block number must be non-negative")
	}
	fromValue := strconv.FormatInt(from.Unix(), 10)
	toValue := strconv.FormatInt(to.Unix(), 10)
	var out []RewardRateEvent
	for kind, q := range []string{createdForMonthQuery, updatedForMonthQuery} {
		after := ""
		pages := 0
		for {
			pages++
			if pages > c.maxPages || len(out) >= c.maxEvents {
				return nil, fmt.Errorf("GraphQL reward-rate pagination limit exceeded")
			}
			var r struct {
				Data struct {
					Created []configRow `json:"distributorCreatedEvents"`
					Updated []configRow `json:"rewardRateUpdatedEvents"`
				} `json:"data"`
				Errors []struct {
					Message string `json:"message"`
				} `json:"errors"`
			}
			v := map[string]any{"first": c.pageSize, "from": fromValue, "to": toValue, "after": after, "block": atBlock}
			if err := c.doQuery(ctx, q, v, &r); err != nil {
				return nil, err
			}
			if len(r.Errors) > 0 {
				return nil, fmt.Errorf("GraphQL: %s", r.Errors[0].Message)
			}
			rs := r.Data.Created
			if kind == 1 {
				rs = r.Data.Updated
			}
			for _, x := range rs {
				if x.Anomaly != "" {
					return nil, fmt.Errorf("anomalous reward-rate event %s: %s", x.ID, x.Anomaly)
				}
				ts, e := strconv.ParseInt(x.Timestamp, 10, 64)
				if e != nil {
					return nil, e
				}
				b, e := strconv.ParseInt(x.BlockNumber, 10, 64)
				if e != nil {
					return nil, e
				}
				l, e := strconv.ParseInt(x.LogIndex, 10, 64)
				if e != nil {
					return nil, e
				}
				out = append(out, RewardRateEvent{ID: x.ID, Resolver: x.Resolver, Distributor: x.Distributor, RewardAddress: x.RewardAddress, RewardRate: x.RewardRate, RewardsUpdater: x.RewardsUpdater, Timestamp: ts, BlockNumber: b, LogIndex: l})
			}
			if len(rs) < c.pageSize {
				break
			}
			next := rs[len(rs)-1].ID
			if next <= after {
				return nil, fmt.Errorf("reward-rate event cursor did not advance")
			}
			after = next
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Timestamp != out[j].Timestamp {
			return out[i].Timestamp < out[j].Timestamp
		}
		if out[i].BlockNumber != out[j].BlockNumber {
			return out[i].BlockNumber < out[j].BlockNumber
		}
		return out[i].LogIndex < out[j].LogIndex
	})
	return out, nil
}

func (c *Client) do(ctx context.Context, v map[string]any, out *result) error {
	if err := c.doQuery(ctx, query, v, out); err != nil {
		return err
	}
	if len(out.Errors) > 0 {
		return fmt.Errorf("GraphQL: %s", out.Errors[0].Message)
	}
	return nil
}

func (c *Client) doQuery(ctx context.Context, gql string, v map[string]any, out any) error {
	if c.endpoint == "" {
		return fmt.Errorf("GRAPH_ENDPOINT is required")
	}
	u, err := url.Parse(c.endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("invalid GRAPH_ENDPOINT %q", c.endpoint)
	}
	if c.http == nil {
		return fmt.Errorf("graph HTTP client is nil")
	}
	b, e := json.Marshal(map[string]any{"query": gql, "variables": v})
	if e != nil {
		return e
	}
	req, e := http.NewRequestWithContext(ctx, "POST", c.endpoint, bytes.NewReader(b))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	res, e := c.http.Do(req)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes+1))
	if e != nil {
		return e
	}
	if len(raw) > maxResponseBytes {
		return fmt.Errorf("GraphQL response exceeds %d bytes", maxResponseBytes)
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("GraphQL %s: %s", res.Status, raw)
	}
	if e = json.Unmarshal(raw, out); e != nil {
		return e
	}
	return nil
}
func convert(r result) ([]delegation.Event, error) {
	out := []delegation.Event{}
	f := func(k delegation.Kind, rs []row) error {
		for _, x := range rs {
			if x.Anomaly != "" {
				return fmt.Errorf("anomalous %s event %s: %s", k, x.ID, x.Anomaly)
			}
			if x.ID == "" || x.Delegator == "" {
				return fmt.Errorf("invalid %s event identity", k)
			}
			if k == delegation.Redelegated && (x.OldResolver == "" || x.NewResolver == "") {
				return fmt.Errorf("invalid redelegated event %s resolvers", x.ID)
			}
			if k != delegation.Redelegated && x.Resolver == "" {
				return fmt.Errorf("invalid %s event resolver", k)
			}
			a, ok := new(big.Int).SetString(x.Amount, 10)
			if !ok || a.Sign() < 0 {
				return fmt.Errorf("bad amount")
			}
			ts, e := strconv.ParseInt(x.Timestamp, 10, 64)
			if e != nil {
				return e
			}
			b, e := strconv.ParseInt(x.BlockNumber, 10, 64)
			if e != nil {
				return e
			}
			l, e := strconv.ParseInt(x.LogIndex, 10, 64)
			if e != nil {
				return e
			}
			if ts < 0 || b < 0 || l < 0 {
				return fmt.Errorf("invalid event metadata for %s", x.ID)
			}
			out = append(out, delegation.Event{
				Kind:        k,
				ID:          x.ID,
				Delegator:   x.Delegator,
				Resolver:    x.Resolver,
				OldResolver: x.OldResolver,
				NewResolver: x.NewResolver,
				Amount:      a,
				Timestamp:   time.Unix(ts, 0).UTC(),
				BlockNumber: b,
				LogIndex:    l,
			})
		}
		return nil
	}
	if e := f(delegation.Delegated, r.Data.D); e != nil {
		return nil, e
	}
	if e := f(delegation.Undelegated, r.Data.U); e != nil {
		return nil, e
	}
	if e := f(delegation.Redelegated, r.Data.R); e != nil {
		return nil, e
	}
	return out, nil
}
