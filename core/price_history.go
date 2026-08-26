package core

// PricePoint is one historical price sample for a liquidity pool -
// price is expressed as CCT per one unit of the paired token (matches
// the "1 SYMBOL = X CCT" convention pool.html already shows as its
// live spot price), scaled by priceScale since reserves are plain
// integers and true price is usually a fraction.
type PricePoint struct {
	// Timestamp is a BLOCK timestamp, not wall-clock - same convention
	// as every other time-series field in this codebase (see
	// core/staking_store.go's UnbondingEntry.MaturesAt), so replaying
	// the same chain history produces the same chart on every node.
	Timestamp int64  `json:"timestamp"`
	Price     uint64 `json:"price"`
}

// priceScale multiplies the CCT-per-token ratio before storing it as
// an integer. 1e6 gives six decimal digits of precision - enough for
// a token trading at a small fraction of a CCT to still show
// meaningful movement instead of rounding to zero.
const priceScale = 1_000_000

// PriceHistoryStore keeps a bounded, per-symbol time series of a
// pool's price - purely observational (chart data). It never feeds
// back into any pricing/swap decision - LiquidityPool's own reserves
// remain the single source of truth for what a swap actually executes
// at; this is a read-only side channel recording what that price WAS
// at each point in time.
type PriceHistoryStore struct {
	History map[string][]PricePoint // symbol -> chronological price points
}

func NewPriceHistoryStore() *PriceHistoryStore {
	return &PriceHistoryStore{History: make(map[string][]PricePoint)}
}

// MaxPricePointsPerSymbol bounds memory/storage growth - a constantly-
// traded pool would otherwise accumulate an ever-growing history
// forever. Oldest points are dropped first once the cap is hit, same
// "keep the most recent N, discard the rest" convention as
// api/db.go's dbSnapshotRetention.
const MaxPricePointsPerSymbol = 500

// Record appends a price point for symbol, computed from pool's
// current reserves - a no-op if either reserve is zero (an empty pool
// has no meaningful price yet) or if the price is identical to the
// immediately-preceding point (no need to chart a new sample every
// time something merely re-confirms the same ratio, e.g. a
// proportional add/remove-liquidity that doesn't move price at all -
// only when the ratio actually changes).
func (s *PriceHistoryStore) Record(symbol string, pool *LiquidityPool, timestamp int64) {
	if pool == nil || pool.CCTReserve == 0 || pool.TokenReserve == 0 {
		return
	}
	price := (pool.CCTReserve * priceScale) / pool.TokenReserve

	points := s.History[symbol]
	if len(points) > 0 && points[len(points)-1].Price == price {
		return
	}

	points = append(points, PricePoint{Timestamp: timestamp, Price: price})
	if len(points) > MaxPricePointsPerSymbol {
		points = points[len(points)-MaxPricePointsPerSymbol:]
	}
	s.History[symbol] = points
}

// Get returns symbol's full recorded history, oldest first.
func (s *PriceHistoryStore) Get(symbol string) []PricePoint {
	return s.History[symbol]
}

// RecordPrice is Record's sibling for a symbol whose price comes from
// a real completed trade (see OrderBookStore.Match) rather than a
// pool's reserve ratio - same dedup/cap/append logic, just fed an
// already-computed price instead of deriving one from *LiquidityPool.
// This is what lets GET /pool/price-history and the existing chart
// rendering keep working unchanged for a symbol that's moved off the
// AMM onto the order book.
func (s *PriceHistoryStore) RecordPrice(symbol string, price uint64, timestamp int64) {
	if price == 0 {
		return
	}

	points := s.History[symbol]
	if len(points) > 0 && points[len(points)-1].Price == price {
		return
	}

	points = append(points, PricePoint{Timestamp: timestamp, Price: price})
	if len(points) > MaxPricePointsPerSymbol {
		points = points[len(points)-MaxPricePointsPerSymbol:]
	}
	s.History[symbol] = points
}
