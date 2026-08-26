package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/creditchain/creditchaind/core"
)

func HandleBorrow(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	type Request struct {
		Amount uint64
	}

	var req Request

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if req.Amount == 0 {
		http.Error(w, "amount must be positive", http.StatusBadRequest)
		return
	}

	if !hasEnoughCollateral(address, req.Amount, time.Now().Unix()) {
		http.Error(
			w,
			"insufficient locked collateral - lock staked CCT, oETH, or STETH_CCC1 covering at least this amount first",
			http.StatusBadRequest,
		)
		return
	}

	wallet := walletStore.Get(
		address,
	)

	// Repayment Trust specifically, not overall TSI - credit terms
	// should key off the actually-relevant credit-risk signal, so a
	// weak Trading/Governance/etc. dimension can never mask (or a
	// strong one paper over) real repayment history. See
	// core.TrustDimensionValue's doc comment for why this needs no
	// cutover check of its own - it already falls back to the flat
	// Reputation for any wallet whose Trust isn't seeded yet.
	limit := core.BorrowLimit(
		core.TrustDimensionValue(wallet, core.DimensionRepayment),
	)

	if req.Amount > limit {
		http.Error(
			w,
			"borrow limit exceeded",
			http.StatusBadRequest,
		)
		return
	}

	// The interest rate is locked in now, at the reputation (and
	// collateral price) the borrower has at submission time, rather
	// than recomputed when the block confirms - otherwise a
	// reputation or price change between submission and confirmation
	// would silently change the terms of a loan the borrower already
	// agreed to.
	interest := core.InterestRate(
		core.TrustDimensionValue(wallet, core.DimensionRepayment),
	)

	// oETH collateral is a 1:1 receipt for staked ETHTEST, so its
	// market value - and this loan's real risk - moves with the
	// ETHTEST/CCT pool's own price. CCT collateral gets no adjustment:
	// CCT is the numeraire everything else prices IN, so there's no
	// separate "CCT price" to react to. See
	// core.PriceAdjustedInterestRate's doc comment.
	if collateralSymbol, ok := peekCollateralSymbolForLoan(address, req.Amount, time.Now().Unix()); ok && collateralSymbol == "OETH" {
		interest = core.PriceAdjustedInterestRate(interest, priceHistoryStore.Get("ETHTEST"))
	}

	data, _ := json.Marshal(borrowData{Interest: interest})

	tx := recordTx(auth, "", address, req.Amount, core.TxBorrow, string(data))

	json.NewEncoder(w).Encode(
		map[string]string{
			"status": "pending",
			"txId":   tx.ID,
		},
	)
}
