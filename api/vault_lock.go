package api

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/creditchain/creditchaind/core"
)

// currentChainHeight is the CreditChain block index "now" - the latest
// applied block's own Index. Safe to call from within an apply*
// function (applyVaultBurn/applyVaultReleaseApprove): every ApplyBlock
// caller appends the block to blockchain.Blocks BEFORE calling
// ApplyBlock, so by the time a transaction inside that block is being
// applied, blockchain.LatestBlock() already IS that same block - making
// this deterministic and identical across every node applying the same
// block. Returns 0 if blockchain isn't initialized yet (startup/tests).
func currentChainHeight() uint64 {
	if blockchain == nil || len(blockchain.Blocks) == 0 {
		return 0
	}
	return blockchain.LatestBlock().Index
}

// vaultReleaseConfirmationsRemaining reports how many more CreditChain
// blocks must be produced before approval's burn clears
// core.VaultReleaseConfirmationDepthBlocks - 0 once it's already deep
// enough. Shared by the submission-time gate (HandleVaultReleaseApprove)
// and the read-only status endpoint (HandleVaultReleaseStatus).
func vaultReleaseConfirmationsRemaining(approval *core.VaultReleaseApproval) uint64 {
	height := currentChainHeight()
	if height <= approval.BurnBlockHeight {
		return core.VaultReleaseConfirmationDepthBlocks
	}
	confirmed := height - approval.BurnBlockHeight
	if confirmed >= core.VaultReleaseConfirmationDepthBlocks {
		return 0
	}
	return core.VaultReleaseConfirmationDepthBlocks - confirmed
}

// vaultChainValid is the shared "is this a chain the vault bridge
// actually supports" check - ETH/BTC/USDC/STETH only, per the user's
// explicit scope (not YEN-e or any other asset). STETH is Lido's real
// stETH, locked into the same vault contract via depositERC20() - see
// api/lido_stake.go.
func vaultChainValid(chain string) bool {
	switch chain {
	case "ETH", "BTC", "USDC", "STETH":
		return true
	default:
		return false
	}
}

// legacyVaultLockAttestationID is the ORIGINAL dedup key (audit finding
// C-05's root cause): chain + external tx identifier only, deliberately
// NOT covering the claimed recipient/amount, so every validator
// attesting to the same real external event - honest or fraudulent -
// collided on one shared record. Still used two ways after the full
// fix below: (1) as the first half of the new content-derived ID's
// preimage, and (2) on its own, to detect and continue an attestation
// process that already started under this scheme before the fix
// shipped - see vaultLockAttestationID's doc comment.
func legacyVaultLockAttestationID(chain, externalTxHash string, externalIndex uint32) string {
	return chain + ":" + externalTxHash + ":" + strconv.FormatUint(uint64(externalIndex), 10)
}

// vaultLockAttestationID is the dedup key every bridge validator's
// independent attestation for the SAME real external lock event
// converges on - see core.VaultLockAttestation's doc comment for why
// this must be derivable identically by every node from public
// information, never assigned by whichever node happens to attest
// first.
//
// Full fix for audit finding C-05 (2026-08-16), superseding the
// earlier partial mitigation (a reject-on-conflict check still in
// applyVaultLockAttest, now primarily a legacy-path safety net - see
// its own comment): the ID now folds in the claimed recipient/amount,
// not just chain + external tx identifier, so a malicious validator's
// front-run claim and honest validators' real claim naturally diverge
// into separate, independently-progressing records instead of
// colliding on one shared ID the way legacyVaultLockAttestationID
// always did. The fraudulent record can still be created, but it can
// never block or absorb votes intended for the real one - it just sits
// stuck at whatever vote count the colluding minority reaches, forever
// short of threshold (assuming fewer than threshold validators are
// dishonest, the entire trust model's baseline assumption already).
//
// Migration safety: an attestation process already under way under
// legacyVaultLockAttestationID (any attestation for this exact
// chain+tx+index already recorded before this fix, or still catching
// up during a rolling deploy) CONTINUES on that same legacy ID rather
// than splitting its already-accumulated votes into a new bucket -
// every node deterministically agrees on this by independently
// checking the same replicated store, so it never causes disagreement
// about which ID "the" record for an event lives at. Only a GENUINELY
// NEW event (no legacy record exists yet, checked identically by every
// node) uses the new content-derived scheme, from its very first vote
// onward - so every honest validator attesting to a new event's real
// facts converges on the identical new ID from the start.
func vaultLockAttestationID(data vaultLockAttestData) string {
	legacyID := legacyVaultLockAttestationID(data.Chain, data.ExternalTxHash, data.ExternalIndex)
	if _, exists := vaultLockAttestationStore.Get(legacyID); exists {
		return legacyID
	}
	sum := sha256.Sum256([]byte(legacyID + ":" + data.DepositorCcAddress + ":" + data.DepositorPublicKey + ":" + data.AmountSmallestUnit))
	return hex.EncodeToString(sum[:])
}

