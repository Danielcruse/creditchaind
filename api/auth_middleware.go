package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/creditchain/creditchaind/core"
)

// seenSignatures guards against replaying an identical, otherwise
// still-valid signed request (e.g. a captured request re-sent before
// its timestamp expires). Pruned lazily on insert - simple and
// adequate at this scale rather than a background sweeper.
var seenSignatures = struct {
	mu   sync.Mutex
	seen map[string]time.Time
}{seen: make(map[string]time.Time)}

func rememberSignature(sig string) bool {
	seenSignatures.mu.Lock()
	defer seenSignatures.mu.Unlock()

	now := time.Now()

	for s, t := range seenSignatures.seen {
		if now.Sub(t) > core.RequestTimestampWindow {
			delete(seenSignatures.seen, s)
		}
	}

	if _, ok := seenSignatures.seen[sig]; ok {
		return false
	}

	seenSignatures.seen[sig] = now
	return true
}

// requireSignedRequest is what every mutating handler calls instead
// of decoding the request body directly. It returns the VERIFIED
// envelope and address - callers must use the address, not any
// "address"/"from" field inside the payload (that's exactly what an
// attacker would forge otherwise), and must pass the envelope through
// to recordTx so the resulting transaction carries its own
// independently-reverifiable proof of authenticity (see
// core.Transaction's Auth* fields) instead of only being trustworthy
// to the one node that happened to receive this HTTP request.
func requireSignedRequest(r *http.Request, out any) (core.SignedEnvelope, string, error) {
	var envelope core.SignedEnvelope

	if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
		return core.SignedEnvelope{}, "", errors.New("invalid request envelope")
	}

	if err := envelope.Verify(); err != nil {
		return core.SignedEnvelope{}, "", err
	}

	if !rememberSignature(envelope.Signature) {
		return core.SignedEnvelope{}, "", errors.New("duplicate/replayed request")
	}

	if out != nil {
		if err := json.Unmarshal(envelope.Payload, out); err != nil {
			return core.SignedEnvelope{}, "", errors.New("invalid payload")
		}
	}

	return envelope, envelope.Address, nil
}

func writeUnauthorized(w http.ResponseWriter, err error) {
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
