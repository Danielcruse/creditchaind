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

	// ExternalTxHash/ExternalTxLink surface the real external-chain
	// transaction a bridge withdrawal ultimately broadcast as - see
	// core.TxBridgeTxHashRecord's doc comment for why ETH's hash is a
	// side-store lookup (not known until after this transaction was
	// already confirmed) while BTC's txid is already present directly in
	// this same transaction's own Data (known by the time it's
	// recorded). Both omitted entirely for any transaction that isn't a
	// bridge withdrawal, or an ETH withdrawal whose broadcast hasn't
	// been recorded yet.
	ExternalTxHash string `json:"externalTxHash,omitempty"`
	ExternalTxLink string `json:"externalTxLink,omitempty"`
}

func viewTransaction(tx core.Transaction) TransactionView {
	v := TransactionView{Transaction: tx, Summary: buildTransactionSummary(tx)}
	if event, ok := reputationEventStore.Get(tx.ID); ok {
		v.ReputationDelta = event.Delta
		v.ReputationTarget = event.Target
	}

	switch tx.Type {
	case core.TxBridgeWithdraw:
		if hash, ok := lookupBridgeExternalTxHash(tx.ID); ok {
			v.ExternalTxHash = hash
			v.ExternalTxLink = "https://sepolia.etherscan.io/tx/" + hash
		}
	case core.TxBridgeBtcWithdraw:
		var data bridgeBtcWithdrawData
		if err := json.Unmarshal([]byte(tx.Data), &data); err == nil && data.BtcTxid != "" {
			v.ExternalTxHash = data.BtcTxid
			v.ExternalTxLink = "https://blockstream.info/testnet/tx/" + data.BtcTxid
		}
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
			cctIn, _ := core.ParseCctSmallest(data.AmountIn)
			return fmt.Sprintf("Swapped CCT for %s via the liquidity pool (~%d CCT in)", data.Symbol, core.CctSmallestToWholeFloor(core.FormatCctSmallest(cctIn)))
		}
		return fmt.Sprintf("Swapped %s for CCT via the liquidity pool (%s %s in)", data.Symbol, data.AmountIn, data.Symbol)

	case core.TxSwapViaCCT:
		var data swapViaCCTData
		json.Unmarshal([]byte(tx.Data), &data)
		amountDisplay := data.AmountIn
		if data.FromSymbol == "CCT" {
			cctIn, _ := core.ParseCctSmallest(data.AmountIn)
			amountDisplay = fmt.Sprintf("~%d", core.CctSmallestToWholeFloor(core.FormatCctSmallest(cctIn)))
		}
		return fmt.Sprintf("Swapped %s %s for %s (routed via the CCT hub)", amountDisplay, data.FromSymbol, data.ToSymbol)

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

	case core.TxBridgeWithdraw:
		return fmt.Sprintf("Bridged %d wei of ETH out to a real Sepolia address", tx.Amount)

	case core.TxBridgeBtcWithdraw:
		return fmt.Sprintf("Bridged %d sats of BTC out to a real Bitcoin testnet address", tx.Amount)

	// --- Vault (lock-and-mint bridge) ---------------------------------

	case core.TxVaultLockAttest:
		var data vaultLockAttestData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Bridge validator attested a real %s deposit from %s (tx %s) - contributes toward minting %s_CCC1", data.Chain, short(data.DepositorCcAddress), short(data.ExternalTxHash), data.Chain)

	case core.TxVaultBurn:
		var data vaultBurnData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Burned %s_CCC1 to withdraw the real locked asset to %s", data.Chain, short(data.DestinationExtAddress))

	case core.TxVaultReleaseApprove:
		var data vaultReleaseApproveData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Bridge validator co-signed the real release for burn %s", short(data.BurnTxId))

	case core.TxVaultReleaseComplete:
		var data vaultReleaseCompleteData
		json.Unmarshal([]byte(tx.Data), &data)
		if data.ExternalHash != "" {
			return fmt.Sprintf("Vault release broadcast confirmed - real external transaction %s", short(data.ExternalHash))
		}
		return "Vault release broadcast confirmed"

	case core.TxBridgeValidatorRegister:
		var data bridgeValidatorRegisterData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Registered as a bridge validator (ETH signing address %s)", short(data.EthSigningAddress))

	// --- Legacy mirror-model bridge (ETHB/BTCB) - node-authored, report the poll result -----

	case core.TxBridgeDeposit:
		var data bridgeDepositReportData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Detected a real ETH deposit for %s - synced bridged balance", short(data.CreditChainAddress))

	case core.TxBridgeBtcDeposit:
		var data btcBridgeDepositReportData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Detected a real BTC deposit for %s - synced bridged balance", short(data.CreditChainAddress))

	case core.TxBridgeTxHashRecord:
		var data bridgeTxHashRecordData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Recorded the real external transaction hash %s for a bridge withdrawal", short(data.ExternalHash))

	// --- P2P disputes --------------------------------------------------

	case core.TxP2PSellerDispute:
		return "Seller disputed a buyer's payment claim - summoned the assigned moderator"

	case core.TxP2PBuyerDispute:
		return "Buyer escalated an unresponsive seller - summoned the assigned moderator"

	// --- Validator delegation/commission --------------------------------

	case core.TxValidatorDelegate:
		var data validatorDelegateData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Delegated %d CCT to validator %s", data.Amount, short(data.Validator))

	case core.TxValidatorUndelegate:
		var data validatorUndelegateData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Undelegated %d CCT from validator %s", data.Amount, short(data.Validator))

	case core.TxValidatorSetCommission:
		var data validatorSetCommissionData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Set validator commission to %.1f%%", float64(data.CommissionBps)/100)

	// --- Loan requests (peer-negotiated) --------------------------------

	case core.TxLoanRequestCreate:
		var data loanRequestCreateData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Posted a loan request: %d %s wanted, %d %s offered as collateral", data.Amount, data.RequestedSymbol, data.CollateralAmount, data.CollateralSymbol)

	case core.TxLoanRequestCancel:
		return "Cancelled a loan request"

	case core.TxLoanRequestFulfill:
		return "Fulfilled a loan request via negotiation"

	case core.TxLoanAddCollateral:
		var data loanAddCollateralData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Added %d more collateral to an open loan", data.Amount)

	case core.TxLoanRedeemShortfall:
		return "Redeemed a defaulted loan's shortfall"

	// --- Trading Hub / company verification -----------------------------

	case core.TxCompanyUpdateContact:
		return "Updated Trading Hub contact email"

	case core.TxCompanyUploadCertificate:
		return "Uploaded a verification document for Trading Hub review"

	case core.TxWalletConnectHub:
		var data walletConnectHubData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Connected to Trading Hub %s", short(data.Hub))

	case core.TxHubAutomationSubscribe:
		return "Subscribed to Trading Hub automation"

	case core.TxHubWorkerSetAutomation:
		var data hubWorkerSetAutomationData
		json.Unmarshal([]byte(tx.Data), &data)
		if data.IsAutomationAgent {
			return fmt.Sprintf("Enabled automation for hub worker %s", short(data.WorkerAddress))
		}
		return fmt.Sprintf("Disabled automation for hub worker %s", short(data.WorkerAddress))

	// --- Wallet / recovery -----------------------------------------------

	case core.TxHDWalletMigrate:
		var data hdWalletMigrateData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Migrated wallet to a new HD-derived address %s", short(data.NewAddress))

	// --- Treasury / CCT sale ----------------------------------------------

	case core.TxTreasuryPurchaseRequest:
		var data treasuryPurchaseRequestData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Requested to buy %d CCT (%s)", data.CCTAmount, data.Method)

	case core.TxTreasuryPurchaseConfirm:
		return "Admin confirmed a CCT purchase - CCT delivered"

	case core.TxTreasurySetSalePrice:
		var data treasurySetSalePriceData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Set CCT sale price to %d cents/CCT", data.SalePriceCentsPerCCT)

	case core.TxTreasurySetPaused:
		var data treasurySetPausedData
		json.Unmarshal([]byte(tx.Data), &data)
		if data.Paused {
			return "Paused the public CCT sale"
		}
		return "Resumed the public CCT sale"

	case core.TxTreasurySetAddress:
		return "Updated the Foundation Treasury address"

	case core.TxTreasuryPurchaseSendInstructions:
		return "Admin sent payment instructions for a pending CCT purchase"

	case core.TxTreasuryPurchaseBuyerMarkedPaid:
		return "Buyer marked a CCT purchase as paid"

	case core.TxTreasuryPurchaseMarkPaidByBuyer:
		return "Buyer marked a CCT purchase as paid (in-app)"

	case core.TxTreasurySetBankAccount:
		var data adminSetBankAccountData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Admin set the preset bank account for %s", data.Currency)

	// --- Admin / governance ------------------------------------------------

	case core.TxAdminTransfer:
		var data adminTransferData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Admin moved %d %s from %s to %s (balance recovery)", data.Amount, data.Symbol, short(data.From), short(data.To))

	case core.TxAdminNormalizeCcc1Balance:
		var data adminNormalizeCcc1BalanceData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Admin normalized a mis-cased %s balance at %s", data.Symbol, short(data.LegacyAddress))

	case core.TxAdminRefundStuckVaultBurn:
		var data adminRefundStuckVaultBurnData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Admin refunded a stuck vault burn %s back to its original depositor", short(data.BurnTxId))

	case core.TxAnnouncementCreate:
		var data announcementCreateData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Posted an announcement: %s", data.Title)

	case core.TxAdminSetParam:
		var data adminSetParamData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Admin set governance parameter %s = %d", data.Key, data.Value)

	case core.TxAdminMultisigPropose:
		var data adminMultisigProposeData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Proposed a multisig admin action: %s", data.ActionType)

	case core.TxAdminMultisigApprove:
		return "Approved a pending multisig admin proposal"

	case core.TxGenesisPoolAllocation:
		return "Ran the one-time genesis pool allocation"

	case core.TxGenesisPoolWithdraw:
		var data genesisPoolWithdrawData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Admin withdrew %d from the %s genesis pool to %s", data.Amount, data.Pool, short(data.To))

	case core.TxTokenomicsDecimalMigration:
		return "Ran the one-time CCT decimal migration"

	// --- Validator application (staged onboarding) --------------------------

	case core.TxValidatorApplicationCreate:
		return "Started a validator application"

	case core.TxValidatorApplicationSupport:
		var data validatorApplicationSupportData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Supported %s's validator application", short(data.Address))

	case core.TxValidatorApplicationConfirm:
		return "Confirmed a validator application via emailed link"

	// --- KYC / order book / RC / prediction markets --------------------------

	case core.TxKYCSubmit:
		return "Submitted KYC information"

	case core.TxOrderPlace:
		var data orderPlaceData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Placed a %s order: %d %s at %d", data.Side, data.Amount, data.Symbol, data.Price)

	case core.TxOrderCancel:
		return "Cancelled an order"

	case core.TxYeneClaim:
		return "Claimed YEN-e from legacy stake migration"

	case core.TxProposalPromote:
		var data proposalPromoteData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Promoted a governance proposal with %d RC", data.RCAmount)

	case core.TxRCInfuse:
		var data rcInfuseData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Infused %d RC (%s)", data.RCAmount, data.TrustReason)

	case core.TxExecutiveProposalCreate:
		var data executiveProposalCreateData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Created an executive proposal: %s = %d", data.ParamKey, data.ParamValue)

	case core.TxExecutiveProposalVote:
		var data executiveProposalVoteData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Voted %s on an executive proposal", map[bool]string{true: "yes", false: "no"}[data.Support])

	case core.TxPredictionVote:
		var data predictionVoteData
		json.Unmarshal([]byte(tx.Data), &data)
		if data.RCAmount > 0 {
			return fmt.Sprintf("Voted %s on a prediction market, backed with %d RC", data.Side, data.RCAmount)
		}
		return fmt.Sprintf("Voted %s on a prediction market", data.Side)

	// --- Fair-value swap (BTCB/ETHB/YENE only, see api/swap_fair_value.go) ---

	case core.TxFairValueSwap:
		var data fairValueSwapData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Swapped %d %s for %d %s (fair-value, USD-priced)", data.FromAmount, data.FromSymbol, data.ToAmount, data.ToSymbol)

	case core.TxRatingSubmit:
		var data ratingSubmitData
		json.Unmarshal([]byte(tx.Data), &data)
		return fmt.Sprintf("Left a %d-star rating", data.Stars)

	default:
		return string(tx.Type)
	}
}
