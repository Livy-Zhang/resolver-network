package workflow

import "testing"

func TestLinearStages(t *testing.T) {
	stages := []Stage{SettlementPending, SettlementDone, MerklePending, MerkleProcessing, RootPending, RootProcessing, Confirmed}
	for i := 0; i < len(stages)-1; i++ {
		if err := Transition(stages[i], stages[i+1]); err != nil {
			t.Fatal(err)
		}
	}
	if Transition(SettlementPending, RootPending) == nil {
		t.Fatal("skipped stage must be rejected")
	}
}
