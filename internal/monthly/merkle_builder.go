package monthly
// converts settled delegation weights into immutable allocations consumed by the Merkle and root-transaction stages
import (
	"encoding/hex"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"
	"github.com/ethereum/go-ethereum/common"

	"resolver-network/internal/database"
	"resolver-network/internal/delegation"
	"resolver-network/internal/merkle"
	"resolver-network/internal/rewards"
)

// BuildMerkleAllocations converts settled delegation weights into immutable
// allocations consumed by the Merkle and root-transaction stages.
func BuildMerkleAllocations(weights []delegation.Weight, rates map[string]database.RateSnapshot, chainID *big.Int, month time.Time) ([]database.MonthlyAllocation, error) {
	byResolver := map[string][]rewards.DelegationWeight{}
	normalizedRates := make(map[string]database.RateSnapshot, len(rates))
	for key, rate := range rates { normalizedRates[strings.ToLower(key)] = rate }
	for _, w := range weights {
		resolver := strings.ToLower(w.Resolver)
		if rate, ok := normalizedRates[resolver]; ok && rate.RewardRate != "0" {
			byResolver[resolver] = append(byResolver[resolver], rewards.DelegationWeight{Resolver: resolver, Delegator: strings.ToLower(w.Delegator), Weight: w.Value})
		}
	}
	epoch := big.NewInt(int64(month.Year()*100 + int(month.Month())))
	out := []database.MonthlyAllocation{}
	resolvers := make([]string, 0, len(byResolver))
	for resolver := range byResolver { resolvers = append(resolvers, resolver) }
	sort.Strings(resolvers)
	for _, resolver := range resolvers {
		ws := byResolver[resolver]
		rate := normalizedRates[resolver]
		if !common.IsHexAddress(rate.Distributor) { return nil, fmt.Errorf("invalid distributor for %s", resolver) }
		r, ok := new(big.Int).SetString(rate.RewardRate, 10)
		if !ok { return nil, fmt.Errorf("invalid reward rate for %s", resolver) }
		calculated, total, err := rewards.Allocate(ws, r); if err != nil { return nil, err }
		leaves := make([]merkle.Allocation, 0, len(calculated))
		for _, a := range calculated { leaves = append(leaves, merkle.Allocation{Resolver: resolver, ChainID: chainID, Distributor: rate.Distributor, EpochID: epoch, Delegator: a.Delegator, Amount: a.Amount}) }
		if len(leaves) == 0 { continue }
		tree, err := merkle.Build(leaves); if err != nil { return nil, err }
		for _, a := range calculated {
			proof, _, err := tree.Proof(a.Delegator); if err != nil { return nil, err }
			p := make([]string, len(proof)); for i, item := range proof { p[i] = "0x" + hex.EncodeToString(item[:]) }
			out = append(out, database.MonthlyAllocation{Resolver: resolver, Distributor: rate.Distributor, RewardsUpdater: rate.RewardsUpdater, Delegator: a.Delegator, RewardRate: r.String(), Weight: a.Weight.String(), Amount: a.Amount.String(), TotalReward: total.String(), EpochID: epoch.String(), MerkleRoot: tree.RootHex(), Proof: p})
		}
	}
	sort.Slice(out, func(i, j int) bool { if out[i].Resolver != out[j].Resolver { return out[i].Resolver < out[j].Resolver }; return out[i].Delegator < out[j].Delegator })
	return out, nil
}
