package identity_test

import (
	"testing"

	"retry-guardian/internal/identity"
)

func TestCompute_Deterministic(t *testing.T) {
	a := identity.Compute("merch1", "fpr_abc", "PURCHASE")
	b := identity.Compute("merch1", "fpr_abc", "PURCHASE")
	if a != b {
		t.Errorf("same inputs produced different hashes: %q vs %q", a, b)
	}
}

func TestCompute_ChangesWithEachField(t *testing.T) {
	base := identity.Compute("merch1", "fpr_abc", "PURCHASE")
	cases := []struct {
		name string
		got  string
	}{
		{"merchant_id changed", identity.Compute("merch2", "fpr_abc", "PURCHASE")},
		{"card_fingerprint changed", identity.Compute("merch1", "fpr_xyz", "PURCHASE")},
		{"transaction_type changed", identity.Compute("merch1", "fpr_abc", "RECURRING")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got == base {
				t.Error("identity did not change when field changed")
			}
		})
	}
}
