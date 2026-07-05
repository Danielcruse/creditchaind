package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/creditchain/creditchaind/core"
)

// This file is the testnet-only wallet-recovery placeholder - see
// core/recovery.go's doc comment for exactly what it does (a
// migration to a new keypair) and doesn't (recover the actual lost
// private key). Two flows: setting up recovery (signed by the wallet
// itself, while access is intact) and recovering (signed by a brand
// new keypair, since the old one is by definition unavailable).

var recoveryStore = core.NewRecoveryStore()

// emailCodes is ephemeral, node-local, one-time-code state - NOT part
// of consensus (see core.RecoveryStore.IsRecoverable's doc comment
// for why the actual security boundary never depends on this map). A
// code here is only ever a submission-time friction/UX gate, same
// role a CAPTCHA would play, not a replicated fact any other node
// needs to agree on.
var emailCodes = struct {
	mu    sync.Mutex
	codes map[string]emailCodeEntry
}{codes: make(map[string]emailCodeEntry)}

type emailCodeEntry struct {
	Code     string
	Email    string
	IssuedAt int64
}

const emailCodeTTL = 10 * 60 // 10 minutes

func generateCode() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	n := int(b[0])<<16 | int(b[1])<<8 | int(b[2])
	return strconv.Itoa(100000 + n%900000) // 6 digits
}

type recoveryEmailRequestData struct {
	Email string `json:"email"`
}

// HandleRequestEmailCode is step one of setting up recovery - signed
// by the wallet itself (the owner still has their key at this point).
// In a real deployment this code would be emailed; here it's returned
// directly in the response, clearly as a testnet placeholder, since
// there's no real email service wired up.
func HandleRequestEmailCode(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req recoveryEmailRequestData

	_, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if len(req.Email) == 0 || len(req.Email) > 200 {
		http.Error(w, "email must be 1-200 characters", http.StatusBadRequest)
		return
	}

	code := generateCode()

	emailCodes.mu.Lock()
	emailCodes.codes[address] = emailCodeEntry{Code: code, Email: req.Email, IssuedAt: time.Now().Unix()}
	emailCodes.mu.Unlock()

	json.NewEncoder(w).Encode(map[string]string{
		"status": "code_issued",
		"code":   code,
		"note":   "TESTNET MODE: this code would normally be emailed to you, not shown here.",
	})
}

type confirmEmailData struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

// HandleConfirmEmail is step two - the code just proves the requester
// got as far as requesting one (submission-time friction only); the
// actual state change (email verified + linked) is a real, replicated
// transaction like everything else mutating in this app.
func HandleConfirmEmail(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req confirmEmailData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	emailCodes.mu.Lock()
	entry, ok := emailCodes.codes[address]
	if ok {
		delete(emailCodes.codes, address) // one-shot, whether or not it matches
	}
	emailCodes.mu.Unlock()

	if !ok || entry.Code != req.Code || entry.Email != req.Email {
		http.Error(w, "code doesn't match or has expired - request a new one", http.StatusBadRequest)
		return
	}
	if time.Now().Unix()-entry.IssuedAt > emailCodeTTL {
		http.Error(w, "code has expired - request a new one", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(recoveryEmailRequestData{Email: req.Email})
	tx := recordTx(auth, address, "", 0, core.TxSetRecoveryEmail, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}

type recoveryCodeRequestData struct {
	OldAddress string `json:"oldAddress"`
}

// HandleRequestRecoveryCode has no signature to check - that's the
// entire scenario recovery exists for. It only confirms OldAddress
// has a verified email on file at all, and issues the same kind of
// submission-time-only code as the setup flow (see its doc comment
// for why this isn't the real security boundary).
func HandleRequestRecoveryCode(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req recoveryCodeRequestData
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	rec, ok := recoveryStore.Get(req.OldAddress)
	if !ok || !rec.EmailVerified {
		http.Error(w, "this address has no recovery email on file", http.StatusBadRequest)
		return
	}

	code := generateCode()

	emailCodes.mu.Lock()
	emailCodes.codes["recover:"+req.OldAddress] = emailCodeEntry{Code: code, Email: rec.Email, IssuedAt: time.Now().Unix()}
	emailCodes.mu.Unlock()

	json.NewEncoder(w).Encode(map[string]string{
		"status": "code_issued",
		"code":   code,
		"note":   "TESTNET MODE: this code would normally be emailed to the address on file, not shown here.",
	})
}

type recoverWalletData struct {
	OldAddress   string `json:"oldAddress"`
	Email        string `json:"email"`
	ClaimedYear  int    `json:"claimedYear"`
	Code         string `json:"code"`
	OldPublicKey string `json:"oldPublicKey"` // optional - see doc comment below
}

// HandleRecoverWallet is signed by the NEW keypair - the one that
// will actually receive the migrated balance - since by definition
// the old one is unavailable. Security rests on knowing OldAddress's
// exact linked email plus its creation year (see
// core.RecoveryStore.IsRecoverable), both deterministic facts every
// node can independently verify; the code here is only a submission
// friction gate.
//
// OldPublicKey is optional: CCT/REPT/staked-ETH/reputation migrate
// regardless (they're keyed by the raw address), but ETHTEST/USDCTEST
// live at a derived sub-address computed from the wallet's public key
// (see core.DeriveTokenAddress) - a key this server never stored (a
// disclosed, pre-existing gap - see api/wallet_create.go). If the
// recovering user still has that public key saved, supplying it here
// lets those balances migrate too; otherwise they're left behind on
// the frozen old address, a real and disclosed limitation.
func HandleRecoverWallet(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req recoverWalletData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if !recoveryStore.IsRecoverable(req.OldAddress, req.Email) {
		http.Error(w, "no matching recoverable wallet for that address/email", http.StatusBadRequest)
		return
	}

	oldWallet := walletStore.Get(req.OldAddress)
	if oldWallet.CreatedAt == 0 || time.Unix(oldWallet.CreatedAt, 0).UTC().Year() != req.ClaimedYear {
		http.Error(w, "creation year doesn't match", http.StatusBadRequest)
		return
	}

	emailCodes.mu.Lock()
	entry, ok := emailCodes.codes["recover:"+req.OldAddress]
	if ok {
		delete(emailCodes.codes, "recover:"+req.OldAddress)
	}
	emailCodes.mu.Unlock()
	if !ok || entry.Code != req.Code {
		http.Error(w, "code doesn't match or has expired - request a new one", http.StatusBadRequest)
		return
	}

	if req.OldPublicKey != "" {
		pubBytes, err := hex.DecodeString(req.OldPublicKey)
		if err != nil || core.GenerateAddress(pubBytes) != req.OldAddress {
			http.Error(w, "oldPublicKey doesn't match oldAddress", http.StatusBadRequest)
			return
		}
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxRecoverWallet, string(data))

	json.NewEncoder(w).Encode(map[string]string{
		"status": "pending",
		"txId":   tx.ID,
	})
}
