package api

import (
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"strconv"

	"github.com/creditchain/creditchaind/core"
)

type tokenRegisterData struct {
	Symbol        string `json:"symbol"`
	Name          string `json:"name"`
	InitialSupply uint64 `json:"initialSupply"`
	// Standard is a token-standard code from GET /token-standards
	// (currently "C1" independent or "C117S" stable) - see
	// core/token_standard.go. Empty is treated as "C1" for backward
	// compatibility with any client that hasn't picked up the selector
	// yet, matching CustomToken.StandardOrDefault's own fallback.
	Standard string `json:"standard"`
	// PegReference is optional, only meaningful for "C117S" - purely
	// descriptive metadata (e.g. "USD"), never mechanically enforced.
	PegReference string `json:"pegReference"`
}

var tokenStandardRegistry = core.NewTokenStandardRegistry()

// TokenCreationFeeETHTest is charged (in ETHTEST, from the creator's
// derived sub-address - see resolveBalanceAddress) on top of the
// existing stake requirement - a second, cheap anti-spam cost using
// the network's own liquid "imported token" rather than just the
// abstract staking counter. Sent to tokenCreationFeeAddress, the same
// synthetic-holding-address pattern poolAddress/p2pOfferEscrowAddress
// already use, rather than a real Burn - nobody holds the private key
// for it, but the transfer stays visible/auditable in the explorer
// like everything else.
const TokenCreationFeeETHTest = 5

func tokenCreationFeeAddress() string {
	return "FEE:TOKEN_CREATION"
}

// HandleRegisterToken previously let any wallet register a new named
// token directly, self-service. That path is retired - token creation
// now requires a passed TOKEN_CREATION governance proposal (see
// api/governance.go's HandleCreateProposal and
// api/token_creation_governance.go for what a PASSED outcome actually
// does) - deliberately hard to find and expensive, per this project's
// own "token creation should require community vote, not a cheap
// self-service form" decision. This handler is kept live (not deleted,
// not a 404) purely so a client still calling the old endpoint gets a
// clear, actionable error instead of a confusing generic 404 - matches
// this codebase's "disclose, don't silently break" convention.
// core.TxTokenRegister and applyTokenRegister themselves are untouched
// and stay in the codebase - old already-recorded registrations must
// still replay identically from blockchain.json.
func HandleRegisterToken(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)
	http.Error(w, "direct token registration has been retired - create and pass a TOKEN_CREATION governance proposal instead (see /governance)", http.StatusGone)
}

type customTokenInfo struct {
	Symbol        string `json:"symbol"`
	Name          string `json:"name"`
	Creator       string `json:"creator"`
	InitialSupply uint64 `json:"initialSupply"`
	TotalSupply   uint64 `json:"totalSupply"`
	CCTReserve    string `json:"cctReserve"`
	TokenReserve  uint64 `json:"tokenReserve"`
	// Tradeable is true once someone has deposited liquidity into this
	// token's pool - see isTradeable's doc comment for why this is
	// computed from the pool's own reserves rather than stored
	// separately.
	Tradeable    bool   `json:"tradeable"`
	Standard     string `json:"standard"`
	PegReference string `json:"pegReference,omitempty"`
}

// isTradeable is the one place "can this symbol actually be swapped
// right now" is decided - a pool exists (see TokenRegistry.Register)
// from the moment a token is created, but with zero reserves a swap
// against it always fails (QuoteCCTForToken/QuoteTokenForCCT both
// refuse when either reserve is zero). Deliberately NOT a separate
// stored flag: a bool that could drift out of sync with the reserves
// it's describing is worse than just asking the reserves directly
// every time.
func isTradeable(pool *core.LiquidityPool) bool {
	if pool == nil || pool.TokenReserve == 0 {
		return false
	}
	cctReserve, _ := core.ParseCctSmallest(pool.CCTReserve)
	return cctReserve.Sign() > 0
}

// HandleListTokens lists every developer-registered token alongside
// its pool reserves, so the UI can show "no liquidity yet" versus an
// actual implied price without a second round-trip per token.
func HandleListTokens(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	list := []customTokenInfo{}

	for symbol, meta := range tokenRegistry.Meta {
		pool := tokenRegistry.Pools[symbol]
		store := tokenRegistry.Tokens[symbol]

		list = append(list, customTokenInfo{
			Symbol:        meta.Symbol,
			Name:          meta.Name,
			Creator:       meta.Creator,
			InitialSupply: meta.InitialSupply,
			TotalSupply:   store.Token.TotalSupply,
			CCTReserve:    pool.CCTReserve,
			TokenReserve:  pool.TokenReserve,
			Tradeable:     isTradeable(pool),
			Standard:      meta.StandardOrDefault(),
			PegReference:  meta.PegReference,
		})
	}

	json.NewEncoder(w).Encode(list)
}

