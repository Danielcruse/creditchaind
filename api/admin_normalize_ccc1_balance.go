package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

// admin_normalize_ccc1_balance.go - a narrow, one-off migration action
// for exactly one real incident: an ETH/USDC vault release fee, and a
// balance-recovery transfer meant to fix it, both minted an ETH_CCC1
// balance under an un-normalized (mixed-case, checksummed) address key -
// see api/vault_fee.go's creditVaultReleaseFeeToRelayer and
// api/admin_transfer.go's applyAdminTransfer doc comments for the two
// bugs (now fixed) that caused this. The balance is real and on the
// ledger, but permanently unreachable through every normal normalized-
// address code path (HandleVaultBalance, resolveBalanceAddress, and
// TxAdminTransfer itself, now that IT normalizes both sides - which is
// exactly why a second, un-normalized-source-aware tool is needed: once
// TxAdminTransfer normalizes its own "from", it can no longer reach an
// already-un-normalized legacy key at all).
//
// See core.TxAdminNormalizeCcc1Balance's doc comment for the full
// design reasoning.

type adminNormalizeCcc1BalanceData struct {
	Symbol        string `json:"symbol"`        // "ETH_CCC1" or "USDC_CCC1"
	LegacyAddress string `json:"legacyAddress"` // exact, un-normalized key as it's actually stored - NOT normalized before use
}

// HandleAdminNormalizeCcc1Balance is admin-only (see requireAdminAuthed) -
// same trust model as HandleAdminTransfer, and for the same reason: the
// legacy key this moves value out of was never a real, signable address
// in the first place (it only ever existed as an accidental artifact of
// a case-normalization bug), so there is no wallet signature to require.
func HandleAdminNormalizeCcc1Balance(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	var req adminNormalizeCcc1BalanceData
	signedAuth, mode, err := requireAdminAuthed(r, &req)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	if req.Symbol != "ETH_CCC1" && req.Symbol != "USDC_CCC1" {
		http.Error(w, "symbol must be ETH_CCC1 or USDC_CCC1", http.StatusBadRequest)
		return
	}
	if req.LegacyAddress == "" {
		http.Error(w, "legacyAddress is required", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordAdminTx(signedAuth, mode, "", 0, core.TxAdminNormalizeCcc1Balance, string(data))

	json.NewEncoder(w).Encode(map[string]string{"status": "pending", "txId": tx.ID})
}

// applyAdminNormalizeCcc1Balance reads the RAW balance under
// data.LegacyAddress (deliberately NOT normalized - that's the whole
// point, to reach the exact un-normalized key a prior bug minted under)
// and moves it to that same address's properly normalized key. A safe
// no-op if the legacy key is already normalized (nothing to migrate) or
// already has a zero balance (already migrated, or never had one) -
// idempotent against a replayed/duplicate transaction, same convention
// every other apply function in this file follows.
func applyAdminNormalizeCcc1Balance(tx core.Transaction) {
	var data adminNormalizeCcc1BalanceData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if data.LegacyAddress == "" {
		return
	}

	var store *core.DecimalTokenStore
	switch data.Symbol {
	case "ETH_CCC1":
		store = ethCcc1Store
	case "USDC_CCC1":
		store = usdcCcc1Store
	default:
		return
	}

	normalized := normalizeEthAddress(data.LegacyAddress)
	if normalized == data.LegacyAddress {
		return
	}

	raw := store.BalanceOf(data.LegacyAddress)
	if raw == "" || raw == "0" {
		return
	}

	if !store.Burn(data.LegacyAddress, raw) {
		return
	}
	store.Mint(normalized, raw)
}
