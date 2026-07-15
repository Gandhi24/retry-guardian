package engine_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"retry-guardian/internal/engine"
	"retry-guardian/internal/rules"
	"retry-guardian/internal/store"
)

const (
	recPaymentID = "pay_rec_001"
	recIdentity  = "record_test_identity_fixed"
	recNetwork   = "VISA"
)

func newRecordSetup(t *testing.T) (*store.Store, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return store.New(rdb), mr
}

func recordTable() *rules.Table {
	return &rules.Table{
		Version: "test",
		MACRules: map[string]rules.MACRule{
			"40": {Class: rules.PassThrough, Reason: "MAC_NO_ACTION_REQUIRED"},
			"03": {Class: rules.HardDecline, Reason: "MAC_DO_NOT_RETRY"},
		},
		NetworkCodeIndex: map[string]rules.NetworkCodeRule{
			"ANY:41": {Class: rules.HardDecline, Reason: "STOLEN_CARD"},
			"ANY:51": {Class: rules.SchemePenaltyDeclineRetriable, Reason: "INSUFFICIENT_FUNDS", Cooldown: 24 * time.Hour},
		},
	}
}

// seedMapping sets up a payment mapping as /evaluate would have done.
func seedMapping(t *testing.T, st *store.Store, paymentID, identity, network string) {
	t.Helper()
	if err := st.SavePaymentMapping(context.Background(), paymentID, identity, network); err != nil {
		t.Fatalf("SavePaymentMapping: %v", err)
	}
}

func TestRecord_Approved_ClearsIdentityState(t *testing.T) {
	st, _ := newRecordSetup(t)
	ctx := context.Background()
	seedMapping(t, st, recPaymentID, recIdentity, recNetwork)
	_ = st.UpdateState(ctx, recIdentity, rules.SchemePenaltyDeclineRetriable, "INSUFFICIENT_FUNDS", 24*time.Hour, time.Now(), 0, 0)

	err := engine.Record(ctx, engine.RecordRequest{
		PaymentID:  recPaymentID,
		Outcome:    "APPROVED",
		OccurredAt: time.Now(),
	}, st, recordTable())
	if err != nil {
		t.Fatalf("Record APPROVED: %v", err)
	}

	state, _ := st.FetchState(ctx, recIdentity)
	if state != nil {
		t.Error("want nil state after APPROVED (clear), got non-nil")
	}
}

func TestRecord_Declined_WritesStateToRedis(t *testing.T) {
	st, _ := newRecordSetup(t)
	ctx := context.Background()
	seedMapping(t, st, recPaymentID, recIdentity, recNetwork)

	err := engine.Record(ctx, engine.RecordRequest{
		PaymentID: recPaymentID,
		Outcome:   "DECLINED",
		AuthorizationData: &engine.AuthorizationData{
			CardNetworkResponseCode: "51",
		},
		OccurredAt: time.Now(),
	}, st, recordTable())
	if err != nil {
		t.Fatalf("Record DECLINED: %v", err)
	}

	state, _ := st.FetchState(ctx, recIdentity)
	if state == nil {
		t.Fatal("want state after DECLINED, got nil")
	}
	if state.AttemptCount != 1 {
		t.Errorf("AttemptCount: want 1, got %d", state.AttemptCount)
	}
	if state.RetryClass != rules.SchemePenaltyDeclineRetriable {
		t.Errorf("RetryClass: want %q, got %q", rules.SchemePenaltyDeclineRetriable, state.RetryClass)
	}
}

func TestRecord_Error_LeavesStateUnchanged(t *testing.T) {
	st, _ := newRecordSetup(t)
	ctx := context.Background()
	seedMapping(t, st, recPaymentID, recIdentity, recNetwork)

	err := engine.Record(ctx, engine.RecordRequest{
		PaymentID:  recPaymentID,
		Outcome:    "ERROR",
		OccurredAt: time.Now(),
	}, st, recordTable())
	if err != nil {
		t.Fatalf("Record ERROR: %v", err)
	}

	// ERROR must not write any retry state
	state, _ := st.FetchState(ctx, recIdentity)
	if state != nil {
		t.Error("want nil state after ERROR outcome, got non-nil")
	}
}

func TestRecord_DeclinedPassThrough_NoStateWritten(t *testing.T) {
	st, _ := newRecordSetup(t)
	ctx := context.Background()
	// MASTERCARD network so MAC 40 is evaluated
	seedMapping(t, st, recPaymentID, recIdentity, "MASTERCARD")

	err := engine.Record(ctx, engine.RecordRequest{
		PaymentID: recPaymentID,
		Outcome:   "DECLINED",
		AuthorizationData: &engine.AuthorizationData{
			CardNetworkResponseCode: "51",
			MerchantAdviceCode:      "40",
		},
		OccurredAt: time.Now(),
	}, st, recordTable())
	if err != nil {
		t.Fatalf("Record DECLINED PASS_THROUGH: %v", err)
	}

	state, _ := st.FetchState(ctx, recIdentity)
	if state != nil {
		t.Error("want no state for PASS_THROUGH decline, got non-nil")
	}
}

