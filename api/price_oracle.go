package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// priceOracleCache holds the last-known external spot prices - purely
// node-local, read-only market data (not gossiped/consensus state,
// same category as website/js/defi.js's existing client-side
// Blockstream lookups). If a fetch fails, the cache simply isn't
// updated - callers always see the last-known-good values (with a
// stale UpdatedAt) rather than an error, so a transient outage never
// breaks anything that reads this.
type priceOracleData struct {
	EthUsd    float64 `json:"ethUsd"`
	BtcUsd    float64 `json:"btcUsd"`
	UsdcUsd   float64 `json:"usdcUsd"`
	UpdatedAt int64   `json:"updatedAt"`
}

var (
	priceOracleMu    sync.Mutex
	priceOracleCache priceOracleData
)

const priceOraclePollInterval = 60 * time.Second

// coingeckoSimplePriceURL is CoinGecko's free, keyless "simple price"
// endpoint - no API key needed, matching this project's existing "no
// paid external dependency" bar.
const coingeckoSimplePriceURL = "https://api.coingecko.com/api/v3/simple/price?ids=ethereum,bitcoin,usd-coin&vs_currencies=usd"

func startPriceOracleLoop() {
	go func() {
		pollExternalPrices()

		ticker := time.NewTicker(priceOraclePollInterval)
		defer ticker.Stop()

		for range ticker.C {
			pollExternalPrices()
		}
	}()
}

func pollExternalPrices() {
	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(coingeckoSimplePriceURL)
	if err != nil {
		fmt.Println("price oracle fetch failed:", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Println("price oracle fetch failed: status", resp.StatusCode)
		return
	}

	var parsed struct {
		Ethereum struct {
			Usd float64 `json:"usd"`
		} `json:"ethereum"`
		Bitcoin struct {
			Usd float64 `json:"usd"`
		} `json:"bitcoin"`
		UsdCoin struct {
			Usd float64 `json:"usd"`
		} `json:"usd-coin"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		fmt.Println("price oracle decode failed:", err)
		return
	}

	priceOracleMu.Lock()
	priceOracleCache = priceOracleData{
		EthUsd:    parsed.Ethereum.Usd,
		BtcUsd:    parsed.Bitcoin.Usd,
		UsdcUsd:   parsed.UsdCoin.Usd,
		UpdatedAt: time.Now().Unix(),
	}
	priceOracleMu.Unlock()
}

// HandleExternalPrice is a public read (no auth) - a cached, purely
// informational reference price, never an authority over any real
// on-chain rate (HubRate/order book pricing stays exactly as today).
func HandleExternalPrice(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	priceOracleMu.Lock()
	data := priceOracleCache
	priceOracleMu.Unlock()

	json.NewEncoder(w).Encode(data)
}
