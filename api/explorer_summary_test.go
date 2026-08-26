package api

import (
	"testing"

	"github.com/creditchain/creditchaind/core"
)

// TestBuildTransactionSummary_EveryReachableTypeHasARealSummary is a
// regression test for a real user complaint ("transaction history dont
// yet report well"): 58 of 104 TransactionType values fell through
// buildTransactionSummary's default case and rendered as the bare
// uppercase constant string (e.g. "VAULT_BURN", "ADMIN_TRANSFER")
// instead of a plain-English sentence, on every surface that displays
// tx.summary (outernet.js's Recent Transactions, asset.js's History
// tab, tx.js's detail page). This locks in that every type actually
// reachable in production now gets a real sentence, even with empty
// Data (the zero-value fields still produce a sentence, just with
// blank/zero placeholders - json.Unmarshal errors on empty tx.Data are
// ignored here the same way buildTransactionSummary itself ignores
// them). Deliberately excludes TxReward/TxReputation/TxMintOETH/
// TxBurnOETH/TxClaimReward - confirmed dead constants with no
// apply function anywhere in the codebase, so there's no real
// incident risk from them still falling to the default case.
func TestBuildTransactionSummary_EveryReachableTypeHasARealSummary(t *testing.T) {
	types := []core.TransactionType{
		core.TxVaultLockAttest,
		core.TxVaultBurn,
		core.TxVaultReleaseApprove,
		core.TxVaultReleaseComplete,
		core.TxBridgeValidatorRegister,
		core.TxBridgeDeposit,
		core.TxBridgeBtcDeposit,
		core.TxBridgeTxHashRecord,
		core.TxP2PSellerDispute,
		core.TxP2PBuyerDispute,
		core.TxValidatorDelegate,
		core.TxValidatorUndelegate,
		core.TxValidatorSetCommission,
		core.TxLoanRequestCreate,
		core.TxLoanRequestCancel,
		core.TxLoanRequestFulfill,
		core.TxLoanAddCollateral,
		core.TxLoanRedeemShortfall,
		core.TxCompanyUpdateContact,
		core.TxCompanyUploadCertificate,
		core.TxWalletConnectHub,
		core.TxAdminRefundStuckVaultBurn,
		core.TxHubAutomationSubscribe,
		core.TxHubWorkerSetAutomation,
		core.TxHDWalletMigrate,
		core.TxTreasuryPurchaseRequest,
		core.TxTreasuryPurchaseConfirm,
		core.TxTreasurySetSalePrice,
		core.TxTreasurySetPaused,
		core.TxTreasurySetAddress,
		core.TxTreasuryPurchaseSendInstructions,
		core.TxTreasuryPurchaseBuyerMarkedPaid,
		core.TxTreasuryPurchaseMarkPaidByBuyer,
		core.TxTreasurySetBankAccount,
		core.TxAdminTransfer,
		core.TxAdminNormalizeCcc1Balance,
		core.TxAnnouncementCreate,
		core.TxAdminSetParam,
		core.TxAdminMultisigPropose,
		core.TxAdminMultisigApprove,
		core.TxGenesisPoolAllocation,
		core.TxGenesisPoolWithdraw,
		core.TxTokenomicsDecimalMigration,
		core.TxValidatorApplicationCreate,
		core.TxValidatorApplicationSupport,
		core.TxValidatorApplicationConfirm,
		core.TxKYCSubmit,
		core.TxOrderPlace,
		core.TxOrderCancel,
		core.TxYeneClaim,
		core.TxProposalPromote,
		core.TxRCInfuse,
		core.TxExecutiveProposalCreate,
		core.TxExecutiveProposalVote,
		core.TxPredictionVote,
		core.TxFairValueSwap,
		core.TxRatingSubmit,
	}

	for _, txType := range types {
		t.Run(string(txType), func(t *testing.T) {
			summary := buildTransactionSummary(core.Transaction{Type: txType, Data: ""})
			if summary == string(txType) {
				t.Fatalf("buildTransactionSummary(%s) fell through to the raw type string - missing a case", txType)
			}
			if summary == "" {
				t.Fatalf("buildTransactionSummary(%s) returned an empty string", txType)
			}
		})
	}
}
