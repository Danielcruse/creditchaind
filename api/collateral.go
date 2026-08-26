package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

var collateralStore =
	core.NewCollateralStore()

// HandleCollateral reports how much an address currently has locked
// as collateral, broken out by symbol (CCT = staked CCT, OETH = oETH
// receipt balance) rather than one undifferentiated number.
func HandleCollateral(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	address := r.URL.Query().Get("address")

	json.NewEncoder(w).Encode(
		map[string]uint64{
			"lockedCCT":  collateralStore.LockedOf(address, "CCT"),
			"lockedOETH": collateralStore.LockedOf(address, "OETH"),
		},
	)
}
