package api

import (
	"encoding/json"
	"os"
	"sync"

	"github.com/creditchain/creditchaind/core"
)

var stateMu sync.Mutex

// persistedState snapshots every in-memory store used by the API -
// everything except the blockchain itself, which already persists
// separately via blockchain.json. Without this, a restart would wipe
// every wallet's balance, reputation, loans, stakes and votes even
// though the chain of blocks survives.
type persistedState struct {
	Wallets map[string]*core.Wallet `json:"wallets"`
	// StakesLegacy is the pre-multi-token shape (address -> amount,
	// always meaning ETHTEST) - kept ONLY so a state.json written
	// before per-symbol staking existed still decodes without error.
	// LoadState migrates it into Stakes below and never writes it
	// again itself.
	StakesLegacy      map[string]uint64              `json:"stakes"`
	Stakes            map[string]map[string]uint64   `json:"stakesBySymbol"`
	Unbonding         []core.UnbondingEntry          `json:"unbondingEntries"`
	TokenBalances     map[string]uint64              `json:"tokenBalances"`
	TokenSupply       uint64                         `json:"tokenSupply"`
	USDCTestBalances  map[string]uint64              `json:"usdcTestBalances"`
	USDCTestSupply    uint64                         `json:"usdcTestSupply"`
	REPTBalances      map[string]uint64              `json:"reptBalances"`
	REPTSupply        uint64                         `json:"reptSupply"`
	CustomTokens      map[string]*core.CustomToken   `json:"customTokens"`
	CustomTokenStores map[string]*core.TokenStore    `json:"customTokenStores"`
	LiquidityPools    map[string]*core.LiquidityPool `json:"liquidityPools"`
	ETHTestBalances   map[string]uint64              `json:"ethTestBalances"`
	ETHTestSupply     uint64                         `json:"ethTestSupply"`
	CCTEthTestPool    *core.LiquidityPool            `json:"cctEthTestPool"`
	CCTUsdcTestPool   *core.LiquidityPool            `json:"cctUsdcTestPool"`
	P2POffers         map[string]*core.P2POffer      `json:"p2pOffers"`
	LoanOffers        map[string]*core.LoanOffer     `json:"loanOffers"`
	Negotiations      map[string]*core.Negotiation   `json:"negotiations"`
	Loans             map[string]*core.Loan          `json:"loans"`
	// CollateralLockedLegacy is the pre-multi-symbol shape (address ->
	// amount, always meaning CCT, back when collateral only ever came
	// from liquid CCT) - kept ONLY so a state.json written before
	// symbol-aware collateral existed still decodes without error.
	// LoadState migrates it into CollateralLocked below (as a CCT
	// entry) and never writes it again itself.
	CollateralLockedLegacy map[string]uint64                   `json:"collateralLocked"`
	CollateralLocked       map[string]map[string]uint64        `json:"collateralLockedBySymbol"`
	CreditProfiles         map[string]*core.CreditProfile      `json:"creditProfiles"`
	ReputationProfiles     map[string]*core.Reputation         `json:"reputationProfiles"`
	CreditHistory          map[string][]core.CreditEvent       `json:"creditHistory"`
	LenderStats            map[string]*core.LenderStats        `json:"lenderStats"`
	Proposals              map[string]*core.Proposal           `json:"proposals"`
	ProposalVotes          map[string]map[string]bool          `json:"proposalVotes"`
	Validators             map[string]*core.ValidatorStatus    `json:"validators"`
	Delegations            map[string]map[string]uint64        `json:"delegations"`
	DelegationUnbonding    []core.DelegationUnbondingEntry     `json:"delegationUnbonding"`
	Moderators             core.ModeratorSnapshot              `json:"moderators"`
	Companies              map[string]*core.CompanyApplication `json:"companies"`
	BuyRequests            map[string]*core.BuyRequest         `json:"buyRequests"`
	Orders                 map[string]*core.Order              `json:"orders"`
	TradeHistory           map[string][]core.TradeRecord       `json:"tradeHistory"`
	Recovery               map[string]*core.WalletRecovery     `json:"recovery"`
	HubRates               map[string]*core.HubRate            `json:"hubRates"`
	HubRules               map[string]*core.HubRules           `json:"hubRules"`
	HubNotifications       []*core.HubNotification             `json:"hubNotifications"`
	UserNotifications      []*core.UserNotification            `json:"userNotifications"`
	GovernanceStatuses     []*core.GovernanceStatus            `json:"governanceStatuses"`
	HubAgentApplications   []*core.HubAgentApplication         `json:"hubAgentApplications"`
	HubWorkers             map[string]*core.HubWorker          `json:"hubWorkers"`
	HubStorefronts         map[string]*core.HubStorefront      `json:"hubStorefronts"`
	ReputationEvents       map[string]*core.ReputationEvent    `json:"reputationEvents"`
	// EthCustodial is NOT gossiped/replicated to other nodes - see
	// api/eth_custodial.go's top-of-file doc comment for why this store
	// is deliberately node-local (persisted here only so a restart of
	// THIS node doesn't lose the mapping).
	EthCustodial map[string]*core.EthCustodialAccount `json:"ethCustodial"`

	// KYC persists self-attested lending-marketplace access records -
	// see core/kyc.go's doc comment.
	KYC map[string]core.KYCRecord `json:"kyc"`

	// PriceHistory persists the pool price time series shown on
	// pool.html's chart - see core/price_history.go's doc comment.
	// Without this, a restart would silently wipe every recorded
	// price point even though the pools themselves (and the chain that
	// produced those prices) are unaffected.
	PriceHistory map[string][]core.PricePoint `json:"priceHistory"`

	// AppliedTxIDs/AppliedBlockHashes persist the dedup guards from
	// apply.go. Without this, a restart wipes them (fresh empty map),
	// but the stores above they protect ARE reloaded from this same
	// file - so a subsequent full chain resync (syncChainFromPeer)
	// would replay and RE-APPLY every transaction this node already
	// applied in a previous process lifetime, double-counting stakes,
	// balances, everything. Persisting these closes that gap.
	AppliedTxIDs       []string `json:"appliedTxIds"`
	AppliedBlockHashes []string `json:"appliedBlockHashes"`
	SlashDedupKeys     []string `json:"slashDedupKeys"`

	// Mempool persists pending (not-yet-confirmed) transactions. Without
	// this, a crash loses anything submitted but not yet swept into a
	// block, silently dropping the user's stake/borrow/vote/etc. with
	// no error - they'd have no way to know it needs resubmitting.
	Mempool []core.Transaction `json:"mempool"`

	// FaucetClaims persists which addresses already claimed the
	// testnet faucet, so a restart doesn't let everyone claim again.
	FaucetClaims []string `json:"faucetClaims"`
}

