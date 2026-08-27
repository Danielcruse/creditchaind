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

// --- Per-peer rate limiting (gossip endpoints) -----------------------------
//
// A dedicated, tighter token bucket for /peer/tx and /peer/block
// specifically, separate from allowRequest's general per-IP bucket
// above - so a chatty gossip peer's traffic never competes with, or
// borrows budget from, this same IP's ordinary API traffic, and vice
// versa. This is Phase 1 of the permissionless-networking design in
// audit/consensus_redesign_proposals.md: it closes that document's
// named gap ("no per-peer or per-identity rate limit at all... only
// the generic, route-agnostic per-IP token bucket applies") without
// yet introducing trust tiers - that needs Phase 2's TxPeerRegister/
// validator-backed recognition, deliberately not built here.
//
// Disclosed follow-up, not yet wired: the proposal also calls for a
// rate-limit violation to count toward PeerStore.RecordFailure (so a
// peer that floods and gets throttled every request eventually gets
// Suspended). That requires mapping this request's raw remote IP back
// to a specific Peer.Address, which core.PeerStore doesn't currently
// index by IP (only by the peer's self-declared Address string) -
// attempting a hostname-to-IP resolution inline here would add a DNS
// lookup (and its failure modes) to every rate-limited request, which
// isn't a change to make casually. Left as follow-up work rather than
// guessed at now.
const (
	peerRateLimitSustainedPerSec = 20
	peerRateLimitBurst           = 40
)

var peerRateLimiter = struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}{buckets: make(map[string]*bucket)}

// allowPeerRequest mirrors allowRequest's token-bucket logic exactly,
// against its own separate bucket map and its own (currently
// identical, but independently tunable) constants - deliberately not
// sharing a helper with allowRequest yet, since general-API and
// gossip-specific budgets are conceptually different limits that may
// need to diverge in size later.
func allowPeerRequest(remoteAddr string) bool {
	ip := remoteAddr
	if idx := strings.LastIndex(remoteAddr, ":"); idx != -1 {
		ip = remoteAddr[:idx]
	}

	peerRateLimiter.mu.Lock()
	defer peerRateLimiter.mu.Unlock()

	now := time.Now()

	b, ok := peerRateLimiter.buckets[ip]
	if !ok {
		b = &bucket{tokens: peerRateLimitBurst, lastRefill: now}
		peerRateLimiter.buckets[ip] = b
	}

	elapsed := now.Sub(b.lastRefill).Seconds()
	b.tokens += elapsed * peerRateLimitSustainedPerSec
	if b.tokens > peerRateLimitBurst {
		b.tokens = peerRateLimitBurst
	}
	b.lastRefill = now

	// Lazily drop long-idle buckets, same convention as allowRequest.
	if len(peerRateLimiter.buckets) > 10000 {
		for k, v := range peerRateLimiter.buckets {
			if now.Sub(v.lastRefill) > 10*time.Minute {
				delete(peerRateLimiter.buckets, k)
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

// maxCertificateUploadRequestBytes is a narrow, per-route exception to
// maxRequestBodyBytes for exactly one endpoint
// (/companies/upload-certificate) - a real certificate file
// (maxCertificateFileBytes = 5MB, see api/companies.go) travels
// base64-encoded inside the signed JSON envelope this codebase's auth
// model already uses (see requireSignedRequest's doc comment on why
// this isn't a raw multipart upload), which costs ~33% size overhead
// plus room for the rest of the envelope - sized generously above
// that. Every other route keeps the tight 64KB default.
const maxCertificateUploadRequestBytes = 7 * 1024 * 1024
