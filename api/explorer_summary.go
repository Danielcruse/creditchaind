package api

import (
	"encoding/json"
	"fmt"

	"github.com/creditchain/creditchaind/core"
)

// TransactionView is what the explorer actually serves for a
// transaction - the raw on-chain core.Transaction plus a
// human-readable Summary (built fresh at read time from Type + Data,
// looking up whatever loan/offer/rate record it references) and
// whatever reputation change it caused (see core.ReputationEvent's
// doc comment for why that's a side-channel lookup rather than a
// field on Transaction itself).
type TransactionView struct {
	core.Transaction
	Summary          string `json:"summary,omitempty"`
	ReputationDelta  int64  `json:"reputationDelta,omitempty"`
	ReputationTarget string `json:"reputationTarget,omitempty"`
}

func viewTransaction(tx core.Transaction) TransactionView {
	v := TransactionView{Transaction: tx, Summary: buildTransactionSummary(tx)}
	if event, ok := reputationEventStore.Get(tx.ID); ok {
		v.ReputationDelta = event.Delta
		v.ReputationTarget = event.Target
	}
	return v
}

func viewTransactions(txs []core.Transaction) []TransactionView {
	out := make([]TransactionView, len(txs))
	for i, tx := range txs {
		out[i] = viewTransaction(tx)
	}
	return out
}

func short(addr string) string {
	if len(addr) <= 14 {
		return addr
	}
	return addr[:8] + "..." + addr[len(addr)-6:]
}

