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

	// Moderators are reputation-vetted community members automatically
	// assigned to observe trade agreements (loan negotiations, P2P
	// offers) and attest on-chain whether they concluded successfully -
	// see core/moderator.go's doc comment for what this is (and isn't)
	// a guarantee of.
	TxModeratorRegister TransactionType = "MODERATOR_REGISTER"
	TxModeratorConfirm  TransactionType = "MODERATOR_CONFIRM"

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