// vaultLockAttestData is TxVaultLockAttest's payload.
type vaultLockAttestData struct {
	Chain              string `json:"chain"`
	ExternalTxHash     string `json:"externalTxHash"`
	ExternalIndex      uint32 `json:"externalIndex"`
	DepositorCcAddress string `json:"depositorCcAddress"`
	DepositorPublicKey string `json:"depositorPublicKey"`
	AmountSmallestUnit string `json:"amountSmallestUnit"`
}

// ccc1SymbolForChain maps a vault chain to the CCC1 token symbol it
// mints - see core/address.go's ETHCcc1AddressTag/UsdcCcc1AddressTag/
// BtcCcc1AddressTag.
func ccc1SymbolForChain(chain string) string {
	switch chain {
	case "ETH":
		return "ETH_CCC1"
	case "USDC":
		return "USDC_CCC1"
	case "BTC":
		return "BTC_CCC1"
	case "STETH":
		return "STETH_CCC1"
	default:
		return ""
	}
}

// mintVaultToken credits the depositor's CCC1 balance for the given
// chain - ETH/USDC use the big-int DecimalTokenStore (wei/USDC-6dp
// amounts can exceed uint64), BTC uses the plain uint64 TokenStore
// (sats never overflow, matches bridgedBtcStore's existing precedent).
// Returns false only on a malformed amount, never on "insufficient
// balance" (minting has no such concept).
func mintVaultToken(chain, ccAddress, publicKeyHex, amountSmallestUnit string) bool {
	symbol := ccc1SymbolForChain(chain)
	if symbol == "" {
		return false
	}
	address := resolveBalanceAddress(ccAddress, publicKeyHex, symbol)

	switch chain {
	case "ETH":
		return ethCcc1Store.Mint(address, amountSmallestUnit) == nil
	case "USDC":
		return usdcCcc1Store.Mint(address, amountSmallestUnit) == nil
	case "STETH":
		return stethCcc1Store.Mint(address, amountSmallestUnit) == nil
	case "BTC":
		sats, err := strconv.ParseUint(amountSmallestUnit, 10, 64)
		if err != nil {
			return false
		}
		btcCcc1Store.Mint(address, sats)
		return true
	default:
		return false
	}
}

// HandleVaultLockAttest lets one bridge validator submit their own
// independent confirmation that they personally observed a real vault
// lock event (at the required confirmation depth - enforced by the
// Phase B/C watch loop that calls this, not here) - see
// applyVaultLockAttest for the threshold-triggered mint.
func HandleVaultLockAttest(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req vaultLockAttestData
	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}
	if !isBridgeValidatorAddress(address) {
		http.Error(w, "address is not a configured bridge validator", http.StatusForbidden)
		return
	}
	if !vaultChainValid(req.Chain) {
		http.Error(w, "chain must be ETH, BTC, USDC, or STETH", http.StatusBadRequest)
		return
	}
	if req.ExternalTxHash == "" || req.DepositorCcAddress == "" || req.DepositorPublicKey == "" {
		http.Error(w, "externalTxHash, depositorCcAddress, and depositorPublicKey are required", http.StatusBadRequest)
		return
	}
	if _, err := core.ParseDecimalSmallest(req.AmountSmallestUnit); err != nil {
		http.Error(w, "invalid amountSmallestUnit", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxVaultLockAttest, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status":        "pending",
		"txId":          tx.ID,
		"attestationId": vaultLockAttestationID(req),
	})
}

