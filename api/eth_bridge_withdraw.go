package api

import (
	"net/http"
	"sync"
)

// ethbWithdrawGasLimitEstimate is the plain-transfer gas limit used to
// turn a live-tracked wei-per-gas price into a wei fee estimate - same
// value buildAndBroadcastWithdrawal itself uses for the real broadcast
// (a plain ETH transfer, no contract call), so the fee quoted here
// matches what CreditChain actually expects to pay.
const ethbWithdrawGasLimitEstimate = 21_000

// ethbWithdrawBridgeFeeBps is the bridge's own cut on top of the live
// gas cost - 0.05% (5 basis points) of the amount being withdrawn,
// charged only on the "leaving Outernet entirely" ERC-network path (a
// CCT-network move never leaves CreditChain at all, so it has no
// bridge-fee component - see ETHBCCTNetworkFeeWei).
const ethbWithdrawBridgeFeeBps = 5

// ethbWithdrawFeeWei computes the total ERC-network withdrawal fee -
// live gas price (from ethNonceAndGasPrice, the same RPC call this
// handler already needs for the real broadcast) times a fixed gas-
// limit estimate, plus 0.05% of the amount - both components in wei.
// Computed once, at request time, by the trusted Go server (never
// client-supplied) and then snapshotted into the transaction's Data so
// every node that later applies the confirmed block debits the
// identical amount regardless of what live gas price looks like by
// then - same "server computes it once, apply-time trusts the
// snapshot" trust boundary the old compile-time ETHBridgeFeeWei
// constant already had, just no longer a compile-time constant.
func ethbWithdrawFeeWei(fromAddr string, amountWei uint64) (uint64, error) {
	_, gasPrice, err := ethNonceAndGasPrice(fromAddr)
	if err != nil {
		return 0, err
	}
	liveGasFeeWei := gasPrice.Uint64() * ethbWithdrawGasLimitEstimate
	bridgeFeeWei := amountWei * ethbWithdrawBridgeFeeBps / 10000
	return liveGasFeeWei + bridgeFeeWei, nil
}

// bridgeWithdrawRequest is exactly what the client signs - To/AmountWei
// only. Kept as its own type (rather than inlined into
// bridgeWithdrawData below) so verifyTxAuth's TxBridgeWithdraw case
// (api/apply.go) can compare tx.AuthPayload against tx.Data using ONLY
// this narrower type - unmarshaling tx.Data's superset JSON into it
// simply ignores the extra "feeWei" key. Comparing the FULL
// bridgeWithdrawData struct instead (as this code originally did) is a
// real, previously-shipped bug: FeeWei is absent from the client's
// signed payload (decodes to its zero value, 0) but always nonzero in
// tx.Data once ethbWithdrawFeeWei computes a real live fee, so
// signed==claimed would be false for every genuine withdrawal that
// makes it this far - silently rejecting every real ERC-network
// withdrawal as "unauthorized" the moment gas prices are nonzero.
type bridgeWithdrawRequest struct {
	To        string `json:"to"`
	AmountWei string `json:"amountWei"`
}

// bridgeWithdrawData is the transaction Data for a TxBridgeWithdraw -
// bridgeWithdrawRequest (what was signed) plus FeeWei, which the
// handler computes live via ethbWithdrawFeeWei and sets on this same
// struct before re-marshaling for tx.Data, so apply-time reads a
// trustworthy, already-decided number. FeeWei is never part of what the
// client signs - see bridgeWithdrawRequest's doc comment above for why
// that matters to verifyTxAuth.
type bridgeWithdrawData struct {
	bridgeWithdrawRequest
	FeeWei uint64 `json:"feeWei,omitempty"`
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

// HandleBridgedEthWithdraw ("ERC network" ETHB withdrawal) is RETIRED.
// It used to broadcast real Sepolia ETH out of a CreditChain-held
// custodial account (see ethCustodialStore.Get(from) in the version of
// this function this comment replaces) - but custodial accounts are no
// longer creatable (see api/eth_custodial.go's
// HandleCreateEthCustodialAccount), so there is no longer any
// CreditChain-controlled reserve for this endpoint to draw from.
//
// This mirrors BTCB's own already-correct design exactly: BTCB has
// never had a "withdraw from reserve" endpoint at all - real BTC moves
// only ever happen via the user's own self-custodial signing
// (send-bridged-btc.html's "BTC network" mode, client-side BIP143,
// never touching a CreditChain-held key). ETHB now follows the same
// shape - it stays a free, instant, CCT-network-only ledger mirror of
// the user's self-custodial ETH balance (see applyTransfer's ETHB
// branch); moving real ETH is always done directly through the
// self-custodial account (api/eth_selfcustody.go, send-eth.html),
// which never required a fee or a CreditChain-held key to begin with.
//
// core.TxBridgeWithdraw and applyBridgeWithdraw (api/apply.go) are
// deliberately NOT removed - any already-confirmed historical
// TxBridgeWithdraw transaction from before this retirement must still
// replay identically when a node re-syncs the chain from genesis.
func HandleBridgedEthWithdraw(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)
	http.Error(w, "ETHB ERC-network withdrawal is retired - CreditChain never holds an ETH signing key on your behalf. ETHB moves are CCT-network transfers only (instant, between CreditChain users); to move real ETH, use your self-custodial ETH account directly.", http.StatusGone)
}

// HandleBridgedEthWithdrawQuote is RETIRED alongside HandleBridgedEthWithdraw
// above - see that function's doc comment.
func HandleBridgedEthWithdrawQuote(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)
	http.Error(w, "ETHB ERC-network withdrawal is retired - use your self-custodial ETH account directly to move real ETH.", http.StatusGone)
}
