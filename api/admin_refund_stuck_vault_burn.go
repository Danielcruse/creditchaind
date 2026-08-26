package api

import (
	"encoding/json"
	"math/big"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

// admin_refund_stuck_vault_burn.go - a narrow, incident-specific admin
// action for exactly one real situation: a vault release approval
// (core.VaultReleaseApproval) that burned a user's ETH_CCC1/USDC_CCC1
// balance but can never actually deliver anything - either because its
// bridge-validator signatures are permanently stuck (see
// api/vault_release_approve_watch.go's stale-signature fix, which
// prevents new occurrences but can't undo one already stuck before the
// fix shipped) or because api/vault_fee.go's splitVaultReleaseFee
// zeroed the release amount out entirely against a flat gas-cost fee.
// Safe specifically because the real backing ETH/USDC never actually
// left the deployed vault contract in either case - the on-chain
// release() call has never succeeded (still PENDING), so restoring the
// burner's off-chain CCC1 claim on it is a correction, not new,
// fictional supply.
//
// Deliberately the narrowest possible tool: it can only ever act on an
// approval that is still PENDING (refuses outright once RELEASED,
// where the real value genuinely did leave the vault - refunding that
// too would double-spend), and it refunds the exact original burned
// amount (AmountSmallestUnit + PendingFeeAmountSmallestUnit, i.e. the
// full pre-fee amount the user originally burned) to that approval's
// own burner, nobody else. Marks the approval REFUNDED so it can never
// be signed, relayed, or refunded a second time.

type adminRefundStuckVaultBurnData struct {
	BurnTxId string `json:"burnTxId"`
}

// HandleAdminRefundStuckVaultBurn is admin-only (see requireAdminAuthed) -
// same trust model as HandleAdminTransfer/HandleAdminNormalizeCcc1Balance.
func HandleAdminRefundStuckVaultBurn(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	var req adminRefundStuckVaultBurnData
	signedAuth, mode, err := requireAdminAuthed(r, &req)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	if req.BurnTxId == "" {
		http.Error(w, "burnTxId is required", http.StatusBadRequest)
		return
	}
	approval, ok := vaultReleaseApprovalStore.Get(req.BurnTxId)
	if !ok {
		http.Error(w, "no release approval found for that burn transaction", http.StatusNotFound)
		return
	}
	if approval.Status != core.VaultReleasePending {
		http.Error(w, "this approval is not PENDING - it has either already released (real value left the vault, nothing to refund) or was already refunded", http.StatusBadRequest)
		return
	}
	if approval.Chain == "ETH" || approval.Chain == "USDC" {
		if _, ok := ethAddressStore.Get(approval.BurnCcAddress); !ok {
			http.Error(w, "could not resolve a real self-custodial address for the burner - refusing rather than guess", http.StatusUnprocessableEntity)
			return
		}
	}

	data, _ := json.Marshal(req)
	tx := recordAdminTx(signedAuth, mode, "", 0, core.TxAdminRefundStuckVaultBurn, string(data))

	json.NewEncoder(w).Encode(map[string]string{"status": "pending", "txId": tx.ID})
}

// applyAdminRefundStuckVaultBurn re-checks everything the HTTP fast-fail
// did (never trusting that pass alone) and mints the full original
// burned amount (release + the fee that would otherwise have gone
// uncredited forever) back to the burner's real, already-registered
// self-custodial ETH/USDC address - the SAME address
// resolveBalanceAddress would have resolved to at burn time (looked up
// directly via ethAddressStore rather than re-deriving, so this can
// never accidentally credit a fallback hash-derived address instead of
// the real one a depositor's actual funds live at).
func applyAdminRefundStuckVaultBurn(tx core.Transaction) {
	var data adminRefundStuckVaultBurnData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if data.BurnTxId == "" {
		return
	}
	approval, ok := vaultReleaseApprovalStore.Get(data.BurnTxId)
	if !ok || approval.Status != core.VaultReleasePending {
		return
	}

	released, err := core.ParseDecimalSmallest(approval.AmountSmallestUnit)
	if err != nil {
		return
	}
	fee, err := core.ParseDecimalSmallest(approval.PendingFeeAmountSmallestUnit)
	if err != nil {
		fee = big.NewInt(0) // no fee was ever recorded (e.g. BTC) - refund just the release amount
	}
	total := new(big.Int).Add(released, fee)
	if total.Sign() <= 0 {
		return
	}
	totalStr := total.String()

	switch approval.Chain {
	case "ETH":
		dest, ok := ethAddressStore.Get(approval.BurnCcAddress)
		if !ok {
			return
		}
		if err := ethCcc1Store.Mint(normalizeEthAddress(dest.EthAddress), totalStr); err != nil {
			return
		}
	case "USDC":
		dest, ok := ethAddressStore.Get(approval.BurnCcAddress)
		if !ok {
			return
		}
		if err := usdcCcc1Store.Mint(normalizeEthAddress(dest.EthAddress), totalStr); err != nil {
			return
		}
	default:
		return
	}

	approval.Status = core.VaultReleaseRefunded
}
