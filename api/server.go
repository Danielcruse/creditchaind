package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

var blockchain *core.Blockchain
var config *core.Config
var mempool = core.Mempool{}

var creditEngine = core.NewCreditEngine()

func enableCors(
	w *http.ResponseWriter,
) {
	(*w).Header().Set(
		"Access-Control-Allow-Origin",
		"*",
	)
}

func StartServer(port string) {
cfg, err :=
	core.LoadConfig(
		"config.json",
	)

if err != nil {
	panic(err)
}

config = cfg

	loaded, err := core.LoadBlockchain(
		"blockchain.json",
	)

	if err != nil {
		blockchain = core.NewBlockchain()
	} else {
		blockchain = loaded
	}

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
	"/validator-pools",
	HandleValidatorPools,
)

http.HandleFunc(
	"/validator-pools/create",
	HandleCreatePool,
)

http.HandleFunc(
	"/validator-pools/add-member",
	HandleAddPoolMember,
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
	"/validator-pools/rewards",
	HandlePoolRewards,
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
	"/credit",
	HandleCredit,
)

http.HandleFunc(
	"/token/mint",
	HandleTokenMint,
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
	"/wallet/create",
	HandleCreateWallet,
)
http.HandleFunc(
	"/chain",
	HandleChain,
)

         


	http.HandleFunc(
		"/transactions",
		HandleTransaction,
	)

	http.HandleFunc(
		"/mine",
		HandleMine,
	)

	http.HandleFunc(
		"/loans/create",
		HandleCreateLoan,
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
	"/credit/reputation",
	HandleReputationUpdate,
)

	http.HandleFunc(
		"/loans/repay",
		HandleRepayLoan,
	)

	fmt.Println(
		"CreditChain API running on port",
		port,
	)

	err = http.ListenAndServe(
	port,
		nil,
	)

	if err != nil {
		fmt.Println(
			"Server error:",
			err,
		)
	}
}