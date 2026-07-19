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

		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)

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
		panic(err)
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
	InitDB()
	InitEthSigner()
	InitEthRPC()
	startEthBridgeDepositLoop()
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
		"/companies/apply",
		HandleApplyCompany,
	)
	http.HandleFunc(
		"/companies/review",
		HandleReviewCompany,
	)
	http.HandleFunc(
		"/companies/reassign-moderator",
		HandleReassignCompanyModerator,
	)
	http.HandleFunc(
		"/companies/update-contact",
		HandleUpdateCompanyContact,
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
		"/ethb/balance",
		HandleEthbBalance,
	)
	http.HandleFunc(
		"/eth-bridge/withdraw",
		HandleBridgedEthWithdraw,
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
		"/faucet",
		HandleFaucet,
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

	startConsensusLoop()
	startForkMonitor()

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
