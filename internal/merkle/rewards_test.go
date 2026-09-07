package merkle

import (
	"math/big"
	"testing"
)

func TestBuildIsOrderIndependentAndProofRecreatesRoot(t *testing.T) {
	base := []Allocation{
		{Resolver: "0x0000000000000000000000000000000000000001", ChainID: big.NewInt(11155111), Distributor: "0x0000000000000000000000000000000000000002", EpochID: big.NewInt(202608), Delegator: "0x0000000000000000000000000000000000000003", Amount: big.NewInt(10)},
		{Resolver: "0x0000000000000000000000000000000000000001", ChainID: big.NewInt(11155111), Distributor: "0x0000000000000000000000000000000000000002", EpochID: big.NewInt(202608), Delegator: "0x0000000000000000000000000000000000000004", Amount: big.NewInt(20)},
	}
	a, err := Build(base)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Build([]Allocation{base[1], base[0]})
	if err != nil {
		t.Fatal(err)
	}
	if a.Root() != b.Root() {
		t.Fatal("root depends on input order")
	}
	p, amount, err := a.Proof(base[0].Delegator)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := Verify(a.Root(), base[0].ChainID, base[0].Distributor, base[0].EpochID, base[0].Delegator, amount, p)
	if err != nil || !ok {
		t.Fatal("proof does not recreate root")
	}
}

func TestBuildRejectsDuplicateDelegator(t *testing.T) {
	a := Allocation{Resolver: "0x0000000000000000000000000000000000000001", ChainID: big.NewInt(1), Distributor: "0x0000000000000000000000000000000000000002", EpochID: big.NewInt(1), Delegator: "0x0000000000000000000000000000000000000003", Amount: big.NewInt(1)}
	if _, err := Build([]Allocation{a, a}); err == nil {
		t.Fatal("expected duplicate error")
	}
}

func validAllocation() Allocation { return Allocation{Resolver: "0x0000000000000000000000000000000000000001", ChainID: big.NewInt(1), Distributor: "0x0000000000000000000000000000000000000002", EpochID: big.NewInt(1), Delegator: "0x0000000000000000000000000000000000000003", Amount: big.NewInt(1)} }

func TestBuildSupportsSingleAndOddLeaves(t *testing.T) {
	a := validAllocation(); tree, err := Build([]Allocation{a}); if err != nil { t.Fatal(err) }
	p, amount, err := tree.Proof(a.Delegator); if err != nil { t.Fatal(err) }
	ok, err := Verify(tree.Root(), a.ChainID, a.Distributor, a.EpochID, a.Delegator, amount, p); if err != nil || !ok { t.Fatalf("single proof valid=%v err=%v", ok, err) }
	b, c := a, a; b.Delegator = "0x0000000000000000000000000000000000000004"; c.Delegator = "0x0000000000000000000000000000000000000005"
	if _, err = Build([]Allocation{a, b, c}); err != nil { t.Fatal(err) }
}

func TestBuildRejectsInvalidAllocations(t *testing.T) {
	base := validAllocation()
	cases := []Allocation{base, base, base, base, base}
	cases[0].Amount = big.NewInt(0); if _, err := Build(cases[:1]); err == nil { t.Fatal("expected zero amount error") }
	cases[0] = base; cases[0].Resolver = "bad"; if _, err := Build(cases[:1]); err == nil { t.Fatal("expected resolver error") }
	cases[0] = base; cases[0].ChainID = big.NewInt(0); if _, err := Build(cases[:1]); err == nil { t.Fatal("expected chain id error") }
	cases[0] = base; cases[0].Amount = new(big.Int).Lsh(big.NewInt(1), 256); if _, err := Build(cases[:1]); err == nil { t.Fatal("expected uint256 overflow error") }
	if _, err := Build(nil); err == nil { t.Fatal("expected empty allocation error") }
}

func TestProofAndVerifyRejectTampering(t *testing.T) {
	a := validAllocation(); b := a; b.Delegator = "0x0000000000000000000000000000000000000004"; b.Amount = big.NewInt(2)
	tree, err := Build([]Allocation{a, b}); if err != nil { t.Fatal(err) }
	p, amount, err := tree.Proof(a.Delegator); if err != nil { t.Fatal(err) }
	if ok, _ := Verify(tree.Root(), a.ChainID, a.Distributor, a.EpochID, a.Delegator, new(big.Int).Add(amount, big.NewInt(1)), p); ok { t.Fatal("tampered amount verified") }
	if ok, _ := Verify(tree.Root(), a.ChainID, a.Distributor, a.EpochID, a.Delegator, amount, append(p, [32]byte{1})); ok { t.Fatal("tampered proof verified") }
	if _, _, err := (*Tree)(nil).Proof(a.Delegator); err == nil { t.Fatal("expected nil tree error") }
	if ok, err := Verify([32]byte{}, a.ChainID, a.Distributor, a.EpochID, a.Delegator, amount, p); err == nil || ok { t.Fatal("expected zero root error") }
}

func TestProofReturnsDefensiveCopies(t *testing.T) {
	a := validAllocation(); tree, err := Build([]Allocation{a}); if err != nil { t.Fatal(err) }
	p, amount, err := tree.Proof(a.Delegator); if err != nil { t.Fatal(err) }; if len(p) > 0 { p[0] = [32]byte{1} }; amount.SetInt64(999)
	p2, amount2, err := tree.Proof(a.Delegator); if err != nil { t.Fatal(err) }; if len(p2) > 0 && p2[0] == ([32]byte{1}) || amount2.Cmp(big.NewInt(1)) != 0 { t.Fatal("tree data was mutated") }
}
