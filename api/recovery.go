package api

import (
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

// This file is the wallet-recovery placeholder - see
// core/recovery.go's doc comment for exactly what it did (a
// migration to a new keypair) and didn't (recover the actual lost
// private key). Discontinued everywhere (2026-08-16, see
// api/apply.go's WalletRecoveryDiscontinuedCutoverUnixSeconds): every
// handler below now fails fast with walletRecoveryDiscontinuedMessage
// instead of accepting a submission that the apply layer will only
// silently drop, or - on testnet, before the apply-layer cutover
// passes - actually running a mechanism the product no longer wants
// offered at all. The consensus-layer apply functions
// (applySetRecoveryEmail/applyRecoverWallet, api/apply.go) still exist
// so a resyncing node correctly replays testnet's real historical
// recovery transactions from before this decision - only NEW
// submissions are blocked, here at the door. recoveryStore itself
// stays live (apply.go still reads/writes it for that same historical-
// replay reason, and IsMigrated still gates a frozen address's
// applyTxOnce entirely regardless of when the migration happened).

const walletRecoveryDiscontinuedMessage = "Wallet recovery has been discontinued. This mechanism never recovered a lost private key - it migrated balances to a new keypair using data (a linked email, your wallet's creation year) that turned out to be too weak a security boundary. Your BIP-39 backup phrase, saved when you first created your wallet, is the only real way to regain access - there is no alternative recovery path."

var recoveryStore = core.NewRecoveryStore()

// recoveryEmailRequestData/recoverWalletData stay defined here (rather
// than moving to apply.go) since that's still where their JSON tags
// were originally documented, and applySetRecoveryEmail/
// applyRecoverWallet (api/apply.go) unmarshal historical transactions'
// Data into these same shapes during replay.
type recoveryEmailRequestData struct {
	Email string `json:"email"`
}

type recoverWalletData struct {
	OldAddress   string `json:"oldAddress"`
	Email        string `json:"email"`
	ClaimedYear  int    `json:"claimedYear"`
	Code         string `json:"code"`
	OldPublicKey string `json:"oldPublicKey"`
}

func HandleRequestEmailCode(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)
	http.Error(w, walletRecoveryDiscontinuedMessage, http.StatusGone)
}

func HandleConfirmEmail(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)
	http.Error(w, walletRecoveryDiscontinuedMessage, http.StatusGone)
}

func HandleRequestRecoveryCode(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)
	http.Error(w, walletRecoveryDiscontinuedMessage, http.StatusGone)
}

func HandleRecoverWallet(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)
	http.Error(w, walletRecoveryDiscontinuedMessage, http.StatusGone)
}
