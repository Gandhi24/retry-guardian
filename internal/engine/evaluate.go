package engine

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"retry-guardian/internal/identity"
	"retry-guardian/internal/rules"
	"retry-guardian/internal/store"
)

const (
	DecisionAllow = "ALLOW"
	DecisionBlock = "BLOCK"
)

type EvaluateRequest struct {
	PaymentID          string `json:"payment_id"          binding:"required"`
	MerchantID         string `json:"merchant_id"         binding:"required"`
	CardFingerprint    string `json:"card_fingerprint"    binding:"required"`
	Amount             int64  `json:"amount"`
	Currency           string `json:"currency"`
	TransactionType    string `json:"transaction_type"    binding:"required"`
	Network            string `json:"network"             binding:"required"`
	Is3DSAuthenticated bool   `json:"is_3ds_authenticated"`
}

type EvaluateResponse struct {
	Decision          string     `json:"decision"`
	ReasonCode        string     `json:"reason_code,omitempty"`
	Message           string     `json:"message"`
	RetryClass        *string    `json:"retry_class,omitempty"`
	RetryAllowedAfter *time.Time `json:"retry_allowed_after,omitempty"`
	AttemptsRemaining *int       `json:"attempts_remaining,omitempty"`
}

// Evaluate returns an ALLOW or BLOCK decision for a payment attempt.
// It always returns a response — Redis failures fail-open (ALLOW with DEGRADED reason).
func Evaluate(ctx context.Context, req EvaluateRequest, st *store.Store, table *rules.Table) EvaluateResponse {
	id := identity.Compute(req.MerchantID, req.CardFingerprint, req.TransactionType)

	// Store payment mapping so /record can resolve identity by payment_id.
	// Non-fatal: log and continue — /evaluate still returns a decision.
	if err := st.SavePaymentMapping(ctx, req.PaymentID, id, req.Network); err != nil {
		slog.WarnContext(ctx, "failed to save payment mapping; /record will not resolve this payment",
			"payment_id", req.PaymentID, "error", err)
	}

	state, err := st.FetchState(ctx, id)
	if err != nil {
		slog.ErrorContext(ctx, "redis unavailable, failing open",
			"payment_id", req.PaymentID, "error", err)
		return allowResp("DEGRADED_STATE_UNAVAILABLE",
			"Retry state temporarily unavailable; proceeding with caution.", nil)
	}

	// No history at all → first-ever attempt for this identity.
	if state == nil {
		return allowResp("NO_PRIOR_DECLINE",
			"No blocking decline history for this transaction.", nil)
	}

	// A hard decline blocks permanently — no cooldown or count logic applies.
	if state.RetryClass == rules.HardDecline {
		cls := string(state.RetryClass)
		return blockResp(
			state.BlockReason,
			fmt.Sprintf("Payment permanently blocked (%s). Do not retry — scheme fines may apply.", state.BlockReason),
			&cls, nil,
		)
	}

	now := time.Now().UTC()

	// Cooldown: scheme mandates a minimum gap between attempts.
	if !state.RetryNotBefore.IsZero() && now.Before(state.RetryNotBefore) {
		cls := string(state.RetryClass)
		t := state.RetryNotBefore
		return blockResp(
			"RETRY_TOO_SOON",
			fmt.Sprintf("Scheme mandates a cooldown period. Retry allowed after %s.", t.Format(time.RFC3339)),
			&cls, &t,
		)
	}

	// Count budget — only applies when the declined code carries a scheme limit.
	if state.MaxAttempts > 0 {
		window := time.Duration(state.WindowSecs) * time.Second
		windowEnd := state.FirstAttemptAt.Add(window)

		// Window has rolled over → budget resets, allow freely.
		if now.After(windowEnd) {
			days := int(window.Hours() / 24)
			return allowResp("WINDOW_RESET",
				fmt.Sprintf("The %d-day retry window has reset. This transaction may be retried.", days), nil)
		}

		// Count budget exhausted within the current window.
		if state.AttemptCount >= int64(state.MaxAttempts) {
			cls := string(state.RetryClass)
			return blockResp(
				"RETRY_LIMIT_EXCEEDED",
				fmt.Sprintf("%d of %d permitted retries used. Window resets at %s.",
					state.AttemptCount, state.MaxAttempts, windowEnd.Format(time.RFC3339)),
				&cls, &windowEnd,
			)
		}

		remaining := int(int64(state.MaxAttempts) - state.AttemptCount)
		return allowResp(
			"WITHIN_RETRY_BUDGET",
			fmt.Sprintf("%d of %d permitted attempts used. Retry is allowed.",
				state.AttemptCount, state.MaxAttempts),
			&remaining,
		)
	}

	// No count limit for this code (e.g. SCHEME_NON_PENALTY) — cooldown was the only constraint.
	return allowResp("WITHIN_RETRY_BUDGET", "Retry is allowed.", nil)
}

// ---- response builders ---------------------------------------------------

func allowResp(reasonCode, message string, attemptsRemaining *int) EvaluateResponse {
	return EvaluateResponse{
		Decision:          DecisionAllow,
		ReasonCode:        reasonCode,
		Message:           message,
		AttemptsRemaining: attemptsRemaining,
	}
}

func blockResp(reasonCode, message string, retryClass *string, retryAllowedAfter *time.Time) EvaluateResponse {
	return EvaluateResponse{
		Decision:          DecisionBlock,
		ReasonCode:        reasonCode,
		Message:           message,
		RetryClass:        retryClass,
		RetryAllowedAfter: retryAllowedAfter,
	}
}
