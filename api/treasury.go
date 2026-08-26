package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/creditchain/creditchaind/core"
)

var treasuryStore = core.NewTreasuryStore()

type treasuryInfoResponse struct {
	FoundationAddress    string `json:"foundationAddress"`
	SalePriceCentsPerCCT uint64 `json:"salePriceCentsPerCCT"`
	Paused               bool   `json:"paused"`
	RemainingAllocation  string `json:"remainingAllocation"`
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
		RemainingAllocation:  cctStore.BalanceOf(t.FoundationAddress),
	})
}

type treasuryPurchaseRequestData struct {
	CCTAmount uint64              `json:"cctAmount"`
	Method    core.PurchaseMethod `json:"method"`
	// BuyerEmail is required - it's the only way the admin's payment
	// instructions and confirmation link actually reach the buyer (see
	// HandleAdminSendPurchaseInstructions).
	BuyerEmail string `json:"buyerEmail"`
	// BuyerName/BuyerContact are collected on every method's form now
	// (not just bank transfer) - BuyerName additionally doubles as the
	// "pay from an account in this name" instruction shown alongside a
	// preset bank account, so the admin can actually match an incoming
	// wire to the right purchase request.
	BuyerName    string `json:"buyerName"`
	BuyerContact string `json:"buyerContact"`
	// Currency is required only for PurchaseMethodBankTransfer - which
	// admin-preset BankAccountDetails to show. Ignored for every other
	// method.
	Currency string `json:"currency"`
}

// HandleCreatePurchaseRequest is the buyer-facing "Buy CCT" submit -
// wallet-signed, validates then queues via recordTx (never mutates
// inline), same convention every mutating handler in this codebase
// follows. Card/Apple Pay/Google Pay/Online Wallet all collect ONLY
// contact info here, exactly like bank transfer - see
// core.PurchaseMethod's doc comment for why raw card details are never
// collected through any form on this site; the admin follows up by
// email with a real, PCI-compliant payment link for those methods.
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
	if !core.ValidPurchaseMethod(req.Method) {
		http.Error(w, "method must be BANK_TRANSFER, CARD, ONLINE_WALLET, APPLE_PAY, GOOGLE_PAY, or CRYPTO_TRANSFER", http.StatusBadRequest)
		return
	}
	req.BuyerEmail = strings.TrimSpace(req.BuyerEmail)
	if req.BuyerEmail == "" || !strings.Contains(req.BuyerEmail, "@") {
		http.Error(w, "a valid buyerEmail is required so payment instructions can reach you", http.StatusBadRequest)
		return
	}
	if len(req.BuyerEmail) > 254 {
		http.Error(w, "buyerEmail must be 254 characters or fewer", http.StatusBadRequest)
		return
	}
	req.BuyerName = strings.TrimSpace(req.BuyerName)
	req.BuyerContact = strings.TrimSpace(req.BuyerContact)
	if len(req.BuyerName) > 200 || len(req.BuyerContact) > 200 {
		http.Error(w, "buyerName/buyerContact must be 200 characters or fewer", http.StatusBadRequest)
		return
	}
	req.Currency = strings.TrimSpace(strings.ToUpper(req.Currency))
	if core.MethodNeedsPresetBankAccount(req.Method) {
		if req.BuyerName == "" {
			http.Error(w, "buyerName is required for bank transfer - the admin needs it to match your incoming wire", http.StatusBadRequest)
			return
		}
		if req.Currency == "" {
			http.Error(w, "currency is required for bank transfer", http.StatusBadRequest)
			return
		}
		if _, ok := treasuryStore.GetBankAccount(req.Currency); !ok {
			http.Error(w, fmt.Sprintf("no preset bank account configured for currency %q yet - try a different currency or contact support", req.Currency), http.StatusBadRequest)
			return
		}
	}
	if !hasCctBalance(treasuryStore.Treasury.FoundationAddress, req.CCTAmount) {
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

// HandleListBankAccounts is public, unauthenticated - same "where to
// send money" reasoning every other deposit-address endpoint in this
// codebase already uses (e.g. api/vault_info.go's HandleVaultInfo):
// there's no confidentiality value in an account buyers are meant to
// pay into, and buy-cct.html's currency picker needs this to know which
// currencies are even choosable before the buyer signs anything.
func HandleListBankAccounts(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)
	json.NewEncoder(w).Encode(treasuryStore.ListBankAccounts())
}

type adminSetBankAccountData struct {
	core.BankAccountDetails
}

