package metrics

import "expvar"

var (
	SettlementsStarted   = expvar.NewInt("settlements_started_total")
	SettlementsSucceeded = expvar.NewInt("settlements_succeeded_total")
	SettlementsFailed    = expvar.NewInt("settlements_failed_total")
	RootSubmitted        = expvar.NewInt("root_submitted_total")
	RootConfirmed        = expvar.NewInt("root_confirmed_total")
	RootFailed           = expvar.NewInt("root_failed_total")
	RootReplacements     = expvar.NewInt("root_replacements_total")
	MerkleStarted        = expvar.NewInt("merkle_started_total")
	MerkleSucceeded      = expvar.NewInt("merkle_succeeded_total")
	MerkleFailed         = expvar.NewInt("merkle_failed_total")
	RootReceiptChecks    = expvar.NewInt("root_receipt_checks_total")
	RootRetryableFailures = expvar.NewInt("root_retryable_failures_total")
)