// HandleVaultLockStatus is a public, unauthenticated read - "how many
// of the M required validators have confirmed my deposit" is exactly
// what a depositor's own frontend needs to poll while waiting for CCC1
// to appear (see website/js/send-bridged-eth.js's review->confirm-
// >broadcast precedent this reuses the shape of).
//
// depositorCcAddress/depositorPublicKey/amountSmallestUnit are optional
// query params, added alongside vaultLockAttestationID's C-05 full fix:
// a genuinely NEW event (see that function's doc comment) is stored
// under a content-derived ID this endpoint can't otherwise reconstruct
// from chain+tx+index alone. A caller's own deposit is the one case
// where it always knows its own recipient/amount already (it's the one
// who made the deposit) - see website/js/vault.js's fetchLockStatus,
// now passed by every caller. Omitting them still works for any event
// still resolving under the legacy ID scheme (see
// legacyVaultLockAttestationID) - only a genuinely new event's status
// would be unavailable without them, a disclosed, narrow gap for a
// caller that hasn't been updated to pass its own known deposit facts.
func HandleVaultLockStatus(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	chain := r.URL.Query().Get("chain")
	txHash := r.URL.Query().Get("externalTxHash")
	index, _ := strconv.ParseUint(r.URL.Query().Get("externalIndex"), 10, 32)

	id := legacyVaultLockAttestationID(chain, txHash, uint32(index))
	if _, exists := vaultLockAttestationStore.Get(id); !exists {
		depositorCcAddress := r.URL.Query().Get("depositorCcAddress")
		depositorPublicKey := r.URL.Query().Get("depositorPublicKey")
		amountSmallestUnit := r.URL.Query().Get("amountSmallestUnit")
		if depositorCcAddress != "" && depositorPublicKey != "" && amountSmallestUnit != "" {
			id = vaultLockAttestationID(vaultLockAttestData{
				Chain:              chain,
				ExternalTxHash:     txHash,
				ExternalIndex:      uint32(index),
				DepositorCcAddress: depositorCcAddress,
				DepositorPublicKey: depositorPublicKey,
				AmountSmallestUnit: amountSmallestUnit,
			})
		}
	}
	attestation, ok := vaultLockAttestationStore.Get(id)
	if !ok {
		json.NewEncoder(w).Encode(map[string]any{
			"found":             false,
			"attestations":      0,
			"requiredThreshold": bridgeValidatorThreshold(),
		})
		return
	}
	json.NewEncoder(w).Encode(map[string]any{
		"found":             true,
		"status":            attestation.Status,
		"attestations":      len(attestation.Attestors),
		"requiredThreshold": bridgeValidatorThreshold(),
		"mintedAt":          attestation.MintedAt,
	})
}

