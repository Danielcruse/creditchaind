package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// supportedCurrencies is the fixed set of fiat currencies this node
// tracks alongside USD - a judgment call on a reasonable starter list,
// not derived from any spec, and easy to extend later since the
// frontend's selector is driven by HandleSupportedCurrencies rather
// than a duplicated hardcoded list of its own.
var supportedCurrencies = []string{"USD", "EUR", "GBP", "NGN", "CAD", "JPY", "INR"}

// currencyRates holds one currency's spot rates for the three assets
// this oracle tracks.
type currencyRates struct {
	Eth  float64
	Btc  float64
	Usdc float64
}

// priceOracleData holds the last-known external spot prices, one set
// per supported currency - purely node-local, read-only market data
// (not gossiped/consensus state, same category as website/js/defi.js's
// existing client-side Blockstream lookups). If a fetch fails, the
// cache simply isn't updated - callers always see the last-known-good
// values (with a stale UpdatedAt) rather than an error, so a transient
// outage never breaks anything that reads this.
type priceOracleData struct {
	Rates     map[string]currencyRates
	UpdatedAt int64
}

var (
	priceOracleMu    sync.Mutex
	priceOracleCache priceOracleData
)

const priceOraclePollInterval = 60 * time.Second

// coingeckoSimplePriceURL is CoinGecko's free, keyless "simple price"
// endpoint - no API key needed, matching this project's existing "no
// paid external dependency" bar. vs_currencies takes a comma-separated
// list in one call, so every supported currency is fetched together.
const coingeckoSimplePriceURL = "https://api.coingecko.com/api/v3/simple/price?ids=ethereum,bitcoin,usd-coin&vs_currencies=usd,eur,gbp,ngn,cad,jpy,inr"

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
		Ethereum map[string]float64 `json:"ethereum"`
		Bitcoin  map[string]float64 `json:"bitcoin"`
		UsdCoin  map[string]float64 `json:"usd-coin"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		fmt.Println("price oracle decode failed:", err)
		return
	}

	rates := make(map[string]currencyRates, len(supportedCurrencies))
	for _, cur := range supportedCurrencies {
		key := strings.ToLower(cur)
		rates[cur] = currencyRates{
			Eth:  parsed.Ethereum[key],
			Btc:  parsed.Bitcoin[key],
			Usdc: parsed.UsdCoin[key],
		}
	}

	priceOracleMu.Lock()
	priceOracleCache = priceOracleData{Rates: rates, UpdatedAt: time.Now().Unix()}
	priceOracleMu.Unlock()
}

// legacyUsdPriceResponse is HandleExternalPrice's default (no
// ?currency= param) response shape - kept byte-compatible with the
// original USD-only oracle so existing callers (p2p.js's hub-rate
// reference line, send-bridged-eth.js/send-bridged-btc.js's fee
// previews) that want USD specifically for gas/fee estimates see no
// change at all.
type legacyUsdPriceResponse struct {
	EthUsd    float64 `json:"ethUsd"`
	BtcUsd    float64 `json:"btcUsd"`
	UsdcUsd   float64 `json:"usdcUsd"`
	UpdatedAt int64   `json:"updatedAt"`
}

// currencyPriceResponse is HandleExternalPrice's response shape when a
// specific ?currency= is requested - generic field names since it's no
// longer USD-specific.
type currencyPriceResponse struct {
	Eth       float64 `json:"eth"`
	Btc       float64 `json:"btc"`
	Usdc      float64 `json:"usdc"`
	Currency  string  `json:"currency"`
	UpdatedAt int64   `json:"updatedAt"`
}

// HandleExternalPrice is a public read (no auth) - a cached, purely
// informational reference price, never an authority over any real
// on-chain rate (HubRate/order book pricing stays exactly as today).
// With no ?currency= param it returns the original USD-only shape;
// with one, it returns that currency's rates instead.
func HandleExternalPrice(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	currency := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("currency")))

	priceOracleMu.Lock()
	rates := priceOracleCache.Rates
	updatedAt := priceOracleCache.UpdatedAt
	priceOracleMu.Unlock()

	if currency == "" {
		usd := rates["USD"]
		json.NewEncoder(w).Encode(legacyUsdPriceResponse{
			EthUsd:    usd.Eth,
			BtcUsd:    usd.Btc,
			UsdcUsd:   usd.Usdc,
			UpdatedAt: updatedAt,
		})
		return
	}

	cr, ok := rates[currency]
	if !ok {
		http.Error(w, "unsupported currency", http.StatusBadRequest)
		return
	}
	json.NewEncoder(w).Encode(currencyPriceResponse{
		Eth:       cr.Eth,
		Btc:       cr.Btc,
		Usdc:      cr.Usdc,
		Currency:  currency,
		UpdatedAt: updatedAt,
	})
}

// HandleSupportedCurrencies lists the fiat currency codes this node
// tracks, so the frontend's display-currency selector is populated
// from the server rather than a duplicated hardcoded list.
func HandleSupportedCurrencies(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)
	json.NewEncoder(w).Encode(supportedCurrencies)
}
