package offline

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// HLC is a hybrid logical clock timestamp: wall-clock milliseconds, a
// counter for events in the same millisecond, and the node (device) that
// made it. Its string form sorts in clock order, so it can be compared as
// text in SQL and in IndexedDB.
type HLC struct {
	Wall  int64  // Unix milliseconds
	Count uint32 // 0..maxCount
	Node  string // device id; breaks ties
}

const maxCount = 99999

// ErrBadHLC is returned for a timestamp that does not parse.
var ErrBadHLC = errors.New("offline: malformed hlc")

// String encodes the timestamp as "000001732700000000:00000:node".
func (h HLC) String() string {
	if h.IsZero() {
		return ""
	}
	return fmt.Sprintf("%015d:%05d:%s", h.Wall, h.Count, h.Node)
}

// IsZero reports whether h is the zero timestamp.
func (h HLC) IsZero() bool { return h.Wall == 0 && h.Count == 0 && h.Node == "" }

// Compare returns -1, 0 or +1.
func (h HLC) Compare(o HLC) int {
	switch {
	case h.Wall != o.Wall:
		return cmp(h.Wall < o.Wall)
	case h.Count != o.Count:
		return cmp(h.Count < o.Count)
	case h.Node != o.Node:
		return cmp(h.Node < o.Node)
	}
	return 0
}

// After reports whether h is later than o.
func (h HLC) After(o HLC) bool { return h.Compare(o) > 0 }

func cmp(less bool) int {
	if less {
		return -1
	}
	return 1
}

// ParseHLC decodes the string form. The empty string is the zero HLC.
func ParseHLC(s string) (HLC, error) {
	if s == "" {
		return HLC{}, nil
	}
	parts := strings.SplitN(s, ":", 3)
	if len(parts) != 3 || len(parts[0]) != 15 || len(parts[1]) != 5 || parts[2] == "" || len(parts[2]) > 64 {
		return HLC{}, ErrBadHLC
	}
	wall, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || wall < 0 {
		return HLC{}, ErrBadHLC
	}
	count, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return HLC{}, ErrBadHLC
	}
	return HLC{Wall: wall, Count: uint32(count), Node: parts[2]}, nil
}

// MaxHLC returns the later of two encoded timestamps (empty and malformed
// strings count as zero).
func MaxHLC(a, b string) string {
	ha, _ := ParseHLC(a)
	hb, _ := ParseHLC(b)
	if hb.After(ha) {
		return hb.String()
	}
	return ha.String()
}

// Clock issues HLC timestamps for one node.
type Clock struct {
	mu   sync.Mutex
	last HLC
	node string
	now  func() time.Time
}

// NewClock returns a clock for node. now defaults to time.Now.
func NewClock(node string, now func() time.Time) *Clock {
	if now == nil {
		now = time.Now
	}
	return &Clock{node: node, now: now}
}

// Now returns a timestamp later than any this clock has issued or observed.
func (c *Clock) Now() HLC {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.last = c.advance(c.last, c.now().UnixMilli())
	c.last.Node = c.node
	return c.last
}

// Observe merges a remote timestamp, so later local timestamps sort after it.
func (c *Clock) Observe(remote HLC) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if remote.After(c.last) {
		c.last = HLC{Wall: remote.Wall, Count: remote.Count, Node: c.node}
	}
}

// Physical returns the clock's wall time.
func (c *Clock) Physical() time.Time { return c.now() }

func (c *Clock) advance(last HLC, wall int64) HLC {
	if wall > last.Wall {
		return HLC{Wall: wall}
	}
	if last.Count >= maxCount {
		return HLC{Wall: last.Wall + 1}
	}
	return HLC{Wall: last.Wall, Count: last.Count + 1}
}