// HandleAdminSetBankAccount lets the admin add or replace one
// currency's preset receiving-account details - see
// core.TreasuryStore.SetBankAccount's doc comment for why this is a
// full replacement, not a partial merge.
func HandleAdminSetBankAccount(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req adminSetBankAccountData
	signedAuth, mode, err := requireAdminAuthed(r, &req)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	req.Currency = strings.TrimSpace(strings.ToUpper(req.Currency))
	if req.Currency == "" {
		http.Error(w, "currency is required", http.StatusBadRequest)
		return
	}
	if req.AccountName == "" || req.AccountNumber == "" || req.BankName == "" {
		http.Error(w, "accountName, accountNumber, and bankName are required", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordAdminTx(signedAuth, mode, "", 0, core.TxTreasurySetBankAccount, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

type treasuryMarkPaidByBuyerData struct {
	PurchaseID string `json:"purchaseId"`
}

// HandleTreasuryMarkPaidByBuyer is the in-app "I Have Paid" button - see
// core.TreasuryStore.MarkBuyerPaidBySignedRequest's doc comment for how
// this differs from the older email-link version. Wallet-signed like
// any ordinary buyer action; verifyTxAuth's case for this type (see
// api/apply.go) is what actually enforces the caller is this exact
// purchase's own buyer, not this handler.
func HandleTreasuryMarkPaidByBuyer(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req treasuryMarkPaidByBuyerData
	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	purchase, ok := treasuryStore.GetPurchase(req.PurchaseID)
	if !ok || purchase.BuyerAddress != address {
		http.Error(w, "purchase not found", http.StatusNotFound)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxTreasuryPurchaseMarkPaidByBuyer, string(data))

	if smtpConfigured() {
		if recipient := smtpNotifyRecipient(); recipient != "" {
			subject := "A buyer marked a CCT purchase as paid"
			body := fmt.Sprintf("Purchase %s (%d CCT, buyer %s) was marked paid by the buyer in-app. Verify payment was actually received, then confirm it in the admin dashboard.", purchase.ID, purchase.CCTAmount, purchase.BuyerAddress)
			go sendEmail(recipient, subject, body)
		}
	}

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
	signedAuth, mode, err := requireAdminAuthed(r, &req)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	purchase, ok := treasuryStore.GetPurchase(req.PurchaseID)
	if !ok || purchase.Status == core.PurchaseConfirmed {
		http.Error(w, "purchase not found or already confirmed", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordAdminTx(signedAuth, mode, "", 0, core.TxTreasuryPurchaseConfirm, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

// newPurchaseConfirmToken generates a random, unguessable token for
// one instructions round - 32 bytes, matching every other random-
// token generator in this codebase (e.g. api/hub_agents.go's
// newHubAgentApplicationID).
func newPurchaseConfirmToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

type adminSendPurchaseInstructionsData struct {
	PurchaseID   string `json:"purchaseId"`
	Instructions string `json:"instructions"`
}

const maxPurchaseInstructionsLen = 2000

// HandleAdminSendPurchaseInstructions is the admin providing payment
// details (bank account, crypto address, wallet handle) for one
// pending purchase - generates the confirmation token and, if the
// buyer gave an email and this node has live outbound email
// configured, sends it right away (optimistic, same "notify at
// submission time, not confirmation time" convention as
// HandleApproveHubAgent's notifyUser call - see api/hub_agents.go).
// The actual state transition still only takes effect once the
// underlying transaction confirms, like every mutation here.
func HandleAdminSendPurchaseInstructions(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req adminSendPurchaseInstructionsData
	signedAuth, mode, err := requireAdminAuthed(r, &req)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	req.Instructions = strings.TrimSpace(req.Instructions)
	if req.Instructions == "" {
		http.Error(w, "instructions are required", http.StatusBadRequest)
		return
	}
	if len(req.Instructions) > maxPurchaseInstructionsLen {
		http.Error(w, fmt.Sprintf("instructions must be %d characters or fewer", maxPurchaseInstructionsLen), http.StatusBadRequest)
		return
	}

	purchase, ok := treasuryStore.GetPurchase(req.PurchaseID)
	if !ok || purchase.Status != core.PurchaseAwaitingPayment {
		http.Error(w, "purchase not found or not awaiting instructions", http.StatusBadRequest)
		return
	}

	token, err := newPurchaseConfirmToken()
	if err != nil {
		http.Error(w, "failed to generate confirmation token", http.StatusInternalServerError)
		return
	}

	fullData := struct {
		adminSendPurchaseInstructionsData
		Token string `json:"token"`
	}{req, token}
	data, _ := json.Marshal(fullData)
	tx := recordAdminTx(signedAuth, mode, "", 0, core.TxTreasuryPurchaseSendInstructions, string(data))

	if purchase.BuyerEmail != "" && smtpConfigured() {
		confirmLink := purchaseConfirmLink(purchase.ID, token)
		subject := "Complete your CCT purchase"
		body := fmt.Sprintf(
			"Please complete payment for %d CCT using the details below:\n\n%s\n\nOnce you've sent payment, confirm it here so we know to check for it:\n%s\n\nAn admin will verify and release your CCT shortly after confirming.",
			purchase.CCTAmount, req.Instructions, confirmLink,
		)
		go sendEmail(purchase.BuyerEmail, subject, body)
	}

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

// purchaseConfirmLink builds the buyer-facing "I've paid" link -
// points directly at this node's own API (not a separate website
// page), which responds with a small, self-contained HTML page so the
// buyer gets a clear result without needing any other page to load
// first. Falls back to a relative path (still useful in the API
// response even without a usable email) if SELF_URL was never
// configured on this node.
func purchaseConfirmLink(purchaseID, token string) string {
	base := strings.TrimSuffix(selfBaseURL, "/")
	return fmt.Sprintf("%s/treasury/purchase/confirm-payment?id=%s&token=%s", base, purchaseID, token)
}

// HandleBuyerConfirmPurchasePayment is the buyer's "I've paid" email
// link - GET, since it's clicked straight from an email client with no
// wallet available to sign anything. Token possession (not a wallet
// signature) is the only proof of intent here, which is why this is
// explicitly NOT treated as proof of payment - see
// core.TreasuryStore.MarkBuyerPaid's doc comment. Queues a node-
// authored transaction (api/node_identity.go's selfSignedEnvelope),
// same trust model as a slash report, rather than mutating state
// inline.
func HandleBuyerConfirmPurchasePayment(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	purchaseID := r.URL.Query().Get("id")
	token := r.URL.Query().Get("token")

	purchase, ok := treasuryStore.GetPurchase(purchaseID)
	if !ok || purchase.Status != core.PurchaseInstructionsSent || token == "" || purchase.ConfirmToken != token {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, purchaseConfirmResultPage(false))
		return
	}

	payload := map[string]string{"purchaseId": purchaseID, "token": token}
	data, _ := json.Marshal(payload)
	auth := selfSignedEnvelope(data)
	recordTx(auth, purchase.BuyerAddress, "", 0, core.TxTreasuryPurchaseBuyerMarkedPaid, string(data))

	if smtpConfigured() {
		if recipient := smtpNotifyRecipient(); recipient != "" {
			subject := "A buyer marked a CCT purchase as paid"
			body := fmt.Sprintf("Purchase %s (%d CCT, buyer %s) was marked paid by the buyer. Verify payment was actually received, then confirm it in the admin dashboard.", purchase.ID, purchase.CCTAmount, purchase.BuyerAddress)
			go sendEmail(recipient, subject, body)
		}
	}

	fmt.Fprint(w, purchaseConfirmResultPage(true))
}

// purchaseConfirmResultPage is a minimal, dependency-free HTML page -
// no need for a full website page/deploy just to show one of two
// possible outcomes to someone arriving from an email link.
func purchaseConfirmResultPage(ok bool) string {
	if ok {
		return `<!doctype html><html><head><title>Payment Confirmed</title></head>` +
			`<body style="font-family:sans-serif;max-width:480px;margin:60px auto;text-align:center;">` +
			`<h2>Thanks - we've been notified.</h2>` +
			`<p>An admin will verify your payment and release your CCT shortly. You can close this page.</p>` +
			`</body></html>`
	}
	return `<!doctype html><html><head><title>Link Expired</title></head>` +
		`<body style="font-family:sans-serif;max-width:480px;margin:60px auto;text-align:center;">` +
		`<h2>This link is no longer valid.</h2>` +
		`<p>It may have already been used, or this purchase is at a different stage. Contact support if you still need help.</p>` +
		`</body></html>`
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
	signedAuth, mode, err := requireAdminAuthed(r, &req)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	data, _ := json.Marshal(req)
	tx := recordAdminTx(signedAuth, mode, "", 0, core.TxTreasurySetSalePrice, string(data))

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
	signedAuth, mode, err := requireAdminAuthed(r, &req)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	data, _ := json.Marshal(req)
	tx := recordAdminTx(signedAuth, mode, "", 0, core.TxTreasurySetPaused, string(data))

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
	signedAuth, mode, err := requireAdminAuthed(r, &req)
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
	tx := recordAdminTx(signedAuth, mode, "", 0, core.TxTreasurySetAddress, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}
