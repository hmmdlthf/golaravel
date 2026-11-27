package offline

import (
	"crypto/rand"
	"encoding/binary"
	"sync"
	"time"
)

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var (
	ulidMu   sync.Mutex
	ulidLast [16]byte
	ulidMs   int64
)

// NewID returns a ULID: 48 bits of milliseconds and 80 random bits, in
// Crockford base32. IDs from one process increase monotonically.
func NewID() string {
	ulidMu.Lock()
	defer ulidMu.Unlock()
	ms := time.Now().UnixMilli()
	var b [16]byte
	if ms <= ulidMs {
		// Same millisecond (or clock went back): increment the random part.
		b = ulidLast
		for i := 15; i >= 6; i-- {
			b[i]++
			if b[i] != 0 {
				break
			}
		}
	} else {
		ulidMs = ms
		var t [8]byte
		binary.BigEndian.PutUint64(t[:], uint64(ms))
		copy(b[:6], t[2:])
		_, _ = rand.Read(b[6:])
	}
	ulidLast = b
	return encodeULID(b)
}

func encodeULID(b [16]byte) string {
	// 128 bits -> 26 chars of 5 bits, the first carrying 3 bits.
	out := make([]byte, 26)
	hi := binary.BigEndian.Uint64(b[:8])
	lo := binary.BigEndian.Uint64(b[8:])
	for i := 25; i >= 0; i-- {
		out[i] = crockford[lo&31]
		lo = lo>>5 | hi<<59
		hi >>= 5
	}
	return string(out)
}

// ValidMutationID reports whether s is a ULID or a canonical UUID
// (UUIDv7 recommended), the forms clients may use for mutation ids.
func ValidMutationID(s string) bool {
	switch len(s) {
	case 26:
		if s[0] > '7' {
			return false
		}
		for i := 0; i < 26; i++ {
			if !isCrockford(s[i]) {
				return false
			}
		}
		return true
	case 36:
		for i := 0; i < 36; i++ {
			c := s[i]
			if i == 8 || i == 13 || i == 18 || i == 23 {
				if c != '-' {
					return false
				}
				continue
			}
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
				return false
			}
		}
		return true
	}
	return false
}

func isCrockford(c byte) bool {
	if c >= 'a' && c <= 'z' {
		c -= 'a' - 'A'
	}
	return c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' && c != 'I' && c != 'L' && c != 'O' && c != 'U'
}

// validKey reports whether s is usable as an entity id, entity name or
// device id: 1..191 printable ASCII bytes without separators we rely on.
func validKey(s string) bool {
	if s == "" || len(s) > 191 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c <= ' ' || c >= 0x7f || c == '|' {
			return false
		}
	}
	return true
}
