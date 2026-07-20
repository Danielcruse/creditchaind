package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

var adminMultisigStore = core.NewAdminMultisigStore()

// adminMultisigProposeData/adminMultisigApproveData are the payload
// shapes for TxAdminMultisigPropose/TxAdminMultisigApprove - see
// core/transactions.go's doc comment on those types and
// recordAdminTx's doc comment in admin.go for how a normal admin
// action becomes one of these once multisig is configured.
type adminMultisigProposeData struct {
	ActionType core.TransactionType `json:"actionType"`
	Payload    string               `json:"payload"`
}

type adminMultisigApproveData struct {
	ProposalID string `json:"proposalId"`
}

// allowedAdminMultisigActionType is the exhaustive whitelist of admin
// action types multisig is allowed to gate/execute - deliberately
// explicit rather than "any TransactionType", so a malformed or
// unexpected ActionType in a proposal can never reach
// executeAdminMultisigAction's switch and silently no-op in a
// confusing way, and so this list is the one place to look to see
// everything multisig actually governs.
func allowedAdminMultisigActionType(t core.TransactionType) bool {
	switch t {
	case core.TxTokenMint,
		core.TxTreasuryPurchaseConfirm,
		core.TxTreasurySetSalePrice,
		core.TxTreasurySetPaused,
		core.TxTreasurySetAddress,
		core.TxGenesisPoolAllocation,
		core.TxGenesisPoolWithdraw,
		core.TxAdminTransfer,
		core.TxAnnouncementCreate:
		return true
	default:
		return false
	}
}

// HandleAdminMultisigProposals is a public, unauthenticated read - a
// pending proposal (and who's approved it so far) is exactly the kind
// of thing every authorized signer needs to be able to see without
// first proving they're one of them, the same reasoning
// HandleAdminWhoAmI already uses. Approvals are visible on-chain
// anyway once any signer's approve transaction confirms, so there's
// no confidentiality to protect here either.
func HandleAdminMultisigProposals(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)
	json.NewEncoder(w).Encode(adminMultisigStore.Pending())
}

