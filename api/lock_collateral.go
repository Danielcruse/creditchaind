package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

// lockCollateralData is both the client's signed payload shape and
// the transaction's Data shape - keeping them identical is what lets
// verifyTxAuth's signed/claimed comparison work with a plain struct
// equality check, the same pattern every other tx type here uses.
type lockCollateralData struct {
	Amount uint64 `json:"amount"`
	// Symbol is "CCT" (locks from the caller's STAKED CCT position,
	// not liquid/spendable CCT), "OETH" (locks from the caller's oETH
	// receipt balance - see core.Wallet.OETHBalance's doc comment;
	// oETH is deliberately never transferable/sendable anywhere else in
	// this app - this lock is its only real use), or "STETH_CCC1"
	// (locks from the caller's real vault-locked stETH balance - see
	// api/vault_token.go's stethCcc1Store - the first collateral type
	// backed by an actual real-world asset rather than a purely
	// internal counter).
	Symbol string `json:"symbol"`
}

// HandleLockCollateral moves Amount of Symbol out of the caller's
// staked CCT or oETH balance into the shared CollateralStore - never
// out of liquid CCT. See applyLockCollateral for why: collateral that
// came from an already-staked/receipt position is real skin already
// committed, and (unlike locking arbitrary spendable balance) can be
// released back to that exact same staked/oETH balance once a loan
// backed by it is repaid.
func HandleLockCollateral(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	var req lockCollateralData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if req.Amount == 0 {
		http.Error(w, "amount must be positive", http.StatusBadRequest)
		return
	}
	if req.Symbol != "CCT" && req.Symbol != "OETH" && req.Symbol != "STETH_CCC1" {
		http.Error(w, "symbol must be 'CCT', 'OETH', or 'STETH_CCC1'", http.StatusBadRequest)
		return
	}

	var available uint64
	switch req.Symbol {
	case "CCT":
		available = stakingStore.StakeOf(address, "CCT")
	case "OETH":
		available = walletStore.Get(address).OETHBalance
	case "STETH_CCC1":
		available = stethCcc1TransferAdapter.BalanceOf(resolveBalanceAddress(address, auth.PublicKey, "STETH_CCC1"))
	}
	if available < req.Amount {
		json.NewEncoder(w).Encode(
			map[string]bool{
				"success": false,
			},
		)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", req.Amount, core.TxLockCollateral, string(data))

	json.NewEncoder(w).Encode(
		map[string]any{
			"status": "pending",
			"txId":   tx.ID,
		},
	)
}
