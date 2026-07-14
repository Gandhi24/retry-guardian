package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Compute returns a stable hex-encoded SHA-256 hash that uniquely identifies
// a (merchant, card, transaction type) triple.
func Compute(merchantID, cardFingerprint, transactionType string) string {
	var b strings.Builder
	b.WriteString(merchantID)
	b.WriteByte('|')
	b.WriteString(cardFingerprint)
	b.WriteByte('|')
	b.WriteString(transactionType)

	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}