// applyVaultLockAttest re-checks everything the handler already checked
// (never trust submission-time state), adds this validator's
// attestation to the shared record (lazily created on first
// attestation for a given external lock event, same lazy-creation
// discipline this codebase already uses elsewhere - e.g.
// core.PredictionPool), and mints CCC1 exactly once the moment enough
// DISTINCT validators have each independently confirmed the same
// event. This is the entire fix for the mirror model's flaw: no single
// node's word is ever enough on its own.
func applyVaultLockAttest(tx core.Transaction) {
	if depositHeld("VAULT_LOCK_ATTEST", tx.ID) {
		return
	}

	var data vaultLockAttestData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if !isBridgeValidatorAddress(tx.AuthAddress) {
		return
	}
	if !vaultChainValid(data.Chain) {
		return
	}
	if data.ExternalTxHash == "" || data.DepositorCcAddress == "" || data.DepositorPublicKey == "" {
		return
	}
	if _, err := core.ParseDecimalSmallest(data.AmountSmallestUnit); err != nil {
		return
	}

	id := vaultLockAttestationID(data)
	attestation, ok := vaultLockAttestationStore.Get(id)
	if !ok {
		attestation = &core.VaultLockAttestation{
			ID:                 id,
			Chain:              data.Chain,
			ExternalTxHash:     data.ExternalTxHash,
			DepositorCcAddress: data.DepositorCcAddress,
			DepositorPublicKey: data.DepositorPublicKey,
			AmountSmallestUnit: data.AmountSmallestUnit,
			Attestors:          map[string]bool{},
			Status:             core.VaultLockPending,
			CreatedAt:          tx.Timestamp,
		}
		vaultLockAttestationStore.Create(attestation)
	}

	// Already minted (or this validator already attested) - both are
	// no-ops, not errors, so a resubmitted/duplicate attestation can
	// never double-mint or inflate the count.
	if attestation.Status == core.VaultLockMinted {
		return
	}
	if attestation.Attestors[tx.AuthAddress] {
		return
	}

	// Originally audit finding C-05's full regression guard, back when
	// EVERY event (not just ones still resolving under
	// legacyVaultLockAttestationID) shared one ID regardless of claimed
	// recipient/amount. Now that vaultLockAttestationID folds
	// recipient/amount into a genuinely new event's ID itself (see its
	// doc comment), two attestations sharing an ID can only ever
	// disagree here if the event is still resolving under the legacy
	// scheme (a real conflict there is possible, though narrower now:
	// only events already in flight at the moment of the fix, not any
	// new one) - kept as a defense-in-depth safety net for that legacy
	// path rather than removed, since it's still strictly correct
	// (a mismatch here should never be silently absorbed) even though
	// it's no longer the primary defense.
	if attestation.DepositorCcAddress != data.DepositorCcAddress ||
		attestation.DepositorPublicKey != data.DepositorPublicKey ||
		attestation.AmountSmallestUnit != data.AmountSmallestUnit {
		log.Println("vault lock attestation CONFLICT (possible front-run, see audit finding C-05) - rejecting: id", id, "validator", tx.AuthAddress, "claimed recipient/amount does not match the stored record")
		return
	}

	attestation.Attestors[tx.AuthAddress] = true

	if len(attestation.Attestors) < bridgeValidatorThreshold() {
		return
	}
	if !mintVaultToken(attestation.Chain, attestation.DepositorCcAddress, attestation.DepositorPublicKey, attestation.AmountSmallestUnit) {
		return
	}
	attestation.Status = core.VaultLockMinted
	attestation.MintedAt = tx.Timestamp
}

// --- Release side: TxVaultBurn + TxVaultReleaseApprove ---

type vaultBurnData struct {
	Chain                 string `json:"chain"`
	DestinationExtAddress string `json:"destinationExtAddress"`
	AmountSmallestUnit    string `json:"amountSmallestUnit"`
}

// HandleVaultBurn lets a user burn their own CCC1 balance to request
// release of the real locked asset - see applyVaultBurn for the
// balance check (re-verified, never trusted from this preview) and
// core.VaultReleaseApproval for what happens next.
func HandleVaultBurn(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req vaultBurnData
	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}
	if !vaultChainValid(req.Chain) {
		http.Error(w, "chain must be ETH, BTC, USDC, or STETH", http.StatusBadRequest)
		return
	}
	if bridgePaused() {
		http.Error(w, "bridge releases are temporarily paused by an emergency governance vote - try again once it lifts", http.StatusServiceUnavailable)
		return
	}
	if req.Chain == "STETH" {
		// STETH_CCC1 can only be locked (via the ETH liquid-staking
		// flow, see api/lido_stake.go) - unstaking/withdrawal is a
		// deliberately separate follow-up (Lido's own withdrawal queue
		// has its own multi-step, waiting-period mechanics that don't
		// fit this burn+release path unchanged). Rejected explicitly
		// here rather than left to applyVaultBurn's switch, which has
		// no STETH case and would otherwise silently no-op - a real
		// user-submitted burn transaction that never actually releases
		// anything is a worse failure mode than a clear 400 up front.
		http.Error(w, "STETH_CCC1 withdrawal is not supported yet - staking is one-way for now", http.StatusBadRequest)
		return
	}
	if req.DestinationExtAddress == "" {
		http.Error(w, "destinationExtAddress is required", http.StatusBadRequest)
		return
	}
	amount, err := core.ParseDecimalSmallest(req.AmountSmallestUnit)
	if err != nil || amount.Sign() <= 0 {
		http.Error(w, "amountSmallestUnit must be a positive amount", http.StatusBadRequest)
		return
	}
	if req.Chain == "ETH" || req.Chain == "USDC" {
		if releaseAmount, feeAmount := splitVaultReleaseFee(req.Chain, req.AmountSmallestUnit); releaseAmount == "0" {
			http.Error(w, fmt.Sprintf("this withdrawal amount is too small to survive its own release fee (currently ~%s wei) - withdraw more, or wait for the network's gas-price estimate to drop", feeAmount), http.StatusBadRequest)
			return
		}
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxVaultBurn, string(data))

	json.NewEncoder(w).Encode(map[string]string{"status": "pending", "txId": tx.ID})
}

