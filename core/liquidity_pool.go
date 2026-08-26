package core

import "math/big"

// LiquidityPool is a constant-product (x*y=k) automated market maker
// pairing CCT with one developer-registered token - the same pricing
// model real DEXs (Uniswap v2) use. Price is never set directly by
// anyone; it's whatever ratio of reserves the pool currently holds,
// which is exactly the answer to "how should a new token's value be
// determined" this was built for: by CCT actually staked into the
// pool against it, not by fiat declaration.
//
// CCTReserve/TotalShares/Shares are big-int-backed decimal strings,
// not uint64 - CCT is always one leg of every pool, and at 18-decimal
// smallest-unit resolution a plain uint64 overflows silently on
// ordinary swap math (confirmed during the tokenomics decimals
// migration: amountIn*(10000-fee) alone overflows uint64 once amounts
// reach the 10^15-10^18 range). TotalShares/Shares scale with
// CCTReserve by construction (initial shares are
// isqrt(cctAmount*tokenAmount)), so they need the same headroom.
// TokenReserve is the OTHER token in the pair (ETHTEST/USDCTEST/a
// custom token) - unaffected by CCT's decimals migration, stays
// uint64 exactly as before.
type LiquidityPool struct {
	Symbol string

	CCTReserve   string // smallest-unit CCT, decimal string
	TokenReserve uint64

	TotalShares string // decimal string
	Shares      map[string]string
}

func NewLiquidityPool(symbol string) *LiquidityPool {
	return &LiquidityPool{
		Symbol:      symbol,
		CCTReserve:  "0",
		TotalShares: "0",
		Shares:      make(map[string]string),
	}
}

// swapFeeBps is taken from every swap's input and left in the pool
// (paid to liquidity providers via their share of larger reserves),
// exactly like Uniswap v2's 0.3% fee.
const swapFeeBps = 30

var bpsScale = big.NewInt(10000)
var bpsAfterFee = big.NewInt(10000 - swapFeeBps)

func bigCct(s string) *big.Int {
	n, err := ParseCctSmallest(s)
	if err != nil {
		return big.NewInt(0)
	}
	return n
}

func (p *LiquidityPool) shareOf(address string) *big.Int {
	return bigCct(p.Shares[address])
}

// AddLiquidity deposits cctAmount (smallest-unit decimal string) +
// tokenAmount and returns the LP shares minted (decimal string). The
// FIRST deposit into a pool sets its initial price via whatever ratio
// the depositor chooses - every deposit after that is expected to
// already match the pool's existing ratio (the caller,
// api/apply.go's applyPoolAddLiquidity, enforces that before calling
// this; this function trusts the amounts it's given).
func (p *LiquidityPool) AddLiquidity(address string, cctAmount string, tokenAmount uint64) string {
	cctIn := bigCct(cctAmount)
	tokenIn := new(big.Int).SetUint64(tokenAmount)
	cctReserve := bigCct(p.CCTReserve)
	tokenReserve := new(big.Int).SetUint64(p.TokenReserve)
	totalShares := bigCct(p.TotalShares)

	var minted *big.Int
	if totalShares.Sign() == 0 {
		product := new(big.Int).Mul(cctIn, tokenIn)
		minted = new(big.Int).Sqrt(product)
	} else {
		byCCT := new(big.Int)
		if cctReserve.Sign() != 0 {
			byCCT.Mul(cctIn, totalShares)
			byCCT.Div(byCCT, cctReserve)
		}
		byToken := new(big.Int)
		if tokenReserve.Sign() != 0 {
			byToken.Mul(tokenIn, totalShares)
			byToken.Div(byToken, tokenReserve)
		}
		if byCCT.Cmp(byToken) < 0 {
			minted = byCCT
		} else {
			minted = byToken
		}
	}

	if minted.Sign() <= 0 {
		return "0"
	}

	cctReserve.Add(cctReserve, cctIn)
	p.CCTReserve = FormatCctSmallest(cctReserve)
	p.TokenReserve += tokenAmount
	totalShares.Add(totalShares, minted)
	p.TotalShares = FormatCctSmallest(totalShares)

	share := p.shareOf(address)
	share.Add(share, minted)
	p.Shares[address] = FormatCctSmallest(share)

	return FormatCctSmallest(minted)
}