// SaveState writes a snapshot of every store to disk. Called after
// every mutating (POST) request - see corsMiddleware.
func SaveState() error {
	stateMu.Lock()
	defer stateMu.Unlock()

	snapshot := persistedState{
		Wallets:   walletStore.Wallets,
		Stakes:    stakingStore.Stakes,
		Unbonding: stakingStore.Unbonding,
		// StakesLegacy is intentionally left unset - only ever read for
		// migrating an old-format file, never written back.
		TokenBalances:        tokenStore.Balances,
		TokenSupply:          tokenStore.Token.TotalSupply,
		USDCTestBalances:     usdcTestStore.Balances,
		USDCTestSupply:       usdcTestStore.Token.TotalSupply,
		REPTBalances:         reptStore.Balances,
		REPTSupply:           reptStore.Token.TotalSupply,
		CustomTokens:         tokenRegistry.Meta,
		CustomTokenStores:    tokenRegistry.Tokens,
		LiquidityPools:       tokenRegistry.Pools,
		ETHTestBalances:      ethTestStore.Balances,
		ETHTestSupply:        ethTestStore.Token.TotalSupply,
		CCTEthTestPool:       cctEthTestPool,
		CCTUsdcTestPool:      cctUsdcTestPool,
		P2POffers:            p2pOfferStore.Offers,
		LoanOffers:           loanOfferStore.Offers,
		Negotiations:         negotiationStore.Negotiations,
		Loans:                loanStore.Loans,
		CollateralLocked:     collateralStore.Locked,
		CreditProfiles:       creditStore.Profiles,
		ReputationProfiles:   creditEngine.Profiles.Profiles,
		CreditHistory:        creditHistory.Events,
		LenderStats:          lenderStore.Stats,
		Proposals:            proposalStore.Proposals,
		ProposalVotes:        proposalStore.Voted,
		Validators:           validatorStore.Validators,
		Delegations:          delegationStore.Delegations,
		DelegationUnbonding:  delegationStore.Unbonding,
		Moderators:           moderatorStore.Snapshot(),
		Companies:            companyStore.Applications,
		BuyRequests:          buyRequestStore.Requests,
		Orders:               orderBookStore.Orders,
		TradeHistory:         tradeHistoryStore.Trades,
		Recovery:             recoveryStore.Records,
		HubRates:             hubRateStore.Rates,
		HubRules:             hubRulesStore.Snapshot(),
		HubNotifications:     notificationStore.Snapshot(),
		UserNotifications:    userNotificationStore.Snapshot(),
		GovernanceStatuses:   governanceStatusStore.Snapshot(),
		HubAgentApplications: hubAgentApplicationStore.Snapshot(),
		HubWorkers:           hubWorkerStore.Snapshot(),
		HubStorefronts:       hubStorefrontStore.Snapshot(),
		ReputationEvents:     reputationEventStore.Snapshot(),
		EthCustodial:         ethCustodialStore.Snapshot(),
		KYC:                  kycStore.Records,
		PriceHistory:         priceHistoryStore.History,
		AppliedTxIDs:         snapshotAppliedTxIDs(),
		AppliedBlockHashes:   snapshotAppliedBlockHashes(),
		SlashDedupKeys:       snapshotSlashDedup(),
		Mempool:              mempool.Transactions,
		FaucetClaims:         snapshotFaucetClaims(),
	}

	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}

	if err := core.AtomicWriteFile(dataPath("state.json"), data, 0644); err != nil {
		return err
	}

	// Dual-write to Postgres if configured - see db.go's doc comment for
	// why this is fire-and-forget and never affects the return value
	// here: state.json above is still the authoritative write, this is
	// purely an additional durable copy while that remains true.
	saveSnapshotToDB(data)
	return nil
}