// vaultReleaseIDFor derives a deterministic uint64 release nonce from
// the burn transaction's own ID - every node computes the identical
// value from the same already-agreed-upon tx.ID, so it's usable
// directly as the ETH vault contract's on-chain replay-guard nonce
// (contracts/CreditChainVault.sol's usedReleaseIds) without any new
// consensus-critical counter state.
func vaultReleaseIDFor(burnTxID string) uint64 {
	h := sha256.Sum256([]byte(burnTxID))
	return binary.BigEndian.Uint64(h[:8])
}

// applyVaultBurn re-checks the chain/fields, burns from the correct
// CCC1 store (silently no-ops on insufficient balance, same convention
// every TokenStore.Burn/DecimalTokenStore.Burn caller already follows),
// and - only on a successful burn - opens the release-approval record
// bridge validators will co-sign.
func applyVaultBurn(tx core.Transaction) {
	if bridgePaused() {
		return
	}
	var data vaultBurnData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if !vaultChainValid(data.Chain) || data.DestinationExtAddress == "" {
		return
	}
	if data.Chain == "STETH" {
		// Re-checked here too, never trusted from HandleVaultBurn's own
		// fast-fail alone - see that handler's matching check for why.
		return
	}
	amount, err := core.ParseDecimalSmallest(data.AmountSmallestUnit)
	if err != nil || amount.Sign() <= 0 {
		return
	}

	// Computed BEFORE burning anything - ETH/USDC's flat, gas-cost-based
	// fee (see splitVaultReleaseFee's doc comment) doesn't scale with the
	// withdrawal amount, so a small enough request would previously have
	// its ENTIRE value consumed by the fee: the burn already happened
	// unconditionally, only afterward would the split zero out the
	// release amount, leaving the user's CCC1 balance gone and nothing
	// ever delivered (the real incident this check closes - see
	// audit/KNOWN_GAPS.md). Refusing outright here means the burn never
	// happens at all for a withdrawal too small to survive its own fee,
	// rather than silently succeeding into a worthless release.
	if data.Chain == "ETH" || data.Chain == "USDC" {
		if releaseAmount, _ := splitVaultReleaseFee(data.Chain, data.AmountSmallestUnit); releaseAmount == "0" {
			return
		}
	}

	symbol := ccc1SymbolForChain(data.Chain)
	address := resolveBalanceAddress(tx.AuthAddress, tx.AuthPublicKey, symbol)

	var burned bool
	switch data.Chain {
	case "ETH":
		burned = ethCcc1Store.Burn(address, data.AmountSmallestUnit)
	case "USDC":
		burned = usdcCcc1Store.Burn(address, data.AmountSmallestUnit)
	case "BTC":
		sats, perr := strconv.ParseUint(data.AmountSmallestUnit, 10, 64)
		if perr != nil {
			return
		}
		burned = btcCcc1Store.Burn(address, sats)
	}
	if !burned {
		return
	}

	// The user's own balance is debited the FULL requested amount above
	// (burned == what they asked to withdraw) - the fee only affects how
	// much of that gets split between "actually released to the real
	// external address" and "credited to the validator/fee recipient,"
	// see api/vault_fee.go's doc comment for why this never touches the
	// vault's real locked-value backing either way.
	//
	// BTC's fee is credited immediately (creditVaultReleaseFee is a
	// no-op for ETH/USDC now); ETH/USDC's fee is only ever HELD here -
	// see PendingFeeAmountSmallestUnit's doc comment for why crediting it
	// has to wait until a real relayer's TxVaultReleaseComplete says who
	// actually paid the gas.
	releaseAmount, feeAmount := splitVaultReleaseFee(data.Chain, data.AmountSmallestUnit)
	creditVaultReleaseFee(data.Chain, feeAmount)

	pendingFee := "0"
	if data.Chain == "ETH" || data.Chain == "USDC" {
		pendingFee = feeAmount
	}

	vaultReleaseApprovalStore.Create(&core.VaultReleaseApproval{
		ID:                           tx.ID,
		Chain:                        data.Chain,
		BurnCcAddress:                tx.AuthAddress,
		DestinationExtAddress:        data.DestinationExtAddress,
		AmountSmallestUnit:           releaseAmount,
		ReleaseID:                    vaultReleaseIDFor(tx.ID),
		Signatures:                   map[string][]byte{},
		Status:                       core.VaultReleasePending,
		CreatedAt:                    tx.Timestamp,
		PendingFeeAmountSmallestUnit: pendingFee,
		BurnBlockHeight:              currentChainHeight(),
	})
}

