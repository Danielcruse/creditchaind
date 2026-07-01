package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/creditchain/creditchaind/core"
)

func HandleBorrow(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	type Request struct {
		Address string
		Amount  uint64
	}

	var req Request

	err := json.NewDecoder(
		r.Body,
	).Decode(
		&req,
	)

	if err != nil {
		http.Error(
			w,
			err.Error(),
			http.StatusBadRequest,
		)
		return
	}

	collateral := collateralStore.Locked[req.Address]

	if collateral == 0 {
		http.Error(
			w,
			"no collateral",
			http.StatusBadRequest,
		)
		return
	}

	profile := creditStore.Get(
		req.Address,
	)

	limit := core.BorrowLimit(
		profile.Score,
	)

	if req.Amount > limit {
		http.Error(
			w,
			"borrow limit exceeded",
			http.StatusBadRequest,
		)
		return
	}

	interest := core.InterestRate(
		profile.Score,
	)

	id :=
		"loan-" +
			strconv.Itoa(
				len(
					loanStore.Loans,
				)+1,
			)

	loanStore.CreateLoan(
		id,
		req.Address,
		req.Amount,
		interest,
	)

	creditHistory.Add(
		req.Address,
		core.CreditEvent{
			Type:   "BORROW",
			Amount: req.Amount,
		},
	)

	core.RecordLoanTaken(
		profile,
	)

	tokenStore.Mint(
		req.Address,
		req.Amount,
	)

	json.NewEncoder(w).Encode(
		map[string]string{
			"loanID": id,
		},
	)
}