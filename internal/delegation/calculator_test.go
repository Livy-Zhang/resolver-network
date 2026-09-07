package delegation

import (
	"math/big"
	"testing"
	"time"
)

func TestCalculate(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	k := Key{"r", "d"}
	w, s, e := Calculate(State{k: big.NewInt(100)}, []Event{{Kind: Delegated, ID: "x", Resolver: "r", Delegator: "d", Amount: big.NewInt(200), Timestamp: start.Add(10 * 24 * time.Hour)}}, start, end)
	if e != nil {
		t.Fatal(e)
	}
	want := new(big.Int).Add(new(big.Int).Mul(big.NewInt(100), big.NewInt(864000)), new(big.Int).Mul(big.NewInt(300), big.NewInt(1814400)))
	if len(w) != 1 || w[0].Value.Cmp(want) != 0 || s[k].Cmp(big.NewInt(300)) != 0 {
		t.Fatal("unexpected result")
	}
}

func TestReplayRejectsInvalidUndelegation(t *testing.T) {
	err := Replay(State{}, []Event{{Kind: Undelegated, ID: "bad", Resolver: "r", Delegator: "d", Amount: big.NewInt(1)}})
	if err == nil {
		t.Fatal("expected insufficient delegation error")
	}
}

func TestCalculateRejectsEventsOutsideMonth(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	_, _, err := Calculate(State{}, []Event{{Kind: Delegated, ID: "late", Resolver: "r", Delegator: "d", Amount: big.NewInt(1), Timestamp: start.AddDate(0, 1, 0)}}, start, start.AddDate(0, 1, 0))
	if err == nil {
		t.Fatal("expected outside range error")
	}
}

func TestCalculateHandlesPartialUndelegationAndRedelegation(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	es := []Event{
		{Kind: Delegated, ID: "d", Resolver: "r1", Delegator: "u", Amount: big.NewInt(100), Timestamp: start},
		{Kind: Undelegated, ID: "u", Resolver: "r1", Delegator: "u", Amount: big.NewInt(40), Timestamp: start.Add(10 * time.Second)},
		{Kind: Redelegated, ID: "r", OldResolver: "r1", NewResolver: "r2", Delegator: "u", Amount: big.NewInt(20), Timestamp: start.Add(20 * time.Second)},
	}
	weights, state, err := Calculate(State{}, es, start, start.Add(30*time.Second))
	if err != nil { t.Fatal(err) }
	got := map[Key]*big.Int{}
	for _, w := range weights { got[Key{w.Resolver, w.Delegator}] = w.Value }
	if got[Key{"r1", "u"}].Cmp(big.NewInt(2000)) != 0 || got[Key{"r2", "u"}].Cmp(big.NewInt(200)) != 0 { t.Fatalf("weights=%v", got) }
	if state[Key{"r1", "u"}].Cmp(big.NewInt(40)) != 0 || state[Key{"r2", "u"}].Cmp(big.NewInt(20)) != 0 { t.Fatalf("state=%v", state) }
}

func TestCalculateRejectsNegativeAmountAndInvalidRange(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	if _, _, err := Calculate(State{}, []Event{{Kind: Delegated, Amount: big.NewInt(-1), Timestamp: start}}, start, start.AddDate(0, 1, 0)); err == nil { t.Fatal("expected negative amount error") }
	if _, _, err := Calculate(State{}, nil, start, start); err == nil { t.Fatal("expected invalid range error") }
}

func TestCalculateDoesNotMutateEventOrder(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	es := []Event{
		{Kind: Delegated, ID: "late", Resolver: "r", Delegator: "d", Amount: big.NewInt(1), Timestamp: start.Add(time.Hour), BlockNumber: 2},
		{Kind: Delegated, ID: "early", Resolver: "r", Delegator: "d", Amount: big.NewInt(1), Timestamp: start, BlockNumber: 1},
	}
	if _, _, err := Calculate(State{}, es, start, start.Add(2*time.Hour)); err != nil { t.Fatal(err) }
	if es[0].ID != "late" || es[1].ID != "early" { t.Fatalf("input events were reordered: %+v", es) }
}

func TestReplayRejectsUnknownKindAndCopiesNilValues(t *testing.T) {
	if err := Replay(State{}, []Event{{Kind: Kind("unknown"), ID: "x", Amount: big.NewInt(1)}}); err == nil { t.Fatal("expected unknown event error") }
	if copied := Copy(State{Key{"r", "d"}: nil}); copied[Key{"r", "d"}] != nil { t.Fatal("nil state value should remain nil") }
}

func TestZeroAmountEventIsANoop(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	opening := State{Key{"r", "d"}: big.NewInt(5)}
	weights, state, err := Calculate(opening, []Event{{Kind: Delegated, ID: "zero", Resolver: "r", Delegator: "d", Amount: big.NewInt(0), Timestamp: start}}, start, start.Add(time.Second))
	if err != nil { t.Fatal(err) }
	if len(weights) != 1 || weights[0].Value.Cmp(big.NewInt(5)) != 0 || state[Key{"r", "d"}].Cmp(big.NewInt(5)) != 0 { t.Fatalf("weights=%v state=%v", weights, state) }
}

