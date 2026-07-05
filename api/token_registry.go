package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

type tokenRegisterData struct {
	Symbol        string `json:"symbol"`
	Name          string `json:"name"`
	InitialSupply uint64 `json:"initialSupply"`
}

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

// HandleRegisterToken lets any wallet register a new named token - the
// developer-facing entry point on explorer.html's tools bar. This only
// creates the ledger and mints the creator's own initial supply; it
// does NOT give the token a price (see core/token_registry.go's doc
// comment) - that's HandlePoolAddLiquidity's job.
func HandleRegisterToken(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req tokenRegisterData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	// Requiring some ETH staked before creating a token is the same
	// "skin in the game" gate applyBorrow already uses via
	// hasCollateral - a cheap anti-spam bar (this checks the existing
	// stakingStore, not a new lock), re-checked again at apply time
	// since stake can move between submission and confirmation.
	if stakingStore.StakeOf(address, "ETHTEST") == 0 {
		http.Error(w, "you must stake some test ETH before creating a token", http.StatusBadRequest)
		return
	}
	ethTestSide := resolveBalanceAddress(address, auth.PublicKey, "ETHTEST")
	if ethTestStore.BalanceOf(ethTestSide) < TokenCreationFeeETHTest {
		http.Error(w, fmt.Sprintf("you need at least %d ETHTEST to cover the token creation fee", TokenCreationFeeETHTest), http.StatusBadRequest)
		return
	}
	if !core.ValidateTokenSymbol(req.Symbol) {
		http.Error(w, "symbol must be 2-10 uppercase letters and not already reserved (CCT/ETHTEST/USDCTEST/REPT)", http.StatusBadRequest)
		return
	}
	if !core.ValidateTokenName(req.Name) {
		http.Error(w, "name must be 1-64 characters", http.StatusBadRequest)
		return
	}
	if req.InitialSupply == 0 || req.InitialSupply > core.MaxCustomTokenSupply {
		http.Error(w, "initial supply must be positive and at most 1,000,000,000", http.StatusBadRequest)
		return
	}
	if _, exists := tokenRegistry.Meta[req.Symbol]; exists {
		http.Error(w, "symbol already registered", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxTokenRegister, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

type customTokenInfo struct {
	Symbol        string `json:"symbol"`
	Name          string `json:"name"`
	Creator       string `json:"creator"`
	InitialSupply uint64 `json:"initialSupply"`
	TotalSupply   uint64 `json:"totalSupply"`
	CCTReserve    uint64 `json:"cctReserve"`
	TokenReserve  uint64 `json:"tokenReserve"`
	// Tradeable is true once someone has deposited liquidity into this
	// token's pool - see isTradeable's doc comment for why this is
	// computed from the pool's own reserves rather than stored
	// separately.
	Tradeable bool `json:"tradeable"`
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
	return pool != nil && pool.CCTReserve > 0 && pool.TokenReserve > 0
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
		})
	}

	json.NewEncoder(w).Encode(list)
}

// poolSummary is one row of HandleListPools - every symbol that's
// swappable-against-CCT-in-principle (native ETHTEST/USDCTEST, plus
// every developer-registered token), regardless of whether it
// currently has liquidity. This is what the swap UI filters against to
// only ever offer symbols that will actually succeed if swapped, in
// one round-trip instead of querying each pool individually.
type poolSummary struct {
	Symbol       string `json:"symbol"`
	CCTReserve   uint64 `json:"cctReserve"`
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
	CCTReserve   uint64 `json:"cctReserve"`
	TokenReserve uint64 `json:"tokenReserve"`
	TotalShares  uint64 `json:"totalShares"`
	YourShares   uint64 `json:"yourShares,omitempty"`
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

type poolAddLiquidityData struct {
	Symbol      string `json:"symbol"`
	CCTAmount   uint64 `json:"cctAmount"`
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

	if req.CCTAmount == 0 || req.TokenAmount == 0 {
		http.Error(w, "both cctAmount and tokenAmount must be positive", http.StatusBadRequest)
		return
	}
	if tokenStore.BalanceOf(address) < req.CCTAmount {
		http.Error(w, "insufficient CCT balance", http.StatusBadRequest)
		return
	}
	tokenSide := resolveBalanceAddress(address, auth.PublicKey, req.Symbol)
	if tokenStoreForSymbol.BalanceOf(tokenSide) < req.TokenAmount {
		http.Error(w, "insufficient token balance", http.StatusBadRequest)
		return
	}
	if pool.TotalShares > 0 && !ratioMatches(req.CCTAmount, req.TokenAmount, pool.CCTReserve, pool.TokenReserve) {
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
func ratioMatches(cctAmount, tokenAmount, cctReserve, tokenReserve uint64) bool {
	left := cctAmount * tokenReserve
	right := tokenAmount * cctReserve

	var diff uint64
	if left > right {
		diff = left - right
	} else {
		diff = right - left
	}

	tolerance := right / 100
	if tolerance == 0 {
		tolerance = 1
	}

	return diff <= tolerance
}

type poolRemoveLiquidityData struct {
	Symbol string `json:"symbol"`
	Shares uint64 `json:"shares"`
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

	if req.Shares == 0 {
		http.Error(w, "shares must be positive", http.StatusBadRequest)
		return
	}
	if pool.Shares[address] < req.Shares {
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
	AmountIn  uint64 `json:"amountIn"`
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

	if req.AmountIn == 0 {
		http.Error(w, "amountIn must be positive", http.StatusBadRequest)
		return
	}

	switch req.Direction {
	case "cct_to_token":
		if tokenStore.BalanceOf(address) < req.AmountIn {
			http.Error(w, "insufficient CCT balance", http.StatusBadRequest)
			return
		}
	case "token_to_cct":
		tokenSide := resolveBalanceAddress(address, auth.PublicKey, req.Symbol)
		if tokenStoreForSymbol.BalanceOf(tokenSide) < req.AmountIn {
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
	AmountIn   uint64 `json:"amountIn"`
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

	if req.AmountIn == 0 {
		http.Error(w, "amountIn must be positive", http.StatusBadRequest)
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
		fromSide := resolveBalanceAddress(address, auth.PublicKey, req.FromSymbol)
		if fromStore.BalanceOf(fromSide) < req.AmountIn {
			http.Error(w, "insufficient balance", http.StatusBadRequest)
			return
		}
	} else if tokenStore.BalanceOf(address) < req.AmountIn {
		http.Error(w, "insufficient CCT balance", http.StatusBadRequest)
		return
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
	switch symbol {
	case "ETHTEST":
		return ethTestStore, cctEthTestPool, nil
	case "USDCTEST":
		return usdcTestStore, cctUsdcTestPool, nil
	}

	store, ok := tokenRegistry.Tokens[symbol]
	if !ok {
		return nil, nil, errUnknownToken
	}
	return store, tokenRegistry.Pools[symbol], nil
}
