package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"retry-guardian/internal/classifier"
	"retry-guardian/internal/rules"
	"retry-guardian/internal/store"
)

// ErrMissingAuthData is returned when a DECLINED record is missing the
// card_network_response_code that the classifier needs.
var ErrMissingAuthData = errors.New("authorization_data.card_network_response_code is required for DECLINED outcome")

// ErrPaymentAlreadyRecorded is returned when /record is called a second time for
// the same payment_id but with a different outcome than the first call.
var ErrPaymentAlreadyRecorded = errors.New("payment already recorded with a conflicting outcome")

type RecordRequest struct {
	PaymentID         string             `json:"payment_id"         binding:"required"`
	Outcome           string             `json:"outcome"            binding:"required"` // APPROVED | DECLINED | ERROR
	AuthorizationData *AuthorizationData `json:"authorization_data"`
	OccurredAt        time.Time          `json:"occurred_at"        binding:"required"`
}

type AuthorizationData struct {
	CardNetworkResponseCode string `json:"card_network_response_code"`
	MerchantAdviceCode      string `json:"merchant_advice_code"`
}

// Record updates retry state based on the outcome of a payment attempt.
//
//   - APPROVED → clears the retry state so the merchant can start fresh.
//   - DECLINED → classifies the scheme codes and writes the new state to Redis.
//   - ERROR    → no state change; errors are not retries and carry no scheme codes.
func Record(ctx context.Context, req RecordRequest, st *store.Store, table *rules.Table) error {
	txIdentity, network, recordedOutcome, err := st.GetPaymentContext(ctx, req.PaymentID)
	if err != nil {
		return err // store.ErrPaymentNotFound propagates to the handler
	}

	// Idempotency: exact same call already processed — safe to ack again.
	if recordedOutcome == req.Outcome {
		return nil
	}
	// Conflict: a different outcome was already recorded for this payment_id.
	if recordedOutcome != "" {
		return ErrPaymentAlreadyRecorded
	}

	switch req.Outcome {
	case "APPROVED":
		if err := st.ClearState(ctx, txIdentity); err != nil {
			return err
		}
	case "ERROR":
		// no state change
	case "DECLINED":
		if err := recordDecline(ctx, txIdentity, network, req, st, table); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown outcome %q: must be APPROVED, DECLINED, or ERROR", req.Outcome)
	}

	return st.MarkPaymentRecorded(ctx, req.PaymentID, txIdentity, network, req.Outcome)
}

func recordDecline(
	ctx context.Context,
	txIdentity, network string,
	req RecordRequest,
	st *store.Store,
	table *rules.Table,
) error {
	if req.AuthorizationData == nil {
		return ErrMissingAuthData
	}

	result := classifier.Classify(
		network,
		req.AuthorizationData.CardNetworkResponseCode,
		req.AuthorizationData.MerchantAdviceCode,
		table,
	)

	// PassThrough codes impose no restriction — no state update needed.
	if result.Class == rules.PassThrough {
		return nil
	}

	return st.UpdateState(ctx, txIdentity, result.Class, result.Reason, result.Cooldown, req.OccurredAt, result.MaxAttempts, result.Window)
}
