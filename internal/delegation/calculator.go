package delegation
// calculate delegation weights for resolvers and delegators over a given time period
// based on a series of delegation events
import (
	"fmt"
	"math/big"
	"sort"
	"time"
)

type Kind string

const (
	Delegated   Kind = "delegated"
	Undelegated Kind = "undelegated"
	Redelegated Kind = "redelegated"
)

type Event struct {
	Kind                                              Kind
	ID, Delegator, Resolver, OldResolver, NewResolver string
	Amount                                            *big.Int
	Timestamp                                         time.Time
	BlockNumber, LogIndex                             int64
}
type Key struct{ Resolver, Delegator string }
type State map[Key]*big.Int
type Weight struct {
	MonthStart          time.Time
	Resolver, Delegator string
	Value               *big.Int
}

func Copy(s State) State {
	r := make(State, len(s))
	for k, v := range s {
		if v != nil {
			r[k] = new(big.Int).Set(v)
		}
	}
	return r
}
func Replay(s State, es []Event) error {
	s = Copy(s)
	for _, e := range ordered(es) {
		if err := apply(s, e); err != nil {
			return fmt.Errorf("event %s: %w", e.ID, err)
		}
	}
	return nil
}
func Calculate(opening State, es []Event, start, end time.Time) ([]Weight, State, error) {
	if !start.Before(end) {
		return nil, nil, fmt.Errorf("invalid month range")
	}
	s := Copy(opening)
	totals := map[Key]*big.Int{}
	orderedEvents := ordered(es)
	last := start
	for _, e := range orderedEvents {
		if e.Timestamp.Before(start) || !e.Timestamp.Before(end) {
			return nil, nil, fmt.Errorf("event outside range: %s", e.ID)
		}
		addTime(totals, s, e.Timestamp.Sub(last))
		if err := apply(s, e); err != nil {
			return nil, nil, err
		}
		last = e.Timestamp
	}
	addTime(totals, s, end.Sub(last))
	ws := []Weight{}
	for k, v := range totals {
		if v.Sign() > 0 {
			ws = append(ws, Weight{start, k.Resolver, k.Delegator, v})
		}
	}
	sort.Slice(ws, func(i, j int) bool {
		if ws[i].Resolver == ws[j].Resolver {
			return ws[i].Delegator < ws[j].Delegator
		}
		return ws[i].Resolver < ws[j].Resolver
	})
	return ws, s, nil
}
func addTime(t map[Key]*big.Int, s State, d time.Duration) {
	sec := int64(d / time.Second)
	if sec <= 0 {
		return
	}
	for k, a := range s {
		if a.Sign() <= 0 {
			continue
		}
		v := new(big.Int).Mul(a, big.NewInt(sec))
		if t[k] == nil {
			t[k] = v
		} else {
			t[k].Add(t[k], v)
		}
	}
}
func apply(s State, e Event) error {
	if e.Amount == nil || e.Amount.Sign() < 0 {
		return fmt.Errorf("invalid amount")
	}
	switch e.Kind {
	case Delegated:
		plus(s, Key{e.Resolver, e.Delegator}, e.Amount)
	case Undelegated:
		return minus(s, Key{e.Resolver, e.Delegator}, e.Amount)
	case Redelegated:
		if err := minus(s, Key{e.OldResolver, e.Delegator}, e.Amount); err != nil {
			return err
		}
		plus(s, Key{e.NewResolver, e.Delegator}, e.Amount)
	default:
		return fmt.Errorf("unknown event type")
	}
	return nil
}
func plus(s State, k Key, a *big.Int) {
	if s[k] == nil {
		s[k] = new(big.Int)
	}
	s[k].Add(s[k], a)
}
func minus(s State, k Key, a *big.Int) error {
	if s[k] == nil || s[k].Cmp(a) < 0 {
		return fmt.Errorf("insufficient delegation for %s/%s", k.Resolver, k.Delegator)
	}
	s[k].Sub(s[k], a)
	if s[k].Sign() == 0 {
		delete(s, k)
	}
	return nil
}
func ordered(es []Event) []Event {
	result := append([]Event(nil), es...)
	sort.SliceStable(result, func(i, j int) bool {
		if !result[i].Timestamp.Equal(result[j].Timestamp) {
			return result[i].Timestamp.Before(result[j].Timestamp)
		}
		if result[i].BlockNumber != result[j].BlockNumber {
			return result[i].BlockNumber < result[j].BlockNumber
		}
		return result[i].LogIndex < result[j].LogIndex
	})
	return result
}
