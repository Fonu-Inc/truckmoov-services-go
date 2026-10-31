package httpx

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

type tokenBucket struct {
	tokens    float64
	maxTokens float64
	refillRPS float64
	lastRefil time.Time
	mu        sync.Mutex
}

func (b *tokenBucket) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(b.lastRefil).Seconds()
	b.tokens = min(b.maxTokens, b.tokens+elapsed*b.refillRPS)
	b.lastRefil = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*tokenBucket
	rps     float64
	burst   float64
}

func newRateLimiter(rps, burst int) *rateLimiter {
	return &rateLimiter{
		buckets: make(map[string]*tokenBucket),
		rps:     float64(rps),
		burst:   float64(burst),
	}
}

func (rl *rateLimiter) bucket(ip string) *tokenBucket {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	b, ok := rl.buckets[ip]
	if !ok {
		b = &tokenBucket{
			tokens:    rl.burst,
			maxTokens: rl.burst,
			refillRPS: rl.rps,
			lastRefil: time.Now(),
		}
		rl.buckets[ip] = b
	}
	return b
}

// WithRateLimit wraps a handler with per-IP token bucket rate limiting.
// rps = sustained requests per second, burst = max burst size.
// If rps <= 0 the middleware is a no-op.
// GPS ingest is excluded: device traffic funnels through a few gateway IPs
// (Traccar/vendors), so an IP bucket of ~100 RPS starves the live map under load.
func WithRateLimit(next http.Handler, rps, burst int) http.Handler {
	if rps <= 0 {
		return next
	}
	rl := newRateLimiter(rps, burst)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isGPSIngestPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		ip := clientIP(r)
		if !rl.bucket(ip).allow() {
			w.Header().Set("Retry-After", "1")
			WriteError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isGPSIngestPath(path string) bool {
	return strings.HasPrefix(path, "/tracking/ingest")
}

func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		return fwd
	}
	return r.RemoteAddr
}
