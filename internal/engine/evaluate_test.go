package engine_test

import (
	"context"
	"testing"
	"time"

	"retry-guardian/internal/engine"
	"retry-guardian/internal/identity"
	"retry-guardian/internal/rules"
	"retry-guardian/internal/store"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// evalIdentity is the fixed identity for the card used across all evaluate tests.
var evalIdentity = identity.Compute("merch_eval", "fpr_eval_card", "PURCHASE")

func newEvalSetup(t *testing.T) (*store.Store, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return store.New(rdb), mr
}

func evalRequest(paymentID string) engine.EvaluateRequest {
	return engine.EvaluateRequest{
		PaymentID:       paymentID,
		MerchantID:      "merch_eval",
		CardFingerprint: "fpr_eval_card",
		TransactionType: "PURCHASE",
		Network:         "VISA",
	}
}

func TestEvaluate_NoPriorState_AllowsWithNoPriorDecline(t *testing.T) {
	st, _ := newEvalSetup(t)
	r := engine.Evaluate(context.Background(), evalRequest("pay_001"), st)

	if r.Decision != engine.DecisionAllow {
		t.Errorf("decision: want ALLOW, got %s", r.Decision)
	}
	if r.ReasonCode != "NO_PRIOR_DECLINE" {
		t.Errorf("reason: want NO_PRIOR_DECLINE, got %s", r.ReasonCode)
	}
	if r.AttemptsRemaining != nil {
		t.Error("want nil attempts_remaining for fresh card")
	}
	if r.RetryAllowedAfter != nil {
		t.Error("want nil retry_allowed_after for fresh card")
	}
}

func TestEvaluate_AfterHardDecline_BlocksPermanently(t *testing.T) {
	st, _ := newEvalSetup(t)
	ctx := context.Background()
	_ = st.UpdateState(ctx, evalIdentity, rules.HardDecline, "STOLEN_CARD", 0, time.Now(), 0, 0)

	r := engine.Evaluate(ctx, evalRequest("pay_001"), st)

	if r.Decision != engine.DecisionBlock {
		t.Errorf("decision: want BLOCK, got %s", r.Decision)
	}
	// Hard declines have no cooldown expiry — retry_allowed_after must be absent
	if r.RetryAllowedAfter != nil {
		t.Error("hard decline must not set retry_allowed_after (block is permanent)")
	}
}

func TestEvaluate_WithinCooldown_BlocksWithRetryAllowedAfter(t *testing.T) {
	st, _ := newEvalSetup(t)
	ctx := context.Background()
	now := time.Now()
	_ = st.UpdateState(ctx, evalIdentity, rules.SchemePenaltyDeclineRetriable, "INSUFFICIENT_FUNDS", 24*time.Hour, now, 0, 0)

	r := engine.Evaluate(ctx, evalRequest("pay_001"), st)

	if r.Decision != engine.DecisionBlock {
		t.Errorf("decision: want BLOCK, got %s", r.Decision)
	}
	if r.ReasonCode != "RETRY_TOO_SOON" {
		t.Errorf("reason: want RETRY_TOO_SOON, got %s", r.ReasonCode)
	}
	if r.RetryAllowedAfter == nil {
		t.Error("want retry_allowed_after to be set")
	}
}

func TestEvaluate_AfterCooldownExpiry_AllowsWithinBudget(t *testing.T) {
	st, _ := newEvalSetup(t)
	ctx := context.Background()
	// Set retry_not_before in the past by using a negative cooldown offset:
	// UpdateState stores retry_not_before = now + cooldown.
	// Passing now = 25h ago with cooldown = 24h gives retry_not_before = 1h ago.
	past := time.Now().Add(-25 * time.Hour)
	_ = st.UpdateState(ctx, evalIdentity, rules.SchemePenaltyDeclineRetriable, "INSUFFICIENT_FUNDS", 24*time.Hour, past, 0, 0)

	r := engine.Evaluate(ctx, evalRequest("pay_001"), st)

	if r.Decision != engine.DecisionAllow {
		t.Errorf("decision: want ALLOW after cooldown, got %s", r.Decision)
	}
	if r.ReasonCode != "WITHIN_RETRY_BUDGET" {
		t.Errorf("reason: want WITHIN_RETRY_BUDGET, got %s", r.ReasonCode)
	}
}

func TestEvaluate_Code65_WithinBudget_ShowsAttemptsRemaining(t *testing.T) {
	st, _ := newEvalSetup(t)
	ctx := context.Background()
	// 2 declines within the 16-day window; cooldown=0 so no RETRY_TOO_SOON block.
	recentTime := time.Now().Add(-time.Hour)
	_ = st.UpdateState(ctx, evalIdentity, rules.SchemePenaltyDeclineRetriable, "EXCEEDS_FREQUENCY_LIMIT", 0, recentTime, 4, 384*time.Hour)
	_ = st.UpdateState(ctx, evalIdentity, rules.SchemePenaltyDeclineRetriable, "EXCEEDS_FREQUENCY_LIMIT", 0, recentTime.Add(time.Minute), 4, 384*time.Hour)

	r := engine.Evaluate(ctx, evalRequest("pay_001"), st)

	if r.Decision != engine.DecisionAllow {
		t.Errorf("decision: want ALLOW (within budget), got %s", r.Decision)
	}
	if r.ReasonCode != "WITHIN_RETRY_BUDGET" {
		t.Errorf("reason: want WITHIN_RETRY_BUDGET, got %s", r.ReasonCode)
	}
	if r.AttemptsRemaining == nil {
		t.Fatal("want attempts_remaining to be set")
	}
	if *r.AttemptsRemaining != 2 {
		t.Errorf("attempts_remaining: want 2, got %d", *r.AttemptsRemaining)
	}
}

func TestEvaluate_Code65_BudgetExhausted_BlocksWithWindowEnd(t *testing.T) {
	st, _ := newEvalSetup(t)
	ctx := context.Background()
	recentTime := time.Now().Add(-time.Hour)
	// 4 declines (max for code 65), cooldown=0 to bypass RETRY_TOO_SOON
	for i := 0; i < 4; i++ {
		_ = st.UpdateState(ctx, evalIdentity, rules.SchemePenaltyDeclineRetriable, "EXCEEDS_FREQUENCY_LIMIT", 0, recentTime.Add(time.Duration(i)*time.Minute), 4, 384*time.Hour)
	}

	r := engine.Evaluate(ctx, evalRequest("pay_001"), st)

	if r.Decision != engine.DecisionBlock {
		t.Errorf("decision: want BLOCK, got %s", r.Decision)
	}
	if r.ReasonCode != "RETRY_LIMIT_EXCEEDED" {
		t.Errorf("reason: want RETRY_LIMIT_EXCEEDED, got %s", r.ReasonCode)
	}
	if r.RetryAllowedAfter == nil {
		t.Error("want retry_allowed_after (window end date) to be set")
	}
}

func TestEvaluate_Code65_WindowExpired_AllowsAndResetsWindow(t *testing.T) {
	st, _ := newEvalSetup(t)
	ctx := context.Background()
	// first_attempt_at = 17 days ago → outside the 16-day window
	sevenTeenDaysAgo := time.Now().Add(-17 * 24 * time.Hour)
	for i := 0; i < 4; i++ {
		_ = st.UpdateState(ctx, evalIdentity, rules.SchemePenaltyDeclineRetriable, "EXCEEDS_FREQUENCY_LIMIT", 0, sevenTeenDaysAgo.Add(time.Duration(i)*time.Minute), 4, 384*time.Hour)
	}

	r := engine.Evaluate(ctx, evalRequest("pay_001"), st)

	if r.Decision != engine.DecisionAllow {
		t.Errorf("decision: want ALLOW (window reset), got %s", r.Decision)
	}
	if r.ReasonCode != "WINDOW_RESET" {
		t.Errorf("reason: want WINDOW_RESET, got %s", r.ReasonCode)
	}
}

func TestEvaluate_RedisUnavailable_FailsOpen(t *testing.T) {
	st, mr := newEvalSetup(t)
	mr.Close() // make Redis unavailable

	r := engine.Evaluate(context.Background(), evalRequest("pay_001"), st)

	if r.Decision != engine.DecisionAllow {
		t.Errorf("decision: want ALLOW (fail-open on Redis error), got %s", r.Decision)
	}
	if r.ReasonCode != "DEGRADED_STATE_UNAVAILABLE" {
		t.Errorf("reason: want DEGRADED_STATE_UNAVAILABLE, got %s", r.ReasonCode)
	}
}