// LoadState restores every store from disk at startup. A missing or
// unreadable file just leaves the stores at their fresh-init zero
// state, which is correct for a first run.
func LoadState() {
	stateMu.Lock()
	defer stateMu.Unlock()

	data, err := os.ReadFile(dataPath("state.json"))
	if err != nil {
		return
	}

	var snapshot persistedState

	if err := json.Unmarshal(data, &snapshot); err != nil {
		return
	}

	if snapshot.Wallets != nil {
		walletStore.Wallets = snapshot.Wallets

		// Wallets persisted before HighestTierReached existed load
		// with it at the Go zero value (""), which ranks the same as
		// Bronze - that would let their first reputation increase
		// after this deploy mint a retroactive REPT reward for every
		// tier they already reached before REPT existed. Backfilling
		// to their CURRENT tier here (not Bronze) matches exactly what
		// NewWallet() does for a wallet created fresh today: the
		// watermark starts at "where you already are," so only
		// genuine future progress ever mints REPT.
		for _, wallet := range walletStore.Wallets {
			if wallet.HighestTierReached == "" {
				wallet.HighestTierReached = core.TierForReputation(wallet.Reputation)
			}
		}
	}
	if snapshot.Stakes != nil {
		stakingStore.Stakes = snapshot.Stakes
	} else if snapshot.StakesLegacy != nil {
		// Migrating a state.json written before multi-token staking -
		// every stake back then was implicitly ETHTEST, since that was
		// the only stakeable token.
		migrated := make(map[string]map[string]uint64, len(snapshot.StakesLegacy))
		for address, amount := range snapshot.StakesLegacy {
			migrated[address] = map[string]uint64{"ETHTEST": amount}
		}
		stakingStore.Stakes = migrated
	}
	if snapshot.Unbonding != nil {
		stakingStore.Unbonding = snapshot.Unbonding

		// Entries persisted before Symbol/CreditAddress existed decode
		// with those at the Go zero value - default to ETHTEST (the
		// only token stakeable back then) and fall back to the
		// account's own raw address for CreditAddress. That fallback
		// is imperfect for ETHTEST (the real sub-address can't be
		// re-derived without the staker's public key, which old
		// entries never stored), but UnbondingPeriodSeconds is only 60s
		// - any entry old enough to predate this migration has almost
		// certainly already matured and been processed long ago, so
		// this path is a safety net for an edge case, not the common
		// case.
		for i := range stakingStore.Unbonding {
			if stakingStore.Unbonding[i].Symbol == "" {
				stakingStore.Unbonding[i].Symbol = "ETHTEST"
			}
			if stakingStore.Unbonding[i].CreditAddress == "" {
				stakingStore.Unbonding[i].CreditAddress = stakingStore.Unbonding[i].Address
			}
		}
	}
	if snapshot.TokenBalances != nil {
		tokenStore.Balances = snapshot.TokenBalances
	}
	tokenStore.Token.TotalSupply = snapshot.TokenSupply
	if snapshot.USDCTestBalances != nil {
		usdcTestStore.Balances = snapshot.USDCTestBalances
	}
	usdcTestStore.Token.TotalSupply = snapshot.USDCTestSupply
	if snapshot.REPTBalances != nil {
		reptStore.Balances = snapshot.REPTBalances
	}
	reptStore.Token.TotalSupply = snapshot.REPTSupply
	if snapshot.CustomTokens != nil {
		tokenRegistry.Meta = snapshot.CustomTokens
	}
	if snapshot.CustomTokenStores != nil {
		tokenRegistry.Tokens = snapshot.CustomTokenStores
	}
	if snapshot.LiquidityPools != nil {
		tokenRegistry.Pools = snapshot.LiquidityPools
	}
	if snapshot.ETHTestBalances != nil {
		ethTestStore.Balances = snapshot.ETHTestBalances
	}
	ethTestStore.Token.TotalSupply = snapshot.ETHTestSupply
	if snapshot.CCTEthTestPool != nil {
		cctEthTestPool = snapshot.CCTEthTestPool
	}
	if snapshot.CCTUsdcTestPool != nil {
		cctUsdcTestPool = snapshot.CCTUsdcTestPool
	}
	if snapshot.P2POffers != nil {
		p2pOfferStore.Offers = snapshot.P2POffers
	}
	if snapshot.LoanOffers != nil {
		loanOfferStore.Offers = snapshot.LoanOffers
	}
	if snapshot.Negotiations != nil {
		negotiationStore.Negotiations = snapshot.Negotiations
	}
	if snapshot.Loans != nil {
		loanStore.Loans = snapshot.Loans
	}
	if snapshot.CollateralLocked != nil {
		collateralStore.Locked = snapshot.CollateralLocked
	} else if snapshot.CollateralLockedLegacy != nil {
		// Migrating a state.json written before symbol-aware collateral -
		// every locked amount back then was implicitly CCT, since that
		// was the only symbol collateral could ever be. Disclosed
		// caveat: pre-migration collateral came out of LIQUID CCT (see
		// the old collateral_logic.go), not staked CCT - migrating it
		// into the new staked-CCT-backed model means releasing it later
		// credits staked CCT rather than the liquid balance it actually
		// left. Acceptable for a testnet demo; a real migration would
		// need a separate "legacy liquid CCT" release path instead.
		migrated := make(map[string]map[string]uint64, len(snapshot.CollateralLockedLegacy))
		for address, amount := range snapshot.CollateralLockedLegacy {
			migrated[address] = map[string]uint64{"CCT": amount}
		}
		collateralStore.Locked = migrated
	}
	if snapshot.CreditProfiles != nil {
		creditStore.Profiles = snapshot.CreditProfiles
	}
	if snapshot.ReputationProfiles != nil {
		creditEngine.Profiles.Profiles = snapshot.ReputationProfiles
	}
	if snapshot.CreditHistory != nil {
		creditHistory.Events = snapshot.CreditHistory
	}
	if snapshot.LenderStats != nil {
		lenderStore.Stats = snapshot.LenderStats
	}
	if snapshot.Proposals != nil {
		proposalStore.Proposals = snapshot.Proposals
	}
	if snapshot.ProposalVotes != nil {
		proposalStore.Voted = snapshot.ProposalVotes
	}
	if snapshot.Validators != nil {
		validatorStore.Validators = snapshot.Validators
	}
	if snapshot.Delegations != nil {
		delegationStore.Delegations = snapshot.Delegations
	}
	if snapshot.DelegationUnbonding != nil {
		delegationStore.Unbonding = snapshot.DelegationUnbonding
	}
	if snapshot.Moderators.Moderators != nil {
		moderatorStore.Restore(snapshot.Moderators)
	}
	if snapshot.Companies != nil {
		companyStore.Applications = snapshot.Companies
	}
	if snapshot.BuyRequests != nil {
		buyRequestStore.Requests = snapshot.BuyRequests
	}
	if snapshot.Orders != nil {
		orderBookStore.Orders = snapshot.Orders
	}
	if snapshot.TradeHistory != nil {
		tradeHistoryStore.Trades = snapshot.TradeHistory
	}
	if snapshot.Recovery != nil {
		recoveryStore.Records = snapshot.Recovery
	}
	if snapshot.HubRates != nil {
		hubRateStore.Rates = snapshot.HubRates
		// Re-key every entry from its own fields - state.json written
		// before FiatSymbol joined the key would otherwise sit under
		// stale keys that Get can never match again (see
		// core.HubRateStore.Renormalize's doc comment).
		hubRateStore.Renormalize()
	}
	if snapshot.HubRules != nil {
		hubRulesStore.Restore(snapshot.HubRules)
	}
	if snapshot.HubNotifications != nil {
		notificationStore.Restore(snapshot.HubNotifications)
	}
	if snapshot.UserNotifications != nil {
		userNotificationStore.Restore(snapshot.UserNotifications)
	}
	if snapshot.GovernanceStatuses != nil {
		governanceStatusStore.Restore(snapshot.GovernanceStatuses)
	}
	if snapshot.HubAgentApplications != nil {
		hubAgentApplicationStore.Restore(snapshot.HubAgentApplications)
	}
	if snapshot.HubWorkers != nil {
		hubWorkerStore.Restore(snapshot.HubWorkers)
	}
	if snapshot.HubStorefronts != nil {
		hubStorefrontStore.Restore(snapshot.HubStorefronts)
	}
	if snapshot.ReputationEvents != nil {
		reputationEventStore.Restore(snapshot.ReputationEvents)
	}
	if snapshot.EthCustodial != nil {
		ethCustodialStore.Restore(snapshot.EthCustodial)
	}
	if snapshot.KYC != nil {
		kycStore.Records = snapshot.KYC
	}
	if snapshot.PriceHistory != nil {
		priceHistoryStore.History = snapshot.PriceHistory
	}

	restoreAppliedTxIDs(snapshot.AppliedTxIDs)
	restoreAppliedBlockHashes(snapshot.AppliedBlockHashes)
	restoreSlashDedup(snapshot.SlashDedupKeys)

	if snapshot.Mempool != nil {
		mempool.Transactions = snapshot.Mempool
	}

	restoreFaucetClaims(snapshot.FaucetClaims)
}
