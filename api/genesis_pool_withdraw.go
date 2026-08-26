package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

type genesisPoolWithdrawData struct {
	Pool   string `json:"pool"`
	To     string `json:"to"`
	Amount uint64 `json:"amount"`
}

// isKeylessGenesisPool whitelists exactly the 4 genesis pools that
// have no controlling keypair of their own - see
// core.TxGenesisPoolWithdraw's doc comment. Deliberately excludes
// Foundation Treasury, which already has its own real address
// (treasuryStore.Treasury.FoundationAddress) and its own admin
// controls (HandleAdminSetTreasuryAddress etc.) - this is the one
// correctness-critical guard in this file, since a typo'd allowlist
// here would open a second, unintended withdrawal route for Treasury
// funds.
func isKeylessGenesisPool(name string) bool {
	switch name {
	case genesisPoolValidatorReward, genesisPoolEcosystemDev, genesisPoolGovernanceTreasury, genesisPoolCommunityIncentives:
		return true
	default:
		return false
	}
}

// HandleGenesisPoolWithdraw moves Amount of CCT out of one of the 4
// keyless genesis pools to a real destination address - admin-only
// (requireAdminAuthed), recorded as a TxGenesisPoolWithdraw so it
// replicates identically to every node rather than only mutating
// whichever node received this HTTP request.
func HandleGenesisPoolWithdraw(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req genesisPoolWithdrawData
	signedAuth, mode, err := requireAdminAuthed(r, &req)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	if !isKeylessGenesisPool(req.Pool) {
		http.Error(w, "unknown or non-withdrawable pool", http.StatusBadRequest)
		return
	}
	if req.To == "" {
		http.Error(w, "to is required", http.StatusBadRequest)
		return
	}
	if req.Amount == 0 {
		http.Error(w, "amount must be positive", http.StatusBadRequest)
		return
	}
	if !hasCctBalance(genesisPoolAddress(req.Pool), req.Amount) {
		http.Error(w, "amount exceeds pool balance", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordAdminTx(signedAuth, mode, "", 0, core.TxGenesisPoolWithdraw, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

// applyGenesisPoolWithdraw is HandleGenesisPoolWithdraw's apply-side
// counterpart - never trusts submission-time state, same convention
// as every other apply function in this codebase. tokenStore.Transfer
// itself already guards insufficient balance (silent no-op), so no
// separate balance re-check is needed beyond the pool-name guard.
func applyGenesisPoolWithdraw(tx core.Transaction) {
	var data genesisPoolWithdrawData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if !isKeylessGenesisPool(data.Pool) || data.To == "" || data.Amount == 0 {
		return
	}

	transferCctWhole(genesisPoolAddress(data.Pool), data.To, data.Amount)
}
