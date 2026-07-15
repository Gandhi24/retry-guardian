package classifier_test

import (
	"testing"
	"time"

	"retry-guardian/internal/classifier"
	"retry-guardian/internal/rules"
)

// testTable returns a minimal rules.Table covering all classification branches.
func testTable() *rules.Table {
	return &rules.Table{
		Version: "test",
		MACRules: map[string]rules.MACRule{
			"03": {Class: rules.HardDecline, Reason: "MAC_DO_NOT_RETRY"},
			"02": {Class: rules.SchemePenaltyDeclineRetriable, Reason: "MAC_RETRY_AFTER_72H", Cooldown: 72 * time.Hour},
			"40": {Class: rules.PassThrough, Reason: "MAC_NO_ACTION_REQUIRED"},
		},
		NetworkCodeIndex: map[string]rules.NetworkCodeRule{
			"ANY:41": {Class: rules.HardDecline, Reason: "STOLEN_CARD"},
			"ANY:51": {Class: rules.SchemePenaltyDeclineRetriable, Reason: "INSUFFICIENT_FUNDS", Cooldown: 24 * time.Hour},
			"ANY:65": {
				Class:       rules.SchemePenaltyDeclineRetriable,
				Reason:      "EXCEEDS_FREQUENCY_LIMIT",
				Cooldown:    24 * time.Hour,
				MaxAttempts: 4,
				Window:      384 * time.Hour,
			},
			"MASTERCARD:N4": {Class: rules.SchemePenaltyDeclineRetriable, Reason: "INSUFFICIENT_FUNDS_OR_OVER_LIMIT", Cooldown: 24 * time.Hour},
			"ANY:19":        {Class: rules.RetryClass("SCHEME_NON_PENALTY_DECLINE_RETRIABLE"), Reason: "RE_ENTER_TRANSACTION", Cooldown: time.Hour},
		},
	}
}