// HandleTokenStandards lists the token standards a user can select at
// registration time (see core/token_standard.go) - server-driven so
// the frontend selector isn't a duplicated hardcoded list.
func HandleTokenStandards(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)
	json.NewEncoder(w).Encode(tokenStandardRegistry.Creatable())
}

// poolSummary is one row of HandleListPools - every symbol that's
// swappable-against-CCT-in-principle (native ETHTEST/USDCTEST, plus
// every developer-registered token), regardless of whether it
// currently has liquidity. This is what the swap UI filters against to
// only ever offer symbols that will actually succeed if swapped, in
// one round-trip instead of querying each pool individually.
type poolSummary struct {
	Symbol       string `json:"symbol"`
	CCTReserve   string `json:"cctReserve"`
	TokenReserve uint64 `json:"tokenReserve"`
	Tradeable    bool   `json:"tradeable"`
}

// HandleListPools lists every pool in the system - the network's own
// native ETHTEST/USDCTEST pools plus every developer-registered
// token's pool - each flagged with whether it's actually tradeable yet
// (see isTradeable). Registering a token or importing ETHTEST/USDCTEST
// doesn't make it tradeable by itself; only a real liquidity deposit
// does, and this is the one endpoint that reports that state for
// everything at once.
func HandleListPools(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	list := []poolSummary{}

	natives := []struct {
		Symbol string
		Pool   *core.LiquidityPool
	}{
		{"ETHTEST", cctEthTestPool},
		{"USDCTEST", cctUsdcTestPool},
	}
	for _, n := range natives {
		list = append(list, poolSummary{
			Symbol:       n.Symbol,
			CCTReserve:   n.Pool.CCTReserve,
			TokenReserve: n.Pool.TokenReserve,
			Tradeable:    isTradeable(n.Pool),
		})
	}

	for symbol, pool := range tokenRegistry.Pools {
		list = append(list, poolSummary{
			Symbol:       symbol,
			CCTReserve:   pool.CCTReserve,
			TokenReserve: pool.TokenReserve,
			Tradeable:    isTradeable(pool),
		})
	}

	json.NewEncoder(w).Encode(list)
}

// HandleCustomTokenBalance mirrors HandleTokenBalance for a
// developer-registered token, looked up by symbol.
func HandleCustomTokenBalance(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	symbol := r.URL.Query().Get("symbol")
	address := r.URL.Query().Get("address")

	store, _, err := lookupCustomToken(symbol)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(map[string]uint64{
		"balance": store.BalanceOf(address),
	})
}

type poolInfo struct {
	Symbol       string `json:"symbol"`
	CCTReserve   string `json:"cctReserve"`
	TokenReserve uint64 `json:"tokenReserve"`
	TotalShares  string `json:"totalShares"`
	YourShares   string `json:"yourShares,omitempty"`
	Tradeable    bool   `json:"tradeable"`
}

// HandlePoolInfo reports a pool's current reserves/shares - the
// implied price is tokenReserve/cctReserve (or the reverse), left for
// the client to compute rather than duplicating that math server-side
// for a value that's purely derived from the two numbers already here.
func HandlePoolInfo(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	symbol := r.URL.Query().Get("symbol")
	address := r.URL.Query().Get("address")

	_, pool, err := lookupCustomToken(symbol)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	info := poolInfo{
		Symbol:       symbol,
		CCTReserve:   pool.CCTReserve,
		TokenReserve: pool.TokenReserve,
		TotalShares:  pool.TotalShares,
		Tradeable:    isTradeable(pool),
	}
	if address != "" {
		info.YourShares = pool.Shares[address]
	}

	json.NewEncoder(w).Encode(info)
}

// HandlePoolPriceHistory returns symbol's recorded price time series
// (oldest first) - see core/price_history.go. An unrecognized or
// never-traded symbol simply returns an empty list, not an error, since
// "no history yet" is a normal, expected state for a freshly registered
// token's pool.
func HandlePoolPriceHistory(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	symbol := r.URL.Query().Get("symbol")

	json.NewEncoder(w).Encode(priceHistoryStore.Get(symbol))
}

// parseUint64Field parses a plain (non-CCT) token amount submitted as
// a decimal string - every amount field that can ever be CCT-
// denominated moved to string as part of the 18-decimal migration
// (see core/cct_amount.go), and non-CCT amount fields sharing the same
// JSON field (e.g. poolSwapData.AmountIn, used for either leg
// depending on Direction) followed suit for one consistent wire shape
// rather than forking the type per-symbol.
func parseUint64Field(s string) (uint64, error) {
	if s == "" {
		return 0, nil
	}
	return strconv.ParseUint(s, 10, 64)
}

