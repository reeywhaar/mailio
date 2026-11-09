package api

import (
	"sync"
	"time"
)

// limiter is a token bucket per key.
//
// It lives on the Server rather than at package level: two instances in one process — which is
// what a test suite is — would otherwise share one budget and lock each other out.
type limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    time.Duration
	burst   int
	now     func() time.Time
	swept   time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// idleBucket is how long a bucket nobody touches is kept. A full one is the same as a missing
// one, and it is full long before this.
const idleBucket = time.Hour

func newLimiter(burst int, per time.Duration) *limiter {
	return &limiter{
		buckets: map[string]*bucket{},
		rate:    per,
		burst:   burst,
		now:     time.Now,
	}
}

// allow reports whether key may proceed, and spends a token if so.
func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	// Inline rather than on a timer of its own, so a long-running process does not accumulate
	// one bucket per username anybody has ever guessed at.
	if now.Sub(l.swept) >= idleBucket {
		for k, b := range l.buckets {
			if now.Sub(b.last) >= idleBucket {
				delete(l.buckets, k)
			}
		}
		l.swept = now
	}

	b := l.buckets[key]
	if b == nil {
		b = &bucket{tokens: float64(l.burst), last: now}
		l.buckets[key] = b
	}
	b.tokens += now.Sub(b.last).Seconds() / l.rate.Seconds()
	if b.tokens > float64(l.burst) {
		b.tokens = float64(l.burst)
	}
	b.last = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
