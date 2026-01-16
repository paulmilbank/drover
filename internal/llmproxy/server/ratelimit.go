package server

import (
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/cloud-shuttle/drover/internal/llmproxy"
)

// RateLimiter implements token bucket rate limiting
type RateLimiter struct {
	config          llmproxy.RateLimitConfig
	requestBuckets  map[string]*tokenBucket
	tokenBuckets    map[string]*tokenBucket
	mu              sync.RWMutex
	cleanupInterval time.Duration
}

// tokenBucket represents a token bucket for rate limiting
type tokenBucket struct {
	tokens     float64
	maxTokens  float64
	refillRate float64
	lastRefill time.Time
	mu         sync.Mutex
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(config llmproxy.RateLimitConfig) *RateLimiter {
	rl := &RateLimiter{
		config:          config,
		requestBuckets:  make(map[string]*tokenBucket),
		tokenBuckets:    make(map[string]*tokenBucket),
		cleanupInterval: 5 * time.Minute,
	}

	// Start cleanup goroutine
	go rl.cleanup()

	return rl
}

// Middleware returns an HTTP middleware for rate limiting
func (r *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !r.config.Enabled {
			next.ServeHTTP(w, req)
			return
		}

		key := getClientKey(req)

		// Check request rate limit
		if !r.allowRequest(key) {
			log.Printf("Rate limit exceeded for %s (requests)", key)
			http.Error(w, `{"error":{"message":"Rate limit exceeded","type":"rate_limit_error"}}`, http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, req)
	})
}

// allowRequest checks if a request is allowed under the rate limit
func (r *RateLimiter) allowRequest(key string) bool {
	if r.config.RequestsPerMinute <= 0 {
		return true
	}

	r.mu.Lock()
	bucket, exists := r.requestBuckets[key]
	if !exists {
		bucket = &tokenBucket{
			tokens:     float64(r.config.RequestsPerMinute),
			maxTokens:  float64(r.config.RequestsPerMinute),
			refillRate: float64(r.config.RequestsPerMinute) / 60.0, // per second
			lastRefill: time.Now(),
		}
		r.requestBuckets[key] = bucket
	}
	r.mu.Unlock()

	return bucket.consume(1)
}

// AllowTokens checks if token consumption is allowed
func (r *RateLimiter) AllowTokens(key string, tokens int) bool {
	if r.config.TokensPerMinute <= 0 {
		return true
	}

	r.mu.Lock()
	bucket, exists := r.tokenBuckets[key]
	if !exists {
		bucket = &tokenBucket{
			tokens:     float64(r.config.TokensPerMinute),
			maxTokens:  float64(r.config.TokensPerMinute),
			refillRate: float64(r.config.TokensPerMinute) / 60.0,
			lastRefill: time.Now(),
		}
		r.tokenBuckets[key] = bucket
	}
	r.mu.Unlock()

	return bucket.consume(float64(tokens))
}

// consume attempts to consume tokens from the bucket
func (b *tokenBucket) consume(count float64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Refill tokens based on time elapsed
	now := time.Now()
	elapsed := now.Sub(b.lastRefill).Seconds()
	b.tokens += elapsed * b.refillRate
	b.lastRefill = now

	// Cap at max tokens
	if b.tokens > b.maxTokens {
		b.tokens = b.maxTokens
	}

	// Check if we have enough tokens
	if b.tokens >= count {
		b.tokens -= count
		return true
	}

	return false
}

// cleanup periodically removes old buckets
func (r *RateLimiter) cleanup() {
	ticker := time.NewTicker(r.cleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		r.mu.Lock()
		now := time.Now()

		// Clean request buckets
		for key, bucket := range r.requestBuckets {
			bucket.mu.Lock()
			if now.Sub(bucket.lastRefill) > r.cleanupInterval {
				delete(r.requestBuckets, key)
			}
			bucket.mu.Unlock()
		}

		// Clean token buckets
		for key, bucket := range r.tokenBuckets {
			bucket.mu.Lock()
			if now.Sub(bucket.lastRefill) > r.cleanupInterval {
				delete(r.tokenBuckets, key)
			}
			bucket.mu.Unlock()
		}

		r.mu.Unlock()
	}
}

// getClientKey generates a client identifier for rate limiting
func getClientKey(req *http.Request) string {
	// Try to get API key from header
	if apiKey := req.Header.Get("Authorization"); apiKey != "" {
		return "api:" + apiKey
	}

	// Fall back to IP address
	if ip := req.RemoteAddr; ip != "" {
		return "ip:" + ip
	}

	return "unknown"
}