// HandleAdminMultisigApprove lets one authorized signer add their own
// approval to an existing pending proposal - see
// applyAdminMultisigApprove for what happens once approvals reach the
// configured threshold. Reuses requireAdminAuthed exactly like every
// other admin handler; only mode==adminAuthMultisig is ever accepted
// here (the propose side already handles the single-wallet/legacy
// tiers for the OTHER 9 handlers - approval only makes sense as a
// concept once multisig itself is configured).
func HandleAdminMultisigApprove(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !adminMultisigEnabled() {
		http.Error(w, "multisig not configured on this network", http.StatusNotFound)
		return
	}

	var req adminMultisigApproveData
	signedAuth, mode, err := requireAdminAuthed(r, &req)
	if err != nil || mode != adminAuthMultisig {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": "request not signed by an authorized multisig signer"})
		return
	}
	if req.ProposalID == "" {
		http.Error(w, "proposalId is required", http.StatusBadRequest)
		return
	}
	if proposal, ok := adminMultisigStore.Get(req.ProposalID); !ok || proposal.Status != core.AdminMultisigPending {
		http.Error(w, "proposal not found or already resolved", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(signedAuth, signedAuth.Address, "", 0, core.TxAdminMultisigApprove, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

// applyAdminMultisigPropose creates a new pending proposal - the
// proposer's own signature already counts as its first approval,
// consistent with a real M-of-N multisig (the person initiating an
// action is, definitionally, one of the people who wants it to
// happen). Uses tx.ID as the proposal's own ID since transaction IDs
// are already unique and this transaction only ever produces one
// proposal.
func applyAdminMultisigPropose(tx core.Transaction) {
	var data adminMultisigProposeData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if !allowedAdminMultisigActionType(data.ActionType) {
		return
	}
	if _, exists := adminMultisigStore.Get(tx.ID); exists {
		return
	}

	proposal := &core.AdminMultisigProposal{
		ID:         tx.ID,
		ActionType: data.ActionType,
		Payload:    data.Payload,
		Proposer:   tx.AuthAddress,
		Approvals:  map[string]bool{tx.AuthAddress: true},
		Status:     core.AdminMultisigPending,
		CreatedAt:  tx.Timestamp,
	}
	adminMultisigStore.Create(proposal)

	maybeExecuteAdminMultisigProposal(proposal, tx.Timestamp)
}

// applyAdminMultisigApprove adds one more authorized signer's
// approval to an existing proposal - a signer who has already
// approved (including the original proposer) is a no-op, not a
// double-count, so resubmitting the same approval can never inflate
// progress toward the threshold.
func applyAdminMultisigApprove(tx core.Transaction) {
	var data adminMultisigApproveData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}

	proposal, ok := adminMultisigStore.Get(data.ProposalID)
	if !ok || proposal.Status != core.AdminMultisigPending {
		return
	}
	if proposal.Approvals[tx.AuthAddress] {
		return
	}
	proposal.Approvals[tx.AuthAddress] = true

	maybeExecuteAdminMultisigProposal(proposal, tx.Timestamp)
}

// maybeExecuteAdminMultisigProposal is the single chokepoint that
// decides whether a proposal has reached its approval threshold and,
// if so, executes it exactly once. The Status!=Pending guard is what
// actually enforces "exactly once" - without it, a late-arriving
// extra approval after execution (harmless on its own, since
// Approvals only ever grows) would re-run executeAdminMultisigAction
// and double-apply the underlying mint/transfer/etc.
//
// adminMultisigThreshold() is read live, not snapshotted at proposal
// creation - if an operator raises the threshold while a proposal is
// still pending, that proposal needs the new, higher bar too. This
// can only ever make execution require MORE approvals, never fewer,
// so it's a conservative direction to be dynamic in.
func maybeExecuteAdminMultisigProposal(p *core.AdminMultisigProposal, asOf int64) {
	if p.Status != core.AdminMultisigPending {
		return
	}
	if len(p.Approvals) < adminMultisigThreshold() {
		return
	}

	executeAdminMultisigAction(p, asOf)
	p.Status = core.AdminMultisigExecuted
	p.ExecutedAt = asOf
}

// executeAdminMultisigAction is what actually runs the underlying
// admin mutation, once and only once, by calling the EXACT SAME
// apply* function every one of these action types already had before
// multisig existed - directly, not through ApplyTransaction/
// applyTxOnce's normal dispatch, since that path insists on
// independently re-verifying a real signature on the transaction it's
// given (core.VerifyTransactionAuth), and this synthetic transaction
// deliberately carries none. That's correct here, not a gap: this
// action's authorization was already established through the real,
// independently-verified propose/approve signature chain that got it
// to threshold - re-deriving a signature check on a transaction that
// was never itself signed by anyone would be theater, not security.
func executeAdminMultisigAction(p *core.AdminMultisigProposal, asOf int64) {
	synthetic := core.Transaction{
		ID:        p.ID + "-executed",
		Type:      p.ActionType,
		Data:      p.Payload,
		Timestamp: asOf,
	}

	switch p.ActionType {
	case core.TxTokenMint:
		applyTokenMint(synthetic)
	case core.TxTreasuryPurchaseConfirm:
		applyTreasuryPurchaseConfirm(synthetic)
	case core.TxTreasurySetSalePrice:
		applyTreasurySetSalePrice(synthetic)
	case core.TxTreasurySetPaused:
		applyTreasurySetPaused(synthetic)
	case core.TxTreasurySetAddress:
		applyTreasurySetAddress(synthetic)
	case core.TxGenesisPoolAllocation:
		applyGenesisPoolAllocation(synthetic)
	case core.TxGenesisPoolWithdraw:
		applyGenesisPoolWithdraw(synthetic)
	case core.TxAdminTransfer:
		applyAdminTransfer(synthetic)
	case core.TxAnnouncementCreate:
		applyAnnouncementCreate(synthetic)
	}
}

// verifyAdminMultisigTxAuth is the verifyTxAuth case for both
// TxAdminMultisigPropose and TxAdminMultisigApprove -
// core.VerifyTransactionAuth (already run before this by every
// ApplyTransaction path) already confirmed tx.AuthAddress genuinely
// signed this transaction; this just confirms that address is one of
// the network's configured signers.
func verifyAdminMultisigTxAuth(tx core.Transaction) bool {
	return isAdminMultisigSigner(tx.AuthAddress)
}
