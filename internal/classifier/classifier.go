package classifier

import (
	"retry-guardian/internal/rules"
	"time"
)

// Result is the output of classifying a single declined authorization.
type Result struct {
	Class    rules.RetryClass
	Reason   string
	Cooldown time.Duration // zero means no cooldown applies
}

// Classify determines the retry class for a declined authorization.
//
// Precedence order:
//  1. If the network is MASTERCARD and a non-empty MAC is present → MAC rule wins.
//     If the MAC is unrecognised, fall through to the network code rule.
//  2. Network response code rule (specific network first, then ANY wildcard).
//  3. No match → PassThrough (unknown code, no restriction imposed).
func Classify(network, responseCode, mac string, table *rules.Table) Result {
	if network == "MASTERCARD" && mac != "" {
		if r, ok := table.LookupMAC(mac); ok {
			return Result{Class: r.Class, Reason: r.Reason, Cooldown: r.Cooldown}
		}
	}

	if r, ok := table.LookupNetworkCode(network, responseCode); ok {
		return Result{Class: r.Class, Reason: r.Reason, Cooldown: r.Cooldown}
	}

	return Result{Class: rules.PassThrough, Reason: "UNKNOWN_CODE"}
}
