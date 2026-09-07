package workflow

// Status defines the stages of the monthly settlement workflow and provides a function to validate transitions between them
import "fmt"

type Stage string

const (
	SettlementPending Stage = "settlement_pending"
	SettlementDone    Stage = "settled"
	MerklePending     Stage = "merkle_pending"
	MerkleProcessing  Stage = "merkle_processing"
	RootPending       Stage = "root_pending"
	RootProcessing    Stage = "root_processing"
	Confirmed         Stage = "confirmed"
)

func CanTransition(from, to Stage) bool {
	if from == to {
		return true
	}
	switch from {
	case SettlementPending:
		return to == SettlementDone
	case SettlementDone:
		return to == MerklePending
	case MerklePending:
		return to == MerkleProcessing
	case MerkleProcessing:
		return to == RootPending
	case RootPending:
		return to == RootProcessing
	case RootProcessing:
		return to == Confirmed
	default:
		return false
	}
}

func Transition(from, to Stage) error {
	if !CanTransition(from, to) {
		return fmt.Errorf("invalid workflow transition %q -> %q", from, to)
	}
	return nil
}
