package rewards
// Calculator calculates the reward allocations for a given month based on the delegations and the reward rate
import (
	"fmt"
	"math/big"
)

const secondsPerYear int64 = 365 * 24 * 60 * 60

var rateScale = big.NewInt(1_000_000_000_000_000_000)

type DelegationWeight struct {
	Resolver  string
	Delegator string
	Weight    *big.Int
}

type Allocation struct {
	Resolver  string
	Delegator string
	Weight    *big.Int
	Amount    *big.Int
}

func Amount(weight, rewardRate *big.Int) (*big.Int, error) {
	if weight == nil || rewardRate == nil || weight.Sign() < 0 || rewardRate.Sign() < 0 {
		return nil, fmt.Errorf("weight and reward rate must be non-negative")
	}
	denominator := new(big.Int).Mul(big.NewInt(secondsPerYear), rateScale)
	numerator := new(big.Int).Mul(weight, rewardRate)
	return numerator.Quo(numerator, denominator), nil
}

// Allocate rounds each delegator independently, excludes zero-value leaves,
// and returns the sum of the actual leaf amounts as totalReward.
func Allocate(weights []DelegationWeight, rewardRate *big.Int) ([]Allocation, *big.Int, error) {
	if rewardRate == nil || rewardRate.Sign() < 0 {
		return nil, nil, fmt.Errorf("reward rate must be non-negative")
	}
	allocations := make([]Allocation, 0, len(weights))
	total := new(big.Int)
	for _, weight := range weights {
		amount, err := Amount(weight.Weight, rewardRate)
		if err != nil {
			return nil, nil, fmt.Errorf("allocation %s/%s: %w", weight.Resolver, weight.Delegator, err)
		}
		if amount.Sign() == 0 {
			continue
		}
		allocations = append(allocations, Allocation{
			Resolver: weight.Resolver, Delegator: weight.Delegator,
			Weight: new(big.Int).Set(weight.Weight), Amount: amount,
		})
		total.Add(total, amount)
	}
	return allocations, total, nil
}
