package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/creditchain/creditchaind/core"
)

// BTC bridge (BTCB) is deliberately self-custodial on the deposit side,
// unlike ETHB's KMS-custodial corner - CreditChain never generates or
// holds a BTC private key (see core/btc_address.go's doc comment). This
// file's register-address handler is purely node-local bookkeeping
// (which address to watch), same "not gossiped, persisted per-node
// only" shape as api/eth_custodial.go's EthCustodialStore - see that
// file's top-of-file comment for the reasoning this mirrors.

type btcRegisterAddressData struct {
	BtcAddress string `json:"btcAddress"`
}

// HandleBtcRegisterAddress lets a wallet tell this node which
// self-custodial BTC testnet address (already derived client-side by
// website/js/hdwallet.js's deriveBtcTestnetAddress, and already shown
// on the Assets/Receive page) to watch for deposits. Synchronous, not
// routed through recordTx/ApplyTransaction - this moves no funds and
// touches no consensus-critical state, exactly like
// HandleCreateEthCustodialAccount.
func HandleBtcRegisterAddress(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req btcRegisterAddressData
	envelope, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	btcAddress := strings.TrimSpace(req.BtcAddress)
	// Loose shape check only - native segwit addresses are the only
	// kind this bridge ever watches or later sends from (see the send
	// flow's own scope note), a wrong/malformed value here just means
	// deposit-detection harmlessly never finds a match, never a
	// security concern (see core.BtcWatchedAddress's doc comment).
	// Expected prefix depends on which real Bitcoin network this node
	// is configured against (bc1... mainnet, tb1... testnet) - this
	// used to hardcode tb1 unconditionally, which meant a real mainnet
	// bc1... address could never be registered for deposit-watching at
	// all on a mainnet deployment.
	expectedPrefix := "tb1"
	if mainnetMode() {
		expectedPrefix = "bc1"
	}
	if !strings.HasPrefix(btcAddress, expectedPrefix) || len(btcAddress) < 14 || len(btcAddress) > 90 {
		http.Error(w, "btcAddress must be a native segwit "+expectedPrefix+"... address for this network", http.StatusBadRequest)
		return
	}

	btcAddressStore.Set(&core.BtcWatchedAddress{
		CreditChainAddress:   address,
		CreditChainPublicKey: envelope.PublicKey,
		BtcAddress:           btcAddress,
		CreatedAt:            time.Now().Unix(),
	})

	json.NewEncoder(w).Encode(map[string]string{
		"status":     "registered",
		"btcAddress": btcAddress,
	})
}

// HandleBtcWatchedAddress is a lightweight lookup of which BTC address
// (if any) this wallet has registered - lets the frontend confirm
// registration succeeded / avoid re-registering every page load.
func HandleBtcWatchedAddress(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	address := r.URL.Query().Get("address")
	watched, ok := btcAddressStore.Get(address)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"btcAddress":       watched.BtcAddress,
		"lastCreditedSats": watched.LastCreditedSats,
	})
}

// HandleBtcbBalance mirrors HandleEthbBalance for bridgedBtcStore -
// satoshi amounts stay well within JS's safe-integer range even at
// Bitcoin's full 21M-BTC supply, so unlike ETHB's wei balances this is
// safe to return as a plain JSON number.
func HandleBtcbBalance(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	address := r.URL.Query().Get("address")
	balance := bridgedBtcStore.BalanceOf(address)

	json.NewEncoder(w).Encode(map[string]uint64{
		"balance": balance,
	})
}

// HandleBtcUtxos proxies btcAddressUtxos for the client-side send flow
// (B.4/B.8) - server-side so the browser never needs to talk to
// Blockstream directly for this (consistent with keeping all Esplora
// calls server-side, see btcAPIBase's doc comment).
func HandleBtcUtxos(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	address := r.URL.Query().Get("address")
	if address == "" {
		http.Error(w, "address is required", http.StatusBadRequest)
		return
	}

	utxos, err := btcAddressUtxos(address)
	if err != nil {
		http.Error(w, "failed to fetch UTXOs: "+err.Error(), http.StatusBadGateway)
		return
	}

	json.NewEncoder(w).Encode(utxos)
}

// btcBridgeDepositPollInterval mirrors ethBridgeDepositPollInterval's
// reasoning exactly - a separate, slower ticker than the consensus
// loop, since BTC testnet deposits aren't latency-sensitive and the
// public Esplora API shouldn't be hammered.
const btcBridgeDepositPollInterval = 30 * time.Second

// btcBridgeDepositReportData is BTCB's deposit-report payload - same
// "report the observed ABSOLUTE balance, not a delta" shape as
// bridgeDepositReportData (api/eth_bridge.go), for the identical
// replay-safety reason: the apply side computes and credits the delta
// itself from the address's own already-applied baseline.
type btcBridgeDepositReportData struct {
	CreditChainAddress string `json:"creditChainAddress"`
	NewBalanceSats     uint64 `json:"newBalanceSats"`
}