func TestClassify(t *testing.T) {
	table := testTable()

	tests := []struct {
		name            string
		network         string
		code            string
		mac             string
		wantClass       rules.RetryClass
		wantReason      string
		wantCooldown    time.Duration
		wantMaxAttempts int
		wantWindow      time.Duration
	}{
		{
			name:       "MASTERCARD + MAC 03 → HARD_DECLINE (MAC wins over network code)",
			network:    "MASTERCARD",
			code:       "51",
			mac:        "03",
			wantClass:  rules.HardDecline,
			wantReason: "MAC_DO_NOT_RETRY",
		},
		{
			name:         "MASTERCARD + MAC 02 → SCHEME_PENALTY with 72h cooldown",
			network:      "MASTERCARD",
			code:         "51",
			mac:          "02",
			wantClass:    rules.SchemePenaltyDeclineRetriable,
			wantReason:   "MAC_RETRY_AFTER_72H",
			wantCooldown: 72 * time.Hour,
			// MAC rules never carry a count limit regardless of the network code
			wantMaxAttempts: 0,
			wantWindow:      0,
		},
		{
			name:       "MASTERCARD + MAC 40 → PASS_THROUGH",
			network:    "MASTERCARD",
			code:       "51",
			mac:        "40",
			wantClass:  rules.PassThrough,
			wantReason: "MAC_NO_ACTION_REQUIRED",
		},
		{
			name:         "MASTERCARD + MAC that has no rule falls through to network code",
			network:      "MASTERCARD",
			code:         "51",
			mac:          "99",
			wantClass:    rules.SchemePenaltyDeclineRetriable,
			wantReason:   "INSUFFICIENT_FUNDS",
			wantCooldown: 24 * time.Hour,
		},
		{
			name:         "MASTERCARD + empty MAC uses network code rule",
			network:      "MASTERCARD",
			code:         "51",
			mac:          "",
			wantClass:    rules.SchemePenaltyDeclineRetriable,
			wantReason:   "INSUFFICIENT_FUNDS",
			wantCooldown: 24 * time.Hour,
		},
		{
			name:       "VISA + code 41 → HARD_DECLINE via ANY wildcard",
			network:    "VISA",
			code:       "41",
			wantClass:  rules.HardDecline,
			wantReason: "STOLEN_CARD",
		},
		{
			name:            "VISA + code 51 → SCHEME_PENALTY, cooldown-only (no count limit)",
			network:         "VISA",
			code:            "51",
			wantClass:       rules.SchemePenaltyDeclineRetriable,
			wantReason:      "INSUFFICIENT_FUNDS",
			wantCooldown:    24 * time.Hour,
			wantMaxAttempts: 0,
			wantWindow:      0,
		},
		{
			name:            "VISA + code 65 → SCHEME_PENALTY with count limit (max 4 in 16 days)",
			network:         "VISA",
			code:            "65",
			wantClass:       rules.SchemePenaltyDeclineRetriable,
			wantReason:      "EXCEEDS_FREQUENCY_LIMIT",
			wantCooldown:    24 * time.Hour,
			wantMaxAttempts: 4,
			wantWindow:      384 * time.Hour,
		},
		{
			name:         "MASTERCARD + code N4 → network-specific rule (not ANY fallback)",
			network:      "MASTERCARD",
			code:         "N4",
			wantClass:    rules.SchemePenaltyDeclineRetriable,
			wantReason:   "INSUFFICIENT_FUNDS_OR_OVER_LIMIT",
			wantCooldown: 24 * time.Hour,
		},
		{
			name:       "VISA + code N4 → PASS_THROUGH (N4 is MASTERCARD-only, no ANY:N4)",
			network:    "VISA",
			code:       "N4",
			wantClass:  rules.PassThrough,
			wantReason: "UNKNOWN_CODE",
		},
		{
			name:         "VISA + code 19 → SCHEME_NON_PENALTY",
			network:      "VISA",
			code:         "19",
			wantClass:    rules.RetryClass("SCHEME_NON_PENALTY_DECLINE_RETRIABLE"),
			wantReason:   "RE_ENTER_TRANSACTION",
			wantCooldown: time.Hour,
		},
		{
			name:       "unknown code → PASS_THROUGH",
			network:    "VISA",
			code:       "ZZ",
			wantClass:  rules.PassThrough,
			wantReason: "UNKNOWN_CODE",
		},
		{
			name:         "MASTERCARD + MAC on code 65 → MAC result, MaxAttempts always 0",
			network:      "MASTERCARD",
			code:         "65",
			mac:          "02",
			wantClass:    rules.SchemePenaltyDeclineRetriable,
			wantReason:   "MAC_RETRY_AFTER_72H",
			wantCooldown: 72 * time.Hour,
			// code 65 has MaxAttempts=4 but MAC rule wins and carries no count limit
			wantMaxAttempts: 0,
			wantWindow:      0,
		},
		{
			name:         "VISA with MAC present → MAC is ignored (only MASTERCARD evaluates MAC)",
			network:      "VISA",
			code:         "51",
			mac:          "03",
			wantClass:    rules.SchemePenaltyDeclineRetriable,
			wantReason:   "INSUFFICIENT_FUNDS",
			wantCooldown: 24 * time.Hour,
		},
		{
			name:       "MASTERCARD + HARD_DECLINE code, no MAC → network code rule applies",
			network:    "MASTERCARD",
			code:       "41",
			mac:        "",
			wantClass:  rules.HardDecline,
			wantReason: "STOLEN_CARD",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classifier.Classify(tc.network, tc.code, tc.mac, table)

			if got.Class != tc.wantClass {
				t.Errorf("Class: want %q, got %q", tc.wantClass, got.Class)
			}
			if got.Reason != tc.wantReason {
				t.Errorf("Reason: want %q, got %q", tc.wantReason, got.Reason)
			}
			if got.Cooldown != tc.wantCooldown {
				t.Errorf("Cooldown: want %v, got %v", tc.wantCooldown, got.Cooldown)
			}
			if got.MaxAttempts != tc.wantMaxAttempts {
				t.Errorf("MaxAttempts: want %d, got %d", tc.wantMaxAttempts, got.MaxAttempts)
			}
			if got.Window != tc.wantWindow {
				t.Errorf("Window: want %v, got %v", tc.wantWindow, got.Window)
			}
		})
	}
}
