package api

import (
	"strings"
	"sync"
	"time"
)

// --- Per-IP rate limiting -------------------------------------------------
//
// A simple token bucket per remote address. Not a substitute for a
// real edge/WAF rate limiter in front of a production deployment, but
// closes the trivial "hammer the API from one IP" gap for a bare
// Go process.

const (
	rateLimitSustainedPerSec = 20
	rateLimitBurst           = 40
)

type bucket struct {
	tokens     float64
	lastRefill time.Time
}

var rateLimiter = struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}{buckets: make(map[string]*bucket)}

func allowRequest(remoteAddr string) bool {
	ip := remoteAddr
	if idx := strings.LastIndex(remoteAddr, ":"); idx != -1 {
		ip = remoteAddr[:idx]
	}

	rateLimiter.mu.Lock()
	defer rateLimiter.mu.Unlock()

	now := time.Now()

	b, ok := rateLimiter.buckets[ip]
	if !ok {
		b = &bucket{tokens: rateLimitBurst, lastRefill: now}
		rateLimiter.buckets[ip] = b
	}

	elapsed := now.Sub(b.lastRefill).Seconds()
	b.tokens += elapsed * rateLimitSustainedPerSec
	if b.tokens > rateLimitBurst {
		b.tokens = rateLimitBurst
	}
	b.lastRefill = now

	// Lazily drop long-idle buckets so this map doesn't grow forever
	// under a distributed low-and-slow scan.
	if len(rateLimiter.buckets) > 10000 {
		for k, v := range rateLimiter.buckets {
			if now.Sub(v.lastRefill) > 10*time.Minute {
				delete(rateLimiter.buckets, k)
			}
		}
	}

	if b.tokens < 1 {
		return false
	}

	b.tokens--
	return true
}

// --- Request body size cap ------------------------------------------------

// maxRequestBodyBytes bounds how much any single request body can
// make the server allocate while decoding JSON - without this, a
// single oversized POST can exhaust memory.
const maxRequestBodyBytes = 64 * 1024
