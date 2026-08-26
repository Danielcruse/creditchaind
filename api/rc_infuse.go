package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

var rcInfusionStore = core.NewRCInfusionStore()

// maxTrustReasonLen mirrors the existing 500-char governance-status
// precedent (core.GovernanceStatusStore) - a bounded free-text field,
// not unlimited.
const maxTrustReasonLen = 500

// rcInfusionBoostCap is the ceiling on how much a wallet's own
// accumulated RCInfusionWeightBps can add to its miningPowerScore
// (api/mining_power.go) - so infusion alone can never produce
// unbounded emission. A judgment-call ceiling, e.g. capping the
// effective score at 150/100.
const rcInfusionBoostCap = 50

type rcInfuseData struct {
	RCAmount    uint64 `json:"rcAmount"`
	TrustReason string `json:"trustReason"`
}

// HandleInfuseRC lets a wallet burn its own RC and mint the identical
// amount into the governance RC pool as a self-declared statement of
// "real world trust" (core.RCInfusion) - TrustReason is explicitly
// self-attested, never independently verified, same disclosed-metadata
// convention as the Token Standards work's PegReference.
func HandleInfuseRC(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req rcInfuseData
	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if req.RCAmount == 0 {
		http.Error(w, "rcAmount must be positive", http.StatusBadRequest)
		return
	}
	if len(req.TrustReason) > maxTrustReasonLen {
		http.Error(w, "trustReason must be under 500 characters", http.StatusBadRequest)
		return
	}
	if reptStore.BalanceOf(address) < req.RCAmount {
		http.Error(w, fmt.Sprintf("insufficient RC balance - you have %d", reptStore.BalanceOf(address)), http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxRCInfuse, string(data))

	json.NewEncoder(w).Encode(map[string]string{"status": "pending", "txId": tx.ID})
}

// applyRCInfuse burns RC from the sender, mints the same amount into
// the governance RC pool, records the infusion, and boosts the
// sender's own wallet.RCInfusionWeightBps - scaled by the
// RealWorldTrustWeightBps executives have currently voted in effect AT
// THE TIME of this infusion (never retroactively revalued later - see
// core.Wallet.RCInfusionWeightBps's doc comment).
func applyRCInfuse(tx core.Transaction) {
	var data rcInfuseData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if data.RCAmount == 0 || len(data.TrustReason) > maxTrustReasonLen {
		return
	}
	if !reptStore.Burn(tx.From, data.RCAmount) {
		return
	}
	reptStore.Mint(governanceRCPoolAddress(), data.RCAmount)

	weightBps := governanceParam(realWorldTrustWeightParamKey, 0)
	boost := data.RCAmount * weightBps / 10000

	wallet := walletStore.Get(tx.From)
	wallet.RCInfusionWeightBps += boost

	rcInfusionStore.Record(&core.RCInfusion{
		Address:     tx.From,
		RCAmount:    data.RCAmount,
		TrustReason: data.TrustReason,
		CreatedAt:   tx.Timestamp,
	})
}

// HandleRecentRCInfusions is a public read of the most recent
// infusions - a self-attested, publicly-listable record, same
// "an application's status carries no value to protect" reasoning
// already used elsewhere in this package.
func HandleRecentRCInfusions(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)
	json.NewEncoder(w).Encode(rcInfusionStore.Recent(50))
}
