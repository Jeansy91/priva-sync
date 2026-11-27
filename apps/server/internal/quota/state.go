package quota

// QuotaState is the user's storage-usage band. Replaces the prior pair of
// boolean flags (quota_warned_80, quota_warned_95) with a single sum-type so
// transitions can be exhaustively checked.
type QuotaState string

const (
	QuotaStateOK         QuotaState = "ok"
	QuotaStateWarning80  QuotaState = "warning80"
	QuotaStateCritical95 QuotaState = "critical95"
)

// transitionQuotaState computes the next state given the current state and
// the current usage percentage. Hysteresis: once in warning80/critical95 the
// state only resets when usage drops back below the corresponding threshold.
func transitionQuotaState(_ QuotaState, usedPct float64) QuotaState {
	switch {
	case usedPct >= 95:
		return QuotaStateCritical95
	case usedPct >= 80:
		return QuotaStateWarning80
	default:
		return QuotaStateOK
	}
}

// isEscalation reports whether the band moved upward (more usage). Only
// escalations trigger a warning mail — de-escalations stay silent.
func isEscalation(old, next QuotaState) bool {
	return stateRank(next) > stateRank(old)
}

func stateRank(s QuotaState) int {
	switch s {
	case QuotaStateWarning80:
		return 1
	case QuotaStateCritical95:
		return 2
	default:
		return 0
	}
}
