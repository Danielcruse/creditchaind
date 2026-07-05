package core

// LiquidityPool is a constant-product (x*y=k) automated market maker
// pairing CCT with one developer-registered token - the same pricing
// model real DEXs (Uniswap v2) use. Price is never set directly by
// anyone; it's whatever ratio of reserves the pool currently holds,
// which is exactly the answer to "how should a new token's value be
// determined" this was built for: by CCT actually staked into the
// pool against it, not by fiat declaration.
type LiquidityPool struct {
	Symbol string

	CCTReserve   uint64
	TokenReserve uint64

	TotalShares uint64
	Shares      map[string]uint64 // address -> LP shares
}

func NewLiquidityPool(symbol string) *LiquidityPool {
	return &LiquidityPool{
		Symbol: symbol,
		Shares: make(map[string]uint64),
	}
}

// swapFeeBps is taken from every swap's input and left in the pool
// (paid to liquidity providers via their share of larger reserves),
// exactly like Uniswap v2's 0.3% fee.
const swapFeeBps = 30

// isqrt is an integer square root (Newton's method) - used only for
// the very first deposit into a pool, where LP shares are seeded as
// the geometric mean of the two deposited amounts so neither reserve
// dominates the initial share price.
func isqrt(n uint64) uint64 {
	if n == 0 {
		return 0
	}
	x := n
	y := (x + 1) / 2
	for y < x {
		x = y
		y = (x + n/x) / 2
	}
	return x
}

// AddLiquidity deposits cctAmount+tokenAmount and returns the LP
// shares minted. The FIRST deposit into a pool sets its initial price
// via whatever ratio the depositor chooses - every deposit after that
// is expected to already match the pool's existing ratio (the caller,
// api/apply.go's applyPoolAddLiquidity, enforces that before calling
// this; this function trusts the amounts it's given).
func (p *LiquidityPool) AddLiquidity(address string, cctAmount, tokenAmount uint64) uint64 {
	var minted uint64

	if p.TotalShares == 0 {
		minted = isqrt(cctAmount * tokenAmount)
	} else {
		byCCT := (cctAmount * p.TotalShares) / p.CCTReserve
		byToken := (tokenAmount * p.TotalShares) / p.TokenReserve
		if byCCT < byToken {
			minted = byCCT
		} else {
			minted = byToken
		}
	}

	if minted == 0 {
		return 0
	}

	p.CCTReserve += cctAmount
	p.TokenReserve += tokenAmount
	p.TotalShares += minted
	p.Shares[address] += minted

	return minted
}

// RemoveLiquidity burns shares and returns the proportional amount of
// each reserve owed back to address.
func (p *LiquidityPool) RemoveLiquidity(address string, shares uint64) (cctOut, tokenOut uint64, ok bool) {
	if shares == 0 || p.TotalShares == 0 || p.Shares[address] < shares {
		return 0, 0, false
	}

	cctOut = (shares * p.CCTReserve) / p.TotalShares
	tokenOut = (shares * p.TokenReserve) / p.TotalShares

	p.Shares[address] -= shares
	p.TotalShares -= shares
	p.CCTReserve -= cctOut
	p.TokenReserve -= tokenOut

	return cctOut, tokenOut, true
}

// QuoteCCTForToken/QuoteTokenForCCT compute what a swap WOULD return,
// without mutating any reserves - the same constant-product formula
// SwapCCTForToken/SwapTokenForCCT apply for real, split out so a
// multi-hop swap (see api/apply.go's applySwapViaCCT) can confirm
// BOTH legs of a route would succeed before committing to either one.
// Without this, a two-hop swap that completes its first leg and then
// discovers the second leg lacks liquidity would need to unwind an
// already-applied AMM state change - quoting first avoids ever being
// in that position.
func (p *LiquidityPool) QuoteCCTForToken(amountIn uint64) (amountOut uint64, ok bool) {
	if amountIn == 0 || p.CCTReserve == 0 || p.TokenReserve == 0 {
		return 0, false
	}

	amountInWithFee := amountIn * (10000 - swapFeeBps)
	numerator := amountInWithFee * p.TokenReserve
	denominator := p.CCTReserve*10000 + amountInWithFee
	amountOut = numerator / denominator

	if amountOut == 0 || amountOut >= p.TokenReserve {
		return 0, false
	}
	return amountOut, true
}

func (p *LiquidityPool) QuoteTokenForCCT(amountIn uint64) (amountOut uint64, ok bool) {
	if amountIn == 0 || p.CCTReserve == 0 || p.TokenReserve == 0 {
		return 0, false
	}

	amountInWithFee := amountIn * (10000 - swapFeeBps)
	numerator := amountInWithFee * p.CCTReserve
	denominator := p.TokenReserve*10000 + amountInWithFee
	amountOut = numerator / denominator

	if amountOut == 0 || amountOut >= p.CCTReserve {
		return 0, false
	}
	return amountOut, true
}

// SwapCCTForToken/SwapTokenForCCT apply the constant-product formula
// with the fee taken from the input side, identical to Uniswap v2 -
// same math as the Quote* functions above, but actually committing
// the reserve changes.
func (p *LiquidityPool) SwapCCTForToken(amountIn uint64) (amountOut uint64, ok bool) {
	amountOut, ok = p.QuoteCCTForToken(amountIn)
	if !ok {
		return 0, false
	}

	p.CCTReserve += amountIn
	p.TokenReserve -= amountOut
	return amountOut, true
}

func (p *LiquidityPool) SwapTokenForCCT(amountIn uint64) (amountOut uint64, ok bool) {
	amountOut, ok = p.QuoteTokenForCCT(amountIn)
	if !ok {
		return 0, false
	}

	p.TokenReserve += amountIn
	p.CCTReserve -= amountOut
	return amountOut, true
}