// buildTransactionSummary is the single place that turns a raw
// Type+Data pair into the plain-English line the explorer shows -
// specifically calling out, for loans, who actually lent (a real
// wallet) versus who the system minted for with no real lender behind
// it, and for every swap, the exact two sides and amounts that moved.
// Falls back to a generic description for any type not given its own
// case below, so a search never comes back with nothing to show.
func buildTransactionSummary(tx core.Transaction) string {
	switch tx.Type {

	case core.TxBorrow:
		// Self-funded only - see core/loan.go's doc comment: TxBorrow
		// always mints instantly with no real lender, unlike
		// TxLoanOfferDraw below. tx.To is the borrower (tx.From is
		// left empty to represent the lending pool - see
		// core.Transaction.Auth* doc comment).
		return fmt.Sprintf("Borrowed %d USDCTEST - self-funded by the protocol (no lender, no interest paid to anyone)", tx.Amount)

	case core.TxLoanOfferDraw:
		var data loanOfferDrawData
		json.Unmarshal([]byte(tx.Data), &data)
		if offer, ok := loanOfferStore.Get(data.OfferID); ok {
			return fmt.Sprintf("Borrowed %d %s from %s (peer-funded loan, %d%% interest)", data.Amount, offer.Symbol, short(offer.Lender), offer.RatePercent)
		}
		return fmt.Sprintf("Borrowed %d units from a peer-funded loan offer", data.Amount)

	case core.TxLoanOfferCreate:
		var data loanOfferCreateData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Posted a loan offer: up to %d %s available to borrow at %d%% interest", data.TotalAmount, data.Symbol, data.RatePercent)

	case core.TxLoanOfferClose:
		return "Closed a loan offer, withdrew remaining undrawn capital"

	case core.TxRepay:
		var data repayData
		json.Unmarshal([]byte(tx.Data), &data)
		loan, ok := loanStore.Get(data.LoanID)
		if !ok {
			return "Repaid a loan"
		}
		if data.Mode == "p2p" || loan.Lender != "" {
			return fmt.Sprintf("Repaid %d %s (+interest) to %s (peer-funded loan)", loan.Amount, loanSymbol(loan), short(loan.Lender))
		}
		return fmt.Sprintf("Repaid %d %s (+interest) - self-funded by the protocol (no lender)", loan.Amount, loanSymbol(loan))

	case core.TxLend:
		var data lendData
		json.Unmarshal([]byte(tx.Data), &data)
		if loan, ok := loanStore.Get(data.LoanID); ok {
			return fmt.Sprintf("Funded a loan: lent %d %s to %s", loan.Amount, loanSymbol(loan), short(loan.Borrower))
		}
		return "Funded a loan"

	case core.TxPoolSwap:
		var data poolSwapData
		json.Unmarshal([]byte(tx.Data), &data)
		if data.Direction == "cct_to_token" {
			return fmt.Sprintf("Swapped CCT for %s via the liquidity pool (%d CCT in)", data.Symbol, data.AmountIn)
		}
		return fmt.Sprintf("Swapped %s for CCT via the liquidity pool (%d %s in)", data.Symbol, data.AmountIn, data.Symbol)

	case core.TxSwapViaCCT:
		var data swapViaCCTData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Swapped %d %s for %s (routed via the CCT hub)", data.AmountIn, data.FromSymbol, data.ToSymbol)

	case core.TxP2POfferCreate:
		var data p2pOfferCreateData
		json.Unmarshal([]byte(tx.Data), &data)
		mode := "on-chain, atomic swap"
		if data.PaymentMode == "EXTERNAL" {
			mode = "off-app payment"
		}
		return fmt.Sprintf("Listed a P2P offer: %d %s for %d %s (%s)", data.OfferAmount, data.OfferSymbol, data.WantAmount, data.WantSymbol, mode)

	case core.TxP2POfferAccept:
		var data p2pOfferActionData
		json.Unmarshal([]byte(tx.Data), &data)
		if offer, ok := p2pOfferStore.Get(data.OfferID); ok {
			return fmt.Sprintf("Accepted a P2P offer: swapped %d %s for %d %s with %s (both parties earned reputation)", offer.WantAmount, offer.WantSymbol, offer.OfferAmount, offer.OfferSymbol, short(offer.Creator))
		}
		return "Accepted a P2P offer"

	case core.TxP2POfferCancel:
		return "Cancelled a P2P offer, refunded to self"

	case core.TxP2PExternalAccept:
		var data p2pExternalAcceptData
		json.Unmarshal([]byte(tx.Data), &data)
		if offer, ok := p2pOfferStore.Get(data.OfferID); ok {
			return fmt.Sprintf("Accepted an off-app P2P trade: paying %d %s to receive %d %s from %s", offer.WantAmount, offer.WantSymbol, offer.OfferAmount, offer.OfferSymbol, short(offer.Creator))
		}
		return "Accepted an off-app P2P trade"

	case core.TxP2PSellerConfirmPaid:
		return "Seller confirmed receiving off-app payment"

	case core.TxP2PBuyerMarkPaid:
		return "Buyer marked off-app payment as sent (not yet confirmed by seller)"

	case core.TxP2PBuyerConfirmPaid:
		var data p2pBuyerConfirmData
		json.Unmarshal([]byte(tx.Data), &data)
		if data.Agree {
			return "Buyer confirmed the trade - escrow released"
		}
		return "Buyer disputed the seller's claim - sent to moderator"

	case core.TxP2PModeratorResolve:
		var data p2pModeratorResolveData
		json.Unmarshal([]byte(tx.Data), &data)
		if data.Release {
			return "Moderator resolved dispute: released escrow to buyer"
		}
		return "Moderator resolved dispute: refunded escrow to seller"

	case core.TxHubTradeRequest:
		var data hubTradeRequestData
		json.Unmarshal([]byte(tx.Data), &data)
		verb := "Bought"
		if data.Direction == "SELL_TO_HUB" {
			verb = "Sold"
		}
		return fmt.Sprintf("%s %d %s %s Trading Hub %s", verb, data.Amount, data.Symbol, map[bool]string{true: "from", false: "to"}[data.Direction == "BUY_FROM_HUB"], short(data.Hub))

	case core.TxHubRateSet:
		var data hubRateSetData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Posted/updated a Trading Hub rate for %s", data.Symbol)

	case core.TxHubRulesSet:
		return "Updated Trading Hub trading rules"

	case core.TxHubWorkerAdd:
		var data hubWorkerAddData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Added %s as a delegated hub worker", short(data.WorkerAddress))

	case core.TxHubWorkerRemove:
		return "Removed a delegated hub worker"

	case core.TxHubStorefrontSet:
		return "Updated Trading Hub storefront design"

	case core.TxBuyRequestCreate:
		var data buyRequestCreateData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Trading Hub posted a buy request: bidding %d %s for %d %s", data.PayAmount, data.PaySymbol, data.WantAmount, data.WantSymbol)

	case core.TxBuyRequestFulfill:
		return "Fulfilled a Trading Hub buy request"

	case core.TxBuyRequestCancel:
		return "Cancelled a Trading Hub buy request"

	case core.TxStakeETH:
		var data stakeData
		json.Unmarshal([]byte(tx.Data), &data)
		symbol := data.Symbol
		if symbol == "" {
			symbol = "ETHTEST"
		}
		return fmt.Sprintf("Staked %d %s", data.Amount, symbol)

	case core.TxUnstakeETH:
		var data stakeData
		json.Unmarshal([]byte(tx.Data), &data)
		symbol := data.Symbol
		if symbol == "" {
			symbol = "ETHTEST"
		}
		return fmt.Sprintf("Began unstaking %d %s (unbonding period applies)", data.Amount, symbol)

	case core.TxLockCollateral:
		var data lockCollateralData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Locked %d %s as loan collateral", data.Amount, data.Symbol)

	case core.TxFaucetClaim:
		return "Claimed the one-time testnet faucet"

	case core.TxGovernance:
		var data governanceData
		json.Unmarshal([]byte(tx.Data), &data)
		if data.Action == "vote" {
			return fmt.Sprintf("Voted %s on a governance proposal", map[bool]string{true: "yes", false: "no"}[data.Support])
		}
		return fmt.Sprintf("Created governance proposal: %s", data.Title)

	case core.TxValidatorRegister:
		return "Registered as a validator"

	case core.TxModeratorRegister:
		return "Registered as a moderator"

	case core.TxModeratorConfirm:
		return "Moderator attested to a trade's outcome"

	case core.TxCompanyApply:
		var data companyApplyData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Applied for Trading Hub verification: %s", data.Name)

	case core.TxCompanyReview:
		var data companyReviewData
		json.Unmarshal([]byte(tx.Data), &data)
		if data.Approve {
			return "Approved a Trading Hub verification application"
		}
		return "Rejected a Trading Hub verification application"

	case core.TxCompanyReassignModerator:
		return "Reassigned a Trading Hub application to a new moderator"

	case core.TxTokenRegister:
		return "Registered a new custom token"

	case core.TxPoolAddLiquidity:
		return "Added liquidity to a pool"

	case core.TxPoolRemoveLiquidity:
		return "Removed liquidity from a pool"

	case core.TxExchange:
		return fmt.Sprintf("Transferred %d tokens", tx.Amount)

	case core.TxSetRecoveryEmail:
		return "Set a wallet recovery email"

	case core.TxRecoverWallet:
		return "Recovered a wallet to a new address"

	case core.TxSlashReport:
		return "Reported a validator slashing event"

	case core.TxTokenMint:
		return fmt.Sprintf("Admin minted %d tokens (testnet faucet/seeding)", tx.Amount)

	default:
		return string(tx.Type)
	}
}