type vaultReleaseApproveData struct {
	BurnTxId     string `json:"burnTxId"`
	SignatureHex string `json:"signatureHex"`
}

// HandleVaultReleaseApprove lets one bridge validator submit their own
// real external-chain signature (computed independently, on their own
// node) approving a pending release - see applyVaultReleaseApprove.
func HandleVaultReleaseApprove(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req vaultReleaseApproveData
	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}
	if !isBridgeValidatorAddress(address) {
		http.Error(w, "address is not a configured bridge validator", http.StatusForbidden)
		return
	}
	if req.BurnTxId == "" {
		http.Error(w, "burnTxId is required", http.StatusBadRequest)
		return
	}
	if _, err := hex.DecodeString(req.SignatureHex); err != nil || req.SignatureHex == "" {
		http.Error(w, "signatureHex must be valid hex", http.StatusBadRequest)
		return
	}
	approval, ok := vaultReleaseApprovalStore.Get(req.BurnTxId)
	if !ok || approval.Status != core.VaultReleasePending {
		http.Error(w, "release approval not found or already resolved", http.StatusBadRequest)
		return
	}
	if remaining := vaultReleaseConfirmationsRemaining(approval); remaining > 0 {
		http.Error(w, fmt.Sprintf("burn not yet confirmed deep enough - %d more block(s) needed before this release can be co-signed", remaining), http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxVaultReleaseApprove, string(data))

	json.NewEncoder(w).Encode(map[string]string{"status": "pending", "txId": tx.ID})
}

// HandleVaultReleaseStatus is a public, unauthenticated read - mirrors
// HandleVaultLockStatus for the withdrawal side.
func HandleVaultReleaseStatus(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	burnTxID := r.URL.Query().Get("burnTxId")
	approval, ok := vaultReleaseApprovalStore.Get(burnTxID)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{
		"status":                    approval.Status,
		"signatures":                len(approval.Signatures),
		"requiredThreshold":         bridgeValidatorThreshold(),
		"releaseId":                 approval.ReleaseID,
		"releasedAt":                approval.ReleasedAt,
		"externalReleaseTxHash":     approval.ExternalReleaseTxHash,
		"confirmationsRemaining":    vaultReleaseConfirmationsRemaining(approval),
		"confirmationDepthRequired": core.VaultReleaseConfirmationDepthBlocks,
	})
}

// vaultReleaseQueueEntry is one ready-to-relay ETH/USDC release - every
// field a client-side relay (website/js/vault-relay.js) needs to build
// and broadcast the real release() call itself, since - unlike the
// server-side relay in api/vault_release_relay.go - it has no direct
// access to vaultReleaseApprovalStore.
type vaultReleaseQueueEntry struct {
	ID                    string   `json:"id"`
	Chain                 string   `json:"chain"`
	DestinationExtAddress string   `json:"destinationExtAddress"`
	AmountSmallestUnit    string   `json:"amountSmallestUnit"`
	ReleaseID             uint64   `json:"releaseId"`
	Signatures            []string `json:"signatures"`
}

