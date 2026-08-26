package core

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type TransactionType string

const (
	TxBorrow     TransactionType = "BORROW"
	TxLend       TransactionType = "LEND"
	TxExchange   TransactionType = "EXCHANGE"
	TxReward     TransactionType = "REWARD"
	TxGovernance TransactionType = "GOVERNANCE"
	TxReputation TransactionType = "REPUTATION"

	TxStakeETH   TransactionType = "STAKE_ETH"
	TxUnstakeETH TransactionType = "UNSTAKE_ETH"

	TxMintOETH TransactionType = "MINT_OETH"
	TxBurnOETH TransactionType = "BURN_OETH"

	TxClaimReward       TransactionType = "CLAIM_REWARD"
	TxRepay             TransactionType = "REPAY"
	TxValidatorRegister TransactionType = "VALIDATOR_REGISTER"
	TxLockCollateral    TransactionType = "LOCK_COLLATERAL"
	TxSlashReport       TransactionType = "SLASH_REPORT"

	TxValidatorDelegate      TransactionType = "VALIDATOR_DELEGATE"
	TxValidatorUndelegate    TransactionType = "VALIDATOR_UNDELEGATE"
	TxValidatorSetCommission TransactionType = "VALIDATOR_SET_COMMISSION"

	TxBridgeDeposit  TransactionType = "BRIDGE_DEPOSIT"
	TxBridgeWithdraw TransactionType = "BRIDGE_WITHDRAW"

	// TxBridgeBtcDeposit is BTCB's deposit-report counterpart to
	// TxBridgeDeposit - see api/btc_bridge.go's pollAndReportBtcDeposit.
	TxBridgeBtcDeposit TransactionType = "BRIDGE_BTC_DEPOSIT"

	// TxBridgeTxHashRecord is a small node-authored follow-up to a
	// TxBridgeWithdraw, submitted once the real external-chain broadcast
	// succeeds and its tx hash becomes known - see
	// api/eth_bridge_withdraw.go's HandleBridgedEthWithdraw. The real
	// hash can't live in the original transaction's own Data (that's
	// already fixed and hashed into a confirmed block before the
	// broadcast happens), so this is recorded as its own separate,
	// gossiped/replicated transaction instead - every node converges on
	// the same mapping, same "report it, don't mutate history" shape
	// TxSlashReport already established.
	TxBridgeTxHashRecord TransactionType = "BRIDGE_TX_HASH_RECORD"

	// TxBridgeBtcWithdraw records a real BTC-network withdrawal - unlike
	// TxBridgeWithdraw's ETH counterpart, this doesn't debit any
	// CreditChain-side balance (a BTC-network send spends the user's own
	// self-custodial UTXOs directly, entirely off CreditChain's ledger)
	// - its only purpose is to give the withdrawal an explorer-visible,
	// permanent record with the real Bitcoin txid attached, closing the
	// "BTCB withdrawals are otherwise completely invisible" gap. See
	// api/btc_bridge.go's HandleBtcBroadcast.
	TxBridgeBtcWithdraw TransactionType = "BRIDGE_BTC_WITHDRAW"

	// TxVaultReleaseComplete is the vault-bridge's own version of
	// TxBridgeTxHashRecord above - a node-authored follow-up recording
	// that a real release() call (ETH/USDC) or BTC broadcast actually
	// succeeded, once the node running the relay observes it. Doesn't
	// move any funds itself (the real external-chain transaction already
	// did that, independently verifiable on a block explorer) - this
	// only flips core.VaultReleaseApproval.Status so every node's own
	// view (and the withdrawal page's status poll) converges on the
	// outcome. See api/vault_release_relay.go / api/vault_btc_release_relay.go.
	TxVaultReleaseComplete TransactionType = "VAULT_RELEASE_COMPLETE"

	TxTokenRegister       TransactionType = "TOKEN_REGISTER"
	TxPoolAddLiquidity    TransactionType = "POOL_ADD_LIQUIDITY"
	TxPoolRemoveLiquidity TransactionType = "POOL_REMOVE_LIQUIDITY"
	TxPoolSwap            TransactionType = "POOL_SWAP"

	// TxSwapViaCCT is a swap between any two tokens (native ETHTEST/
	// USDCTEST or a developer-registered one) that routes through CCT
	// as the universal hub currency - a single hop if either side IS
	// CCT, or two hops (token -> CCT -> token) otherwise. See
	// api/apply.go's applySwapViaCCT.
	TxSwapViaCCT TransactionType = "SWAP_VIA_CCT"

	// P2P offers are fixed-price trader-to-trader trades (testnet
	// tokens only - see core/p2p_offer.go's doc comment for how this
	// differs from the floating-price AMM pools).
	TxP2POfferCreate TransactionType = "P2P_OFFER_CREATE"
	TxP2POfferAccept TransactionType = "P2P_OFFER_ACCEPT"
	TxP2POfferCancel TransactionType = "P2P_OFFER_CANCEL"

	// TxFaucetClaim replaces the faucet's old instant, node-local mint -
	// see api/apply.go's applyFaucetClaim doc comment for why an
	// unreplicated mint let two production nodes' CCT supply drift
	// apart from each other.
	TxFaucetClaim TransactionType = "FAUCET_CLAIM"

	// Loan offers are the peer-funded lending marketplace - a lender
	// posts a standing offer (rate + capital), a borrower discovers it
	// and draws whatever amount they choose against it (see
	// core/loan_offer.go's doc comment). Distinct from the older
	// self-funded TxBorrow, which still mints instantly with no lender.
	TxLoanOfferCreate TransactionType = "LOAN_OFFER_CREATE"
	TxLoanOfferDraw   TransactionType = "LOAN_OFFER_DRAW"
	TxLoanOfferClose  TransactionType = "LOAN_OFFER_CLOSE"

	// Loan requests are the borrower-initiated mirror of loan offers - a
	// borrower posts what they want to borrow and against what
	// collateral (see core/loan_request.go's doc comment), and a lender
	// discovers it and funds it at or below the borrower's stated
	// maximum rate. Fulfillment reuses the existing negotiation
	// chat/confirm system rather than a separate direct-fund path - see
	// core.Negotiation's RequestID field.
	TxLoanRequestCreate  TransactionType = "LOAN_REQUEST_CREATE"
	TxLoanRequestCancel  TransactionType = "LOAN_REQUEST_CANCEL"
	TxLoanRequestFulfill TransactionType = "LOAN_REQUEST_FULFILL"

	// Moderators are reputation-vetted community members automatically
	// assigned to observe trade agreements (loan negotiations, P2P
	// offers) and attest on-chain whether they concluded successfully -
	// see core/moderator.go's doc comment for what this is (and isn't)
	// a guarantee of.
	TxModeratorRegister TransactionType = "MODERATOR_REGISTER"
	TxModeratorConfirm  TransactionType = "MODERATOR_CONFIRM"

	// TxTradeReview is one counterparty (or a Trading Hub on one side of
	// a hub-facilitated trade) reviewing the other after a P2P offer
	// settles successfully - see core/trade_review.go's doc comment.
	TxTradeReview TransactionType = "TRADE_REVIEW"

	// TxDisputeFaultPay/TxDisputeFaultDecline are the at-fault party's
	// two active choices on a refund-resolved P2P dispute - see
	// core.DisputeFaultChoice's own doc comment for the full mechanic
	// (a third, passive outcome - DEFAULTED - never has a transaction
	// of its own, it's applied automatically by
	// api/apply.go's resolveExpiredDisputeFaultChoices once the window
	// passes with neither of these ever confirming).
	TxDisputeFaultPay     TransactionType = "DISPUTE_FAULT_PAY"
	TxDisputeFaultDecline TransactionType = "DISPUTE_FAULT_DECLINE"

	// EXTERNAL-mode P2P offer settlement - see core/p2p_offer.go's
	// SettlementStatus doc comment for the full state machine these
	// four transaction types drive.
	TxP2PExternalAccept    TransactionType = "P2P_EXTERNAL_ACCEPT"
	TxP2PSellerConfirmPaid TransactionType = "P2P_SELLER_CONFIRM_PAID"
	// TxP2PBuyerMarkPaid is the buyer's own non-binding "I've sent the
	// payment" signal, recorded while still AWAITING_PAYMENT - distinct
	// from TxP2PBuyerConfirmPaid below, which only happens AFTER the
	// seller has already claimed receipt. See
	// core.P2POfferStore.MarkBuyerPaid's doc comment.
	TxP2PBuyerMarkPaid    TransactionType = "P2P_BUYER_MARK_PAID"
	TxP2PBuyerConfirmPaid TransactionType = "P2P_BUYER_CONFIRM_PAID"
	TxP2PModeratorResolve TransactionType = "P2P_MODERATOR_RESOLVE"
	// TxP2PSellerDispute lets the receiving side reject a payment claim
	// while still AWAITING_PAYMENT, instead of only being able to
	// disagree after already confirming receipt - see
	// core.P2POfferStore.DisputeAwaitingPayment's doc comment.
	TxP2PSellerDispute TransactionType = "P2P_SELLER_DISPUTE"
	// TxP2PBuyerDispute is the buyer's own escalation path when the
	// seller neither confirms nor disputes their payment claim within
	// core.DisputeEligibleAfterSeconds - see
	// core.P2POfferStore.DisputeAwaitingPaymentByBuyer's doc comment.
	TxP2PBuyerDispute TransactionType = "P2P_BUYER_DISPUTE"

	// Company verification is a testnet-only placeholder badge - see
	// core/company.go's doc comment for what it is (and very much
	// isn't) a guarantee of.
	TxCompanyApply  TransactionType = "COMPANY_APPLY"
	TxCompanyReview TransactionType = "COMPANY_REVIEW"

	// TxCompanyUpdateContact lets an applicant/hub change where they
	// get emailed trade-attention notifications (see api/email.go,
	// core.CompanyStore.SetContactEmail) - not moderator-gated, since
	// it's the applicant's own notification preference.
	TxCompanyUpdateContact TransactionType = "COMPANY_UPDATE_CONTACT"

	// TxCompanyReassignModerator lets any currently-registered moderator
	// claim a still-PENDING application away from its originally
	// assigned moderator - see core.CompanyStore.ReassignModerator's doc
	// comment for why this exists (an application should never be
	// permanently stuck just because its assigned moderator's key is
	// lost or otherwise unreachable).
	TxCompanyReassignModerator TransactionType = "COMPANY_REASSIGN_MODERATOR"

	// TxCompanyUploadCertificate records a real uploaded certificate
	// document against a still-PENDING application (see
	// core.CompanyStore.SetCertificate, api/companies.go's
	// HandleUploadCertificate) - a moderator's approval is only valid
	// once one of these has landed.
	TxCompanyUploadCertificate TransactionType = "COMPANY_UPLOAD_CERTIFICATE"

	// TxWalletConnectHub lets an ALREADY-EXISTING wallet set
	// core.Wallet.ConnectedHub after the fact - e.g. a wallet holder who
	// registered long ago now follows a Trading Hub's shareable link
	// (website/hub-link.html) and picks "I already have a wallet."
	// Signed by the wallet itself (see api/apply.go's
	// applyWalletConnectHub) so nobody else can silently attach a
	// stranger's wallet to a hub they never chose. A BRAND-NEW wallet
	// created directly through a hub link sets ConnectedHub straight at
	// registration instead (api/wallet_create.go) - no transaction
	// needed there, same as how ReferredBy itself is set unauthenticated
	// at creation time.
	TxWalletConnectHub TransactionType = "WALLET_CONNECT_HUB"

	// BuyRequest is the reverse of a P2P offer - a verified Trading Hub
	// posting a standing bid to buy crypto for real-world payment. See
	// core/buy_request.go's doc comment.
	TxBuyRequestCreate  TransactionType = "BUY_REQUEST_CREATE"
	TxBuyRequestFulfill TransactionType = "BUY_REQUEST_FULFILL"
	TxBuyRequestCancel  TransactionType = "BUY_REQUEST_CANCEL"

	// Wallet recovery is a testnet-only migration placeholder - see
	// core/recovery.go's doc comment for what it actually does (and
	// doesn't) recover.
	TxSetRecoveryEmail TransactionType = "SET_RECOVERY_EMAIL"
	TxRecoverWallet    TransactionType = "RECOVER_WALLET"

	// TxRequestContactEmailCode/TxConfirmContactEmail are a DIFFERENT,
	// deliberately separate mechanism from the wallet-recovery pair
	// just above, despite writing into the same core.WalletRecovery
	// record's Email/EmailVerified fields - see core/recovery.go's
	// SetPendingEmail/ConfirmPendingEmail doc comments. This exists
	// purely so a wallet has a verified notification address (already
	// consumed by api/validator_application.go, api/p2p_trade_start_notify.go,
	// api/p2p_dispute_notify.go), NOT to authorize account recovery -
	// that capability stays fully retired (api/recovery.go's
	// walletRecoveryDiscontinuedMessage, still returned unconditionally
	// by every wallet-recovery handler). Found as a real gap: retiring
	// recovery accidentally also retired the ONLY way to ever get
	// EmailVerified=true, silently starving all three notification
	// features for every wallet created after the cutover, since they
	// had no other path to a verified email at all.
	TxRequestContactEmailCode TransactionType = "REQUEST_CONTACT_EMAIL_CODE"
	TxConfirmContactEmail     TransactionType = "CONFIRM_CONTACT_EMAIL"

	// TxHDWalletMigrate moves a pre-Multi-Chain-HD-Wallet legacy
	// address's balances/reputation/loans to a new HD-derived address,
	// signed by the OLD key (unlike TxRecoverWallet, which exists for
	// when the old key is lost) - see core/recovery.go's MarkMigrated
	// and api/apply.go's migrateWalletAssets, both reused here.
	TxHDWalletMigrate TransactionType = "HD_WALLET_MIGRATE"

	// A HubRate is a verified Trading Hub's standing rate quote for an
	// in-app token against an off-app currency - see core/hub_rate.go's
	// doc comment. TxHubRateSet posts/updates it; TxHubTradeRequest is
	// a regular user instantiating an actual trade against that quote
	// for whatever amount they choose (capped by real-time balance, not
	// a pre-fixed listing amount). The resulting trade is an ordinary
	// EXTERNAL-mode P2POffer from that point on, so it settles through
	// the exact same TxP2PSellerConfirmPaid/TxP2PBuyerConfirmPaid/
	// TxP2PModeratorResolve machinery as any other hub-created offer or
	// fulfilled buy request - no new settlement state machine needed.
	TxHubRateSet      TransactionType = "HUB_RATE_SET"
	TxHubTradeRequest TransactionType = "HUB_TRADE_REQUEST"

	// TxHubRulesSet posts/updates a hub's preset trading rules text -
	// see core/hub_rules.go's doc comment. A real block-confirmed
	// transaction (not a node-local mutation like chat messages),
	// consistent with every other hub-configuration change (e.g.
	// TxHubRateSet above), so it replicates identically across nodes.
	TxHubRulesSet TransactionType = "HUB_RULES_SET"

	// A hub worker is a wallet the hub itself has delegated day-to-day
	// pricing to - see core/hub_worker.go's doc comment. Only the hub's
	// own signature can add or remove one; a worker can never act on
	// this pair itself.
	TxHubWorkerAdd    TransactionType = "HUB_WORKER_ADD"
	TxHubWorkerRemove TransactionType = "HUB_WORKER_REMOVE"

	// TxHubStorefrontSet posts/replaces a hub's custom front-page
	// design - see core/hub_storefront.go's doc comment.
	TxHubStorefrontSet TransactionType = "HUB_STOREFRONT_SET"

	// TxTokenMint is the operator-only testnet faucet (see
	// api/token_mint.go's HandleTokenMint) - a system/network action
	// like TxSlashReport, not a wallet action, but routed through the
	// same Transaction/gossip/ApplyBlock machinery as everything else
	// so the mint actually replicates to every node instead of only
	// ever taking effect on whichever single node received the HTTP
	// request (the bug this type fixes - a token minted only on the
	// node an admin happened to call silently diverges every other
	// node's derived state, like pool reserves, the next time a real
	// transaction depends on that balance).
	TxTokenMint TransactionType = "TOKEN_MINT"

	// TxAdminTransfer moves a specific amount of an address's OWN
	// already-existing balance to another address, admin-triggered
	// rather than signed by the sending address - node-authored via
	// selfSignedEnvelope, same trust model as TxTokenMint. Deliberately
	// narrower than TxTokenMint (which can conjure new supply at any
	// address): this only ever moves value an address already holds,
	// never creates any. Built specifically as an emergency recovery
	// tool for the case a validator's private key is permanently
	// unreachable but its address still holds real balance that needs
	// to migrate to a new, key-controlled address - see
	// api/admin_transfer.go's doc comment.
	TxAdminTransfer TransactionType = "ADMIN_TRANSFER"

	// TxAdminNormalizeCcc1Balance is a narrow, one-off migration action
	// for exactly one situation: an ETH_CCC1/USDC_CCC1 balance minted
	// under an un-normalized (mixed-case) address key, by code that
	// predates api/vault_fee.go's/api/admin_transfer.go's own
	// normalizeEthAddress fixes - the balance is real and on the
	// ledger, but permanently unreachable through every current
	// normalized-address code path (HandleVaultBalance,
	// resolveBalanceAddress, and TxAdminTransfer itself once it started
	// normalizing). Unlike TxAdminTransfer, this deliberately does NOT
	// normalize its source address before reading - that's the whole
	// point, to reach the exact un-normalized legacy key - then moves
	// the full balance to that same address's properly normalized key.
	// Idempotent: a second run against an already-zeroed legacy key
	// just moves zero, a safe no-op.
	TxAdminNormalizeCcc1Balance TransactionType = "ADMIN_NORMALIZE_CCC1_BALANCE"

	// TxAdminRefundStuckVaultBurn is a narrow, incident-specific admin
	// action (see api/admin_refund_stuck_vault_burn.go's doc comment):
	// refunds a burner's CCC1 balance for a vault release approval that
	// is STILL PENDING (never actually executed on-chain) - the real
	// backing value never left the deployed vault contract, so
	// restoring the burner's off-chain claim on it doesn't create
	// unbacked supply. Only ever reachable for a still-PENDING
	// approval; refuses outright for one already RELEASED (the real
	// value genuinely did leave the vault in that case, so "refunding"
	// it too would double-spend).
	TxAdminRefundStuckVaultBurn TransactionType = "ADMIN_REFUND_STUCK_VAULT_BURN"

	// TxTreasuryPurchaseRequest is a buyer signaling intent to buy CCT
	// from the Foundation Treasury at the current sale price - see
	// core.TreasuryStore's doc comment. No funds move yet; this only
	// records the request and snapshots the price, so a later admin
	// price change can't retroactively change what this buyer owes.
	TxTreasuryPurchaseRequest TransactionType = "TREASURY_PURCHASE_REQUEST"

	// TxTreasuryPurchaseConfirm is an admin-only attestation that the
	// buyer's off-app payment was received - node-authored via
	// selfSignedEnvelope, same trust model as TxTokenMint/
	// TxSlashReport (ADMIN_TOKEN already checked at the HTTP layer;
	// other nodes trust this node's attestation). Triggers the actual
	// CCT transfer from the treasury address to the buyer at apply
	// time.
	TxTreasuryPurchaseConfirm TransactionType = "TREASURY_PURCHASE_CONFIRM"

	// TxTreasurySetSalePrice/TxTreasurySetPaused/TxTreasurySetAddress
	// are admin-only Treasury/Sale configuration changes - same
	// node-authored trust model as TxTreasuryPurchaseConfirm.
	TxTreasurySetSalePrice TransactionType = "TREASURY_SET_SALE_PRICE"
	TxTreasurySetPaused    TransactionType = "TREASURY_SET_PAUSED"
	TxTreasurySetAddress   TransactionType = "TREASURY_SET_ADDRESS"

	// TxTreasuryPurchaseSendInstructions is an admin providing payment
	// instructions (bank details / crypto address / wallet handle,
	// depending on core.PurchaseRequest.Method) for one specific
	// pending purchase - admin-only, same trust model as
	// TxTreasuryPurchaseConfirm. Generates a random confirmation token
	// the buyer's emailed link carries, so the buyer's own later "I've
	// paid" click (TxTreasuryPurchaseBuyerMarkedPaid) can prove it's
	// tied to this exact instructions round rather than being forgeable
	// from just the purchase ID.
	TxTreasuryPurchaseSendInstructions TransactionType = "TREASURY_PURCHASE_SEND_INSTRUCTIONS"

	// TxTreasuryPurchaseBuyerMarkedPaid is the buyer clicking the
	// "I've paid" link in their instructions email - node-authored via
	// selfSignedEnvelope (see api/node_identity.go), same trust model
	// as TxSlashReport/TxBridgeDeposit, since a buyer reading email has
	// no wallet-signing capability in that context. This does NOT move
	// any funds by itself and is not a security boundary - it only
	// flags the purchase for the admin's attention; the admin's own
	// TxTreasuryPurchaseConfirm is still what actually releases CCT,
	// exactly as before this transaction type existed.
	TxTreasuryPurchaseBuyerMarkedPaid TransactionType = "TREASURY_PURCHASE_BUYER_MARKED_PAID"

	// TxTreasuryPurchaseMarkPaidByBuyer is the in-app "I Have Paid"
	// button's counterpart to TxTreasuryPurchaseBuyerMarkedPaid - a real
	// wallet-signed transaction (the buyer is in the app, not reading
	// email), verified against the purchase's own BuyerAddress at apply
	// time. See core.TreasuryStore.MarkBuyerPaidBySignedRequest's doc
	// comment for why this is allowed from a wider set of source states
	// than the token-gated version. Same non-security-boundary caveat -
	// still just a flag for the admin's attention, not proof of payment.
	TxTreasuryPurchaseMarkPaidByBuyer TransactionType = "TREASURY_PURCHASE_MARK_PAID_BY_BUYER"

	// TxTreasurySetBankAccount is an admin-only preset receiving-account
	// config for one currency (see core.BankAccountDetails) - shown
	// immediately to every bank-transfer buyer who picks that currency,
	// with no per-request admin action needed. Same node-authored trust
	// model as TxTreasurySetSalePrice/TxTreasurySetAddress.
	TxTreasurySetBankAccount TransactionType = "TREASURY_SET_BANK_ACCOUNT"

	// TxAnnouncementCreate is an admin-only site-wide announcement post
	// - same node-authored, self-signed trust model as
	// TxTreasurySetSalePrice (ADMIN_TOKEN already checked at the HTTP
	// layer - see api/announcements.go).
	TxAnnouncementCreate TransactionType = "ANNOUNCEMENT_CREATE"

	// TxAdminSetParam is an admin-only direct override of one
	// GovernanceParamsStore key (see core/governance_params.go) -
	// same node-authored, self-signed trust model as
	// TxTreasurySetSalePrice, restricted to a small explicit key
	// allowlist (api/admin_params.go) so this can never become an
	// arbitrary-key setter. A second, slower writer alongside the
	// existing community PARAM_CHANGE governance-vote path - built
	// for the mainnet-transition need to bootstrap/tune real economic
	// constants (unbonding period, dormancy period, bridge deposit
	// caps) faster than a full proposal+vote cycle allows, especially
	// before any real voting power exists at genesis.
	TxAdminSetParam TransactionType = "ADMIN_SET_PARAM"

	// TxSetProtocolManifest commits every security-critical
	// configuration value (admin wallet/multisig, bridge validator
	// list/threshold, mainnet mode) into replicated consensus state -
	// see core.ProtocolManifest's doc comment. Closes audit findings
	// C-09 (these previously lived only as per-node environment
	// variables with no cross-node consistency guarantee) and C-06
	// (this manifest is the single, explicit record of what's actually
	// active on mainnet). Same node-authored-by-admin trust model as
	// TxAdminSetParam - authorized by whichever admin mechanism
	// (single wallet or multisig) is currently configured, since that's
	// exactly what bootstraps the FIRST manifest commit.
	TxSetProtocolManifest TransactionType = "SET_PROTOCOL_MANIFEST"

	// TxFairValueSwap converts between two real internal-ledger balances
	// (currently BTCB and YENE only - see api/swap_fair_value.go) at a
	// live-price-oracle-derived rate, NOT a floating AMM pool - real
	// bridged assets must not float against CCT (see the earlier,
	// deliberate decision to skip a cctEthbPool/cctBtcbPool). The
	// resulting ToAmount is computed once, deterministically, at the
	// signing wallet's request time and carried in the transaction data
	// so every node applies the identical amount rather than
	// re-querying a live, possibly-since-moved price at apply time -
	// same snapshot-then-replay discipline as the ERC-network
	// withdrawal's live-gas-price fee.
	TxFairValueSwap TransactionType = "FAIR_VALUE_SWAP"

	// TxAdminMultisigPropose/TxAdminMultisigApprove replace the
	// single-key ADMIN_WALLET_ADDRESS model with a real M-of-N
	// multisig - see api/admin_multisig.go. Propose creates a pending
	// core.AdminMultisigProposal wrapping one of the admin-only action
	// types above (TxTokenMint, TxGenesisPoolWithdraw, etc.) and counts
	// as the proposer's own approval; Approve adds another authorized
	// signer's approval to an existing proposal. Both are ordinary
	// wallet-signed transactions (tx.AuthAddress == the signer, checked
	// against the network's configured signer set), not node-authored -
	// unlike every other admin transaction type, there's no single
	// party whose word alone should ever be enough. The underlying
	// action only actually executes once approvals reach the configured
	// threshold (see maybeExecuteAdminMultisigProposal).
	TxAdminMultisigPropose TransactionType = "ADMIN_MULTISIG_PROPOSE"
	TxAdminMultisigApprove TransactionType = "ADMIN_MULTISIG_APPROVE"

	// TxRatingSubmit is a normal wallet-signed site rating (see
	// api/rating.go) - ordinary auth (tx.AuthAddress == tx.From), not
	// node-authored, since this is a real user opinion, not a system
	// fact.
	TxRatingSubmit TransactionType = "RATING_SUBMIT"

	// TxHubAutomationSubscribe/TxHubWorkerSetAutomation are the scoped
	// hub-automation feature (see api/hub_automation.go's doc comment)
	// - both hub-only, ordinary wallet-signed auth just like
	// TxHubWorkerAdd/TxHubRulesSet.
	TxHubAutomationSubscribe TransactionType = "HUB_AUTOMATION_SUBSCRIBE"
	TxHubWorkerSetAutomation TransactionType = "HUB_WORKER_SET_AUTOMATION"

	// TxValidatorApplicationCreate/Support/Confirm are the staged
	// validator-onboarding pipeline (see
	// api/validator_application.go's doc comment) - all ordinary
	// wallet-signed transactions like every other user-originated
	// action, so every node converges on identical application state.
	TxValidatorApplicationCreate  TransactionType = "VALIDATOR_APPLICATION_CREATE"
	TxValidatorApplicationSupport TransactionType = "VALIDATOR_APPLICATION_SUPPORT"
	TxValidatorApplicationConfirm TransactionType = "VALIDATOR_APPLICATION_CONFIRM"

	// TxGenesisPoolAllocation is PoSR's one-time admin-triggered mint
	// into the 5 named genesis pools (Foundation Treasury, Validator
	// Reward Pool, Ecosystem Development, Governance Treasury,
	// Community Incentives) - node-authored via selfSignedEnvelope,
	// same trust model as TxTokenMint/TxTreasuryPurchaseConfirm.
	// Guarded idempotent by core.Treasury.GenesisPoolsAllocated (see
	// api/apply.go's applyGenesisPoolAllocation) so it can only ever
	// fire once, since this chain has been running for a long time
	// already and has no true "genesis" moment to hook into.
	TxGenesisPoolAllocation TransactionType = "GENESIS_POOL_ALLOCATION"

	// TxTokenomicsDecimalMigration is the one-time admin-triggered
	// conversion of every CCT balance from the pre-migration whole-unit
	// uint64 ledger (TokenStore) into the 18-decimal big-int ledger
	// (CctStore) - node-authored via selfSignedEnvelope, same trust
	// model as TxGenesisPoolAllocation. Guarded idempotent by
	// core.Treasury.TokenomicsDecimalMigrationApplied so it can only
	// ever fire once - see api/tokenomics_migration.go.
	TxTokenomicsDecimalMigration TransactionType = "TOKENOMICS_DECIMAL_MIGRATION"

	// TxGenesisPoolWithdraw moves funds out of one of the 4 genesis
	// pools that have no controlling keypair of their own (Validator
	// Reward, Ecosystem Development, Governance Treasury, Community
	// Incentives - NOT Foundation Treasury, which already has its own
	// real address and its own admin controls) - see
	// api/genesis_pool_withdraw.go. Admin-key-gated for now (verified
	// via verifyAdminTxAuth, same as every other admin-only
	// transaction type in api/apply.go); a real governance-vote-gated
	// disbursement mechanism for these pools is disclosed future work
	// in audit/KNOWN_GAPS.md, not attempted here.
	TxGenesisPoolWithdraw TransactionType = "GENESIS_POOL_WITHDRAW"

	// TxKYCSubmit is a self-attested KYC submission gating access to
	// the peer-lending marketplace (Lend/Marketplace/Your Negotiations)
	// - see core/kyc.go's doc comment for what this is (and very much
	// isn't - no real document verification) a guarantee of.
	TxKYCSubmit TransactionType = "KYC_SUBMIT"

	// The CCT order book (see core/order_book.go's doc comment) - real
	// bid/ask limit orders matched by price-time priority, replacing
	// the AMM's reserve-ratio pricing for CCT-vs-ETHTEST/USDCTEST.
	// TxOrderPlace escrows funds and attempts an immediate match (any
	// unfilled remainder rests in the book); TxOrderCancel releases
	// whatever's left of a still-open order's escrow back to its owner.
	TxOrderPlace  TransactionType = "ORDER_PLACE"
	TxOrderCancel TransactionType = "ORDER_CANCEL"

	// TxLoanAddCollateral lets a borrower top up an existing FUNDED
	// loan's earmarked collateral (see core.Loan.CollateralAmount's doc
	// comment - it's fixed at draw time and tied to one specific loan,
	// not a shared wallet-level pool, so "add more collateral" needs
	// its own transaction rather than just staking more). Releases from
	// the borrower's already-locked collateral pool (see
	// api/apply.go's reserveCollateralForLoan) into that specific
	// loan's CollateralAmount - see api/loan_collateral.go.
	TxLoanAddCollateral TransactionType = "LOAN_ADD_COLLATERAL"

	// TxLoanRedeemShortfall lets a borrower pay off exactly
	// Loan.LiquidatedShortfall during the Final Redemption window
	// (RiskStage==AwaitingFinalRedemption) to keep their Mining Power -
	// deliberately NOT TxRepay, which computes a full repayAmountFor(loan)
	// assuming collateral is still intact; here collateral is already
	// gone, only the frozen shortfall is still owed. See
	// api/loan_redemption.go.
	TxLoanRedeemShortfall TransactionType = "LOAN_REDEEM_SHORTFALL"

	// TxYeneClaim is a time-cooldown testnet claim of YEN-e (see
	// api/yene_claim.go) - a stand-in for the real ETH-side YEN-e
	// bridge, which has no live counterpart to bridge from yet. Mints a
	// deterministic pseudo-random amount in [1,100] once every 5 days
	// per address, same "no Amount trusted from the client" shape as
	// TxFaucetClaim.
	TxYeneClaim TransactionType = "YENE_CLAIM"

	// TxProposalPromote burns RC (REPT) from the promoting wallet's own
	// balance to extend a still-open Proposal's PromotedUntil, keeping
	// it sorted near the top of governance.html's Voting Ballots list -
	// see api/rc_promote.go. A pure burn, distinct from TxRCInfuse below
	// (which mints the same amount into the governance RC pool instead).
	TxProposalPromote TransactionType = "PROPOSAL_PROMOTE"

	// TxRCInfuse burns RC from the sender and mints the identical amount
	// into the governance RC pool, recording a self-attested
	// TrustReason - see core.RCInfusion and api/rc_infuse.go.
	TxRCInfuse TransactionType = "RC_INFUSE"

	// TxExecutiveProposalCreate/TxExecutiveProposalVote are the
	// executive-only voting lane (core.ExecutiveProposal) that decides
	// RealWorldTrustWeightBps - see api/executive_proposal.go. Gated to
	// isExecutive (api/rc_executive.go) at both submission and apply
	// time, since the top-N reputation committee can shift between the
	// two.
	TxExecutiveProposalCreate TransactionType = "EXECUTIVE_PROPOSAL_CREATE"
	TxExecutiveProposalVote   TransactionType = "EXECUTIVE_PROPOSAL_VOTE"

	// TxPredictionVote is a PREDICTION-kind proposal's combined
	// vote-plus-optional-RC-stake action - see api/prediction_vote.go.
	// Registers the ordinary Yes/No headcount vote every proposal
	// already has, and (only if RCAmount > 0) burns RC into that
	// proposal's PredictionPool (core/prediction.go) as a real mini-bet.
	TxPredictionVote TransactionType = "PREDICTION_VOTE"

	// TxBridgeValidatorRegister/TxVaultLockAttest/TxVaultBurn/
	// TxVaultReleaseApprove are the lock-and-mint vault bridge's
	// authorization layer (see core/bridge_validator.go,
	// core/vault_lock_attestation.go, core/vault_release_approval.go,
	// api/vault_lock.go) - all ordinary wallet-signed transactions
	// (tx.AuthAddress == tx.From), NOT the self-signed-node-identity
	// pattern TxBridgeDeposit/TxBridgeBtcDeposit use, because here
	// several INDEPENDENTLY ATTRIBUTABLE bridge validators must each be
	// individually counted toward a real M-of-N threshold - the same
	// address-keyed-approval shape TxAdminMultisigApprove already
	// established above, just for a separate, purpose-built signer set
	// (see BRIDGE_VALIDATOR_ADDRESSES in api/bridge_validator.go).

	// TxBridgeValidatorRegister lets a pre-authorized address declare
	// the real external-chain keys (ETH signing address, BTC public
	// key) it will use to attest lock events and co-sign vault
	// releases - core.BridgeValidatorStore.
	TxBridgeValidatorRegister TransactionType = "BRIDGE_VALIDATOR_REGISTER"

	// TxVaultLockAttest is one bridge validator's independent
	// confirmation that they personally observed a real on-chain vault
	// lock (at the required confirmation depth) - core.
	// VaultLockAttestationStore. Once enough distinct validators attest
	// to the same external tx, the corresponding CCC1 token mints
	// exactly once (see applyVaultLockAttest).
	TxVaultLockAttest TransactionType = "VAULT_LOCK_ATTEST"

	// TxVaultBurn is a user burning their own CCC1 balance to request
	// release of the real locked asset back to an external address -
	// ordinary wallet-signed burn, opens a core.VaultReleaseApproval.
	TxVaultBurn TransactionType = "VAULT_BURN"

	// TxVaultReleaseApprove carries one bridge validator's OWN real
	// external-chain signature (ECDSA, ETH or BTC) over the canonical
	// release payload, gossiped/consensus-replicated inside the
	// transaction itself so threshold-counting and real-signature-
	// collection happen through the same stream - see core.
	// VaultReleaseApprovalStore.
	TxVaultReleaseApprove TransactionType = "VAULT_RELEASE_APPROVE"

	// TxVaultGasPriceOracleUpdate is a narrow, single-purpose,
	// bounded auto-update of the VaultReleaseGasPriceWei governance
	// parameter - submitted only by this node's own background loop
	// (api/vault_gas_price_oracle.go's startVaultGasPriceOracleLoop),
	// signed by a SEPARATE dedicated key
	// (VAULT_GAS_PRICE_ORACLE_PRIVATE_KEY_HEX) distinct from both the
	// admin multisig signers and the bridge-validator set - this
	// deliberately does NOT reuse TxAdminSetParam's authorization path
	// (which would otherwise hand an automated key the power to set
	// ANY admin-settable parameter, not just this one gas price).
	// applyVaultGasPriceOracleUpdate re-derives and enforces the actual
	// bound (clampVaultGasPriceOracleUpdate) and a minimum interval
	// since the last update at APPLY time, never trusting the value or
	// timing this transaction merely claims - the real security
	// boundary against a leaked oracle key (or a malfunctioning oracle
	// job) is that even total control of this one key can only ever
	// nudge this one value, within a bounded step, at a bounded rate.
	TxVaultGasPriceOracleUpdate TransactionType = "VAULT_GAS_PRICE_ORACLE_UPDATE"
)

