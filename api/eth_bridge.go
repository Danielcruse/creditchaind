package api

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/creditchain/creditchaind/core"
)

var ethAddressStore = core.NewEthAddressStore()

type ethRegisterAddressData struct {
	EthAddress string `json:"ethAddress"`
}

// HandleEthRegisterAddress lets a wallet tell this node which
// self-custodial Ethereum address (already derived client-side by
// website/js/hdwallet.js's deriveEthAddress, and already shown on the
// Assets/Receive page) to watch for deposits - mirrors
// api/btc_bridge.go's HandleBtcRegisterAddress exactly. Free: unlike
// HandleCreateEthCustodialAccount, this never provisions a real AWS KMS
// key, so there's no anti-Sybil cost to charge - registering a watch on
// an address costs this operator nothing ongoing.
func HandleEthRegisterAddress(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req ethRegisterAddressData
	envelope, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	ethAddress := strings.TrimSpace(req.EthAddress)
	// Loose shape check only - a wrong/malformed value here just means
	// deposit-detection harmlessly never finds a match, same reasoning
	// as HandleBtcRegisterAddress's own check.
	if !strings.HasPrefix(ethAddress, "0x") || len(ethAddress) != 42 {
		http.Error(w, "ethAddress must be a 0x-prefixed 20-byte hex address", http.StatusBadRequest)
		return
	}
	// Ethereum addresses are case-INSENSITIVE (EIP-55 checksum casing is
	// only a client-side typo check, not a semantically different
	// address) - normalizing to lowercase here is what makes this the
	// one canonical key ethCcc1Store/usdcCcc1Store/bridgedEthStore ever
	// see for this depositor, regardless of which client-side function
	// happened to derive it (website/js/hdwallet.js's deriveEthAddress
	// checksums; website/js/vendor/eth-tx.js's addressFromPrivateKey
	// does not) - a real gap caught in review, see api/subaddress.go's
	// resolveBalanceAddress and api/vault_info.go's HandleVaultBalance
	// for the matching read-side normalization.
	ethAddress = normalizeEthAddress(ethAddress)

	ethAddressStore.Set(&core.EthWatchedAddress{
		CreditChainAddress:   address,
		CreditChainPublicKey: envelope.PublicKey,
		EthAddress:           ethAddress,
		CreatedAt:            time.Now().Unix(),
	})

	json.NewEncoder(w).Encode(map[string]string{
		"status":     "registered",
		"ethAddress": ethAddress,
	})
}

// HandleEthWatchedAddress is a lightweight lookup of which ETH address
// (if any) this wallet has registered - mirrors
// api/btc_bridge.go's HandleBtcWatchedAddress.
func HandleEthWatchedAddress(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	address := r.URL.Query().Get("address")
	watched, ok := ethAddressStore.Get(address)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{
		"ethAddress":      watched.EthAddress,
		"lastCreditedWei": watched.LastCreditedWei,
	})
}

// HandleEthbBalance mirrors HandleYenBalance but for bridgedEthStore -
// the auto-bridge feature's unified ETH balance (in wei). Unlike the
// other custom-token balance endpoints (whose uint64 values are small
// token counts, safe as a native JSON number), ETHB balances are
// wei-scale and routinely exceed JS's 2^53 safe-integer ceiling - the
// balance is therefore returned as a decimal string, the same
// convention /eth-selfcustody/balance already uses for weiBalance, so
// a large balance doesn't silently lose precision on the way through
// JSON parsing in the browser.
func HandleEthbBalance(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	address := r.URL.Query().Get("address")
	balance := bridgedEthStore.BalanceOf(address)

	json.NewEncoder(w).Encode(map[string]string{
		"balance": fmt.Sprintf("%d", balance),
	})
}

// ethBridgeDepositPollInterval is how often this node checks its own
// custodial accounts' real Sepolia balances for new deposits. RPC
// calls aren't free/instant and testnet deposits aren't latency-
// sensitive, so this is deliberately much slower than the ~8s
// consensus tick - a separate ticker, not piggybacked onto
// startConsensusLoop's.
const ethBridgeDepositPollInterval = 30 * time.Second

// bridgeDepositReportData is the payload of a TxBridgeDeposit - a
// node-authored, self-signed report (same pattern as
// consensus_loop.go's slashReportData/reportMissedBlock) that a given
// custodial account's real Sepolia balance has risen. Deliberately
// reports the observed ABSOLUTE balance, not a delta - the apply side
// (applyBridgeDeposit, api/apply.go) computes and credits the delta
// itself from the account's own already-applied baseline, so this
// report can't be replayed to double-credit even if resubmitted.
type bridgeDepositReportData struct {
	CreditChainAddress string `json:"creditChainAddress"`
	NewBalanceWei      string `json:"newBalanceWei"`
}

// startEthBridgeDepositLoop polls every known ETH address's real
// on-chain balance and reports any increase as a TxBridgeDeposit - see
// applyBridgeDeposit (api/apply.go) for how that report gets turned
// into an ETHB credit once confirmed.
//
// Primarily polls ethAddressStore - free, self-custodial addresses the
// user's own browser derived and registered (mirrors
// api/btc_bridge.go's watched-address model exactly, see
// core/eth_address.go's doc comment for why this replaced the earlier
// paid AWS-KMS-custodial-account design). ethCustodialStore is still
// polled too, purely for backward compatibility with any account that
// was already provisioned under the old design before this change - no
// new custodial accounts are created by any current UI path.
//
// Inherits the exact same non-gossip limitation already disclosed in
// core/eth_custodial.go's doc comment: only the node an address was
// registered on will ever notice and report deposits to it. Once a
// report IS submitted, though, it's a normal signed transaction that
// gossips and confirms like any other, so every node converges on the
// same ETHB credit - see audit/KNOWN_GAPS.md for the full disclosure.
func startEthBridgeDepositLoop() {
	go func() {
		ticker := time.NewTicker(ethBridgeDepositPollInterval)
		defer ticker.Stop()

		for range ticker.C {
			if ethRPCClient == nil {
				continue // ETH corner not enabled (no ETH_RPC_URL) - nothing to poll
			}

			for _, watched := range ethAddressStore.Snapshot() {
				pollAndReportBridgeDeposit(watched.CreditChainAddress, watched.EthAddress, watched.LastCreditedWei)
			}
			for _, account := range ethCustodialStore.Snapshot() {
				pollAndReportBridgeDeposit(account.CreditChainAddress, account.EthAddress, account.LastCreditedWei)
			}
		}
	}()
}

func pollAndReportBridgeDeposit(creditChainAddress, ethAddress, lastCreditedWei string) {
	current, err := ethBalance(ethAddress)
	if err != nil {
		fmt.Println("bridge deposit poll failed for", ethAddress, ":", err)
		return
	}

	baseline := new(big.Int)
	if lastCreditedWei != "" {
		if _, ok := baseline.SetString(lastCreditedWei, 10); !ok {
			baseline.SetInt64(0)
		}
	}

	if current.Cmp(baseline) <= 0 {
		return // no new deposit since the last credited balance
	}

	data, _ := json.Marshal(bridgeDepositReportData{
		CreditChainAddress: creditChainAddress,
		NewBalanceWei:      current.String(),
	})

	fmt.Println("reporting bridge deposit for", creditChainAddress, "new balance", current.String(), "wei")

	recordTx(selfSignedEnvelope(data), reporterAddress(), "", 0, core.TxBridgeDeposit, string(data))
}
