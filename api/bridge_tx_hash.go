package api

import (
	"encoding/json"
	"sync"

	"github.com/creditchain/creditchaind/core"
)

// bridgeExternalTxHashes maps an original TxBridgeWithdraw transaction's
// ID to the real Sepolia tx hash it ultimately broadcast as. This is a
// separate side-store, not a mutation of the original transaction's own
// Data field - that Data is already fixed and hashed into a confirmed
// block at recordTx time, BEFORE the real broadcast happens and its
// hash becomes known (see HandleBridgedEthWithdraw), so mutating it in
// place after the fact would silently break every node's block-hash
// verification on reload. Recorded via a follow-up self-signed
// TxBridgeTxHashRecord transaction instead (see recordBridgeTxHash),
// exactly the same "report it as its own transaction, don't rewrite
// history" shape TxSlashReport already established for reputation
// penalties.
//
// A pure last-write-wins map set needs no dedup guard the way
// slashDedup's cumulative counters do - applying the same record twice
// (e.g. replaying a chain from genesis) just writes the identical value
// again, harmlessly.
var bridgeExternalTxHashes = struct {
	mu   sync.Mutex
	hash map[string]string // original TxBridgeWithdraw ID -> real external tx hash
}{hash: make(map[string]string)}

func recordBridgeExternalTxHash(originalTxID, externalHash string) {
	bridgeExternalTxHashes.mu.Lock()
	defer bridgeExternalTxHashes.mu.Unlock()
	bridgeExternalTxHashes.hash[originalTxID] = externalHash
}

func lookupBridgeExternalTxHash(originalTxID string) (string, bool) {
	bridgeExternalTxHashes.mu.Lock()
	defer bridgeExternalTxHashes.mu.Unlock()
	h, ok := bridgeExternalTxHashes.hash[originalTxID]
	return h, ok
}

func snapshotBridgeExternalTxHashes() map[string]string {
	bridgeExternalTxHashes.mu.Lock()
	defer bridgeExternalTxHashes.mu.Unlock()
	out := make(map[string]string, len(bridgeExternalTxHashes.hash))
	for k, v := range bridgeExternalTxHashes.hash {
		out[k] = v
	}
	return out
}

func restoreBridgeExternalTxHashes(m map[string]string) {
	bridgeExternalTxHashes.mu.Lock()
	defer bridgeExternalTxHashes.mu.Unlock()
	if m == nil {
		bridgeExternalTxHashes.hash = make(map[string]string)
		return
	}
	bridgeExternalTxHashes.hash = m
}

// bridgeTxHashRecordData is both the AuthPayload and the Data of a
// TxBridgeTxHashRecord - the original withdrawal's transaction ID and
// the real external-chain tx hash it broadcast as.
type bridgeTxHashRecordData struct {
	OriginalTxID string `json:"originalTxId"`
	ExternalHash string `json:"externalHash"`
}

// recordBridgeTxHash submits the follow-up TxBridgeTxHashRecord once a
// real broadcast succeeds - node-authored via selfSignedEnvelope, same
// as reportMissedBlock/pollAndReportBridgeDeposit, since there's no
// wallet-originated HTTP caller for this specific fact (the original
// withdrawal request already came and went before the hash was known).
func recordBridgeTxHash(originalTxID, externalHash string) {
	data, _ := json.Marshal(bridgeTxHashRecordData{
		OriginalTxID: originalTxID,
		ExternalHash: externalHash,
	})
	recordTx(selfSignedEnvelope(data), reporterAddress(), "", 0, core.TxBridgeTxHashRecord, string(data))
}

func applyBridgeTxHashRecord(tx core.Transaction) {
	var data bridgeTxHashRecordData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if data.OriginalTxID == "" || data.ExternalHash == "" {
		return
	}
	recordBridgeExternalTxHash(data.OriginalTxID, data.ExternalHash)
}

// bridgeBtcWithdrawRequest is exactly what the client signs (via
// btcBroadcastData in api/btc_bridge.go, itself carrying rawTxHex/to/
// amountSats/feeSats) - here narrowed to just the three fields also
// present on bridgeBtcWithdrawData below, deliberately excluding
// BtcTxid. verifyTxAuth's TxBridgeBtcWithdraw case (api/apply.go) must
// compare tx.AuthPayload against tx.Data using ONLY this narrower type:
// BtcTxid isn't known until AFTER btcBroadcastRawTx succeeds, so it's
// never part of what the client signs (decodes to "" from
// tx.AuthPayload) but always a real txid in tx.Data - comparing the
// full bridgeBtcWithdrawData struct instead (as this code originally
// did) meant signed==claimed was false for every genuine BTC-network
// withdrawal, silently rejecting each one as "unauthorized" the moment
// the broadcast actually succeeded.
type bridgeBtcWithdrawRequest struct {
	To         string `json:"to"`
	AmountSats uint64 `json:"amountSats"`
	FeeSats    uint64 `json:"feeSats"`
}

// bridgeBtcWithdrawData is the Data of a TxBridgeBtcWithdraw -
// bridgeBtcWithdrawRequest (what was signed) plus BtcTxid, which
// HandleBtcBroadcast only learns after the real broadcast already
// succeeded - see bridgeBtcWithdrawRequest's doc comment above for why
// that field must be excluded from the auth comparison.
type bridgeBtcWithdrawData struct {
	bridgeBtcWithdrawRequest
	BtcTxid string `json:"btcTxid"`
}

// applyBridgeBtcWithdraw intentionally touches no balance - a BTC-
// network send already moved real UTXOs directly on the Bitcoin network
// itself, entirely outside CreditChain's ledger (see this type's doc
// comment in core/transactions.go). This exists purely so the
// withdrawal has a permanent, explorer-visible record with a real
// external txid attached.
func applyBridgeBtcWithdraw(tx core.Transaction) {}
