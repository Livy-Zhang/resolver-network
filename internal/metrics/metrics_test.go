package metrics

import (
	"expvar"
	"testing"
)

func TestMetricsAreRegistered(t *testing.T) {
	for _, name := range []string{
		"settlements_started_total", "settlements_succeeded_total", "settlements_failed_total",
		"root_submitted_total", "root_confirmed_total", "root_failed_total", "root_replacements_total",
		"merkle_started_total", "merkle_succeeded_total", "merkle_failed_total",
		"root_receipt_checks_total", "root_retryable_failures_total",
	} {
		v := expvar.Get(name)
		if v == nil { t.Fatalf("metric %q is not registered", name) }
		if _, ok := v.(*expvar.Int); !ok { t.Fatalf("metric %q has type %T", name, v) }
	}
}

func TestMetricsIncrement(t *testing.T) {
	before := RootSubmitted.Value()
	RootSubmitted.Add(1)
	if got := RootSubmitted.Value(); got != before+1 { t.Fatalf("root submitted=%d want %d", got, before+1) }
	before = SettlementsStarted.Value()
	SettlementsStarted.Add(1)
	if got := SettlementsStarted.Value(); got != before+1 { t.Fatalf("settlements started=%d want %d", got, before+1) }
}