type Transaction struct {
	ID string

	From string
	To   string

	Amount uint64

	Type TransactionType

	Timestamp int64

	Data string

	// Auth carries the original signed-request proof this transaction
	// was built from - a self-contained copy of the SignedEnvelope
	// (see request_auth.go) that authorized it. Without this, a
	// transaction inside a block is just trusted data: whichever node
	// produced the block controls its own mempool directly and could
	// otherwise insert an arbitrary forged transaction (fake stake,
	// transfer, unstake, claiming any From address) that every other
	// node would apply with no way to detect the forgery, since a
	// block's own signature only proves who produced the block, not
	// that its contents are individually authentic. With Auth
	// populated, ANY node - not just the one that first received the
	// HTTP request - can independently re-verify the transaction via
	// VerifyTransactionAuth before applying it. AuthAddress is the
	// actual authenticated actor, which is not always From/To (e.g. a
	// TxBorrow's authenticated actor is the borrower, stored in To,
	// since From is left empty to represent the lending pool).
	AuthAddress   string          `json:"authAddress"`
	AuthPublicKey string          `json:"authPublicKey"`
	AuthTimestamp int64           `json:"authTimestamp"`
	AuthPayload   json.RawMessage `json:"authPayload"`
	AuthSignature string          `json:"authSignature"`
}

