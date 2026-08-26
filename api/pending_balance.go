package api

import (
	"encoding/json"

	"github.com/creditchain/creditchaind/core"
)

// pendingReservedBalance sums every mempool transaction (submitted,
// not yet confirmed) from this address that would draw down this
// symbol's balance once mined. The authoritative safety net remains
// each apply function's own atomic balance check at confirmation
// time (applyTransfer/applyFairValueSwap - "never trust
// submission-time state") - a double-spend attempt can never
// actually succeed even without this function, since Transfer's own
// balance check is re-run fresh against the real, current balance
// when the transaction is applied.
//
// What this closes is a UX gap: without it, two transactions
// submitted back-to-back from the same wallet (e.g. a send
// immediately followed by a swap of the same balance, both still
// sitting in mempool waiting for the next block) would each
// individually pass their own fast-fail check against the same
// stale confirmed balance - only one can actually apply, and
// whichever confirms second would silently no-op with no reason
// ever surfaced to the caller. Checking against balance-minus-
// already-pending here means that second submission is rejected
// immediately, with a clear reason, instead of accepted and then
// silently dropped a block later.
func pendingReservedBalance(address, symbol string) uint64 {
	var reserved uint64
	for _, tx := range mempool.Transactions {
		if tx.From != address {
			continue
		}
		reserved += pendingDebitForSymbol(tx, symbol)
	}
	return reserved
}

// pendingDebitForSymbol returns how much of the given symbol's
// balance one pending transaction would consume once confirmed - 0
// if the transaction is unrelated to that symbol. Covers the two
// user-facing money-movement actions this guard is built for: send
// (EXCHANGE) and swap (FAIR_VALUE_SWAP). Any transaction whose Data
// can't be parsed in the expected shape is treated as reserving
// nothing rather than guessed at - same fail-open-on-parse-error
// convention this file's callers already use elsewhere for
// defensive JSON handling.
func pendingDebitForSymbol(tx core.Transaction, symbol string) uint64 {
	switch tx.Type {
	case core.TxExchange:
		var d tokenTransferData
		if json.Unmarshal([]byte(tx.Data), &d) != nil {
			return 0
		}
		sym := d.Symbol
		if sym == "" {
			sym = "CCT"
		}
		var reserved uint64
		if sym == symbol {
			amt := d.Amount
			switch sym {
			case "CCT":
				amt += governanceParam("CCTTransferFee", CCTTransferFee)
			case "ETHB":
				amt += ETHBCCTNetworkFeeWei
			case "BTCB":
				amt += BTCBCCTNetworkFeeSats
			}
			reserved += amt
		}
		// A USDCTEST send pays its own fee out of a separate ETHB
		// balance (see applyTransfer's USDCTEST branch) - reserve that
		// too when checking ETHB specifically, even though the send
		// itself moves USDCTEST, not ETHB.
		if sym == "USDCTEST" && symbol == "ETHB" {
			reserved += UsdcTestTransferFeeWei
		}
		return reserved
	case core.TxFairValueSwap:
		var d fairValueSwapRequest
		if json.Unmarshal([]byte(tx.Data), &d) != nil {
			return 0
		}
		if d.FromSymbol == symbol {
			return d.FromAmount
		}
		return 0
	default:
		return 0
	}
}
