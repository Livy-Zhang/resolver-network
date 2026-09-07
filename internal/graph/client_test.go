package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"resolver-network/internal/delegation"
)

func TestMetaAndEvents(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(body.Query, "query Meta") {
			_, _ = w.Write([]byte(`{"data":{"_meta":{"block":{"number":"123"},"hasIndexingErrors":false}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"delegatedEvents":[{"id":"a","delegator":"d","resolver":"r","amount":"10","timestamp":"100","blockNumber":"1","logIndex":"0"}],"undelegatedEvents":[],"redelegatedEvents":[]}}`))
	}))
	defer s.Close()
	c := New(s.URL)
	m, err := c.Meta(context.Background())
	if err != nil || m.BlockNumber != 123 {
		t.Fatalf("meta=%+v err=%v", m, err)
	}
	e, err := c.Events(context.Background(), nil, time.Unix(200, 0), 123)
	if err != nil || len(e) != 1 || e[0].Amount.String() != "10" {
		t.Fatalf("events=%#v err=%v", e, err)
	}
}

func TestMetaRejectsIndexingErrors(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"_meta":{"block":{"number":"1"},"hasIndexingErrors":true}}}`))
	}))
	defer s.Close()
	if _, err := New(s.URL).Meta(context.Background()); err == nil {
		t.Fatal("expected indexing error")
	}
}

func TestDelegationsUsesHistoricalBlockAndIDCursor(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Variables["block"] != float64(88) {
			t.Fatalf("block variable = %#v", request.Variables["block"])
		}
		calls++
		rows := make([]map[string]any, 0)
		switch request.Variables["after"] {
		case "":
			for i := 0; i < size; i++ {
				rows = append(rows, map[string]any{"id": fmt.Sprintf("%04d", i), "delegator": "d", "amount": "2", "resolver": map[string]string{"id": "r"}})
			}
		case "0999":
			rows = append(rows, map[string]any{"id": "1000", "delegator": "e", "amount": "3", "resolver": map[string]string{"id": "s"}})
		default:
			t.Fatalf("unexpected cursor %#v", request.Variables["after"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"delegations": rows}})
	}))
	defer s.Close()
	delegations, err := New(s.URL).Delegations(context.Background(), 88)
	if err != nil || calls != 2 || len(delegations) != size+1 || delegations[size].Amount.String() != "3" {
		t.Fatalf("delegations=%d calls=%d err=%v", len(delegations), calls, err)
	}
}

func TestEventsUsesIDCursorAndFixedBlock(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Variables["block"] != float64(777) {
			t.Fatalf("block variable = %#v", request.Variables["block"])
		}
		calls++
		rows := make([]map[string]string, 0)
		switch request.Variables["delegatedAfter"] {
		case "":
			for i := 0; i < size; i++ {
				rows = append(rows, map[string]string{"id": fmt.Sprintf("%04d", i), "delegator": "d", "resolver": "r", "amount": "1", "timestamp": "1", "blockNumber": "1", "logIndex": "0"})
			}
		case "0999":
			rows = append(rows, map[string]string{"id": "1000", "delegator": "d", "resolver": "r", "amount": "1", "timestamp": "1", "blockNumber": "1", "logIndex": "0"})
		default:
			t.Fatalf("unexpected cursor %#v", request.Variables["delegatedAfter"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"delegatedEvents": rows, "undelegatedEvents": []any{}, "redelegatedEvents": []any{}}})
	}))
	defer s.Close()
	e, err := New(s.URL).Events(context.Background(), nil, time.Unix(2, 0), 777)
	if err != nil || len(e) != size+1 || calls != 2 {
		t.Fatalf("events=%d calls=%d err=%v", len(e), calls, err)
	}
}

func TestEventsDoesNotDuplicateShortEventKindsWhilePaginatingAnotherKind(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		delegated := make([]map[string]string, 0)
		if request.Variables["delegatedAfter"] == "" {
			for i := 0; i < size; i++ {
				delegated = append(delegated, map[string]string{"id": fmt.Sprintf("%04d", i), "delegator": "d", "resolver": "r", "amount": "1", "timestamp": "1", "blockNumber": "1", "logIndex": "0"})
			}
		} else {
			delegated = append(delegated, map[string]string{"id": "1000", "delegator": "d", "resolver": "r", "amount": "1", "timestamp": "1", "blockNumber": "1", "logIndex": "0"})
		}
		undelegated := []map[string]string{{"id": "u", "delegator": "d", "resolver": "r", "amount": "1", "timestamp": "1", "blockNumber": "1", "logIndex": "1"}}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"delegatedEvents": delegated, "undelegatedEvents": undelegated, "redelegatedEvents": []any{}}})
	}))
	defer s.Close()
	events, err := New(s.URL).Events(context.Background(), nil, time.Unix(2, 0), 1)
	if err != nil || len(events) != size+2 {
		t.Fatalf("events=%d, err=%v", len(events), err)
	}
	undelegated := 0
	for _, event := range events {
		if event.Kind == delegation.Undelegated {
			undelegated++
		}
	}
	if undelegated != 1 {
		t.Fatalf("undelegated events=%d, want 1", undelegated)
	}
}

func TestEventsRejectsInvalidRange(t *testing.T) {
	start := time.Unix(100, 0)
	if _, err := New("https://example.invalid").Events(context.Background(), &start, start, 1); err == nil {
		t.Fatal("expected invalid range error")
	}
	if _, err := New("https://example.invalid").RewardRateEvents(context.Background(), start, start, 1); err == nil {
		t.Fatal("expected invalid range error")
	}
}

func TestEventsRejectsStalledCursor(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows := make([]map[string]string, size)
		for i := range rows {
			rows[i] = map[string]string{"id": "same", "delegator": "d", "resolver": "r", "amount": "1", "timestamp": "1", "blockNumber": "1", "logIndex": "0"}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"delegatedEvents": rows, "undelegatedEvents": []any{}, "redelegatedEvents": []any{}}})
	}))
	defer s.Close()
	if _, err := New(s.URL).Events(context.Background(), nil, time.Unix(2, 0), 1); err == nil {
		t.Fatal("expected stalled cursor error")
	}
}

func TestConvertRejectsMalformedEventFields(t *testing.T) {
	cases := []result{
		{Data: struct { D []row `json:"delegatedEvents"`; U []row `json:"undelegatedEvents"`; R []row `json:"redelegatedEvents"` }{D: []row{{ID: "", Delegator: "d", Resolver: "r", Amount: "1", Timestamp: "1", BlockNumber: "1", LogIndex: "0"}}}},
		{Data: struct { D []row `json:"delegatedEvents"`; U []row `json:"undelegatedEvents"`; R []row `json:"redelegatedEvents"` }{D: []row{{ID: "x", Delegator: "d", Resolver: "r", Amount: "-1", Timestamp: "1", BlockNumber: "1", LogIndex: "0"}}}},
	}
	for _, input := range cases { if _, err := convert(input); err == nil { t.Fatal("expected malformed event error") } }
	badRedelegation := result{Data: struct { D []row `json:"delegatedEvents"`; U []row `json:"undelegatedEvents"`; R []row `json:"redelegatedEvents"` }{R: []row{{ID: "r", Delegator: "d", Amount: "1", Timestamp: "1", BlockNumber: "1", LogIndex: "0"}}}}
	if _, err := convert(badRedelegation); err == nil { t.Fatal("expected redelegation resolver error") }
}

func TestNewWithOptionsDefaultsAndLimits(t *testing.T) {
	c := NewWithOptions("", Options{Timeout: time.Second, PageSize: 2, MaxPages: 3, MaxEvents: 4})
	if c.pageSize != 2 || c.maxPages != 3 || c.maxEvents != 4 || c.http == nil { t.Fatalf("unexpected options: %+v", c) }
}
