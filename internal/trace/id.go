package trace

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

const (
	minTraceIDLength = 8
	maxTraceIDLength = 64
)

var fallbackCounter uint64

// NewID returns a random RFC 4122 version 4 UUID string, which is used for both
// trace IDs and idempotency keys.
func NewID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand does not fail on supported platforms, but a lookup must
		// never fail because of trace bookkeeping, so fall back instead of
		// panicking inside a request.
		return fallbackID()
	}
	buf[6] = (buf[6] & 0x0f) | 0x40 // version 4
	buf[8] = (buf[8] & 0x3f) | 0x80 // variant 10
	return formatID(buf[:])
}

func fallbackID() string {
	counter := atomic.AddUint64(&fallbackCounter, 1)
	var buf [16]byte
	now := uint64(time.Now().UnixNano())
	for i := 0; i < 8; i++ {
		buf[i] = byte(now >> (8 * (7 - i)))
	}
	for i := 0; i < 8; i++ {
		buf[8+i] = byte(counter >> (8 * (7 - i)))
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return formatID(buf[:])
}

func formatID(buf []byte) string {
	encoded := make([]byte, 36)
	hex.Encode(encoded[0:8], buf[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], buf[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], buf[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], buf[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], buf[10:16])
	return string(encoded)
}

// ValidID reports whether a client-supplied identifier is safe to store, log,
// and embed in a URL path. Anything else is rejected so that a hostile header
// cannot inject log lines or path segments.
func ValidID(value string) bool {
	if len(value) < minTraceIDLength || len(value) > maxTraceIDLength {
		return false
	}
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z':
		case char >= 'A' && char <= 'Z':
		case char >= '0' && char <= '9':
		case char == '-', char == '_', char == '.', char == ':':
		default:
			return false
		}
	}
	return !strings.Contains(value, "..")
}

// NormalizeID trims and validates a client-supplied identifier, returning an
// empty string when it is unusable.
func NormalizeID(value string) string {
	trimmed := strings.TrimSpace(value)
	if !ValidID(trimmed) {
		return ""
	}
	return trimmed
}

// RequiresReport identifies trace statuses that mean the server finished its own
// work but a downstream client stage (Windmill or Emby) has not reported yet.
func RequiresReport(status string) bool {
	return status == StatusSucceeded || status == StatusPartial
}

func formatInt(value int64) string { return fmt.Sprintf("%d", value) }