func TestRecord_DeclinedNilAuthData_ReturnsErrMissingAuthData(t *testing.T) {
	st, _ := newRecordSetup(t)
	ctx := context.Background()
	seedMapping(t, st, recPaymentID, recIdentity, recNetwork)

	err := engine.Record(ctx, engine.RecordRequest{
		PaymentID:         recPaymentID,
		Outcome:           "DECLINED",
		AuthorizationData: nil,
		OccurredAt:        time.Now(),
	}, st, recordTable())
	if !errors.Is(err, engine.ErrMissingAuthData) {
		t.Errorf("want ErrMissingAuthData, got %v", err)
	}
}

func TestRecord_UnknownPaymentID_ReturnsErrPaymentNotFound(t *testing.T) {
	st, _ := newRecordSetup(t)

	err := engine.Record(context.Background(), engine.RecordRequest{
		PaymentID: "pay_never_evaluated",
		Outcome:   "DECLINED",
		AuthorizationData: &engine.AuthorizationData{
			CardNetworkResponseCode: "51",
		},
		OccurredAt: time.Now(),
	}, st, recordTable())
	if !errors.Is(err, store.ErrPaymentNotFound) {
		t.Errorf("want ErrPaymentNotFound, got %v", err)
	}
}

func TestRecord_DuplicateSameOutcome_IsIdempotent(t *testing.T) {
	st, _ := newRecordSetup(t)
	ctx := context.Background()
	seedMapping(t, st, recPaymentID, recIdentity, recNetwork)
	// Simulate that a previous /record call already stamped the outcome
	_ = st.MarkPaymentRecorded(ctx, recPaymentID, recIdentity, recNetwork, "DECLINED")

	err := engine.Record(ctx, engine.RecordRequest{
		PaymentID: recPaymentID,
		Outcome:   "DECLINED",
		AuthorizationData: &engine.AuthorizationData{
			CardNetworkResponseCode: "51",
		},
		OccurredAt: time.Now(),
	}, st, recordTable())
	if err != nil {
		t.Errorf("duplicate with same outcome must be idempotent (nil error), got %v", err)
	}

	// No state should have been written (the pre-existing stamp short-circuited processing)
	state, _ := st.FetchState(ctx, recIdentity)
	if state != nil {
		t.Error("want no state written for idempotent duplicate call")
	}
}

func TestRecord_DuplicateDifferentOutcome_ReturnsErrPaymentAlreadyRecorded(t *testing.T) {
	st, _ := newRecordSetup(t)
	ctx := context.Background()
	seedMapping(t, st, recPaymentID, recIdentity, recNetwork)
	_ = st.MarkPaymentRecorded(ctx, recPaymentID, recIdentity, recNetwork, "DECLINED")

	err := engine.Record(ctx, engine.RecordRequest{
		PaymentID:  recPaymentID,
		Outcome:    "APPROVED",
		OccurredAt: time.Now(),
	}, st, recordTable())
	if !errors.Is(err, engine.ErrPaymentAlreadyRecorded) {
		t.Errorf("want ErrPaymentAlreadyRecorded, got %v", err)
	}
}

func TestRecord_UnknownOutcome_ReturnsError(t *testing.T) {
	st, _ := newRecordSetup(t)
	ctx := context.Background()
	seedMapping(t, st, recPaymentID, recIdentity, recNetwork)

	err := engine.Record(ctx, engine.RecordRequest{
		PaymentID:  recPaymentID,
		Outcome:    "PENDING",
		OccurredAt: time.Now(),
	}, st, recordTable())
	if err == nil {
		t.Error("want error for unknown outcome, got nil")
	}
}

func TestRecord_Approved_StampsOutcomeOnPaymentMapping(t *testing.T) {
	st, _ := newRecordSetup(t)
	ctx := context.Background()
	seedMapping(t, st, recPaymentID, recIdentity, recNetwork)

	_ = engine.Record(ctx, engine.RecordRequest{
		PaymentID:  recPaymentID,
		Outcome:    "APPROVED",
		OccurredAt: time.Now(),
	}, st, recordTable())

	_, _, outcome, err := st.GetPaymentContext(ctx, recPaymentID)
	if err != nil {
		t.Fatalf("GetPaymentContext: %v", err)
	}
	if outcome != "APPROVED" {
		t.Errorf("payment mapping outcome: want APPROVED, got %q", outcome)
	}
}
