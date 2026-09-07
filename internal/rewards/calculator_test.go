package rewards

import (
	"math/big"
	"testing"
)

func TestAllocateRoundsEachDelegatorDownBeforeSumming(t *testing.T) {
	rate := big.NewInt(1_000_000_000_000_000_000) // 100% APR
	weights := []DelegationWeight{
		{Resolver: "resolver", Delegator: "a", Weight: big.NewInt(secondsPerYear + 1)},
		{Resolver: "resolver", Delegator: "b", Weight: big.NewInt(secondsPerYear - 1)},
	}
	allocations, total, err := Allocate(weights, rate)
	if err != nil {
		t.Fatal(err)
	}
	if len(allocations) != 1 || allocations[0].Amount.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("allocations = %#v", allocations)
	}
	if total.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("total = %s, want 1", total)
	}
}

func TestAmountRejectsNegativeValues(t *testing.T) {
	if _, err := Amount(big.NewInt(-1), big.NewInt(1)); err == nil {
		t.Fatal("expected negative weight error")
	}
	if _, _, err := Allocate(nil, big.NewInt(-1)); err == nil {
		t.Fatal("expected negative rate error")
	}
}
