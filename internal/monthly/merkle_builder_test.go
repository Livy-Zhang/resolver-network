package monthly

import (
	"math/big"
	"testing"
	"time"

	"resolver-network/internal/database"
	"resolver-network/internal/delegation"
)

func TestBuildMerkleAllocationsRejectsInvalidRate(t *testing.T) {
	month := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	weights := []delegation.Weight{{MonthStart: month, Resolver: "0x0000000000000000000000000000000000000001", Delegator: "0x0000000000000000000000000000000000000002", Value: big.NewInt(1)}}
	rates := map[string]database.RateSnapshot{"0x0000000000000000000000000000000000000001": {RewardRate: "not-a-number", Distributor: "0x0000000000000000000000000000000000000003"}}
	if _, err := BuildMerkleAllocations(weights, rates, big.NewInt(11155111), month); err == nil { t.Fatal("expected invalid rate error") }
}

func TestBuildMerkleAllocationsRejectsInvalidDistributor(t *testing.T) {
	month := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	weights := []delegation.Weight{{MonthStart: month, Resolver: "0x0000000000000000000000000000000000000001", Delegator: "0x0000000000000000000000000000000000000002", Value: big.NewInt(1)}}
	rates := map[string]database.RateSnapshot{"0x0000000000000000000000000000000000000001": {RewardRate: "1", Distributor: "bad"}}
	if _, err := BuildMerkleAllocations(weights, rates, big.NewInt(11155111), month); err == nil { t.Fatal("expected invalid distributor error") }
}

func TestBuildMerkleAllocationsSortsResolvers(t *testing.T) {
	month := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	a, b := "0x000000000000000000000000000000000000000a", "0x000000000000000000000000000000000000000b"
	weights := []delegation.Weight{{MonthStart: month, Resolver: b, Delegator: a, Value: big.NewInt(1)}, {MonthStart: month, Resolver: a, Delegator: b, Value: big.NewInt(1)}}
	rates := map[string]database.RateSnapshot{a: {RewardRate: "1000000000000000000000000000", Distributor: "0x0000000000000000000000000000000000000003"}, b: {RewardRate: "1000000000000000000000000000", Distributor: "0x0000000000000000000000000000000000000004"}}
	out, err := BuildMerkleAllocations(weights, rates, big.NewInt(11155111), month)
	if err != nil { t.Fatal(err) }
	if len(out) != 2 || out[0].Resolver != a || out[1].Resolver != b { t.Fatalf("unexpected order: %+v", out) }
}
