package rules

import "time"

type RetryClass string

const (
	HardDecline                      RetryClass = "HARD_DECLINE"
	SchemePenaltyDeclineRetriable    RetryClass = "SCHEME_PENALTY_DECLINE_RETRIABLE"
	SchemeNonPenaltyDeclineRetriable RetryClass = "SCHEME_NON_PENALTY_DECLINE_RETRIABLE"
	PassThrough                      RetryClass = "PASS_THROUGH"
)

type Defaults struct {
	Window      time.Duration
	MaxAttempts int
}

type MACRule struct {
	Class    RetryClass
	Reason   string
	Cooldown time.Duration // zero means no cooldown
}

type NetworkCodeRule struct {
	Class    RetryClass
	Reason   string
	Cooldown time.Duration
}

// Table is the fully resolved, ready-to-query rules state loaded at boot.
type Table struct {
	Version          string
	Defaults         Defaults
	MACRules         map[string]MACRule         // keyed by MAC code string, e.g. "03"
	NetworkCodeIndex map[string]NetworkCodeRule // keyed by "NETWORK:CODE" or "ANY:CODE"
}

// LookupMAC returns the rule for the given Mastercard advice code.
func (t *Table) LookupMAC(mac string) (MACRule, bool) {
	r, ok := t.MACRules[mac]
	return r, ok
}

// LookupNetworkCode returns the rule for (network, code).
// Checks the specific network first, then falls back to the ANY wildcard.
func (t *Table) LookupNetworkCode(network, code string) (NetworkCodeRule, bool) {
	if r, ok := t.NetworkCodeIndex[network+":"+code]; ok {
		return r, true
	}
	if r, ok := t.NetworkCodeIndex["ANY:"+code]; ok {
		return r, true
	}
	return NetworkCodeRule{}, false
}