type poolAddLiquidityData struct {
	Symbol      string `json:"symbol"`
	CCTAmount   string `json:"cctAmount"`
	TokenAmount uint64 `json:"tokenAmount"`
}

// HandlePoolAddLiquidity deposits CCT+token into symbol's pool. If the
// pool already has reserves, the deposited ratio must match the
// existing one (within a small tolerance) - otherwise a lopsided
// deposit would move the price for free, which is exactly the
// behavior a real AMM prevents by only minting LP shares proportional
// to the SMALLER of the two implied contributions (see
// LiquidityPool.AddLiquidity) - this check just fails the request
// early with a clear reason instead of silently minting a
// worse-than-expected number of shares.
func HandlePoolAddLiquidity(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req poolAddLiquidityData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	tokenStoreForSymbol, pool, err := lookupCustomToken(req.Symbol)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	cctAmount, err := core.ParseCctSmallest(req.CCTAmount)
	if err != nil || cctAmount.Sign() == 0 || req.TokenAmount == 0 {
		http.Error(w, "both cctAmount and tokenAmount must be positive", http.StatusBadRequest)
		return
	}
	cctBalance, _ := core.ParseCctSmallest(cctStore.BalanceOf(address))
	if cctBalance.Cmp(cctAmount) < 0 {
		http.Error(w, "insufficient CCT balance", http.StatusBadRequest)
		return
	}
	tokenSide := resolveBalanceAddress(address, auth.PublicKey, req.Symbol)
	if tokenStoreForSymbol.BalanceOf(tokenSide) < req.TokenAmount {
		http.Error(w, "insufficient token balance", http.StatusBadRequest)
		return
	}
	poolTotalShares, _ := core.ParseCctSmallest(pool.TotalShares)
	if poolTotalShares.Sign() > 0 && !ratioMatches(req.CCTAmount, req.TokenAmount, pool.CCTReserve, pool.TokenReserve) {
		http.Error(w, "deposit ratio does not match the pool's current price - adjust cctAmount/tokenAmount to match the existing reserve ratio", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxPoolAddLiquidity, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

// ratioMatches allows up to 1% slippage between the deposited ratio
// and the pool's existing ratio - real-world reserves shift between
// when a user reads the price and when their transaction confirms, so
// requiring an exact match would make depositing into an active pool
// practically impossible.
func ratioMatches(cctAmountStr string, tokenAmount uint64, cctReserveStr string, tokenReserve uint64) bool {
	cctAmount, err := core.ParseCctSmallest(cctAmountStr)
	if err != nil {
		return false
	}
	cctReserve, _ := core.ParseCctSmallest(cctReserveStr)

	left := new(big.Int).Mul(cctAmount, new(big.Int).SetUint64(tokenReserve))
	right := new(big.Int).Mul(new(big.Int).SetUint64(tokenAmount), cctReserve)

	diff := new(big.Int).Sub(left, right)
	diff.Abs(diff)

	tolerance := new(big.Int).Div(right, big.NewInt(100))
	if tolerance.Sign() == 0 {
		tolerance = big.NewInt(1)
	}

	return diff.Cmp(tolerance) <= 0
}

type poolRemoveLiquidityData struct {
	Symbol string `json:"symbol"`
	Shares string `json:"shares"`
}

func HandlePoolRemoveLiquidity(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req poolRemoveLiquidityData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	_, pool, err := lookupCustomToken(req.Symbol)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	sharesReq, err := core.ParseCctSmallest(req.Shares)
	if err != nil || sharesReq.Sign() == 0 {
		http.Error(w, "shares must be positive", http.StatusBadRequest)
		return
	}
	sharesHeld, _ := core.ParseCctSmallest(pool.Shares[address])
	if sharesHeld.Cmp(sharesReq) < 0 {
		http.Error(w, "insufficient LP shares", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxPoolRemoveLiquidity, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

type poolSwapData struct {
	Symbol    string `json:"symbol"`
	Direction string `json:"direction"` // "cct_to_token" or "token_to_cct"
	// AmountIn is a decimal string regardless of direction - CCT
	// smallest units for "cct_to_token", plain whole units of Symbol
	// for "token_to_cct" (parsed via parseUint64Field) - one wire shape
	// for both, decoded per-direction server-side.
	AmountIn string `json:"amountIn"`
}

func HandlePoolSwap(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req poolSwapData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	tokenStoreForSymbol, _, err := lookupCustomToken(req.Symbol)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	switch req.Direction {
	case "cct_to_token":
		amountIn, err := core.ParseCctSmallest(req.AmountIn)
		if err != nil || amountIn.Sign() == 0 {
			http.Error(w, "amountIn must be positive", http.StatusBadRequest)
			return
		}
		cctBalance, _ := core.ParseCctSmallest(cctStore.BalanceOf(address))
		if cctBalance.Cmp(amountIn) < 0 {
			http.Error(w, "insufficient CCT balance", http.StatusBadRequest)
			return
		}
	case "token_to_cct":
		amountIn, err := parseUint64Field(req.AmountIn)
		if err != nil || amountIn == 0 {
			http.Error(w, "amountIn must be positive", http.StatusBadRequest)
			return
		}
		tokenSide := resolveBalanceAddress(address, auth.PublicKey, req.Symbol)
		if tokenStoreForSymbol.BalanceOf(tokenSide) < amountIn {
			http.Error(w, "insufficient token balance", http.StatusBadRequest)
			return
		}
	default:
		http.Error(w, "direction must be cct_to_token or token_to_cct", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxPoolSwap, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

type swapViaCCTData struct {
	FromSymbol string `json:"fromSymbol"` // "CCT" or any resolvable token symbol
	ToSymbol   string `json:"toSymbol"`
	// AmountIn is a decimal string - CCT smallest units when FromSymbol
	// is "CCT", plain whole units of FromSymbol otherwise (parsed via
	// parseUint64Field). Same one-wire-shape-for-both convention as
	// poolSwapData.AmountIn.
	AmountIn string `json:"amountIn"`
}

// HandleSwapViaCCT is the universal swap entry point: any resolvable
// token to any other, always routed through CCT (a single hop if
// either side already IS CCT, two hops otherwise) - the actual
// implementation of "every swap passes through CCT, CCT is the hub."
// Direct single-pool swaps (HandlePoolSwap) still exist for the
// devtools panel's simpler CCT<->one-token case; this is the general
// form that also covers token-to-token.
func HandleSwapViaCCT(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req swapViaCCTData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if req.FromSymbol == req.ToSymbol {
		http.Error(w, "fromSymbol and toSymbol must differ", http.StatusBadRequest)
		return
	}

	if req.FromSymbol != "CCT" {
		fromStore, _, err := lookupCustomToken(req.FromSymbol)
		if err != nil {
			http.Error(w, "unknown fromSymbol", http.StatusNotFound)
			return
		}
		amountIn, err := parseUint64Field(req.AmountIn)
		if err != nil || amountIn == 0 {
			http.Error(w, "amountIn must be positive", http.StatusBadRequest)
			return
		}
		fromSide := resolveBalanceAddress(address, auth.PublicKey, req.FromSymbol)
		if fromStore.BalanceOf(fromSide) < amountIn {
			http.Error(w, "insufficient balance", http.StatusBadRequest)
			return
		}
	} else {
		amountIn, err := core.ParseCctSmallest(req.AmountIn)
		if err != nil || amountIn.Sign() == 0 {
			http.Error(w, "amountIn must be positive", http.StatusBadRequest)
			return
		}
		cctBalance, _ := core.ParseCctSmallest(cctStore.BalanceOf(address))
		if cctBalance.Cmp(amountIn) < 0 {
			http.Error(w, "insufficient CCT balance", http.StatusBadRequest)
			return
		}
	}

	if req.ToSymbol != "CCT" {
		if _, _, err := lookupCustomToken(req.ToSymbol); err != nil {
			http.Error(w, "unknown toSymbol", http.StatusNotFound)
			return
		}
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxSwapViaCCT, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

var errUnknownToken = errors.New("unknown token symbol")

// lookupCustomToken resolves a symbol to its TokenStore and CCT-paired
// LiquidityPool - checking the network's own built-in native tokens
// (ETHTEST, USDCTEST) first, then falling back to the
// developer-registered token registry. Every pool endpoint
// (HandlePoolInfo, HandlePoolAddLiquidity, HandlePoolSwap, ...)
// already goes through this one function, so ETHTEST/USDCTEST get a
// real CCT-paired market "for free," using the exact same AMM code
// path as any custom token - this is what makes CCT a genuine
// universal hub currency rather than something only custom tokens
// participate in.
func lookupCustomToken(symbol string) (*core.TokenStore, *core.LiquidityPool, error) {
	if isMainnetBlockedSymbol(symbol) {
		return nil, nil, errTestOnlySymbolBlockedOnMainnet
	}
	switch symbol {
	case "ETHTEST":
		return ethTestStore, cctEthTestPool, nil
	case "BTCTEST":
		return btcTestStore, cctBtcTestPool, nil
	case "USDCTEST":
		return usdcTestStore, cctUsdcTestPool, nil
	case "YENE":
		return yenStore, cctYenPool, nil
	}

	store, ok := tokenRegistry.Tokens[symbol]
	if !ok {
		return nil, nil, errUnknownToken
	}
	return store, tokenRegistry.Pools[symbol], nil
}
