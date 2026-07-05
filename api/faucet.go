package api

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"

	"github.com/creditchain/creditchaind/core"
)

// FaucetAmount/FaucetETHTestAmount/FaucetUSDCTestAmount are how much
// testnet CCT/ETHTEST/USDCTEST a fresh address gets once, on request -
// not real value (see the "no guaranteed value" banner throughout the
// site), just enough to test locking collateral, borrowing, swapping,
// and transfers without waiting to earn it via staking rewards first.
// Always applied as these exact constants (see applyFaucetClaim in
// api/apply.go) - nothing about the amount is ever read from a client
// request.
const FaucetAmount = 100
const FaucetETHTestAmount = 10
const FaucetUSDCTestAmount = 10

// faucetClaimed guards against the same address draining the faucet
// repeatedly. The authoritative check now happens at block-confirmation
// time (applyFaucetClaim), identically on every node - this same map is
// still persisted (see persistence.go) so a restart doesn't reset
// everyone's claim.
var faucetClaimed = struct {
	mu   sync.Mutex
	seen map[string]bool
}{seen: make(map[string]bool)}

func snapshotFaucetClaims() []string {
	faucetClaimed.mu.Lock()
	defer faucetClaimed.mu.Unlock()

	addrs := make([]string, 0, len(faucetClaimed.seen))
	for a := range faucetClaimed.seen {
		addrs = append(addrs, a)
	}
	return addrs
}

func restoreFaucetClaims(addrs []string) {
	faucetClaimed.mu.Lock()
	defer faucetClaimed.mu.Unlock()

	for _, a := range addrs {
		faucetClaimed.seen[a] = true
	}
}

// HandleFaucet queues a one-time testnet CCT/ETHTEST/USDCTEST grant as
// a real transaction (core.TxFaucetClaim), applied identically on
// every node at block-confirmation time (api/apply.go's
// applyFaucetClaim) - same pending-tx pattern as every other mutating
// endpoint now.
//
// This used to mint directly and instantly, entirely local to
// whichever node received the HTTP request, with no signature required
// at all - deliberately simple, since there was "no value at risk" to
// protect. But with no gossip/replication, two production nodes' CCT
// supply drifted apart by however many claims landed on one node but
// not the other (discovered during a live deploy). Requiring a real
// signed envelope here is a small step up from "totally open" (still
// no prior registration needed - any fresh keypair can claim), but it's
// what lets the claim become a genuine, independently-reverifiable
// transaction instead of a local side effect.
func HandleFaucet(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	type Request struct{}
	var req Request

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	// Optimistic fast-fail only - the authoritative check is
	// applyFaucetClaim's, re-run identically on every node once this
	// transaction actually confirms.
	faucetClaimed.mu.Lock()
	alreadyClaimed := faucetClaimed.seen[address]
	faucetClaimed.mu.Unlock()
	if alreadyClaimed {
		http.Error(w, "this address has already claimed the faucet", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxFaucetClaim, string(data))

	pub, _ := hex.DecodeString(auth.PublicKey)
	ethTestAddress := core.DeriveTokenAddress(pub, core.ETHTestAddressTag)
	usdcTestAddress := core.DeriveTokenAddress(pub, core.USDCTestAddressTag)

	json.NewEncoder(w).Encode(map[string]any{
		"status":          "pending",
		"txId":            tx.ID,
		"amount":          FaucetAmount,
		"ethTestAmount":   FaucetETHTestAmount,
		"usdcTestAmount":  FaucetUSDCTestAmount,
		"ethTestAddress":  ethTestAddress,
		"usdcTestAddress": usdcTestAddress,
	})
}
