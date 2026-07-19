package api

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"time"

	"github.com/creditchain/creditchaind/core"
)

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

// startEthBridgeDepositLoop polls every known custodial Ethereum
// account's real Sepolia balance and reports any increase as a
// TxBridgeDeposit - see applyBridgeDeposit (api/apply.go) for how that
// report gets turned into an ETHB credit once confirmed.
//
// Inherits the exact same non-gossip limitation already disclosed in
// core/eth_custodial.go's doc comment: only the node that created a
// given custodial account has it in its own EthCustodialStore, so only
// that node will ever notice and report deposits to it. Once a report
// IS submitted, though, it's a normal signed transaction that gossips
// and confirms like any other, so every node converges on the same
// ETHB credit - see audit/KNOWN_GAPS.md for the full disclosure.
func startEthBridgeDepositLoop() {
	go func() {
		ticker := time.NewTicker(ethBridgeDepositPollInterval)
		defer ticker.Stop()

		for range ticker.C {
			if ethRPCClient == nil {
				continue // ETH corner not enabled (no SEPOLIA_RPC_URL) - nothing to poll
			}

			for _, account := range ethCustodialStore.Snapshot() {
				pollAndReportBridgeDeposit(account)
			}
		}
	}()
}

func pollAndReportBridgeDeposit(account *core.EthCustodialAccount) {
	current, err := ethBalance(account.EthAddress)
	if err != nil {
		fmt.Println("bridge deposit poll failed for", account.EthAddress, ":", err)
		return
	}

	baseline := new(big.Int)
	if account.LastCreditedWei != "" {
		if _, ok := baseline.SetString(account.LastCreditedWei, 10); !ok {
			baseline.SetInt64(0)
		}
	}

	if current.Cmp(baseline) <= 0 {
		return // no new deposit since the last credited balance
	}

	data, _ := json.Marshal(bridgeDepositReportData{
		CreditChainAddress: account.CreditChainAddress,
		NewBalanceWei:      current.String(),
	})

	fmt.Println("reporting bridge deposit for", account.CreditChainAddress, "new balance", current.String(), "wei")

	recordTx(selfSignedEnvelope(data), reporterAddress(), "", 0, core.TxBridgeDeposit, string(data))
}
