package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
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

// coinbaseSpotPriceURLFor is the fallback source when CoinGecko is
// unreachable (see pollExternalPrices) - also free and keyless, same
// "no paid external dependency" bar. Coinbase's spot-price endpoint
// only ever quotes USD (no vs_currencies list the way CoinGecko has),
// so the fallback can only ever refresh the USD row - every other
// supported currency just keeps its last-known-good value (with a
// stale UpdatedAt) until CoinGecko itself recovers. That's not a
// meaningful loss: the one place this oracle actually moves real value
// (swap_fair_value.go's computeFairValueSwap) only ever reads the USD
// row - every other currency is purely informational display pricing.
func coinbaseSpotPriceURLFor(coinbaseSymbol string) string {
	return "https://api.coinbase.com/v2/prices/" + coinbaseSymbol + "-USD/spot"
}

// maxPriceSwingPercent is the circuit breaker's threshold - a judgment
// call, not derived from any spec. Applied per (asset, currency) pair,
// not to the poll as a whole: one bad/manipulated reading for ETH
// doesn't block a perfectly normal BTC update in the same tick. The
// very first reading for a pair (no prior baseline, oldValue <= 0)
// always passes - there's nothing to compare it against yet.
const maxPriceSwingPercent = 20.0

// priceStalenessSeconds bounds how old priceOracleCache.UpdatedAt may
// be before a caller moving REAL value off of it (currently only
// swap_fair_value.go's computeFairValueSwap) must refuse rather than
// silently act on a stale reading - 3x the poll interval, a judgment
// call that tolerates one or two missed ticks (a slow response, a
// transient fallback failure too) without being so generous that a
// genuinely stuck oracle still looks "fresh enough." Every other
// reader of this cache (HandleExternalPrice's informational display)
// deliberately does NOT enforce this - a stale display price is a
// UX nit, a stale price moving real bridged-asset value is not.
const priceStalenessSeconds = int64(3 * priceOraclePollInterval / time.Second)

// acceptPriceUpdate applies the circuit breaker: oldValue<=0 (no real
// baseline yet) or newValue<=0 (nothing to adopt) always resolves to
// newValue/oldValue respectively with no swing check; otherwise a move
// past maxPriceSwingPercent is rejected and the old value is kept.
func acceptPriceUpdate(oldValue, newValue float64) float64 {
	if newValue <= 0 {
		return oldValue
	}
	if oldValue <= 0 {
		return newValue
	}
	swingPercent := (newValue - oldValue) / oldValue * 100
	if swingPercent < 0 {
		swingPercent = -swingPercent
	}
	if swingPercent > maxPriceSwingPercent {
		fmt.Printf("price oracle circuit breaker: rejected a %.1f%% single-tick swing (%.4f -> %.4f)\n", swingPercent, oldValue, newValue)
		return oldValue
	}
	return newValue
}

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

// fetchCoingeckoRates fetches every supported currency's rates in one
// call - returns ok=false on any network/status/decode failure, never
// a partial result, so the caller always knows cleanly whether to try
// the Coinbase fallback.
func fetchCoingeckoRates() (rates map[string]currencyRates, ok bool) {
	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(coingeckoSimplePriceURL)
	if err != nil {
		fmt.Println("price oracle: coingecko fetch failed:", err)
		return nil, false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Println("price oracle: coingecko fetch failed: status", resp.StatusCode)
		return nil, false
	}

	var parsed struct {
		Ethereum map[string]float64 `json:"ethereum"`
		Bitcoin  map[string]float64 `json:"bitcoin"`
		UsdCoin  map[string]float64 `json:"usd-coin"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		fmt.Println("price oracle: coingecko decode failed:", err)
		return nil, false
	}

	rates = make(map[string]currencyRates, len(supportedCurrencies))
	for _, cur := range supportedCurrencies {
		key := strings.ToLower(cur)
		rates[cur] = currencyRates{
			Eth:  parsed.Ethereum[key],
			Btc:  parsed.Bitcoin[key],
			Usdc: parsed.UsdCoin[key],
		}
	}
	return rates, true
}

// fetchCoinbaseUsdRate fetches one asset's USD spot price from
// Coinbase - the fallback path's per-asset building block, called only
// when fetchCoingeckoRates has already failed. Returns ok=false on any
// failure, same discipline as fetchCoingeckoRates.
func fetchCoinbaseUsdRate(coinbaseSymbol string) (price float64, ok bool) {
	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(coinbaseSpotPriceURLFor(coinbaseSymbol))
	if err != nil {
		fmt.Println("price oracle: coinbase fallback fetch failed for", coinbaseSymbol, ":", err)
		return 0, false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Println("price oracle: coinbase fallback failed for", coinbaseSymbol, ": status", resp.StatusCode)
		return 0, false
	}

	var parsed struct {
		Data struct {
			Amount string `json:"amount"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		fmt.Println("price oracle: coinbase fallback decode failed for", coinbaseSymbol, ":", err)
		return 0, false
	}

	price, err = strconv.ParseFloat(parsed.Data.Amount, 64)
	if err != nil || price <= 0 {
		return 0, false
	}
	return price, true
}

// fetchCoinbaseFallbackUsdRates is the fallback path - only ever fills
// in the USD row (see coinbaseSpotPriceURLFor's doc comment), carrying
// forward whatever the existing cache already held for every other
// currency so the caller can still unconditionally overwrite
// rates["USD"] with the result.
func fetchCoinbaseFallbackUsdRates() currencyRates {
	eth, _ := fetchCoinbaseUsdRate("ETH")
	btc, _ := fetchCoinbaseUsdRate("BTC")
	usdc, _ := fetchCoinbaseUsdRate("USDC")
	return currencyRates{Eth: eth, Btc: btc, Usdc: usdc}
}

// mergePriceRates applies the circuit breaker (acceptPriceUpdate) field
// by field, currency by currency, folding newRates onto oldRates - a
// currency newRates doesn't mention (e.g. every non-USD currency
// during a Coinbase fallback tick) simply keeps its old value
// untouched. Factored out of pollExternalPrices as a pure function so
// the circuit breaker's actual decision logic is testable without any
// real network call.
func mergePriceRates(oldRates, newRates map[string]currencyRates) map[string]currencyRates {
	merged := make(map[string]currencyRates, len(oldRates)+len(newRates))
	for cur, old := range oldRates {
		merged[cur] = old
	}
	for cur, next := range newRates {
		old := merged[cur]
		merged[cur] = currencyRates{
			Eth:  acceptPriceUpdate(old.Eth, next.Eth),
			Btc:  acceptPriceUpdate(old.Btc, next.Btc),
			Usdc: acceptPriceUpdate(old.Usdc, next.Usdc),
		}
	}
	return merged
}

func pollExternalPrices() {
	rates, ok := fetchCoingeckoRates()
	if !ok {
		fmt.Println("price oracle: falling back to Coinbase (USD only)")
		rates = map[string]currencyRates{"USD": fetchCoinbaseFallbackUsdRates()}
	}

	priceOracleMu.Lock()
	defer priceOracleMu.Unlock()

	priceOracleCache = priceOracleData{
		Rates:     mergePriceRates(priceOracleCache.Rates, rates),
		UpdatedAt: time.Now().Unix(),
	}
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
