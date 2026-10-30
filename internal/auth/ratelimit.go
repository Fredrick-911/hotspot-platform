package auth

import (
	"sync"
	"time"
)

// Limiter allows at most max failures per key within window; after that the key
// is blocked until the oldest failure ages out. It lives in memory, which is
// right for one API server. (Several servers would need a shared store.)
type Limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	fails  map[string][]time.Time
	now    func() time.Time
}

// NewLimiter returns a Limiter allowing max failures per window.
func NewLimiter(max int, window time.Duration) *Limiter {
	return &Limiter{max: max, window: window, fails: map[string][]time.Time{}, now: time.Now}
}

func (l *Limiter) prune(key string) []time.Time {
	cut := l.now().Add(-l.window)
	kept := l.fails[key][:0]
	for _, t := range l.fails[key] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.fails, key)
		return nil
	}
	l.fails[key] = kept
	return kept
}

// Allowed reports whether key may try again, and if not, how long to wait.
func (l *Limiter) Allowed(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f := l.prune(key)
	if len(f) < l.max {
		return true, 0
	}
	return false, f[0].Add(l.window).Sub(l.now())
}

// Fail records one failed attempt.
func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune(key)
	l.fails[key] = append(l.fails[key], l.now())
	if len(l.fails) > 10_000 { // keep memory bounded under attack
		for k := range l.fails {
			if l.prune(k) == nil {
				continue
			}
		}
	}
}

// Reset forgets key (call after a successful login).
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, key)
}
