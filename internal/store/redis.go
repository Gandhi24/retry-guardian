package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"retry-guardian/internal/rules"

	"github.com/redis/go-redis/v9"
)

const (
	stateKeyPrefix    = "rg:"
	paymentKeyPrefix  = "rg:payment:"
	paymentMappingTTL = 30 * time.Minute
)

// ttlForClass returns how long the state key should live in Redis.
// Hard blocks are kept longer to prevent fine exposure after a Redis flush.
func ttlForClass(class rules.RetryClass) time.Duration {
	switch class {
	case rules.HardDecline:
		return 30 * 24 * time.Hour
	case rules.SchemePenaltyDeclineRetriable:
		return 16 * 24 * time.Hour
	default:
		return 24 * time.Hour
	}
}

// updateStateScript runs atomically inside Redis to record a single decline:
//   - HSETNX ensures first_attempt_at is only written on the very first decline.
//   - HINCRBY safely increments the counter regardless of concurrent callers.
//   - HSET overwrites classification + count-limit fields with the latest decline's data.
//   - EXPIRE resets the TTL on every write.
const updateStateScript = `
local key = KEYS[1]
redis.call('HSETNX', key, 'first_attempt_at', ARGV[1])
redis.call('HINCRBY', key, 'attempt_count', 1)
redis.call('HSET', key,
    'retry_class',      ARGV[2],
    'block_reason',     ARGV[3],
    'retry_not_before', ARGV[4],
    'max_attempts',     ARGV[6],
    'window_secs',      ARGV[7])
redis.call('EXPIRE', key, ARGV[5])
return 1
`

// State is the retry state stored in Redis for a single transaction identity.
type State struct {
	AttemptCount   int64
	FirstAttemptAt time.Time
	RetryNotBefore time.Time // zero value means no active cooldown
	BlockReason    string
	RetryClass     rules.RetryClass
	MaxAttempts    int   // 0 means no count limit for this code
	WindowSecs     int64 // seconds; 0 when MaxAttempts is 0
}

// Store exposes the retry-guardian state operations backed by Redis.
type Store struct {
	client       *redis.Client
	updateScript *redis.Script
}

// New returns a Store backed by the given Redis client.
func New(client *redis.Client) *Store {
	return &Store{
		client:       client,
		updateScript: redis.NewScript(updateStateScript),
	}
}

// Ping verifies connectivity to Redis. Used by the /ready health check.
func (s *Store) Ping(ctx context.Context) error {
	return s.client.Ping(ctx).Err()
}

// FetchState returns the current retry state for identity.
//   - (nil, nil)  → no history exists; this is the first-ever attempt.
//   - (nil, err)  → Redis failure; caller should treat as degraded and ALLOW.
//   - (state, nil) → history found; caller applies decision logic.
func (s *Store) FetchState(ctx context.Context, identity string) (*State, error) {
	vals, err := s.client.HGetAll(ctx, stateKey(identity)).Result()
	if err != nil {
		return nil, fmt.Errorf("redis HGetAll: %w", err)
	}
	if len(vals) == 0 {
		return nil, nil
	}
	return parseState(vals)
}

// UpdateState atomically records a new declined attempt for identity.
func (s *Store) UpdateState(
	ctx context.Context,
	identity string,
	class rules.RetryClass,
	reason string,
	cooldown time.Duration,
	now time.Time,
	maxAttempts int,
	window time.Duration,
) error {
	retryNotBefore := int64(0)
	if cooldown > 0 {
		retryNotBefore = now.Add(cooldown).Unix()
	}
	ttl := int64(ttlForClass(class).Seconds())
	windowSecs := int64(window.Seconds())

	err := s.updateScript.Run(ctx, s.client,
		[]string{stateKey(identity)},
		now.Unix(),
		string(class),
		reason,
		retryNotBefore,
		ttl,
		maxAttempts,
		windowSecs,
	).Err()
	if err != nil {
		return fmt.Errorf("redis updateState script: %w", err)
	}
	return nil
}

// ClearState removes the retry state for identity. Called when a payment is APPROVED
// so the merchant can retry freely on the next transaction.
func (s *Store) ClearState(ctx context.Context, identity string) error {
	if err := s.client.Del(ctx, stateKey(identity)).Err(); err != nil {
		return fmt.Errorf("redis DEL state: %w", err)
	}
	return nil
}

// SavePaymentMapping stores paymentID → (identity, network) for 30 minutes so
// the /record endpoint can resolve the identity without re-receiving all fields.
func (s *Store) SavePaymentMapping(ctx context.Context, paymentID, identity, network string) error {
	val := identity + "|" + network
	if err := s.client.Set(ctx, paymentKey(paymentID), val, paymentMappingTTL).Err(); err != nil {
		return fmt.Errorf("redis SET payment mapping: %w", err)
	}
	return nil
}

// ErrPaymentNotFound is returned when the payment mapping has expired or never existed.
var ErrPaymentNotFound = errors.New("payment not found: /evaluate must be called before /record")

// GetPaymentContext retrieves the (identity, network) pair stored at evaluate time.
func (s *Store) GetPaymentContext(ctx context.Context, paymentID string) (identity, network string, err error) {
	val, err := s.client.Get(ctx, paymentKey(paymentID)).Result()
	if errors.Is(err, redis.Nil) {
		return "", "", ErrPaymentNotFound
	}
	if err != nil {
		return "", "", fmt.Errorf("redis GET payment mapping: %w", err)
	}
	parts := strings.SplitN(val, "|", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("malformed payment mapping value: %q", val)
	}
	return parts[0], parts[1], nil
}

// ---- helpers -------------------------------------------------------------

func stateKey(identity string) string    { return stateKeyPrefix + identity }
func paymentKey(paymentID string) string { return paymentKeyPrefix + paymentID }

func parseState(vals map[string]string) (*State, error) {
	count, err := strconv.ParseInt(vals["attempt_count"], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse attempt_count: %w", err)
	}
	firstAt, err := parseUnixTS(vals["first_attempt_at"])
	if err != nil {
		return nil, fmt.Errorf("parse first_attempt_at: %w", err)
	}
	notBefore, err := parseUnixTS(vals["retry_not_before"])
	if err != nil {
		return nil, fmt.Errorf("parse retry_not_before: %w", err)
	}
	maxAttempts, _ := strconv.Atoi(vals["max_attempts"]) // 0 on missing/malformed = no limit
	windowSecs, _ := strconv.ParseInt(vals["window_secs"], 10, 64)

	return &State{
		AttemptCount:   count,
		FirstAttemptAt: firstAt,
		RetryNotBefore: notBefore,
		BlockReason:    vals["block_reason"],
		RetryClass:     rules.RetryClass(vals["retry_class"]),
		MaxAttempts:    maxAttempts,
		WindowSecs:     windowSecs,
	}, nil
}

// parseUnixTS converts a stored Unix second timestamp back to time.Time.
// "0" and "" both represent "not set" and return the zero time.
func parseUnixTS(s string) (time.Time, error) {
	if s == "" || s == "0" {
		return time.Time{}, nil
	}
	sec, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(sec, 0).UTC(), nil
}
