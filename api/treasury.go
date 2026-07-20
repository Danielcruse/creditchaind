package api

import (
	"encoding/json"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

var treasuryStore = core.NewTreasuryStore()

type treasuryInfoResponse struct {
	FoundationAddress    string `json:"foundationAddress"`
	SalePriceCentsPerCCT uint64 `json:"salePriceCentsPerCCT"`
	Paused               bool   `json:"paused"`
	RemainingAllocation  uint64 `json:"remainingAllocation"`
}

// HandleTreasuryInfo is public sale-page info - no auth needed.
// RemainingAllocation is computed live from tokenStore rather than
// tracked separately in TreasuryStore, since the token store's
// balance at FoundationAddress is the actual source of truth (see
// core/treasury.go's doc comment).
func HandleTreasuryInfo(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	t := treasuryStore.Treasury
	json.NewEncoder(w).Encode(treasuryInfoResponse{
		FoundationAddress:    t.FoundationAddress,
		SalePriceCentsPerCCT: t.SalePriceCentsPerCCT,
		Paused:               t.Paused,
		RemainingAllocation:  tokenStore.BalanceOf(t.FoundationAddress),
	})
}

type treasuryPurchaseRequestData struct {
	CCTAmount uint64 `json:"cctAmount"`
}

// HandleCreatePurchaseRequest is the buyer-facing "Buy CCT" submit -
// wallet-signed, validates then queues via recordTx (never mutates
// inline), same convention every mutating handler in this codebase
// follows.
func HandleCreatePurchaseRequest(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req treasuryPurchaseRequestData
	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if treasuryStore.Treasury.Paused {
		http.Error(w, "the public sale is currently paused", http.StatusBadRequest)
		return
	}
	if req.CCTAmount == 0 {
		http.Error(w, "cctAmount must be positive", http.StatusBadRequest)
		return
	}
	if tokenStore.BalanceOf(treasuryStore.Treasury.FoundationAddress) < req.CCTAmount {
		http.Error(w, "requested amount exceeds remaining sale allocation", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, treasuryStore.Treasury.FoundationAddress, req.CCTAmount, core.TxTreasuryPurchaseRequest, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

// HandleAdminListPurchases feeds the admin dashboard's pending queue.
// POST (not GET) so it goes through the same requireAdminAuthed
// signed-request path as every other admin action - a signature can't
// be attached to a bare GET, and having one endpoint use a different
// admin-auth shape than the other eight would just be a second code
// path to keep in sync. The body carries no meaningful payload.
func HandleAdminListPurchases(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if _, _, err := requireAdminAuthed(r, nil); err != nil {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(treasuryStore.ListPurchases())
}

// HandleAdminConfirmPurchase is the admin attesting that a buyer's
// off-app payment was received - see applyTreasuryPurchaseConfirm for
// the actual CCT transfer, applied at block-confirmation time like
// every other mutation in this codebase.
func HandleAdminConfirmPurchase(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req treasuryPurchaseConfirmData
	signedAuth, legacy, err := requireAdminAuthed(r, &req)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	purchase, ok := treasuryStore.GetPurchase(req.PurchaseID)
	if !ok || purchase.Status != core.PurchaseAwaitingPayment {
		http.Error(w, "purchase not found or not awaiting payment", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordAdminTx(signedAuth, legacy, "", 0, core.TxTreasuryPurchaseConfirm, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

// HandleAdminSetSalePrice/HandleAdminSetPaused/HandleAdminSetTreasuryAddress
// are the remaining Treasury/Sale admin controls - same self-signed
// node-authored transaction pattern as HandleAdminConfirmPurchase /
// HandleTokenMint.

func HandleAdminSetSalePrice(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req treasurySetSalePriceData
	signedAuth, legacy, err := requireAdminAuthed(r, &req)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	data, _ := json.Marshal(req)
	tx := recordAdminTx(signedAuth, legacy, "", 0, core.TxTreasurySetSalePrice, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

func HandleAdminSetPaused(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req treasurySetPausedData
	signedAuth, legacy, err := requireAdminAuthed(r, &req)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	data, _ := json.Marshal(req)
	tx := recordAdminTx(signedAuth, legacy, "", 0, core.TxTreasurySetPaused, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

func HandleAdminSetTreasuryAddress(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req treasurySetAddressData
	signedAuth, legacy, err := requireAdminAuthed(r, &req)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	if req.FoundationAddress == "" {
		http.Error(w, "foundationAddress is required", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordAdminTx(signedAuth, legacy, "", 0, core.TxTreasurySetAddress, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}
