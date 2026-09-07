package merkle
// generate and verify Merkle trees for rewards allocations
import (
	"bytes"
	"encoding/hex"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"golang.org/x/crypto/sha3"
)

type Allocation struct {
	Resolver    string
	ChainID     *big.Int
	Distributor string
	EpochID     *big.Int
	Delegator   string
	Amount      *big.Int
}

type Tree struct {
	root    [32]byte
	leaves  [][32]byte
	proofs  map[string][][32]byte
	amounts map[string]*big.Int
}

// Leaf matches RewardsDistributor.leaf exactly:
// keccak256(abi.encodePacked(chainId, distributor, epochId, delegator, amount)).
func Leaf(chainID *big.Int, distributor string, epochID *big.Int, delegator string, amount *big.Int) ([32]byte, error) {
	var out [32]byte
	chain, err := uint256(chainID)
	if err != nil {
		return out, fmt.Errorf("chain ID: %w", err)
	}
	epoch, err := uint256(epochID)
	if err != nil {
		return out, fmt.Errorf("epoch ID: %w", err)
	}
	value, err := uint256(amount)
	if err != nil {
		return out, fmt.Errorf("amount: %w", err)
	}
	distributorBytes, err := address(distributor)
	if err != nil {
		return out, fmt.Errorf("distributor: %w", err)
	}
	delegatorBytes, err := address(delegator)
	if err != nil {
		return out, fmt.Errorf("delegator: %w", err)
	}
	h := sha3.NewLegacyKeccak256()
	h.Write(chain[:])
	h.Write(distributorBytes[:])
	h.Write(epoch[:])
	h.Write(delegatorBytes[:])
	h.Write(value[:])
	copy(out[:], h.Sum(nil))
	return out, nil
}

func Build(allocations []Allocation) (*Tree, error) {
	if len(allocations) == 0 {
		return nil, fmt.Errorf("at least one allocation is required")
	}
	t := &Tree{proofs: map[string][][32]byte{}, amounts: map[string]*big.Int{}}
	seen := map[string]bool{}
	leafByDelegator := map[string][32]byte{}
	first := allocations[0]
	for _, a := range allocations {
		if a.Resolver == "" || a.Distributor == "" || a.Delegator == "" { return nil, fmt.Errorf("resolver, distributor, and delegator are required") }
		if a.ChainID == nil || a.EpochID == nil || first.ChainID == nil || first.EpochID == nil ||
			a.ChainID.Cmp(first.ChainID) != 0 || a.EpochID.Cmp(first.EpochID) != 0 ||
			!strings.EqualFold(a.Distributor, first.Distributor) || !strings.EqualFold(a.Resolver, first.Resolver) {
			return nil, fmt.Errorf("all allocations must use one resolver, chain, distributor, and epoch")
		}
		if a.ChainID.Sign() <= 0 || a.EpochID.Sign() <= 0 { return nil, fmt.Errorf("chain ID and epoch ID must be positive") }
		if _, err := address(a.Resolver); err != nil { return nil, fmt.Errorf("resolver: %w", err) }
		if _, err := address(a.Delegator); err != nil { return nil, fmt.Errorf("delegator: %w", err) }
		if a.Amount == nil || a.Amount.Sign() <= 0 { return nil, fmt.Errorf("amount must be positive") }
		key := strings.ToLower(a.Delegator)
		if seen[key] {
			return nil, fmt.Errorf("duplicate delegator: %s", a.Delegator)
		}
		leaf, err := Leaf(a.ChainID, a.Distributor, a.EpochID, a.Delegator, a.Amount)
		if err != nil {
			return nil, err
		}
		seen[key] = true
		leafByDelegator[key] = leaf
		t.leaves = append(t.leaves, leaf)
		t.amounts[key] = new(big.Int).Set(a.Amount)
	}
	// Canonical leaf order makes the root independent of database query order.
	sort.Slice(t.leaves, func(i, j int) bool { return bytes.Compare(t.leaves[i][:], t.leaves[j][:]) < 0 })
	index := make(map[[32]byte]int, len(t.leaves))
	for i, leaf := range t.leaves {
		index[leaf] = i
	}
	levels := [][][32]byte{append([][32]byte(nil), t.leaves...)}
	for len(levels[len(levels)-1]) > 1 {
		current := levels[len(levels)-1]
		next := make([][32]byte, 0, (len(current)+1)/2)
		for i := 0; i < len(current); i += 2 {
			if i+1 == len(current) {
				next = append(next, current[i])
			} else {
				next = append(next, hashPair(current[i], current[i+1]))
			}
		}
		levels = append(levels, next)
	}
	t.root = levels[len(levels)-1][0]
	for key, leaf := range leafByDelegator {
		t.proofs[key] = proof(levels, index[leaf])
	}
	return t, nil
}

func (t *Tree) Root() [32]byte  { return t.root }
func (t *Tree) RootHex() string { return "0x" + hex.EncodeToString(t.root[:]) }
func (t *Tree) Proof(delegator string) ([][32]byte, *big.Int, error) {
	if t == nil { return nil, nil, fmt.Errorf("tree is nil") }
	key := strings.ToLower(delegator)
	p, ok := t.proofs[key]
	if !ok {
		return nil, nil, fmt.Errorf("delegator not found: %s", delegator)
	}
	copyProof := append([][32]byte(nil), p...)
	return copyProof, new(big.Int).Set(t.amounts[key]), nil
}

func Verify(root [32]byte, chainID *big.Int, distributor string, epochID *big.Int, delegator string, amount *big.Int, proof [][32]byte) (bool, error) {
	if root == ([32]byte{}) { return false, fmt.Errorf("root cannot be zero") }
	if chainID == nil || chainID.Sign() <= 0 || epochID == nil || epochID.Sign() <= 0 { return false, fmt.Errorf("chain ID and epoch ID must be positive") }
	if amount == nil || amount.Sign() <= 0 { return false, fmt.Errorf("amount must be positive") }
	if len(proof) > 256 { return false, fmt.Errorf("proof is too long") }
	leaf, err := Leaf(chainID, distributor, epochID, delegator, amount)
	if err != nil {
		return false, err
	}
	for _, sibling := range proof {
		leaf = hashPair(leaf, sibling)
	}
	return leaf == root, nil
}

func proof(levels [][][32]byte, pos int) [][32]byte {
	out := [][32]byte{}
	for _, level := range levels[:len(levels)-1] {
		sibling := pos ^ 1
		if sibling < len(level) {
			out = append(out, level[sibling])
		}
		pos /= 2
	}
	return out
}
func hashPair(a, b [32]byte) [32]byte {
	if bytes.Compare(a[:], b[:]) > 0 {
		a, b = b, a
	}
	h := sha3.NewLegacyKeccak256()
	h.Write(a[:])
	h.Write(b[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}
func uint256(v *big.Int) ([32]byte, error) {
	var out [32]byte
	if v == nil || v.Sign() < 0 || v.BitLen() > 256 {
		return out, fmt.Errorf("must be an unsigned 256-bit integer")
	}
	v.FillBytes(out[:])
	return out, nil
}
func address(value string) ([20]byte, error) {
	var out [20]byte
	raw := strings.TrimPrefix(strings.ToLower(value), "0x")
	if len(raw) != 40 {
		return out, fmt.Errorf("must be 20 bytes")
	}
	b, err := hex.DecodeString(raw)
	if err != nil {
		return out, err
	}
	copy(out[:], b)
	return out, nil
}