// newTxID derives a unique id from the transaction's own content plus
// a random nonce, so gossiped/replayed copies of the same logical
// transaction can be recognized and de-duplicated by every node.
func newTxID(from, to string, amount uint64, txType TransactionType, timestamp int64, data string) string {
	nonce := make([]byte, 8)
	_, _ = rand.Read(nonce)

	sum := sha256.Sum256([]byte(fmt.Sprintf(
		"%s|%s|%d|%s|%d|%s|%s",
		from, to, amount, txType, timestamp, data, hex.EncodeToString(nonce),
	)))

	return hex.EncodeToString(sum[:16])
}

// NewTransaction builds an unauthenticated transaction skeleton - see
// NewAuthenticatedTransaction for the version that also embeds the
// proof every mutating action actually needs. This bare constructor
// still exists for transaction types with no external signer (block
// rewards are applied directly in api/apply.go's applyBlockReward,
// never go through here at all).
func NewTransaction(
	from string,
	to string,
	amount uint64,
	txType TransactionType,
	data string,
) Transaction {

	timestamp := time.Now().Unix()

	return Transaction{
		ID:        newTxID(from, to, amount, txType, timestamp, data),
		From:      from,
		To:        to,
		Amount:    amount,
		Type:      txType,
		Timestamp: timestamp,
		Data:      data,
	}
}