// RemoveLiquidity burns shares (decimal string) and returns the
// proportional amount of each reserve owed back to address.
func (p *LiquidityPool) RemoveLiquidity(address string, shares string) (cctOut string, tokenOut uint64, ok bool) {
	sharesBurn := bigCct(shares)
	totalShares := bigCct(p.TotalShares)
	held := p.shareOf(address)

	if sharesBurn.Sign() == 0 || totalShares.Sign() == 0 || held.Cmp(sharesBurn) < 0 {
		return "0", 0, false
	}

	cctReserve := bigCct(p.CCTReserve)
	tokenReserve := new(big.Int).SetUint64(p.TokenReserve)

	cctOutBig := new(big.Int).Mul(sharesBurn, cctReserve)
	cctOutBig.Div(cctOutBig, totalShares)

	tokenOutBig := new(big.Int).Mul(sharesBurn, tokenReserve)
	tokenOutBig.Div(tokenOutBig, totalShares)

	held.Sub(held, sharesBurn)
	p.Shares[address] = FormatCctSmallest(held)
	totalShares.Sub(totalShares, sharesBurn)
	p.TotalShares = FormatCctSmallest(totalShares)
	cctReserve.Sub(cctReserve, cctOutBig)
	p.CCTReserve = FormatCctSmallest(cctReserve)
	p.TokenReserve -= tokenOutBig.Uint64()

	return FormatCctSmallest(cctOutBig), tokenOutBig.Uint64(), true
}

// QuoteCCTForToken/QuoteTokenForCCT compute what a swap WOULD return,
// without mutating any reserves - the same constant-product formula
// SwapCCTForToken/SwapTokenForCCT apply for real, split out so a
// multi-hop swap (see api/apply.go's applySwapViaCCT) can confirm
// BOTH legs of a route would succeed before committing to either one.
func (p *LiquidityPool) QuoteCCTForToken(amountIn string) (amountOut uint64, ok bool) {
	in := bigCct(amountIn)
	cctReserve := bigCct(p.CCTReserve)
	tokenReserve := new(big.Int).SetUint64(p.TokenReserve)

	if in.Sign() == 0 || cctReserve.Sign() == 0 || tokenReserve.Sign() == 0 {
		return 0, false
	}

	amountInWithFee := new(big.Int).Mul(in, bpsAfterFee)
	numerator := new(big.Int).Mul(amountInWithFee, tokenReserve)
	denominator := new(big.Int).Mul(cctReserve, bpsScale)
	denominator.Add(denominator, amountInWithFee)

	out := new(big.Int).Div(numerator, denominator)
	if out.Sign() == 0 || out.Cmp(tokenReserve) >= 0 || !out.IsUint64() {
		return 0, false
	}
	return out.Uint64(), true
}

func (p *LiquidityPool) QuoteTokenForCCT(amountIn uint64) (amountOut string, ok bool) {
	in := new(big.Int).SetUint64(amountIn)
	cctReserve := bigCct(p.CCTReserve)
	tokenReserve := new(big.Int).SetUint64(p.TokenReserve)

	if amountIn == 0 || cctReserve.Sign() == 0 || tokenReserve.Sign() == 0 {
		return "0", false
	}

	amountInWithFee := new(big.Int).Mul(in, bpsAfterFee)
	numerator := new(big.Int).Mul(amountInWithFee, cctReserve)
	denominator := new(big.Int).Mul(tokenReserve, bpsScale)
	denominator.Add(denominator, amountInWithFee)

	out := new(big.Int).Div(numerator, denominator)
	if out.Sign() == 0 || out.Cmp(cctReserve) >= 0 {
		return "0", false
	}
	return FormatCctSmallest(out), true
}

// SwapCCTForToken/SwapTokenForCCT apply the constant-product formula
// with the fee taken from the input side, identical to Uniswap v2 -
// same math as the Quote* functions above, but actually committing
// the reserve changes.
func (p *LiquidityPool) SwapCCTForToken(amountIn string) (amountOut uint64, ok bool) {
	amountOut, ok = p.QuoteCCTForToken(amountIn)
	if !ok {
		return 0, false
	}

	cctReserve := bigCct(p.CCTReserve)
	cctReserve.Add(cctReserve, bigCct(amountIn))
	p.CCTReserve = FormatCctSmallest(cctReserve)
	p.TokenReserve -= amountOut
	return amountOut, true
}

func (p *LiquidityPool) SwapTokenForCCT(amountIn uint64) (amountOut string, ok bool) {
	amountOut, ok = p.QuoteTokenForCCT(amountIn)
	if !ok {
		return "0", false
	}

	p.TokenReserve += amountIn
	cctReserve := bigCct(p.CCTReserve)
	cctReserve.Sub(cctReserve, bigCct(amountOut))
	p.CCTReserve = FormatCctSmallest(cctReserve)
	return amountOut, true
}
