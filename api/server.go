package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/creditchain/creditchaind/core"
)

var blockchain *core.Blockchain
var config *core.Config
var mempool = core.Mempool{}

var creditEngine = core.NewCreditEngine(walletStore)

// recordTx appends a transaction to the shared mempool so real
// actions (stake, borrow, repay, vote, ...) leave a trail an explorer
// can show, instead of only mutating hidden server-side state. It
// gets swept into the next block a validator produces.
//
// auth is the SignedEnvelope that authorized this action - the one
// requireSignedRequest already verified for this HTTP request (or a
// self-signed one this node constructed itself, for node-authored
// transactions like slash reports - see consensus_loop.go's
// reportMissedBlock/peer.go's reportDoubleSign). Embedding it is what
// lets EVERY node that later applies this transaction (not just this
// one, which already trusts it because it just verified the HTTP
// request itself) independently re-confirm it was genuinely
// authorized, instead of just trusting whichever node produced the
// block it arrived in - see core.Transaction's Auth* fields.
func recordTx(
	auth core.SignedEnvelope,
	from string,
	to string,
	amount uint64,
	txType core.TransactionType,
	data string,
) core.Transaction {
	tx := core.NewAuthenticatedTransaction(auth, from, to, amount, txType, data)
	mempool.Add(tx)
	go gossipTx(tx)
	return tx
}

func enableCors(
	w *http.ResponseWriter,
) {
	(*w).Header().Set(
		"Access-Control-Allow-Origin",
		"*",
	)
}

// corsMiddleware wraps the default mux so every route - not just the
// ones that individually call enableCors - answers CORS preflight
// OPTIONS requests. Browsers send those ahead of any POST with a
// JSON body, and without Access-Control-Allow-Methods/Headers here
// the browser blocks the real request even though enableCors set
// Access-Control-Allow-Origin.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Admin-Token, X-Network-Secret")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		if !allowRequest(r.RemoteAddr) {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}

		bodyLimit := int64(maxRequestBodyBytes)
		if r.URL.Path == "/companies/upload-certificate" {
			bodyLimit = maxCertificateUploadRequestBytes
		}
		r.Body = http.MaxBytesReader(w, r.Body, bodyLimit)

		next.ServeHTTP(w, r)

		if r.Method == http.MethodPost {
			if err := SaveState(); err != nil {
				fmt.Println("state save failed:", err)
			}
		}
	})
}