// startBtcBridgeDepositLoop polls every registered BTC address's real
// testnet balance and reports any increase as a TxBridgeBtcDeposit -
// see applyBridgeBtcDeposit (api/apply.go). Inherits the same
// disclosed non-gossip limitation as the ETH bridge's own loop: only
// the node a given address was registered on will ever poll/report
// deposits to it, but once a report IS submitted it's a normal
// gossiped/confirmed transaction, so every node converges on the same
// BTCB credit.
func startBtcBridgeDepositLoop() {
	go func() {
		ticker := time.NewTicker(btcBridgeDepositPollInterval)
		defer ticker.Stop()

		for range ticker.C {
			for _, watched := range btcAddressStore.Snapshot() {
				pollAndReportBtcDeposit(watched)
			}
		}
	}()
}

// HandleBtcFeeEstimate proxies btcFeeEstimateSatsPerVByte - the BTC
// bridge's own live fee tracker (independent of Section A's ETH gas
// tracker, which only affects ETHB). The frontend uses this to size
// the fee budget it hands to BtcTx.signAndBuildTx before signing.
func HandleBtcFeeEstimate(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	rate, err := btcFeeEstimateSatsPerVByte()
	if err != nil {
		http.Error(w, "failed to fetch fee estimate: "+err.Error(), http.StatusBadGateway)
		return
	}

	json.NewEncoder(w).Encode(map[string]float64{"satsPerVByte": rate})
}

// btcBroadcastData is now a signed request (see HandleBtcBroadcast's
// doc comment for why this changed from the original unauthenticated
// relay) - To/AmountSats/FeeSats are purely descriptive metadata for
// the withdrawal RECORD this handler now creates; they play no part in
// validating or authorizing the raw Bitcoin broadcast itself, which
// remains governed entirely by RawTxHex's own independent, already-
// complete Bitcoin-level signature.
type btcBroadcastData struct {
	RawTxHex   string `json:"rawTxHex"`
	To         string `json:"to"`
	AmountSats uint64 `json:"amountSats"`
	FeeSats    uint64 `json:"feeSats"`
}

// HandleBtcBroadcast relays an already-fully-signed raw Bitcoin
// transaction to the real testnet network - it never constructs,
// signs, or holds a Bitcoin key, same as
// api/eth_selfcustody.go's HandleEthSelfCustodyBroadcast. The real
// authorization boundary for the MONEY MOVEMENT itself is still Bitcoin
// alone: this relay can only ever move funds if the caller already
// supplies a validly-signed raw transaction, which every Bitcoin node
// re-verifies independently regardless of what wraps this call.
//
// This IS now gated behind requireSignedRequest, though - a change from
// this handler's original design - purely to learn WHICH CreditChain
// wallet is claiming responsibility for the withdrawal, so it can be
// recorded (see core.TxBridgeBtcWithdraw's doc comment: today a BTCB
// withdrawal is otherwise completely invisible to the explorer, with no
// record anywhere that it ever happened). This tightens rather than
// loosens the endpoint - a broadcast now requires BOTH a genuine
// CreditChain wallet signature AND a genuine already-complete Bitcoin
// signature, not fewer checks than before.
func HandleBtcBroadcast(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req btcBroadcastData
	auth, from, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}
	if req.RawTxHex == "" {
		http.Error(w, "rawTxHex is required", http.StatusBadRequest)
		return
	}

	txid, err := btcBroadcastRawTx(req.RawTxHex)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{
			"status": "broadcast_failed",
			"error":  err.Error(),
		})
		return
	}

	// The broadcast already succeeded and txid is already known, so
	// unlike the ETH side this is a single recordTx call - no earlier
	// placeholder transaction and no later follow-up needed.
	withdrawData, _ := json.Marshal(bridgeBtcWithdrawData{
		bridgeBtcWithdrawRequest: bridgeBtcWithdrawRequest{
			To:         req.To,
			AmountSats: req.AmountSats,
			FeeSats:    req.FeeSats,
		},
		BtcTxid: txid,
	})
	recordTx(auth, from, req.To, req.AmountSats, core.TxBridgeBtcWithdraw, string(withdrawData))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "broadcast",
		"txid":   txid,
	})
}

func pollAndReportBtcDeposit(watched *core.BtcWatchedAddress) {
	current, err := btcAddressBalanceSats(watched.BtcAddress)
	if err != nil {
		fmt.Println("btc bridge deposit poll failed for", watched.BtcAddress, ":", err)
		return
	}

	if current <= watched.LastCreditedSats {
		return // no new deposit since the last credited balance
	}

	data, _ := json.Marshal(btcBridgeDepositReportData{
		CreditChainAddress: watched.CreditChainAddress,
		NewBalanceSats:     current,
	})

	fmt.Println("reporting btc bridge deposit for", watched.CreditChainAddress, "new balance", current, "sats")

	recordTx(selfSignedEnvelope(data), reporterAddress(), "", 0, core.TxBridgeBtcDeposit, string(data))
}
