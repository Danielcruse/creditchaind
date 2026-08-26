package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// btcAPIBase is Blockstream's public Esplora REST API for Bitcoin -
// testnet by default, configurable via BTC_API_BASE_URL so a mainnet
// instance can point at the real Blockstream mainnet API
// (https://blockstream.info/api) instead. The default stays testnet
// (unlike website/js/defi.js's unrelated mainnet address-lookup viewer)
// so an unconfigured node's behavior is unchanged from before this was
// made configurable. Which network's addresses actually get watched/sent
// from is a separate concern - see website/js/hdwallet.js's
// deriveBtcAddress(seed, network) for the matching BIP-84 coin-type/hrp
// choice, which must agree with whichever API base is set here. Server-
// side (not called directly from the browser) so the deposit-detection
// loop can run without a browser open - the same reasoning
// api/eth_rpc.go's RPC client is server-side, and unlike defi.js's
// read-only viewer which has no such background-polling need.
func btcAPIBase() string {
	if url := os.Getenv("BTC_API_BASE_URL"); url != "" {
		return url
	}
	return "https://blockstream.info/testnet/api"
}

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
// from. BlockHeight is additionally needed by the vault watch loop
// (api/vault_btc_watch.go, C.5) to compute confirmation depth - Esplora
// already includes it in every confirmed UTXO's status object, this
// struct simply wasn't capturing it until the vault watch loop needed
// it.
type BtcUtxo struct {
	Txid   string `json:"txid"`
	Vout   uint32 `json:"vout"`
	Value  uint64 `json:"value"`
	Status struct {
		Confirmed   bool   `json:"confirmed"`
		BlockHeight uint32 `json:"block_height"`
	} `json:"status"`
}

// btcChainTipHeight returns the current confirmed chain tip height -
// Esplora's /blocks/tip/height returns this as a bare plain-text integer
// (not JSON), unlike every other endpoint this file calls.
func btcChainTipHeight() (uint32, error) {
	resp, err := btcHTTPClient.Get(btcAPIBase() + "/blocks/tip/height")
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("btc api /blocks/tip/height: status %d: %s", resp.StatusCode, string(body))
	}
	height, err := strconv.ParseUint(strings.TrimSpace(string(body)), 10, 32)
	if err != nil {
		return 0, fmt.Errorf("btc api /blocks/tip/height: unparseable response %q: %w", string(body), err)
	}
	return uint32(height), nil
}

// btcTxOutDetail mirrors one entry of Esplora's /tx/:txid `vout` array -
// enough fields to identify which output pays the vault's P2WSH address
// and which (if any) is an OP_RETURN deposit memo.
type btcTxOutDetail struct {
	ScriptPubKey        string `json:"scriptpubkey"`
	ScriptPubKeyAddress string `json:"scriptpubkey_address"`
	ScriptPubKeyType    string `json:"scriptpubkey_type"`
	Value               uint64 `json:"value"`
}

// btcTxDetail mirrors the subset of Esplora's /tx/:txid response the
// vault watch loop needs.
type btcTxDetail struct {
	Txid   string           `json:"txid"`
	Vout   []btcTxOutDetail `json:"vout"`
	Status struct {
		Confirmed   bool   `json:"confirmed"`
		BlockHeight uint32 `json:"block_height"`
	} `json:"status"`
}

// btcTxByID fetches full transaction detail (all outputs, confirmation
// status) - the vault UTXO-set endpoint alone can't answer "does this
// deposit carry an OP_RETURN memo naming its CreditChain depositor",
// since OP_RETURN outputs are provably unspendable and never appear in
// an address's own UTXO set.
func btcTxByID(txid string) (*btcTxDetail, error) {
	var tx btcTxDetail
	if err := btcAPIGet(fmt.Sprintf("/tx/%s", txid), &tx); err != nil {
		return nil, err
	}
	return &tx, nil
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
	resp, err := btcHTTPClient.Post(btcAPIBase()+"/tx", "text/plain", bytes.NewBufferString(rawTxHex))
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
	resp, err := btcHTTPClient.Get(btcAPIBase() + path)
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
