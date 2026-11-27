package middleware

import (
	"container/list"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"privasync/server/internal/httpx"
)

// defaultMaxLimiterEntries bounds how many caller addresses the limiter tracks
// at once. Generous enough that a real deployment never evicts a legitimate
// caller, small enough that the map cannot become a memory-exhaustion vector.
const defaultMaxLimiterEntries = 10000

type ipLimiter struct {
	limiter  *rate.Limiter
	lastSeen time.Time
	// element is this entry's position in the recency list, so eviction and
	// promotion stay O(1) instead of scanning the whole map.
	element *list.Element
}

type RateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*ipLimiter
	// recency orders keys from least to most recently used; the front is the
	// next eviction candidate.
	recency *list.List
	r       rate.Limit
	b       int
	cleanup time.Duration
	// MaxEntries caps the number of tracked addresses. Set before first use.
	MaxEntries int
}

// NewRateLimiter creates an IP-based rate limiter.
// r = requests per second, b = burst size.
func NewRateLimiter(r rate.Limit, b int) *RateLimiter {
	rl := &RateLimiter{
		limiters:   make(map[string]*ipLimiter),
		recency:    list.New(),
		r:          r,
		b:          b,
		cleanup:    5 * time.Minute,
		MaxEntries: defaultMaxLimiterEntries,
	}
	go rl.cleanupLoop()
	return rl
}

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limiter := rl.getLimiter(httpx.ClientIP(r))
		if !limiter.Allow() {
			http.Error(w, `{"error":"too many requests"}`, http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (rl *RateLimiter) getLimiter(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if l, ok := rl.limiters[ip]; ok {
		l.lastSeen = time.Now()
		rl.recency.MoveToBack(l.element)
		return l.limiter
	}

	// Evict the least recently used entries until there is room. A caller
	// that loses its bucket this way gets a fresh burst allowance, which is
	// why the cap must stay far above any plausible concurrent-caller count.
	for len(rl.limiters) >= rl.MaxEntries {
		oldest := rl.recency.Front()
		if oldest == nil {
			break
		}
		rl.recency.Remove(oldest)
		delete(rl.limiters, oldest.Value.(string))
	}

	l := &ipLimiter{
		limiter:  rate.NewLimiter(rl.r, rl.b),
		lastSeen: time.Now(),
	}
	l.element = rl.recency.PushBack(ip)
	rl.limiters[ip] = l
	return l.limiter
}

// size reports how many addresses are currently tracked.
func (rl *RateLimiter) size() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return len(rl.limiters)
}

func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(rl.cleanup)
	defer ticker.Stop()
	for range ticker.C {
		rl.mu.Lock()
		for ip, l := range rl.limiters {
			if time.Since(l.lastSeen) > rl.cleanup {
				rl.recency.Remove(l.element)
				delete(rl.limiters, ip)
			}
		}
		rl.mu.Unlock()
	}
}
