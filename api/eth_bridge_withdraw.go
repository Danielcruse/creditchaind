package api

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/creditchain/creditchaind/core"
	"github.com/ethereum/go-ethereum/common"
)

// ETHBridgeFeeWei is the flat wei fee charged on an "ERC network" ETHB
// withdrawal, on top of the amount actually sent - testnet/illustrative
// value, not derived from anything. Deliberately sized (as a business/
// pricing decision, not a technical one) to cover CreditChain's
// expected real Sepolia gas cost plus margin: the user is quoted
// "amount you send" and "bridge fee," full stop - gas-price variance
// is CreditChain's own operating cost, never silently deducted from
// the user's payout (see applyBridgeWithdraw's doc comment for the
// full reasoning).
const ETHBridgeFeeWei = 21_000_000_000_000 // 0.000021 ETH-equivalent, illustrative

// bridgeWithdrawData is both the signed request body and the
// transaction Data for a TxBridgeWithdraw - the recipient's real
// Sepolia address and the amount (excluding the bridge fee) to send
// them, in wei as a decimal string (same convention as
// ethCustodialWithdrawData.AmountWei).
type bridgeWithdrawData struct {
	To        string `json:"to"`
	AmountWei string `json:"amountWei"`
}

// bridgeWithdrawReserved tracks ETHB wei this node has already
// committed to a real broadcast but whose debit transaction (see
// applyBridgeWithdraw) hasn't confirmed yet - closes a real race that
// would otherwise exist between "check the ledger balance" and "the
// debit actually lands on chain" (the ETHB debit is queued/confirmed-
// next-block like every other economic action in this app, but the
// real Sepolia broadcast below happens synchronously - without this,
// firing two withdrawal requests inside one block window could pass
// both fast-fail checks and double-broadcast against the same
// balance). Node-local and best-effort by design, not a second
// consensus-critical store: since a given custodial account only ever
// exists in the ONE node that created it (the same disclosed non-
// gossip limitation as core/eth_custodial.go generally), that node is
// also the only one that could ever process a withdrawal for it - a
// node-local guard fully covers the actual race surface here.
var bridgeWithdrawReserved = struct {
	mu  sync.Mutex
	wei map[string]uint64 // ETHB sub-address -> reserved wei
}{wei: make(map[string]uint64)}

func reserveBridgeWithdrawal(ethbAddress string, amount uint64) {
	bridgeWithdrawReserved.mu.Lock()
	defer bridgeWithdrawReserved.mu.Unlock()
	bridgeWithdrawReserved.wei[ethbAddress] += amount
}

func releaseBridgeWithdrawal(ethbAddress string, amount uint64) {
	bridgeWithdrawReserved.mu.Lock()
	defer bridgeWithdrawReserved.mu.Unlock()
	if bridgeWithdrawReserved.wei[ethbAddress] <= amount {
		delete(bridgeWithdrawReserved.wei, ethbAddress)
	} else {
		bridgeWithdrawReserved.wei[ethbAddress] -= amount
	}
}

func reservedBridgeWithdrawal(ethbAddress string) uint64 {
	bridgeWithdrawReserved.mu.Lock()
	defer bridgeWithdrawReserved.mu.Unlock()
	return bridgeWithdrawReserved.wei[ethbAddress]
}

// HandleBridgedEthWithdraw is the "ERC network" half of the auto-
// bridge send flow (see api/eth_bridge.go's doc comment for the "CCT
// network" half, applyTransfer's ETHB branch). Debits the caller's
// ETHB balance for amount+ETHBridgeFeeWei through the normal signed-
// tx/block-confirmation pipeline (auditable, replicated, same as
// every other economic action in this app), then - deliberately NOT
// waiting for that confirmation - broadcasts a real Sepolia
// transaction for amount, synchronously, on this node only, reusing
// buildAndBroadcastWithdrawal exactly like
// HandleEthCustodialWithdraw's own real-broadcast step.
//
// This inherits that same handler's disclosed "not atomic" limitation:
// the ETHB debit and the real broadcast are two separate steps, not
// one. A crash between them leaves a debited-but-not-yet-broadcast
// state (recoverable - nothing was actually sent); a crash or
// non-confirmation of the debit tx after a successful broadcast
// leaves a broadcast-but-not-debited state (NOT automatically
// recoverable - see audit/KNOWN_GAPS.md's entry on this). This is a
// disclosed, testnet-scoped simplification, not solved here - see
// this project's own precedent at eth_custodial.go for why a two-
// phase-commit/outbox pattern is judged disproportionate for a
// testnet-value feature with no other precedent anywhere in this
// codebase. reserveBridgeWithdrawal above closes the more urgent
// double-broadcast race; this remaining gap is a single-request
// crash-timing edge case, not a repeatable exploit.
func HandleBridgedEthWithdraw(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if ethRPCClient == nil || ethSigner == nil {
		http.Error(w, "ETH bridge not enabled (no Sepolia RPC/signer configured)", http.StatusServiceUnavailable)
		return
	}

	var req bridgeWithdrawData
	auth, from, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	account, ok := ethCustodialStore.Get(from)
	if !ok {
		http.Error(w, "no custodial ETH account for this wallet - create one first", http.StatusBadRequest)
		return
	}
	if !common.IsHexAddress(req.To) {
		http.Error(w, "to must be a valid 0x Ethereum address", http.StatusBadRequest)
		return
	}
	amountWei, ok := new(big.Int).SetString(req.AmountWei, 10)
	if !ok || amountWei.Sign() <= 0 || !amountWei.IsUint64() {
		http.Error(w, "amountWei must be a positive decimal integer string", http.StatusBadRequest)
		return
	}

	total := amountWei.Uint64() + ETHBridgeFeeWei // both operands already bounded above tokenStore's uint64 cap

	ethbSide := resolveBalanceAddress(from, auth.PublicKey, "ETHB")

	reserveBridgeWithdrawal(ethbSide, total)
	available := bridgedEthStore.BalanceOf(ethbSide)
	reserved := reservedBridgeWithdrawal(ethbSide)
	if reserved > available {
		// Insufficient (accounting for anything already in flight from
		// this same address) - release what this request just reserved
		// and fail before doing anything irreversible.
		releaseBridgeWithdrawal(ethbSide, total)
		http.Error(w, "insufficient ETHB balance for amount + bridge fee", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, from, req.To, amountWei.Uint64(), core.TxBridgeWithdraw, string(data))

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	txHash, err := buildAndBroadcastWithdrawal(ctx, account.KeyRef, account.EthAddress, req.To, amountWei)
	if err != nil {
		// Nothing was actually sent - safe to release the reservation.
		// The debit tx above is still in the mempool/may still confirm;
		// this is the disclosed debited-but-not-broadcast edge case
		// from this function's doc comment if it does.
		releaseBridgeWithdrawal(ethbSide, total)
		json.NewEncoder(w).Encode(map[string]any{
			"status": "broadcast_failed",
			"txId":   tx.ID,
			"error":  err.Error(),
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]any{
		"status":    "broadcast",
		"txId":      tx.ID,
		"ethTxHash": txHash,
		"feeWei":    ETHBridgeFeeWei,
	})
}