func StartServer(port string) {
	cfg, err :=
		core.LoadConfig(
			dataPath("config.json"),
		)

	if err != nil {
		// Missing config.json is expected on a node that's never had
		// one written yet - a fresh default (matching the zero value
		// every field already effectively had, since this file has
		// apparently never existed on this node) is the same
		// graceful-fallback idiom LoadBlockchain already uses right
		// below. Any OTHER load error (malformed JSON, permissions)
		// still panics - that indicates real corruption, not just an
		// unprovisioned file.
		if os.IsNotExist(err) {
			cfg = &core.Config{}
		} else {
			panic(err)
		}
	}

	config = cfg

	loaded, err := core.LoadBlockchain(
		dataPath("blockchain.json"),
	)

	if err != nil {
		blockchain = core.NewBlockchain()
	} else {
		blockchain = loaded
	}

	LoadState()
	nodeAdmissionStore.SeedGrandfatheredNodeAdmissions(grandfatheredNodeAdmissions)
	InitDB()
	InitEthSigner()
	InitBtcVaultSigner()
	InitEthRPC()
	// startEthBridgeDepositLoop() is RETIRED as of this session - it was
	// deliberately kept running alongside the new lock-and-mint vault
	// (Phase E of the bridge-vault plan, wiggly-moseying-horizon.md -
	// see project memory "deploy all fix" 2026-08-02) only because the
	// vault wasn't actually functional on this deployment yet at the
	// time (no ETH_RPC_URL/VAULT_CONTRACT_ADDRESS/
	// RELAYER_PRIVATE_KEY_HEX/bridge validators configured) - that same
	// comment explicitly named "once the vault is actually configured
	// and proven" as the retirement trigger. The vault is now fully
	// configured AND proven on real mainnet (a real ETH deposit
	// correctly minted ETH_CCC1, and a real withdrawal correctly
	// released ETH, both confirmed on-chain this session). Kept
	// running past that point, this loop's own per-address polling (one
	// eth_getBalance call per registered address, every 30s - see
	// pollAndReportBridgeDeposit) was competing with the vault's own
	// RPC calls for the same provider quota and is the confirmed cause
	// of an Infura 429 rate-limit that took down the ETH custodial
	// corner AND the vault's own ETH watch/relay loops together.
	// startBtcBridgeDepositLoop() is UNCHANGED - BTC uses a separate,
	// unrelated RPC/API, wasn't implicated in this incident, and is left
	// as a follow-up retirement decision rather than bundled into this
	// urgent fix.
	startBtcBridgeDepositLoop()
	startPriceOracleLoop()
	loadNodeIdentity()
	loadPeersFromConfig()

	http.HandleFunc("/", func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		fmt.Fprintln(
			w,
			"Welcome to CreditChain API",
		)
	})
	http.HandleFunc(
		"/node",
		HandleNode,
	)

	http.HandleFunc("/health", func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		enableCors(&w)

		json.NewEncoder(w).Encode(
			map[string]string{
				"status":  "ok",
				"network": "CreditChain",
			},
		)
	})
	http.HandleFunc(
		"/network",
		HandleNetwork,
	)

	http.HandleFunc(
		"/wallet",
		HandleWallet,
	)

	http.HandleFunc(
		"/reputation",
		HandleReputation,
	)
	http.HandleFunc(
		"/reputation/event",
		HandleGetReputationEvent,
	)
	http.HandleFunc(
		"/reputation/math",
		HandleReputationMath,
	)

	http.HandleFunc(
		"/staking",
		HandleStaking,
	)
	http.HandleFunc(
		"/credit/history",
		HandleCreditHistory,
	)

	http.HandleFunc(
		"/borrow",
		HandleBorrow,
	)
	http.HandleFunc(
		"/lender/stats",
		HandleLenderStats,
	)

	http.HandleFunc(
		"/explorer",
		HandleExplorer,
	)
	http.HandleFunc(
		"/explorer/search",
		HandleExplorerSearch,
	)
	http.HandleFunc(
		"/blocks",
		func(w http.ResponseWriter, r *http.Request) {
			enableCors(&w)

			json.NewEncoder(w).Encode(
				blockchain.Blocks,
			)
		},
	)
	http.HandleFunc(
		"/stake",
		HandleStake,
	)
	http.HandleFunc(
		"/unstake",
		HandleUnstake,
	)
	http.HandleFunc(
		"/repay",
		HandleRepay,
	)
	http.HandleFunc(
		"/tx/submit",
		HandleSubmitTx,
	)

	http.HandleFunc(
		"/mempool",
		func(w http.ResponseWriter, r *http.Request) {
			enableCors(&w)

			json.NewEncoder(w).Encode(
				mempool.Transactions,
			)
		},
	)

	http.HandleFunc(
		"/validators",
		HandleValidators,
	)
	http.HandleFunc(
		"/validators/register",
		HandleRegisterValidator,
	)
	http.HandleFunc(
		"/validators/produce-block",
		HandleProduceBlock,
	)
	http.HandleFunc(
		"/validators/delegate",
		HandleDelegateToValidator,
	)
	http.HandleFunc(
		"/validators/undelegate",
		HandleUndelegateFromValidator,
	)
	http.HandleFunc(
		"/validators/commission",
		HandleSetValidatorCommission,
	)
	http.HandleFunc(
		"/validators/delegations",
		HandleListDelegations,
	)

	http.HandleFunc(
		"/peers",
		HandlePeers,
	)

	http.HandleFunc(
		"/metrics",
		HandleMetrics,
	)
	http.HandleFunc(
		"/token/balance",
		HandleTokenBalance,
	)
	http.HandleFunc(
		"/usdctest/balance",
		HandleUSDCTestBalance,
	)
	http.HandleFunc(
		"/yene/balance",
		HandleYenBalance,
	)
	http.HandleFunc(
		"/rept/balance",
		HandleREPTBalance,
	)
	http.HandleFunc(
		"/tokens/register",
		HandleRegisterToken,
	)
	http.HandleFunc(
		"/tokens",
		HandleListTokens,
	)
	http.HandleFunc(
		"/tokens/balance",
		HandleCustomTokenBalance,
	)
	http.HandleFunc(
		"/token-standards",
		HandleTokenStandards,
	)
	http.HandleFunc(
		"/pool",
		HandlePoolInfo,
	)
	http.HandleFunc(
		"/pools",
		HandleListPools,
	)
	http.HandleFunc(
		"/pool/price-history",
		HandlePoolPriceHistory,
	)
	http.HandleFunc(
		"/pool/add-liquidity",
		HandlePoolAddLiquidity,
	)
	http.HandleFunc(
		"/pool/remove-liquidity",
		HandlePoolRemoveLiquidity,
	)
	http.HandleFunc(
		"/pool/swap",
		HandlePoolSwap,
	)
	http.HandleFunc(
		"/swap",
		HandleSwapViaCCT,
	)
	http.HandleFunc(
		"/ethtest/balance",
		HandleETHTestBalance,
	)
	http.HandleFunc(
		"/orders/place",
		HandlePlaceOrder,
	)
	http.HandleFunc(
		"/orders/cancel",
		HandleCancelOrder,
	)
	http.HandleFunc(
		"/orders",
		HandleListOrders,
	)
	http.HandleFunc(
		"/orders/mine",
		HandleMyOrders,
	)
	http.HandleFunc(
		"/trades",
		HandleListTrades,
	)
	http.HandleFunc(
		"/p2p/offers",
		HandleListP2POffers,
	)
	http.HandleFunc(
		"/p2p/offers/create",
		HandleCreateP2POffer,
	)
	http.HandleFunc(
		"/p2p/offers/accept",
		HandleAcceptP2POffer,
	)
	http.HandleFunc(
		"/p2p/offers/cancel",
		HandleCancelP2POffer,
	)
	http.HandleFunc(
		"/loans/offers",
		HandleListLoanOffers,
	)
	http.HandleFunc(
		"/loans/offers/create",
		HandleCreateLoanOffer,
	)
	http.HandleFunc(
		"/loans/offers/draw",
		HandleDrawLoanOffer,
	)
	http.HandleFunc(
		"/loans/offers/close",
		HandleCloseLoanOffer,
	)
	http.HandleFunc(
		"/loan-requests",
		HandleListLoanRequests,
	)
	http.HandleFunc(
		"/loan-requests/create",
		HandleCreateLoanRequest,
	)
	http.HandleFunc(
		"/loan-requests/cancel",
		HandleCancelLoanRequest,
	)
	http.HandleFunc(
		"/protocol/stats",
		HandleProtocolStats,
	)
	http.HandleFunc(
		"/borrowing-power",
		HandleBorrowingPower,
	)
	http.HandleFunc(
		"/mining-power",
		HandleMiningPower,
	)
	http.HandleFunc(
		"/lender/portfolio",
		HandleLenderPortfolio,
	)
	http.HandleFunc(
		"/negotiations",
		HandleListNegotiations,
	)
	http.HandleFunc(
		"/negotiations/start",
		HandleStartNegotiation,
	)
	http.HandleFunc(
		"/negotiations/message",
		HandleSendMessage,
	)
	http.HandleFunc(
		"/negotiations/confirm",
		HandleConfirmNegotiation,
	)
	http.HandleFunc(
		"/negotiations/cancel",
		HandleCancelNegotiation,
	)
	http.HandleFunc(
		"/moderators",
		HandleListModerators,
	)
	http.HandleFunc(
		"/moderators/register",
		HandleRegisterModerator,
	)
	http.HandleFunc(
		"/moderators/confirm",
		HandleConfirmTrade,
	)
	http.HandleFunc(
		"/trade-review/submit",
		HandleSubmitTradeReview,
	)
	http.HandleFunc(
		"/trade-review/for-address",
		HandleGetTradeReviewsForAddress,
	)
	http.HandleFunc(
		"/trade-review/status",
		HandleGetTradeReviewStatus,
	)
	http.HandleFunc(
		"/dispute-fault-choice/pay",
		HandleDisputeFaultChoicePay,
	)
	http.HandleFunc(
		"/dispute-fault-choice/decline",
		HandleDisputeFaultChoiceDecline,
	)
	http.HandleFunc(
		"/dispute-fault-choice/status",
		HandleGetDisputeFaultChoice,
	)
	http.HandleFunc(
		"/p2p/offers/accept-external",
		HandleAcceptExternalP2POffer,
	)
	http.HandleFunc(
		"/p2p/offers/seller-confirm",
		HandleP2PSellerConfirmPaid,
	)
	http.HandleFunc(
		"/p2p/offers/seller-dispute",
		HandleP2PSellerDispute,
	)
	http.HandleFunc(
		"/p2p/offers/buyer-dispute",
		HandleP2PBuyerDispute,
	)
	http.HandleFunc(
		"/p2p/offers/buyer-mark-paid",
		HandleP2PBuyerMarkPaid,
	)
	http.HandleFunc(
		"/p2p/offers/buyer-confirm",
		HandleP2PBuyerConfirmPaid,
	)
	http.HandleFunc(
		"/p2p/offers/resolve-dispute",
		HandleResolveP2PDispute,
	)
	http.HandleFunc(
		"/p2p/offers/message",
		HandleP2PMessage,
	)
	http.HandleFunc(
		"/kyc/submit",
		HandleKYCSubmit,
	)
	http.HandleFunc(
		"/kyc/status",
		HandleKYCStatus,
	)
	http.HandleFunc(
		"/companies",
		HandleListCompanies,
	)
	http.HandleFunc(
		"/companies/countries",
		HandleListHubCountries,
	)
	http.HandleFunc(
		"/companies/status",
		HandleCompanyStatus,
	)
	http.HandleFunc(
		"/companies/apply",
		HandleApplyCompany,
	)
	http.HandleFunc(
		"/companies/review",
		HandleReviewCompany,
	)
	http.HandleFunc(
		"/companies/upload-certificate",
		HandleUploadCertificate,
	)
	http.HandleFunc(
		"/companies/reassign-moderator",
		HandleReassignCompanyModerator,
	)
	http.HandleFunc(
		"/governance/moderator-verifications",
		HandleModeratorVerifications,
	)
	http.HandleFunc(
		"/companies/update-contact",
		HandleUpdateCompanyContact,
	)
	http.HandleFunc(
		"/wallet/connect-hub",
		HandleConnectHub,
	)
	http.HandleFunc(
		"/wallet/connected-hub",
		HandleGetConnectedHub,
	)
	http.HandleFunc(
		"/buy-requests",
		HandleListBuyRequests,
	)
	http.HandleFunc(
		"/buy-requests/create",
		HandleCreateBuyRequest,
	)
	http.HandleFunc(
		"/buy-requests/fulfill",
		HandleFulfillBuyRequest,
	)
	http.HandleFunc(
		"/buy-requests/cancel",
		HandleCancelBuyRequest,
	)
	http.HandleFunc(
		"/eth-custodial/create",
		HandleCreateEthCustodialAccount,
	)
	http.HandleFunc(
		"/eth-custodial/address",
		HandleEthCustodialAddress,
	)
	http.HandleFunc(
		"/eth-custodial/balance",
		HandleEthCustodialBalance,
	)
	http.HandleFunc(
		"/eth-custodial/withdraw",
		HandleEthCustodialWithdraw,
	)
	http.HandleFunc(
		"/eth-selfcustody/balance",
		HandleEthSelfCustodyBalance,
	)
	http.HandleFunc(
		"/eth-selfcustody/broadcast",
		HandleEthSelfCustodyBroadcast,
	)
	http.HandleFunc(
		"/erc20/usdc-balance",
		HandleUsdcSelfCustodyBalance,
	)
	http.HandleFunc(
		"/erc20/balance",
		HandleErc20SelfCustodyBalance,
	)
	http.HandleFunc(
		"/swap/fair-value",
		HandleFairValueSwap,
	)
	http.HandleFunc(
		"/ethb/balance",
		HandleEthbBalance,
	)
	http.HandleFunc(
		"/eth-bridge/register-address",
		HandleEthRegisterAddress,
	)
	http.HandleFunc(
		"/eth-bridge/address",
		HandleEthWatchedAddress,
	)
	http.HandleFunc(
		"/eth-bridge/withdraw",
		HandleBridgedEthWithdraw,
	)
	http.HandleFunc(
		"/eth-bridge/withdraw-quote",
		HandleBridgedEthWithdrawQuote,
	)
	http.HandleFunc(
		"/btc-bridge/register-address",
		HandleBtcRegisterAddress,
	)
	http.HandleFunc(
		"/btc-bridge/address",
		HandleBtcWatchedAddress,
	)
	http.HandleFunc(
		"/btc-bridge/utxos",
		HandleBtcUtxos,
	)
	http.HandleFunc(
		"/btcb/balance",
		HandleBtcbBalance,
	)
	http.HandleFunc(
		"/btc-bridge/fee-estimate",
		HandleBtcFeeEstimate,
	)
	http.HandleFunc(
		"/btc-bridge/broadcast",
		HandleBtcBroadcast,
	)
	http.HandleFunc(
		"/hub-rates",
		HandleListHubRates,
	)
	http.HandleFunc(
		"/hub-rates/set",
		HandleSetHubRate,
	)
	http.HandleFunc(
		"/hub-rates/currencies",
		HandleListOffAppCurrencies,
	)
	http.HandleFunc(
		"/sponsored-posts/create",
		HandleCreateSponsoredPost,
	)
	http.HandleFunc(
		"/sponsored-posts/review",
		HandleReviewSponsoredPost,
	)
	http.HandleFunc(
		"/sponsored-posts/assigned",
		HandleListAssignedSponsoredPosts,
	)
	http.HandleFunc(
		"/sponsored-posts/mine",
		HandleListMySponsoredPosts,
	)
	http.HandleFunc(
		"/sponsored-posts/feed",
		HandleSponsoredPostFeed,
	)
	http.HandleFunc(
		"/hub-rates/trade",
		HandleRequestHubTrade,
	)
	http.HandleFunc(
		"/hub-rates/rules",
		HandleGetHubRules,
	)
	http.HandleFunc(
		"/hub-rates/rules/set",
		HandleSetHubRules,
	)
	http.HandleFunc(
		"/hub-directory-filters",
		HandleListHubDirectoryFilters,
	)
	http.HandleFunc(
		"/hub/notifications",
		HandleListHubNotifications,
	)
	http.HandleFunc(
		"/hub/notifications/read",
		HandleMarkNotificationRead,
	)
	http.HandleFunc(
		"/notifications",
		HandleListUserNotifications,
	)
	http.HandleFunc(
		"/notifications/read",
		HandleMarkUserNotificationRead,
	)
	http.HandleFunc(
		"/session/login-ping",
		HandleLoginPing,
	)
	http.HandleFunc(
		"/ai/chat",
		HandleAIChat,
	)
	http.HandleFunc(
		"/hub-workers/add",
		HandleAddHubWorker,
	)
	http.HandleFunc(
		"/hub-workers/remove",
		HandleRemoveHubWorker,
	)
	http.HandleFunc(
		"/hub-workers",
		HandleListHubWorkers,
	)
	http.HandleFunc(
		"/hub-automation/subscribe",
		HandleHubAutomationSubscribe,
	)
	http.HandleFunc(
		"/hub-automation/status",
		HandleHubAutomationStatus,
	)
	http.HandleFunc(
		"/hub-workers/set-automation",
		HandleSetWorkerAutomation,
	)
	http.HandleFunc(
		"/validator-application/create",
		HandleCreateValidatorApplication,
	)
	http.HandleFunc(
		"/validator-application/support",
		HandleSupportValidatorApplication,
	)
	http.HandleFunc(
		"/validator-application/confirm",
		HandleConfirmValidatorApplication,
	)
	http.HandleFunc(
		"/validator-application",
		HandleGetValidatorApplication,
	)
	http.HandleFunc(
		"/validator-application/list",
		HandleListValidatorApplications,
	)
	http.HandleFunc(
		"/validator-application/my-votes",
		HandleMyValidatorVotes,
	)
	http.HandleFunc(
		"/hub-agents/apply",
		HandleApplyHubAgent,
	)
	http.HandleFunc(
		"/hub-agents/approve",
		HandleApproveHubAgent,
	)
	http.HandleFunc(
		"/hub-agents/reject",
		HandleRejectHubAgent,
	)
	http.HandleFunc(
		"/hub-agents",
		HandleListHubAgentApplications,
	)
	http.HandleFunc(
		"/validator/join-request/submit",
		HandleSubmitValidatorJoinRequest,
	)
	http.HandleFunc(
		"/validator/join-requests",
		HandleListValidatorJoinRequests,
	)
	http.HandleFunc(
		"/validator/join-request/mark-handled",
		HandleMarkValidatorJoinRequestHandled,
	)
	http.HandleFunc(
		"/hub-storefront/set",
		HandleSetHubStorefront,
	)
	http.HandleFunc(
		"/hub-storefront",
		HandleGetHubStorefront,
	)
	http.HandleFunc(
		"/recovery/request-email-code",
		HandleRequestEmailCode,
	)
	http.HandleFunc(
		"/recovery/confirm-email",
		HandleConfirmEmail,
	)
	http.HandleFunc(
		"/recovery/request-recovery-code",
		HandleRequestRecoveryCode,
	)
	http.HandleFunc(
		"/recovery/recover",
		HandleRecoverWallet,
	)
	// Contact-email verification (api/contact_email.go) - a separate,
	// still-live mechanism from the retired recovery endpoints just
	// above, despite sharing the same underlying store. See that
	// file's own doc comment for why this exists.
	http.HandleFunc(
		"/contact-email/request-code",
		HandleRequestContactEmailCode,
	)
	http.HandleFunc(
		"/contact-email/confirm",
		HandleConfirmContactEmail,
	)
	http.HandleFunc(
		"/contact-email/status",
		HandleGetContactEmailStatus,
	)
	http.HandleFunc(
		"/wallet/hd-migrate",
		HandleHDWalletMigrate,
	)
	http.HandleFunc(
		"/health/sync",
		HandleHealth,
	)
	http.HandleFunc(
		"/credit",
		HandleCredit,
	)

	http.HandleFunc(
		"/token/mint",
		HandleTokenMint,
	)
	http.HandleFunc(
		"/treasury/info",
		HandleTreasuryInfo,
	)
	http.HandleFunc(
		"/treasury/purchase/create",
		HandleCreatePurchaseRequest,
	)
	http.HandleFunc(
		"/admin/treasury/purchases",
		HandleAdminListPurchases,
	)
	http.HandleFunc(
		"/admin/treasury/purchase/confirm",
		HandleAdminConfirmPurchase,
	)
	http.HandleFunc(
		"/admin/treasury/purchase/send-instructions",
		HandleAdminSendPurchaseInstructions,
	)
	http.HandleFunc(
		"/treasury/purchase/confirm-payment",
		HandleBuyerConfirmPurchasePayment,
	)
	http.HandleFunc(
		"/admin/treasury/sale-price",
		HandleAdminSetSalePrice,
	)
	http.HandleFunc(
		"/admin/treasury/paused",
		HandleAdminSetPaused,
	)
	http.HandleFunc(
		"/admin/treasury/address",
		HandleAdminSetTreasuryAddress,
	)
	http.HandleFunc(
		"/treasury/bank-accounts",
		HandleListBankAccounts,
	)
	http.HandleFunc(
		"/admin/treasury/bank-account",
		HandleAdminSetBankAccount,
	)
	http.HandleFunc(
		"/treasury/purchase/mark-paid",
		HandleTreasuryMarkPaidByBuyer,
	)
	http.HandleFunc(
		"/admin/params/set",
		HandleAdminSetParam,
	)
	http.HandleFunc(
		"/admin/params",
		HandleGetAdminParams,
	)
	http.HandleFunc(
		"/admin/bridge/excess-deposits",
		HandleAdminBridgeExcessDeposits,
	)
	http.HandleFunc(
		"/admin/aws-credentials/set",
		HandleAdminSetAWSCredentials,
	)
	http.HandleFunc(
		"/admin/protocol-manifest/set",
		HandleSetProtocolManifest,
	)
	http.HandleFunc(
		"/admin/whoami",
		HandleAdminWhoAmI,
	)
	http.HandleFunc(
		"/admin/genesis-pools/allocate",
		HandleAllocateGenesisPools,
	)
	http.HandleFunc(
		"/admin/genesis-pools/withdraw",
		HandleGenesisPoolWithdraw,
	)
	http.HandleFunc(
		"/admin/tokenomics/migrate-decimals",
		HandleTokenomicsDecimalMigration,
	)
	http.HandleFunc(
		"/admin/token/transfer",
		HandleAdminTransfer,
	)
	http.HandleFunc(
		"/admin/token/normalize-ccc1-balance",
		HandleAdminNormalizeCcc1Balance,
	)
	http.HandleFunc(
		"/admin/vault/sweep-signing-key-eth",
		HandleSweepVaultReleaseSigningKeyEth,
	)
	http.HandleFunc(
		"/admin/vault/refund-stuck-burn",
		HandleAdminRefundStuckVaultBurn,
	)
	http.HandleFunc(
		"/admin/smtp/set",
		HandleAdminSetSmtpConfig,
	)
	http.HandleFunc(
		"/admin/smtp/status",
		HandleAdminGetSmtpStatus,
	)
	http.HandleFunc(
		"/hub-kyc/submit",
		HandleSubmitHubKYC,
	)
	http.HandleFunc(
		"/hub-kyc/list",
		HandleListHubKYC,
	)
	http.HandleFunc(
		"/hub-kyc/status",
		HandleHubKYCStatus,
	)
	http.HandleFunc(
		"/admin/multisig/proposals",
		HandleAdminMultisigProposals,
	)
	http.HandleFunc(
		"/admin/multisig/approve",
		HandleAdminMultisigApprove,
	)
	http.HandleFunc(
		"/faucet",
		HandleFaucet,
	)
	http.HandleFunc(
		"/yene/claim",
		HandleYeneClaim,
	)
	http.HandleFunc(
		"/yene/claim-status",
		HandleYeneClaimStatus,
	)
	http.HandleFunc(
		"/marketplace",
		HandleMarketplace,
	)

	http.HandleFunc(
		"/token/transfer",
		HandleTokenTransfer,
	)

	http.HandleFunc(
		"/token/supply",
		HandleTokenSupply,
	)
	http.HandleFunc(
		"/collateral/lock",
		HandleLockCollateral,
	)
	http.HandleFunc(
		"/collateral",
		HandleCollateral,
	)
	http.HandleFunc(
		"/loans/add-collateral",
		HandleLoanAddCollateral,
	)
	http.HandleFunc(
		"/loans/redeem-shortfall",
		HandleLoanRedeemShortfall,
	)
	http.HandleFunc(
		"/loans/health",
		HandleLoanHealth,
	)
	http.HandleFunc(
		"/wallet/create",
		HandleCreateWallet,
	)
	http.HandleFunc(
		"/chain",
		HandleChain,
	)

	http.HandleFunc(
		"/mine",
		HandleMine,
	)

	http.HandleFunc(
		"/loans",
		HandleGetLoans,
	)

	http.HandleFunc(
		"/loans/fund",
		HandleFundLoan,
	)

	http.HandleFunc(
		"/loans/repay",
		HandleRepayLoan,
	)

	http.HandleFunc(
		"/governance",
		HandleGovernance,
	)

	http.HandleFunc(
		"/governance/create",
		HandleCreateProposal,
	)

	http.HandleFunc(
		"/governance/vote",
		HandleVoteProposal,
	)

	http.HandleFunc(
		"/governance/activate",
		HandleActivateGovernance,
	)

	http.HandleFunc(
		"/governance/status/post",
		HandlePostGovernanceStatus,
	)

	http.HandleFunc(
		"/governance/status/recent",
		HandleRecentGovernanceStatuses,
	)

	http.HandleFunc(
		"/governance/my-weight",
		HandleGovernanceMyWeight,
	)

	http.HandleFunc(
		"/governance/status/user",
		HandleGovernanceStatusForUser,
	)

	http.HandleFunc(
		"/governance/votes",
		HandleGovernanceVotesForUser,
	)

	http.HandleFunc(
		"/referrals",
		HandleReferrals,
	)

	http.HandleFunc(
		"/announcements/create",
		HandleCreateAnnouncement,
	)

	http.HandleFunc(
		"/announcements/recent",
		HandleListAnnouncements,
	)

	http.HandleFunc(
		"/ratings/submit",
		HandleSubmitRating,
	)

	http.HandleFunc(
		"/ratings/mine",
		HandleMyRating,
	)

	http.HandleFunc(
		"/ratings/recent",
		HandleRecentRatings,
	)

	http.HandleFunc(
		"/price/external",
		HandleExternalPrice,
	)

	http.HandleFunc(
		"/price/currencies",
		HandleSupportedCurrencies,
	)

	http.HandleFunc(
		"/price/cct",
		HandleCctPrice,
	)

	http.HandleFunc(
		"/peer/handshake",
		HandlePeerHandshake,
	)

	http.HandleFunc(
		"/peer/tx",
		HandlePeerTx,
	)

	http.HandleFunc(
		"/peer/block",
		HandlePeerBlock,
	)

	http.HandleFunc(
		"/peer/chain",
		HandlePeerChain,
	)

	http.HandleFunc(
		"/validators/status",
		HandleConsensusStatus,
	)

	http.HandleFunc(
		"/rc/leaderboard",
		HandleRCLeaderboard,
	)

	http.HandleFunc(
		"/rc/executive/am-i",
		HandleAmIExecutive,
	)

	http.HandleFunc(
		"/proposals/promote",
		HandlePromoteProposal,
	)

	http.HandleFunc(
		"/predictions/vote",
		HandlePredictionVote,
	)

	http.HandleFunc(
		"/predictions/pool",
		HandleGetPredictionPool,
	)

	http.HandleFunc(
		"/rc/infuse",
		HandleInfuseRC,
	)

	http.HandleFunc(
		"/rc/infusions/recent",
		HandleRecentRCInfusions,
	)

	http.HandleFunc(
		"/executive-proposals/create",
		HandleCreateExecutiveProposal,
	)

	http.HandleFunc(
		"/executive-proposals/vote",
		HandleVoteExecutiveProposal,
	)

	http.HandleFunc(
		"/executive-proposals/list",
		HandleListExecutiveProposals,
	)

	http.HandleFunc(
		"/bridge-validator/register",
		HandleBridgeValidatorRegister,
	)

	http.HandleFunc(
		"/bridge-validator/list",
		HandleBridgeValidatorList,
	)

	http.HandleFunc(
		"/vault/info",
		HandleVaultInfo,
	)

	http.HandleFunc(
		"/vault/balance",
		HandleVaultBalance,
	)

	http.HandleFunc(
		"/vault/lock-attest",
		HandleVaultLockAttest,
	)

	http.HandleFunc(
		"/vault/lock-status",
		HandleVaultLockStatus,
	)

	http.HandleFunc(
		"/vault/burn",
		HandleVaultBurn,
	)

	http.HandleFunc(
		"/vault/release-approve",
		HandleVaultReleaseApprove,
	)

	http.HandleFunc(
		"/vault/set-validators-relay",
		HandleVaultSetValidatorsRelay,
	)

	http.HandleFunc(
		"/vault/set-token-allowed-relay",
		HandleVaultSetTokenAllowedRelay,
	)

	http.HandleFunc(
		"/vault/set-per-depositor-caps-relay",
		HandleVaultSetPerDepositorCapsRelay,
	)

	http.HandleFunc(
		"/vault/release-status",
		HandleVaultReleaseStatus,
	)

	http.HandleFunc(
		"/vault/release-queue",
		HandleVaultReleaseQueue,
	)

	http.HandleFunc(
		"/vault/pending-approvals",
		HandleVaultPendingApprovals,
	)

	startConsensusLoop()
	startForkMonitor()
	startVaultEthWatchLoop()
	startBtcVaultWatchLoop()
	startVaultReleaseApproveWatchLoop()
	startVaultReleaseRelayLoop()
	startBtcReleaseRelayLoop()
	startVaultGasPriceOracleLoop()

	fmt.Println(
		"CreditChain API running on port",
		port,
	)

	handler := corsMiddleware(http.DefaultServeMux)

	certFile := os.Getenv("TLS_CERT_FILE")
	keyFile := os.Getenv("TLS_KEY_FILE")

	if certFile != "" && keyFile != "" {
		fmt.Println("TLS enabled")
		err = http.ListenAndServeTLS(port, certFile, keyFile, handler)
	} else {
		fmt.Println("WARNING: TLS_CERT_FILE/TLS_KEY_FILE not set - serving plain HTTP. Fine for local dev, not for a real deployment (terminate TLS here or at a reverse proxy in front of it).")
		err = http.ListenAndServe(port, handler)
	}

	if err != nil {
		fmt.Println(
			"Server error:",
			err,
		)
	}
}
