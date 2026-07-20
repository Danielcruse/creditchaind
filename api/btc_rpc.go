package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// btcAPIBase is Blockstream's public Esplora REST API for Bitcoin
// TESTNET - deliberately the testnet path (unlike website/js/defi.js's
// unrelated mainnet address-lookup viewer), since every BTCB address
// this bridge ever watches or sends from is a bech32 testnet (tb1...)
// address derived per website/js/hdwallet.js's BIP-84 coin-type-1'
// path. Server-side (not called directly from the browser) so the
// deposit-detection loop can run without a browser open - the same
// reasoning api/eth_rpc.go's RPC client is server-side, and unlike
// defi.js's read-only viewer which has no such background-polling need.
const btcAPIBase = "https://blockstream.info/testnet/api"

var btcHTTPClient = &http.Client{Timeout: 15 * time.Second}

// btcAddressStats mirrors the subset of Esplora's
// /address/:address response this bridge actually needs.
type btcAddressStats struct {
	ChainStats struct {
		FundedTxoSum uint64 `json:"funded_txo_sum"`
		SpentTxoSum  uint64 `json:"spent_txo_sum"`
	} `json:"chain_stats"`
	MempoolStats struct {
		FundedTxoSum uint64 `json:"funded_txo_sum"`
		SpentTxoSum  uint64 `json:"spent_txo_sum"`
	} `json:"mempool_stats"`
}

// btcAddressBalanceSats returns addr's current CONFIRMED balance in
// satoshis - deliberately chain_stats only, not mempool_stats, so
// deposit-detection (api/btc_bridge.go) only ever credits BTCB for
// deposits that have actually confirmed, never an unconfirmed mempool
// transaction that could still be replaced/dropped.
func btcAddressBalanceSats(addr string) (uint64, error) {
	var stats btcAddressStats
	if err := btcAPIGet(fmt.Sprintf("/address/%s", addr), &stats); err != nil {
		return 0, err
	}
	if stats.ChainStats.SpentTxoSum > stats.ChainStats.FundedTxoSum {
		return 0, nil // shouldn't happen, defensive floor at zero
	}
	return stats.ChainStats.FundedTxoSum - stats.ChainStats.SpentTxoSum, nil
}

// BtcUtxo mirrors Esplora's /address/:address/utxo entry shape - the
// fields B.4's client-side transaction-building code needs to spend
// from.
type BtcUtxo struct {
	Txid   string `json:"txid"`
	Vout   uint32 `json:"vout"`
	Value  uint64 `json:"value"`
	Status struct {
		Confirmed bool `json:"confirmed"`
	} `json:"status"`
}

// btcAddressUtxos returns every UTXO currently sitting at addr -
// callers (HandleBtcUtxos) are responsible for filtering to confirmed-
// only if that matters for their use case; returned as-is here so the
// caller can decide (e.g. showing an unconfirmed-incoming amount is
// still useful information, just not yet spendable).
func btcAddressUtxos(addr string) ([]BtcUtxo, error) {
	var utxos []BtcUtxo
	if err := btcAPIGet(fmt.Sprintf("/address/%s/utxo", addr), &utxos); err != nil {
		return nil, err
	}
	return utxos, nil
}

// btcBroadcastRawTx submits a raw signed transaction (hex-encoded) to
// the network - Esplora's /tx endpoint takes the raw hex as a plain-
// text POST body (not JSON) and returns the resulting txid as plain
// text on success, or a human-readable rejection reason (e.g.
// "bad-txns-inputs-missingorspent") as the response body on failure.
func btcBroadcastRawTx(rawTxHex string) (string, error) {
	resp, err := btcHTTPClient.Post(btcAPIBase+"/tx", "text/plain", bytes.NewBufferString(rawTxHex))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("broadcast rejected: %s", string(body))
	}
	return string(body), nil
}

// btcFeeEstimateSatsPerVByte returns Esplora's live fee estimate for a
// confirmation target of ~6 blocks - this bridge's own "live tracker
// for network fees" (distinct from and independent of Section A's ETH
// gas-price tracker, which only affects the ERC-network ETHB
// withdrawal fee). Falls back to a small illustrative default if the
// estimate endpoint doesn't have that exact target bucketed (rare, but
// the API only guarantees the buckets it currently has data for).
func btcFeeEstimateSatsPerVByte() (float64, error) {
	var estimates map[string]float64
	if err := btcAPIGet("/fee-estimates", &estimates); err != nil {
		return 0, err
	}
	if v, ok := estimates["6"]; ok {
		return v, nil
	}
	// Fall back to whichever bucket IS present, closest to 6 - the map
	// is small (a couple dozen confirmation-target keys at most), a
	// linear scan is fine.
	var best float64
	bestDist := 1 << 30
	for k, v := range estimates {
		target, err := strconv.Atoi(k)
		if err != nil {
			continue
		}
		dist := target - 6
		if dist < 0 {
			dist = -dist
		}
		if dist < bestDist {
			bestDist = dist
			best = v
		}
	}
	if best == 0 {
		return 0, fmt.Errorf("no fee estimates available")
	}
	return best, nil
}

func btcAPIGet(path string, out interface{}) error {
	resp, err := btcHTTPClient.Get(btcAPIBase + path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("btc api %s: status %d: %s", path, resp.StatusCode, string(body))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