// HandleVaultReleaseQueue is a public, unauthenticated read - deliberately
// so, same reasoning as HandleVaultReleaseStatus: everything it returns
// (destination, amount, releaseId, and the bridge-validator signatures
// already collected via TxVaultReleaseApprove) is already real,
// gossiped consensus data any node/explorer can independently derive
// from the chain, not a secret. This is what lets the relay run entirely
// from a browser tab (see website/js/vault-relay.js's doc comment) -
// the client never needs to authenticate to read what's already public.
// Only ETH/USDC releases are listed (BTC needs no relayer key at all,
// see api/vault_btc_release_relay.go's own doc comment - broadcasting is
// free, so there's nothing for this admin box to do on that side).
func HandleVaultReleaseQueue(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	threshold := bridgeValidatorThreshold()
	entries := []vaultReleaseQueueEntry{}
	if threshold >= 1 {
		for _, approval := range vaultReleaseApprovalStore.Snapshot() {
			if approval.Chain != "ETH" && approval.Chain != "USDC" {
				continue
			}
			if approval.Status != core.VaultReleasePending {
				continue
			}
			if len(approval.Signatures) < threshold {
				continue
			}
			sigs := make([]string, 0, len(approval.Signatures))
			for _, sig := range approval.Signatures {
				sigs = append(sigs, hex.EncodeToString(sig))
			}
			entries = append(entries, vaultReleaseQueueEntry{
				ID:                    approval.ID,
				Chain:                 approval.Chain,
				DestinationExtAddress: approval.DestinationExtAddress,
				AmountSmallestUnit:    approval.AmountSmallestUnit,
				ReleaseID:             approval.ReleaseID,
				Signatures:            sigs,
			})
		}
	}

	json.NewEncoder(w).Encode(map[string]any{"approvals": entries})
}

// vaultPendingApprovalEntry is one ETH/USDC release still short of
// signature threshold - everything a browser-based bridge validator
// (see website/js/vault-lock-attest.js's release-approval counterpart)
// needs to independently recompute this release's exact digest
// (contracts/CreditChainVault.sol's release(): keccak256(abi.encode(to,
// token, amount, releaseId, address(this), block.chainid))), sign it
// with their own relayer key, and submit TxVaultReleaseApprove. BTC is
// deliberately excluded - its approval path is a Bitcoin P2WSH multisig
// witness scheme, not this ecrecover-based one, and needs its own
// dedicated tooling (not yet built).
type vaultPendingApprovalEntry struct {
	BurnTxId               string `json:"burnTxId"`
	Chain                  string `json:"chain"`
	DestinationExtAddress  string `json:"destinationExtAddress"`
	AmountSmallestUnit     string `json:"amountSmallestUnit"`
	ReleaseID              uint64 `json:"releaseId"`
	SignatureCount         int    `json:"signatureCount"`
	ConfirmationsRemaining uint64 `json:"confirmationsRemaining"`
}

// HandleVaultPendingApprovals is a public, unauthenticated read - same
// reasoning as HandleVaultReleaseQueue: every field returned is already
// public (destination/amount/releaseId are gossiped consensus data from
// the burn transaction itself). This is what a bridge validator's
// browser tool polls to find releases that still need ITS signature -
// unlike HandleVaultReleaseQueue, which only lists releases that
// already reached threshold (nothing left to sign, only left to relay).
func HandleVaultPendingApprovals(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	entries := []vaultPendingApprovalEntry{}
	for _, approval := range vaultReleaseApprovalStore.Snapshot() {
		if !vaultChainValid(approval.Chain) {
			continue
		}
		if approval.Status != core.VaultReleasePending {
			continue
		}
		entries = append(entries, vaultPendingApprovalEntry{
			BurnTxId:               approval.ID,
			Chain:                  approval.Chain,
			DestinationExtAddress:  approval.DestinationExtAddress,
			AmountSmallestUnit:     approval.AmountSmallestUnit,
			ReleaseID:              approval.ReleaseID,
			SignatureCount:         len(approval.Signatures),
			ConfirmationsRemaining: vaultReleaseConfirmationsRemaining(approval),
		})
	}

	json.NewEncoder(w).Encode(map[string]any{"approvals": entries})
}