func TestSameTimestampUsesBlockAndLogIndexOrder(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	events := []Event{
		{Kind: Undelegated, ID: "log2", Resolver: "r", Delegator: "d", Amount: big.NewInt(2), Timestamp: start, BlockNumber: 10, LogIndex: 2},
		{Kind: Delegated, ID: "log1", Resolver: "r", Delegator: "d", Amount: big.NewInt(3), Timestamp: start, BlockNumber: 10, LogIndex: 1},
	}
	_, state, err := Calculate(State{Key{"r", "d"}: big.NewInt(2)}, events, start, start.Add(time.Second))
	if err != nil { t.Fatal(err) }
	if state[Key{"r", "d"}].Cmp(big.NewInt(3)) != 0 { t.Fatalf("state=%v", state) }
}

func TestIdenticalSortKeysAreStable(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	events := []Event{
		{Kind: Delegated, ID: "first", Resolver: "r", Delegator: "d", Amount: big.NewInt(1), Timestamp: start, BlockNumber: 1, LogIndex: 1},
		{Kind: Undelegated, ID: "second", Resolver: "r", Delegator: "d", Amount: big.NewInt(1), Timestamp: start, BlockNumber: 1, LogIndex: 1},
	}
	if err := Replay(State{}, events); err != nil { t.Fatalf("stable input order should succeed: %v", err) }
	if events[0].ID != "first" || events[1].ID != "second" { t.Fatalf("events changed: %+v", events) }
}

func TestMonthBoundaryAndNanosecondsUseWholeSeconds(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	opening := State{Key{"r", "d"}: big.NewInt(2)}
	events := []Event{
		{Kind: Delegated, ID: "at-start", Resolver: "r", Delegator: "d", Amount: big.NewInt(1), Timestamp: start},
		{Kind: Undelegated, ID: "before-end", Resolver: "r", Delegator: "d", Amount: big.NewInt(1), Timestamp: end.Add(-500 * time.Millisecond)},
	}
	weights, _, err := Calculate(opening, events, start, end)
	if err != nil { t.Fatal(err) }
	// The final half-second is truncated by addTime; the end boundary itself is excluded.
	want := new(big.Int).Mul(big.NewInt(3), big.NewInt(int64(end.Sub(start)/time.Second-1)))
	if len(weights) != 1 || weights[0].Value.Cmp(want) != 0 { t.Fatalf("weight=%v want=%v", weights, want) }
}

func TestCrossResolverDelegators(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	events := []Event{
		{Kind: Delegated, ID: "a", Resolver: "r1", Delegator: "d1", Amount: big.NewInt(2), Timestamp: start},
		{Kind: Delegated, ID: "b", Resolver: "r2", Delegator: "d2", Amount: big.NewInt(3), Timestamp: start},
		{Kind: Redelegated, ID: "c", OldResolver: "r1", NewResolver: "r2", Delegator: "d1", Amount: big.NewInt(1), Timestamp: start.Add(time.Second)},
	}
	weights, _, err := Calculate(State{}, events, start, start.Add(2*time.Second))
	if err != nil { t.Fatal(err) }
	got := map[Key]*big.Int{}
	for _, w := range weights { got[Key{w.Resolver, w.Delegator}] = w.Value }
	for k, want := range map[Key]int64{{"r1", "d1"}: 3, {"r2", "d1"}: 1, {"r2", "d2"}: 6} {
		if got[k] == nil || got[k].Cmp(big.NewInt(want)) != 0 { t.Fatalf("%v=%v want=%d", k, got[k], want) }
	}
}

func TestReplayAndCalculateDoNotMutateOpeningState(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	opening := State{Key{"r", "d"}: big.NewInt(9)}
	original := new(big.Int).Set(opening[Key{"r", "d"}])
	events := []Event{{Kind: Delegated, ID: "x", Resolver: "r", Delegator: "d", Amount: big.NewInt(1), Timestamp: start}}
	if err := Replay(opening, events); err != nil { t.Fatal(err) }
	if _, _, err := Calculate(opening, events, start, start.Add(time.Second)); err != nil { t.Fatal(err) }
	if opening[Key{"r", "d"}].Cmp(original) != 0 { t.Fatalf("opening state mutated: %v", opening) }
}

func TestLargeAmountsAndWeights(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	amount := new(big.Int).Exp(big.NewInt(10), big.NewInt(60), nil)
	weights, _, err := Calculate(State{Key{"r", "d"}: amount}, nil, start, start.AddDate(0, 1, 0))
	if err != nil { t.Fatal(err) }
	want := new(big.Int).Mul(amount, big.NewInt(int64(start.AddDate(0, 1, 0).Sub(start)/time.Second)))
	if len(weights) != 1 || weights[0].Value.Cmp(want) != 0 { t.Fatalf("large weight mismatch") }
}
