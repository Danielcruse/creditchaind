package api

import (
	"encoding/json"
	"net/http"
	"time"
)

// DefaultCCTPerETH is the disclosed fallback CCT-to-ETH reference rate
// used only until a real CCT/ETHTEST trade exists on the order book -
// it is not a live market rate, just an illustrative starting point
// (the same number this project's own docs/tests have used as an
// example, see core/hub_rate.go's doc comments), shown to the user as
// such rather than presented as a real price.
const DefaultCCTPerETH = 0.000975

// cctPriceInETH returns CCT's current ETH-equivalent price (i.e. "1
// CCT = X ETH") and where that number came from. ETHTEST is already
// treated everywhere else in this app as "test ETH" 1:1 (staking
// yield, bridge framing, etc.), so the real, trade-derived CCT/ETHTEST
// price from priceHistoryStore doubles as a real CCT/ETH rate once at
// least one trade has happened. Until then, it falls back to
// DefaultCCTPerETH rather than reporting a fabricated live price.
func cctPriceInETH() (rate float64, source string) {
	points := priceHistoryStore.Get("ETHTEST")
	if len(points) == 0 {
		return DefaultCCTPerETH, "fallback"
	}

	last := points[len(points)-1]
	if last.Price == 0 {
		return DefaultCCTPerETH, "fallback"
	}

	// last.Price is CCT-per-1-ETHTEST scaled by priceScale (1e6, see
	// core/price_history.go) - i.e. 1 ETHTEST = (last.Price/1e6) CCT.
	// Inverting gives 1 CCT = 1e6/last.Price ETHTEST, treated as ETH.
	cctPerEthtest := float64(last.Price) / 1_000_000
	if cctPerEthtest <= 0 {
		return DefaultCCTPerETH, "fallback"
	}
	return 1 / cctPerEthtest, "orderbook"
}

type cctPriceResponse struct {
	CctPerEth float64 `json:"cctPerEth"`
	Source    string  `json:"source"`
	UpdatedAt int64   `json:"updatedAt"`
}

// HandleCctPrice is a public read (no auth) exposing CCT's current
// ETH-equivalent reference rate, for the frontend's fiat-conversion
// helper to chain through alongside GET /price/external's real ETH/USD
// (or other currency) rate.
func HandleCctPrice(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	rate, source := cctPriceInETH()
	json.NewEncoder(w).Encode(cctPriceResponse{
		CctPerEth: rate,
		Source:    source,
		UpdatedAt: time.Now().Unix(),
	})
}
