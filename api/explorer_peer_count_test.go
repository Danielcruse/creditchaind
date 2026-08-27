package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/creditchain/creditchaind/core"
)

// TestHandleExplorer_PeerCountMatchesPeerStore is a regression test for
// the Explorer stats bar previously having no peer-count field at all
// (unlike /network's HandleNetwork, which already exposed one via
// peerStore.List() - see api/network.go). Confirms /explorer's
// peerCount reflects the same live peerStore, not a hardcoded/zero
// placeholder.
func TestHandleExplorer_PeerCountMatchesPeerStore(t *testing.T) {
	resetApplyTestState()
	blockchain = &core.Blockchain{Blocks: []core.Block{{Index: 0, Hash: "genesis-hash", Timestamp: time.Now().Unix()}}}
	peerStore = core.NewPeerStore()

	peerStore.Add(core.Peer{ID: "peer-a", Address: "http://peer-a.example.com:8080", PublicKey: "aa"})
	peerStore.Add(core.Peer{ID: "peer-b", Address: "http://peer-b.example.com:8080", PublicKey: "bb"})

	req := httptest.NewRequest(http.MethodGet, "/explorer", nil)
	w := httptest.NewRecorder()
	HandleExplorer(w, req)

	var resp ExplorerResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.PeerCount != 2 {
		t.Fatalf("expected peerCount 2 (matching peerStore.List()), got %d", resp.PeerCount)
	}
}

// TestHandleExplorer_PeerCountZeroWhenNoPeers confirms an empty
// peerStore reports 0, not an omitted/null field - a client shouldn't
// need to null-guard this the way AddressProfile's doc comment already
// establishes for its own list fields.
func TestHandleExplorer_PeerCountZeroWhenNoPeers(t *testing.T) {
	resetApplyTestState()
	blockchain = &core.Blockchain{Blocks: []core.Block{{Index: 0, Hash: "genesis-hash", Timestamp: time.Now().Unix()}}}
	peerStore = core.NewPeerStore()

	req := httptest.NewRequest(http.MethodGet, "/explorer", nil)
	w := httptest.NewRecorder()
	HandleExplorer(w, req)

	var resp ExplorerResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.PeerCount != 0 {
		t.Fatalf("expected peerCount 0 with an empty peerStore, got %d", resp.PeerCount)
	}
}