// NewAuthenticatedTransaction builds a transaction and embeds the
// SignedEnvelope that authorized it, so any node can independently
// re-verify authenticity later via VerifyTransactionAuth - see
// Transaction.Auth* fields' doc comment for why this matters.
func NewAuthenticatedTransaction(
	auth SignedEnvelope,
	from string,
	to string,
	amount uint64,
	txType TransactionType,
	data string,
) Transaction {
	tx := NewTransaction(from, to, amount, txType, data)

	// Timestamp is overwritten to the exact value the signature covers
	// (auth.Timestamp) rather than left at NewTransaction's own
	// independent clock read - see H01FixCutoverUnixSeconds in
	// api/apply.go for why: Timestamp is a top-level Transaction field
	// with no per-type Data payload protecting it, so unless it's bound
	// to something the signature actually commits to, a block producer
	// could silently rewrite it after signing (core.ComputeBlockHash
	// only hashes tx.ID, never the full transaction content) with no
	// detectable effect on the block hash. AuthTimestamp is signed
	// (see SignedEnvelope.CanonicalMessage), so this makes Timestamp
	// exactly as tamper-evident as every other field verifyTxAuth
	// already re-derives and compares (see api/apply.go's doc comment
	// on that function for the identical Amount/To reasoning).
	tx.Timestamp = auth.Timestamp
	tx.AuthAddress = auth.Address
	tx.AuthPublicKey = auth.PublicKey
	tx.AuthTimestamp = auth.Timestamp
	tx.AuthPayload = auth.Payload
	tx.AuthSignature = auth.Signature

	return tx
}

// VerifyTransactionAuth independently confirms a transaction really
// was authorized by its AuthAddress's key-holder - the check every
// node applying this transaction runs, not just the one that
// originally received the HTTP request that created it. Uses
// VerifySignatureOnly (not Verify), since by the time another node
// applies this transaction it may be arbitrarily old - staleness was
// already the originating node's concern at submission time, not a
// re-application concern here.
func VerifyTransactionAuth(tx Transaction) error {
	if tx.AuthAddress == "" || tx.AuthSignature == "" {
		return errors.New("transaction has no authentication data")
	}

	env := SignedEnvelope{
		Address:   tx.AuthAddress,
		PublicKey: tx.AuthPublicKey,
		Timestamp: tx.AuthTimestamp,
		Payload:   tx.AuthPayload,
		Signature: tx.AuthSignature,
	}

	return env.VerifySignatureOnly()
}
