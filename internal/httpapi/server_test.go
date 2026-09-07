package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"resolver-network/internal/database"
	"strings"
	"testing"
	"time"
)

type fakeStore struct{}

func (fakeStore) Ready(context.Context) error { return nil }

func (fakeStore) MonthlyRewardDetails(context.Context, time.Time) ([]database.MonthlyRewardDetail, error) {
	return []database.MonthlyRewardDetail{{Resolver: "0xresolver", RewardAmount: "10"}}, nil
}
func (fakeStore) MerkleProofSubmissions(context.Context, time.Time) ([]database.MerkleProofSubmission, error) {
	return []database.MerkleProofSubmission{{Resolver: "0xresolver", Status: "confirmed"}}, nil
}

func TestEndpoints(t *testing.T) {
	h := New(fakeStore{})
	for _, tc := range []struct {
		path string
		want int
	}{{"/healthz", 200}, {"/readyz", 200}, {"/v1/monthly-rewards-details?month=202608", 200}, {"/v1/merkle-proof-submission?month=202608", 200}, {"/v1/monthly-rewards-details?month=2026-08", 400}} {
		r := httptest.NewRequest("GET", tc.path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%s: status=%d want %d", tc.path, w.Code, tc.want)
		}
	}
}

func TestAPIResponseHeadersAndJSON(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/v1/monthly-rewards-details?month=202608", nil)
	w := httptest.NewRecorder(); New(fakeStore{}).ServeHTTP(w, r)
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/json" || !strings.HasSuffix(w.Body.String(), "\n") { t.Fatalf("status=%d content-type=%q body=%q", w.Code, w.Header().Get("Content-Type"), w.Body.String()) }
	var body struct { Month string `json:"month"`; Items []database.MonthlyRewardDetail `json:"items"` }
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Month != "202608" || len(body.Items) != 1 { t.Fatalf("body=%s err=%v", w.Body.String(), err) }
}

func TestAPIMethodNotAllowed(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/healthz", nil); w := httptest.NewRecorder(); New(fakeStore{}).ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed { t.Fatalf("status=%d", w.Code) }
}

func TestMetricsEndpointExposesRegisteredMetrics(t *testing.T) {
	w := httptest.NewRecorder()
	New(fakeStore{}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if w.Code != http.StatusOK { t.Fatalf("status=%d", w.Code) }
	body := w.Body.String()
	for _, name := range []string{"api_requests_total", "api_errors_total", "api_request_duration_ms_total", "api_responses_total"} {
		if !strings.Contains(body, name) { t.Fatalf("metrics response missing %q: %s", name, body) }
	}
}

type unavailableStore struct{ fakeStore }

func (unavailableStore) Ready(context.Context) error { return errors.New("database unavailable") }

func TestReadyzReturnsUnavailable(t *testing.T) {
	r := httptest.NewRequest("GET", "/readyz", nil)
	w := httptest.NewRecorder()
	New(unavailableStore{}).ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatalf("status=%d want 503", w.Code)
	}
}

type queryFailureStore struct{ fakeStore }

func (queryFailureStore) MonthlyRewardDetails(context.Context, time.Time) ([]database.MonthlyRewardDetail, error) {
	return nil, errors.New("connection refused")
}
func TestAPIQueryDatabaseDisconnect(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/monthly-rewards-details?month=202608", nil)
	w := httptest.NewRecorder()
	New(queryFailureStore{}).ServeHTTP(w, r)
	if w.Code != 500 {
		t.Fatalf("status=%d want 500", w.Code)
	}
}