// applyVaultReleaseApprove re-checks everything the handler already
// checked and records this validator's real signature. It deliberately
// does NOT flip Status to RELEASED or attempt any real external
// broadcast itself - once len(Signatures) reaches threshold, a
// separate node-local relay (Phase B's ETH contract-call submitter /
// Phase C's BTC witness assembler) is what actually performs the real,
// irreversible external release and records the outcome - the same
// "consensus state records authorization, a node-local process performs
// the actual one-time external side effect" split this codebase already
// uses for the (now-retired) ETH custodial withdrawal handler.
func applyVaultReleaseApprove(tx core.Transaction) {
	var data vaultReleaseApproveData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if !isBridgeValidatorAddress(tx.AuthAddress) {
		return
	}
	sigBytes, err := hex.DecodeString(data.SignatureHex)
	if err != nil || len(sigBytes) == 0 {
		return
	}

	approval, ok := vaultReleaseApprovalStore.Get(data.BurnTxId)
	if !ok || approval.Status != core.VaultReleasePending {
		return
	}
	// Re-checked here (never trusted from HandleVaultReleaseApprove's own
	// submission-time check alone) - every node applying this same block
	// computes currentChainHeight() identically (see that function's doc
	// comment), so this stays fully deterministic across the network.
	if vaultReleaseConfirmationsRemaining(approval) > 0 {
		return
	}
	// Deliberately overwrites rather than refusing a second submission
	// from the same validator - lets a validator whose
	// VAULT_RELEASE_SIGNING_KEY_HEX has since rotated self-correct by
	// resubmitting, instead of a stale signature staying permanently
	// stuck under this wallet's entry (the real incident that motivated
	// this fix - see api/vault_release_approve_watch.go's matching
	// change on the auto-sign side). Safe to allow: tx.AuthAddress is
	// already independently verified as genuinely this registered
	// validator by the transaction's own signed-envelope pipeline
	// before this function ever runs, and whatever ends up here still
	// has to independently pass the vault contract's own on-chain
	// ecrecover check before any real funds move - this map is
	// coordination bookkeeping, not itself an authorization boundary.
	approval.Signatures[tx.AuthAddress] = sigBytes
}

// vaultReleaseCompleteData is the vault-bridge's own version of
// bridgeTxHashRecordData (api/bridge_tx_hash.go) - the burn transaction's
// ID and the real external-chain tx hash the relay observed once its
// own release()/broadcast call actually succeeded.
type vaultReleaseCompleteData struct {
	BurnTxId     string `json:"burnTxId"`
	ExternalHash string `json:"externalHash"`
	// FeeRecipientEthAddress is which real ETH address should be
	// credited the release's held gas-cost fee (see
	// core.VaultReleaseApproval.PendingFeeAmountSmallestUnit) - the
	// relaying node's own RELAYER_FEE_RECIPIENT_ETH_ADDRESS, named here
	// rather than read from local config at apply time so every node
	// credits the exact same address regardless of its own local
	// settings (see applyVaultReleaseComplete). Empty for BTC, which has
	// no relayer/gas cost and already credited its fee immediately at
	// burn time.
	FeeRecipientEthAddress string `json:"feeRecipientEthAddress,omitempty"`
}

// recordVaultReleaseComplete submits the follow-up TxVaultReleaseComplete
// once a real release/broadcast succeeds - node-authored via
// selfSignedEnvelope, same as recordBridgeTxHash, since there's no
// wallet-originated HTTP caller for this fact (the relay itself is what
// learns it, after the fact, on whichever node happens to be running it).
func recordVaultReleaseComplete(burnTxID, externalHash, feeRecipientEthAddress string) {
	data, _ := json.Marshal(vaultReleaseCompleteData{
		BurnTxId:               burnTxID,
		ExternalHash:           externalHash,
		FeeRecipientEthAddress: feeRecipientEthAddress,
	})
	recordTx(selfSignedEnvelope(data), reporterAddress(), "", 0, core.TxVaultReleaseComplete, string(data))
}

// applyVaultReleaseComplete flips the release to its final state once the
// real external broadcast is confirmed - idempotent (a second report, or
// a replayed one from chain history, is a harmless no-op since Status is
// already VaultReleaseReleased by then). Also credits any held ETH/USDC
// fee to the relaying node's declared recipient - see
// core.VaultReleaseApproval.PendingFeeAmountSmallestUnit's doc comment
// for why this is the first point any node can safely do so.
func applyVaultReleaseComplete(tx core.Transaction) {
	var data vaultReleaseCompleteData
	if err := json.Unmarshal([]byte(tx.Data), &data); err != nil {
		return
	}
	if data.BurnTxId == "" || data.ExternalHash == "" {
		return
	}

	approval, ok := vaultReleaseApprovalStore.Get(data.BurnTxId)
	if !ok || approval.Status != core.VaultReleasePending {
		return
	}
	approval.Status = core.VaultReleaseReleased
	approval.ReleasedAt = tx.Timestamp
	approval.ExternalReleaseTxHash = data.ExternalHash

	creditVaultReleaseFeeToRelayer(approval.Chain, approval.PendingFeeAmountSmallestUnit, data.FeeRecipientEthAddress)
}
