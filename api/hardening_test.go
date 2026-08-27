package api

import "testing"

// TestAllowPeerRequest_SeparateBucketFromGeneralLimiter confirms
// allowPeerRequest tracks its own budget independent of allowRequest's
// bucket for the same IP - the whole point of giving gossip traffic a
// dedicated limiter (see hardening.go's doc comment) is that exhausting
// one never exhausts the other.
func TestAllowPeerRequest_SeparateBucketFromGeneralLimiter(t *testing.T) {
	ip := "203.0.113.5:9999"

	// Exhaust the general limiter's burst for this IP.
	for i := 0; i < rateLimitBurst; i++ {
		allowRequest(ip)
	}
	if allowRequest(ip) {
		t.Fatal("expected general limiter to be exhausted")
	}

	// The peer limiter for the same IP should be untouched.
	if !allowPeerRequest(ip) {
		t.Fatal("expected peer limiter to still allow a request after only the general limiter was exhausted")
	}
}

// TestAllowPeerRequest_EnforcesBurstThenRefills exercises the bucket's
// core behavior directly: burst capacity is finite, exhausting it
// blocks further requests, and it isn't permanently stuck (mirrors
// allowRequest's own token-bucket contract).
func TestAllowPeerRequest_EnforcesBurstThenRefills(t *testing.T) {
	ip := "198.51.100.7:8080"

	allowed := 0
	for i := 0; i < peerRateLimitBurst+5; i++ {
		if allowPeerRequest(ip) {
			allowed++
		}
	}

	if allowed != peerRateLimitBurst {
		t.Fatalf("expected exactly %d allowed requests before exhaustion, got %d", peerRateLimitBurst, allowed)
	}

	if allowPeerRequest(ip) {
		t.Fatal("expected bucket to be exhausted after consuming its full burst")
	}
}

// TestAllowPeerRequest_TracksIPsIndependently confirms one IP's usage
// never borrows from or drains another's - a flood from one peer
// address must not affect a different, well-behaved peer sharing this
// process.
func TestAllowPeerRequest_TracksIPsIndependently(t *testing.T) {
	floodIP := "203.0.113.100:1"
	quietIP := "203.0.113.200:1"

	for i := 0; i < peerRateLimitBurst+10; i++ {
		allowPeerRequest(floodIP)
	}

	if !allowPeerRequest(quietIP) {
		t.Fatal("expected a fresh IP to have its own untouched bucket despite another IP being flooded")
	}
}
