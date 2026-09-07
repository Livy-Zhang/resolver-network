package httpapi
// API server for the resolver network
import (
	"context"
	"encoding/json"
	"expvar"
	"log/slog"
	"net/http"
	"resolver-network/internal/database"
	"time"
)

var apiRequests = expvar.NewInt("api_requests_total")
var apiErrors = expvar.NewInt("api_errors_total")
var apiDuration = expvar.NewInt("api_request_duration_ms_total")
var apiStatuses = expvar.NewMap("api_responses_total")

type Options struct { QueryTimeout, ReadinessTimeout time.Duration }

type Store interface {
	MonthlyRewardDetails(context.Context, time.Time) ([]database.MonthlyRewardDetail, error)
	MerkleProofSubmissions(context.Context, time.Time) ([]database.MerkleProofSubmission, error)
}

type Readiness interface{ Ready(context.Context) error }

func New(store Store) http.Handler { return NewWithOptions(store, Options{}) }
func NewWithOptions(store Store, options Options) http.Handler {
	if options.QueryTimeout <= 0 { options.QueryTimeout = 10*time.Second }
	if options.ReadinessTimeout <= 0 { options.ReadinessTimeout = 3*time.Second }
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", expvar.Handler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	if ready, ok := store.(Readiness); ok {
		mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), options.ReadinessTimeout)
			defer cancel()
			if err := ready.Ready(ctx); err != nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
		})
	} else {
		mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
		})
	}
	mux.HandleFunc("GET /v1/monthly-rewards-details", func(w http.ResponseWriter, r *http.Request) {
		if store == nil { writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error":"service unavailable"}); return }
		monthHandler(options.QueryTimeout, func(ctx context.Context, month time.Time) (any, error) { return store.MonthlyRewardDetails(ctx, month) })(w, r)
	})
	mux.HandleFunc("GET /v1/merkle-proof-submission", func(w http.ResponseWriter, r *http.Request) {
		if store == nil { writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error":"service unavailable"}); return }
		monthHandler(options.QueryTimeout, func(ctx context.Context, month time.Time) (any, error) {
			return store.MerkleProofSubmissions(ctx, month)
		})(w, r)
	})
	return mux
}

func monthHandler(timeout time.Duration, query func(context.Context, time.Time) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		apiRequests.Add(1)
		month, err := time.Parse("200601", r.URL.Query().Get("month"))
		if err != nil || month.Day() != 1 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "month must use YYYYMM"})
			return
		}
		started := time.Now()
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		result, err := query(ctx, month.UTC())
		apiDuration.Add(time.Since(started).Milliseconds())
		if err != nil {
			apiErrors.Add(1); apiStatuses.Add("500", 1); slog.Error("api query failed", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "database query failed"})
			return
		}
		apiStatuses.Add("200", 1)
		writeJSON(w, http.StatusOK, map[string]any{"month": month.Format("200601"), "items": result})
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
