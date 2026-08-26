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
	StakesLegacy map[string]uint64            `json:"stakes"`
	Stakes       map[string]map[string]uint64 `json:"stakesBySymbol"`
	Unbonding    []core.UnbondingEntry        `json:"unbondingEntries"`
	// TokenBalances/TokenSupply are the FROZEN pre-migration CCT ledger
	// - left exactly as they were the moment
	// applyTokenomicsDecimalMigration ran (see api/tokenomics_migration.go),
	// never written to again. CCTBalances/CCTSupply below are the real,
	// live CCT ledger going forward.
	TokenBalances map[string]uint64 `json:"tokenBalances"`
	TokenSupply   uint64            `json:"tokenSupply"`
	// CCTBalances/CCTSupply persist core.CctStore - CCT's real ledger
	// as of the 18-decimal migration (see core/cct_store.go). Decimal
	// strings, not uint64 - the whole point of this store.
	CCTBalances       map[string]string               `json:"cctBalances"`
	CCTSupply         string                          `json:"cctSupply"`
	USDCTestBalances  map[string]uint64               `json:"usdcTestBalances"`
	USDCTestSupply    uint64                          `json:"usdcTestSupply"`
	REPTBalances      map[string]uint64               `json:"reptBalances"`
	REPTSupply        uint64                          `json:"reptSupply"`
	CustomTokens      map[string]*core.CustomToken    `json:"customTokens"`
	CustomTokenStores map[string]*core.TokenStore     `json:"customTokenStores"`
	LiquidityPools    map[string]*core.LiquidityPool  `json:"liquidityPools"`
	ETHTestBalances   map[string]uint64               `json:"ethTestBalances"`
	ETHTestSupply     uint64                          `json:"ethTestSupply"`
	CCTEthTestPool    *core.LiquidityPool             `json:"cctEthTestPool"`
	CCTUsdcTestPool   *core.LiquidityPool             `json:"cctUsdcTestPool"`
	BTCTestBalances   map[string]uint64               `json:"btcTestBalances"`
	BTCTestSupply     uint64                          `json:"btcTestSupply"`
	CCTBtcTestPool    *core.LiquidityPool             `json:"cctBtcTestPool"`
	PredictionPools   map[string]*core.PredictionPool `json:"predictionPools"`
	P2POffers         map[string]*core.P2POffer       `json:"p2pOffers"`
	LoanOffers        map[string]*core.LoanOffer      `json:"loanOffers"`
	LoanRequests      map[string]*core.LoanRequest    `json:"loanRequests"`
	Negotiations      map[string]*core.Negotiation    `json:"negotiations"`
	Loans             map[string]*core.Loan           `json:"loans"`
	// CollateralLockedLegacy is the pre-multi-symbol shape (address ->
	// amount, always meaning CCT, back when collateral only ever came
	// from liquid CCT) - kept ONLY so a state.json written before
	// symbol-aware collateral existed still decodes without error.
	// LoadState migrates it into CollateralLocked below (as a CCT
	// entry) and never writes it again itself.
	CollateralLockedLegacy map[string]uint64                   `json:"collateralLocked"`
	CollateralLocked       map[string]map[string]uint64        `json:"collateralLockedBySymbol"`
	StakePurposeLocked     map[string]map[string]uint64        `json:"stakePurposeLocked"`
	CreditProfiles         map[string]*core.CreditProfile      `json:"creditProfiles"`
	ReputationProfiles     map[string]*core.Reputation         `json:"reputationProfiles"`
	CreditHistory          map[string][]core.CreditEvent       `json:"creditHistory"`
	LenderStats            map[string]*core.LenderStats        `json:"lenderStats"`
	Proposals              map[string]*core.Proposal           `json:"proposals"`
	ProposalVotes          map[string]map[string]bool          `json:"proposalVotes"`
	ExecutiveProposals     map[string]*core.ExecutiveProposal  `json:"executiveProposals"`
	RCInfusions            []*core.RCInfusion                  `json:"rcInfusions"`
	ModeratorVerifications []*core.ModeratorVerificationEntry  `json:"moderatorVerifications"`
	GovernanceRCPoolMint   governanceRCPoolMintSnapshot        `json:"governanceRCPoolMint"`
	Validators             map[string]*core.ValidatorStatus    `json:"validators"`
	Delegations            map[string]map[string]uint64        `json:"delegations"`
	DelegationUnbonding    []core.DelegationUnbondingEntry     `json:"delegationUnbonding"`
	Moderators             core.ModeratorSnapshot              `json:"moderators"`
	TradeReviews           core.TradeReviewSnapshot            `json:"tradeReviews"`
	DisputeFaultChoices    map[string]*core.DisputeFaultChoice `json:"disputeFaultChoices"`
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

	// BridgeExternalTxHashes persists the side-store mapping a
	// TxBridgeWithdraw's ID to the real Sepolia tx hash it broadcast as
	// (see api/bridge_tx_hash.go) - without this, a restart would wipe
	// every recorded external-chain link even though the original
	// withdrawal transactions themselves (persisted via the chain/
	// mempool above) are unaffected.
	BridgeExternalTxHashes map[string]string `json:"bridgeExternalTxHashes"`

	// MissedBlockReporters persists in-flight (not-yet-resolved) M-of-N
	// corroboration state for missed-block slash reports (see
	// missed_block_confirmation.go) - without this, a node that
	// restarts after already reporting a miss won't automatically
	// re-report (its local consensus-loop cycle only reports once per
	// detected miss), silently dropping its own corroboration vote.
	MissedBlockReporters []missedBlockReporterSnapshot `json:"missedBlockReporters"`

	// ReputationMiningSweep persists the in-progress batch cursor for
	// the current epoch's REPT mining sweep (see reputation_mining.go)
	// - required for correctness, not just durability: without this, a
	// restart mid-epoch would re-mint wallets already processed
	// earlier in the same epoch.
	ReputationMiningSweep reputationMiningSweepSnapshot `json:"reputationMiningSweep"`

	// LegacyStakeMigrationSweep persists the one-time ETHTEST/USDCTEST
	// stake redemption sweep's progress (see legacy_stake_migration.go)
	// - see that snapshot type's own doc comment for why.
	LegacyStakeMigrationSweep legacyStakeMigrationSweepSnapshot `json:"legacyStakeMigrationSweep"`

	// GovernanceConvictionSweep/GovernanceConvictionBootstrap persist
	// the recurring and one-time governance-conviction sweeps' progress
	// (see governance_conviction.go) - same non-optional
	// restart-correctness reasoning as ReputationMiningSweep/
	// LegacyStakeMigrationSweep above.
	GovernanceConvictionSweep     governanceConvictionSweepSnapshot     `json:"governanceConvictionSweep"`
	GovernanceConvictionBootstrap governanceConvictionBootstrapSnapshot `json:"governanceConvictionBootstrap"`

	// AdminMultisigProposals persists every admin multisig proposal
	// ever created (pending or executed) - kept as a full audit trail,
	// same "never delete a record" convention as LoanOfferStore/
	// ProposalStore, and required for correctness too: an in-flight
	// PENDING proposal's approvals would otherwise be lost on restart,
	// silently resetting an almost-approved action back to needing
	// every signer to re-approve from scratch.
	AdminMultisigProposals map[string]*core.AdminMultisigProposal `json:"adminMultisigProposals"`

	// Mempool persists pending (not-yet-confirmed) transactions. Without
	// this, a crash loses anything submitted but not yet swept into a
	// block, silently dropping the user's stake/borrow/vote/etc. with
	// no error - they'd have no way to know it needs resubmitting.
	Mempool []core.Transaction `json:"mempool"`

	// FaucetClaims persists which addresses already claimed the
	// testnet faucet, so a restart doesn't let everyone claim again.
	FaucetClaims []string `json:"faucetClaims"`

	// YeneClaims persists each address's last YEN-e claim timestamp
	// (see core.YeneClaimStore) so a restart doesn't reset everyone's
	// 5-day cooldown.
	YeneClaims map[string]int64 `json:"yeneClaims"`

	// YenBalances/YenSupply/CCTYenPool are YEN-e's own ledger and
	// CCT-paired pool, same pattern as the USDCTEST fields above - see
	// api/token.go's yenStore/cctYenPool doc comments.
	YenBalances map[string]uint64   `json:"yenBalances"`
	YenSupply   uint64              `json:"yenSupply"`
	CCTYenPool  *core.LiquidityPool `json:"cctYenPool"`

	// ETHBBalances/ETHBSupply are bridgedEthStore's ledger - the auto-
	// bridge feature's unified ETH balance (api/eth_bridge.go). No
	// CCT-paired pool field, unlike YEN-e - see bridgedEthStore's doc
	// comment in api/token.go for why. Deposit-detection's own dedup
	// state rides along in SlashDedupKeys below (applyBridgeDeposit
	// reuses slashDedup directly) and in EthCustodial's own
	// LastCreditedWei field on each account - no separate persistence
	// needed for either.
	ETHBBalances map[string]uint64 `json:"ethbBalances"`
	ETHBSupply   uint64            `json:"ethbSupply"`

	// BTCBBalances/BTCBSupply are bridgedBtcStore's ledger - BTCB's own
	// counterpart to ETHBBalances/ETHBSupply above. BtcWatchedAddresses
	// is NOT gossiped either (same reasoning as EthCustodial above),
	// persisted here only so this node's own restart doesn't lose the
	// address-watch mapping/deposit-detection baseline.
	BTCBBalances        map[string]uint64                  `json:"btcbBalances"`
	BTCBSupply          uint64                             `json:"btcbSupply"`
	BtcWatchedAddresses map[string]*core.BtcWatchedAddress `json:"btcWatchedAddresses"`

	// EthWatchedAddresses mirrors BtcWatchedAddresses exactly, for the
	// free self-custodial ETH deposit-detection path (see
	// core/eth_address.go's doc comment) - not gossiped, persisted here
	// only so this node's own restart doesn't lose the address-watch
	// mapping/deposit-detection baseline.
	EthWatchedAddresses map[string]*core.EthWatchedAddress `json:"ethWatchedAddresses"`

	// Treasury/Purchases persist the Foundation Treasury Wallet's
	// sale configuration and purchase-request history - see
	// core/treasury.go's doc comment.
	Treasury  core.Treasury                    `json:"treasury"`
	Purchases map[string]*core.PurchaseRequest `json:"treasuryPurchases"`

	// GovernanceParams persists any protocol parameter a passed
	// PARAM_CHANGE proposal has overridden - see
	// core.GovernanceParamsStore's doc comment. Without this, a
	// restart would silently revert every governance-set fee back to
	// its hardcoded default.
	GovernanceParams map[string]uint64 `json:"governanceParams"`

	// GovernanceParamsSetAt persists when each staleness-tracked
	// governance param (currently just EthUsdCentsPerEth) was last
	// SetAt - see core.GovernanceParamsStore.IsStale. Absent/empty on
	// any state.json written before this field existed, which correctly
	// leaves every key with no recorded timestamp (IsStale reports true
	// until an admin/proposal actually SetAt's it again post-upgrade)
	// rather than assuming a pre-upgrade value is freshly confirmed.
	GovernanceParamsSetAt map[string]int64 `json:"governanceParamsSetAt,omitempty"`

	// ActivatedProtocolVersion persists the governance-gated version
	// flag - see core.ActivatedProtocolVersion's doc comment.
	ActivatedProtocolVersion string `json:"activatedProtocolVersion"`

	// ProtocolManifest persists the consensus-committed security-
	// critical config record (audit findings C-06/C-09) - see
	// core.ProtocolManifest's doc comment. Zero value (Set: false) on
	// any state.json written before this field existed, which
	// correctly leaves every node falling back to its own local
	// environment variables exactly as it always has, until an admin
	// explicitly commits a manifest.
	ProtocolManifest core.ProtocolManifest `json:"protocolManifest,omitempty"`

	// ProposalVotesByAddress persists the per-address vote-history log
	// (core.ProposalStore.VotesByAddress) - the "community remembers"
	// permanent record. Voted/Proposals above already persist the
	// tallying state; this is the separate, purely-additive history
	// list.
	ProposalVotesByAddress map[string][]core.VoteRecord `json:"proposalVotesByAddress"`

	// Announcements persists admin-posted site-wide announcements -
	// see core.AnnouncementStore's doc comment.
	Announcements []*core.Announcement `json:"announcements"`

	// Ratings persists every wallet's site rating - see
	// core.RatingStore's doc comment.
	Ratings []*core.Rating `json:"ratings"`

	// HubAutomations persists each hub's Automation-tier subscription
	// state - see core.HubAutomationStore's doc comment. HubWorkers
	// (already persisted below via the existing HubWorker field) rides
	// along with its own new IsAutomationAgent/AutoConfirmCapAmount
	// fields for free, since that store is already saved wholesale.
	HubAutomations map[string]*core.HubAutomation `json:"hubAutomations"`

	// ValidatorApplications persists the staged validator-onboarding
	// pipeline's state - see core.ValidatorApplicationStore's doc
	// comment.
	ValidatorApplications map[string]*core.ValidatorApplication `json:"validatorApplications"`

	// BridgeValidators/VaultLockAttestations/VaultReleaseApprovals
	// persist the lock-and-mint vault bridge's consensus-critical
	// authorization state - see core/bridge_validator.go,
	// core/vault_lock_attestation.go, core/vault_release_approval.go.
	BridgeValidators      map[string]*core.BridgeValidator      `json:"bridgeValidators"`
	VaultLockAttestations map[string]*core.VaultLockAttestation `json:"vaultLockAttestations"`
	VaultReleaseApprovals map[string]*core.VaultReleaseApproval `json:"vaultReleaseApprovals"`

	// NodeAdmissions persists every governance-approved peer-admission
	// grant - see core.NodeAdmissionStore's own doc comment. Keyed by
	// public key, same shape/reasoning as BridgeValidators above.
	NodeAdmissions map[string]*core.NodeAdmission `json:"nodeAdmissions"`

	// EthCcc1Balances/UsdcCcc1Balances/BtcCcc1Balances back ETH_CCC1/
	// USDC_CCC1/BTC_CCC1 - the real, vault-backed tokens (see
	// api/vault_token.go). ETH/USDC use decimal-string balances
	// (core.DecimalTokenStore); BTC stays plain uint64 sats.
	EthCcc1Balances  map[string]string `json:"ethCcc1Balances"`
	EthCcc1Supply    string            `json:"ethCcc1Supply"`
	UsdcCcc1Balances map[string]string `json:"usdcCcc1Balances"`
	UsdcCcc1Supply   string            `json:"usdcCcc1Supply"`
	BtcCcc1Balances  map[string]uint64 `json:"btcCcc1Balances"`
	BtcCcc1Supply    uint64            `json:"btcCcc1Supply"`

	// VaultEthLastScannedBlock is the ETH vault watch loop's node-local
	// progress marker (see api/vault_eth_watch.go) - not consensus
	// state, just worth persisting across restarts to avoid re-scanning
	// from the current head every time (see that file's doc comment on
	// vaultEthLastScannedBlock for why losing this isn't unsafe, just
	// wasteful).
	VaultEthLastScannedBlock uint64 `json:"vaultEthLastScannedBlock"`
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
		TokenBalances:                 tokenStore.Balances,
		TokenSupply:                   tokenStore.Token.TotalSupply,
		CCTBalances:                   cctStore.Snapshot(),
		CCTSupply:                     cctStore.TotalSupply,
		USDCTestBalances:              usdcTestStore.Balances,
		USDCTestSupply:                usdcTestStore.Token.TotalSupply,
		REPTBalances:                  reptStore.Balances,
		REPTSupply:                    reptStore.Token.TotalSupply,
		CustomTokens:                  tokenRegistry.Meta,
		CustomTokenStores:             tokenRegistry.Tokens,
		LiquidityPools:                tokenRegistry.Pools,
		ETHTestBalances:               ethTestStore.Balances,
		ETHTestSupply:                 ethTestStore.Token.TotalSupply,
		CCTEthTestPool:                cctEthTestPool,
		CCTUsdcTestPool:               cctUsdcTestPool,
		BTCTestBalances:               btcTestStore.Balances,
		BTCTestSupply:                 btcTestStore.Token.TotalSupply,
		CCTBtcTestPool:                cctBtcTestPool,
		PredictionPools:               predictionPoolStore.Snapshot(),
		P2POffers:                     p2pOfferStore.Offers,
		LoanOffers:                    loanOfferStore.Offers,
		LoanRequests:                  loanRequestStore.Requests,
		Negotiations:                  negotiationStore.Negotiations,
		Loans:                         loanStore.Loans,
		CollateralLocked:              collateralStore.Locked,
		StakePurposeLocked:            stakePurposeStore.Locked,
		CreditProfiles:                creditStore.Profiles,
		ReputationProfiles:            creditEngine.Profiles.Profiles,
		CreditHistory:                 creditHistory.Events,
		LenderStats:                   lenderStore.Stats,
		Proposals:                     proposalStore.Proposals,
		ProposalVotes:                 proposalStore.Voted,
		ExecutiveProposals:            executiveProposalStore.Proposals,
		RCInfusions:                   rcInfusionStore.Infusions,
		ModeratorVerifications:        moderatorVerificationLog.Snapshot(),
		GovernanceRCPoolMint:          snapshotGovernanceRCPoolMint(),
		Validators:                    validatorStore.Validators,
		Delegations:                   delegationStore.Delegations,
		DelegationUnbonding:           delegationStore.Unbonding,
		Moderators:                    moderatorStore.Snapshot(),
		TradeReviews:                  tradeReviewStore.Snapshot(),
		DisputeFaultChoices:           disputeFaultChoiceStore.Snapshot(),
		Companies:                     companyStore.Applications,
		BuyRequests:                   buyRequestStore.Requests,
		Orders:                        orderBookStore.Orders,
		TradeHistory:                  tradeHistoryStore.Trades,
		Recovery:                      recoveryStore.Records,
		HubRates:                      hubRateStore.Rates,
		HubRules:                      hubRulesStore.Snapshot(),
		HubNotifications:              notificationStore.Snapshot(),
		UserNotifications:             userNotificationStore.Snapshot(),
		GovernanceStatuses:            governanceStatusStore.Snapshot(),
		HubAgentApplications:          hubAgentApplicationStore.Snapshot(),
		HubWorkers:                    hubWorkerStore.Snapshot(),
		HubAutomations:                hubAutomationStore.Snapshot(),
		ValidatorApplications:         validatorApplicationStore.Snapshot(),
		HubStorefronts:                hubStorefrontStore.Snapshot(),
		ReputationEvents:              reputationEventStore.Snapshot(),
		EthCustodial:                  ethCustodialStore.Snapshot(),
		KYC:                           kycStore.Records,
		PriceHistory:                  priceHistoryStore.History,
		AppliedTxIDs:                  snapshotAppliedTxIDs(),
		AppliedBlockHashes:            snapshotAppliedBlockHashes(),
		SlashDedupKeys:                snapshotSlashDedup(),
		BridgeExternalTxHashes:        snapshotBridgeExternalTxHashes(),
		MissedBlockReporters:          snapshotMissedBlockReporters(),
		ReputationMiningSweep:         snapshotReputationMiningSweep(),
		LegacyStakeMigrationSweep:     snapshotLegacyStakeMigrationSweep(),
		GovernanceConvictionSweep:     snapshotGovernanceConvictionSweep(),
		GovernanceConvictionBootstrap: snapshotGovernanceConvictionBootstrap(),
		AdminMultisigProposals:        adminMultisigStore.Proposals,
		Mempool:                       mempool.Transactions,
		FaucetClaims:                  snapshotFaucetClaims(),
		YeneClaims:                    yeneClaimStore.LastClaimedAt,
		YenBalances:                   yenStore.Balances,
		YenSupply:                     yenStore.Token.TotalSupply,
		CCTYenPool:                    cctYenPool,
		Treasury:                      treasuryStore.Treasury,
		Purchases:                     treasuryStore.Purchases,
		ETHBBalances:                  bridgedEthStore.Balances,
		ETHBSupply:                    bridgedEthStore.Token.TotalSupply,
		BTCBBalances:                  bridgedBtcStore.Balances,
		BTCBSupply:                    bridgedBtcStore.Token.TotalSupply,
		BtcWatchedAddresses:           btcAddressStore.Snapshot(),
		EthWatchedAddresses:           ethAddressStore.Snapshot(),
		GovernanceParams:              governanceParamsStore.Snapshot(),
		GovernanceParamsSetAt:         governanceParamsStore.SnapshotSetAt(),
		ActivatedProtocolVersion:      core.ActivatedProtocolVersion,
		ProtocolManifest:              core.ActiveProtocolManifest,
		ProposalVotesByAddress:        proposalStore.VotesByAddress,
		Announcements:                 announcementStore.Snapshot(),
		Ratings:                       ratingStore.Snapshot(),
		BridgeValidators:              bridgeValidatorStore.Snapshot(),
		VaultLockAttestations:         vaultLockAttestationStore.Snapshot(),
		VaultReleaseApprovals:         vaultReleaseApprovalStore.Snapshot(),
		NodeAdmissions:                nodeAdmissionStore.Snapshot(),
		EthCcc1Balances:               ethCcc1Store.Balances,
		EthCcc1Supply:                 ethCcc1Store.TotalSupply,
		UsdcCcc1Balances:              usdcCcc1Store.Balances,
		UsdcCcc1Supply:                usdcCcc1Store.TotalSupply,
		BtcCcc1Balances:               btcCcc1Store.Balances,
		BtcCcc1Supply:                 btcCcc1Store.Token.TotalSupply,
		VaultEthLastScannedBlock:      vaultEthLastScannedBlock,
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

// restoreTrustWeightsFromGovernanceParams re-syncs core.DefaultTrustWeights
// (a core-package mirror var, see syncTrustWeightIfNeeded's doc comment in
// api/governance_params.go) from whatever the six TSIWeight*Bps keys were
// last set to in governanceParamsStore, which LoadState's caller must
// already have restored via governanceParamsStore.Restore/RestoreSetAt
// before calling this. core.DefaultTrustWeights needs no dedicated
// snapshot field of its own - the underlying value already round-trips
// through the same GovernanceParams/GovernanceParamsSetAt fields every
// other governanceParamsStore key persists through; this just re-applies
// it onto the mirror, so a governance-changed weight survives a restart
// instead of silently reverting to the compiled-in default. A key that
// was never set (fresh chain, never governance-changed) is left alone,
// since governanceParamsStore.Get correctly reports not-ok for it.
func restoreTrustWeightsFromGovernanceParams() {
	for _, key := range []string{
		"TSIWeightRepaymentBps",
		"TSIWeightTradingBps",
		"TSIWeightGovernanceBps",
		"TSIWeightContributionBps",
		"TSIWeightVerificationBps",
		"TSIWeightDisputeBps",
	} {
		if v, ok := governanceParamsStore.Get(key); ok {
			syncTrustWeightIfNeeded(key, v)
		}
	}
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
	cctStore.Restore(snapshot.CCTBalances, snapshot.CCTSupply)
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
	if snapshot.BTCTestBalances != nil {
		btcTestStore.Balances = snapshot.BTCTestBalances
	}
	btcTestStore.Token.TotalSupply = snapshot.BTCTestSupply
	if snapshot.CCTBtcTestPool != nil {
		cctBtcTestPool = snapshot.CCTBtcTestPool
	}
	if snapshot.PredictionPools != nil {
		predictionPoolStore.Restore(snapshot.PredictionPools)
	}
	if snapshot.P2POffers != nil {
		p2pOfferStore.Offers = snapshot.P2POffers
	}
	if snapshot.LoanOffers != nil {
		loanOfferStore.Offers = snapshot.LoanOffers
	}
	if snapshot.LoanRequests != nil {
		loanRequestStore.Requests = snapshot.LoanRequests
	}
	if snapshot.Negotiations != nil {
		negotiationStore.Negotiations = snapshot.Negotiations
	}
	if snapshot.Loans != nil {
		loanStore.Loans = snapshot.Loans
	}
	if snapshot.StakePurposeLocked != nil {
		stakePurposeStore.Locked = snapshot.StakePurposeLocked
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
	if snapshot.ExecutiveProposals != nil {
		executiveProposalStore.Proposals = snapshot.ExecutiveProposals
	}
	if snapshot.RCInfusions != nil {
		rcInfusionStore.Infusions = snapshot.RCInfusions
	}
	if snapshot.ModeratorVerifications != nil {
		moderatorVerificationLog.Restore(snapshot.ModeratorVerifications)
	}
	restoreGovernanceRCPoolMint(snapshot.GovernanceRCPoolMint)
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
	if snapshot.TradeReviews.Reviews != nil {
		tradeReviewStore.Restore(snapshot.TradeReviews)
	}
	if snapshot.DisputeFaultChoices != nil {
		disputeFaultChoiceStore.Restore(snapshot.DisputeFaultChoices)
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
	if snapshot.HubAutomations != nil {
		hubAutomationStore.Restore(snapshot.HubAutomations)
	}
	if snapshot.ValidatorApplications != nil {
		validatorApplicationStore.Restore(snapshot.ValidatorApplications)
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
	restoreBridgeExternalTxHashes(snapshot.BridgeExternalTxHashes)
	restoreMissedBlockReporters(snapshot.MissedBlockReporters)
	restoreReputationMiningSweep(snapshot.ReputationMiningSweep)
	restoreLegacyStakeMigrationSweep(snapshot.LegacyStakeMigrationSweep)
	restoreGovernanceConvictionSweep(snapshot.GovernanceConvictionSweep)
	restoreGovernanceConvictionBootstrap(snapshot.GovernanceConvictionBootstrap)

	if snapshot.AdminMultisigProposals != nil {
		adminMultisigStore.Proposals = snapshot.AdminMultisigProposals
	}

	if snapshot.Mempool != nil {
		mempool.Transactions = snapshot.Mempool
	}

	restoreFaucetClaims(snapshot.FaucetClaims)

	if snapshot.YeneClaims != nil {
		yeneClaimStore.LastClaimedAt = snapshot.YeneClaims
	}

	if snapshot.YenBalances != nil {
		yenStore.Balances = snapshot.YenBalances
	}
	yenStore.Token.TotalSupply = snapshot.YenSupply
	if snapshot.CCTYenPool != nil {
		cctYenPool = snapshot.CCTYenPool
	}
	treasuryStore.Treasury = snapshot.Treasury
	if snapshot.Purchases != nil {
		treasuryStore.Purchases = snapshot.Purchases
	}

	if snapshot.ETHBBalances != nil {
		bridgedEthStore.Balances = snapshot.ETHBBalances
	}
	bridgedEthStore.Token.TotalSupply = snapshot.ETHBSupply

	if snapshot.BTCBBalances != nil {
		bridgedBtcStore.Balances = snapshot.BTCBBalances
	}
	bridgedBtcStore.Token.TotalSupply = snapshot.BTCBSupply
	if snapshot.BtcWatchedAddresses != nil {
		btcAddressStore.Restore(snapshot.BtcWatchedAddresses)
	}
	if snapshot.EthWatchedAddresses != nil {
		ethAddressStore.Restore(snapshot.EthWatchedAddresses)
	}

	if snapshot.GovernanceParams != nil {
		governanceParamsStore.Restore(snapshot.GovernanceParams)
	}
	if snapshot.GovernanceParamsSetAt != nil {
		governanceParamsStore.RestoreSetAt(snapshot.GovernanceParamsSetAt)
	}
	restoreTrustWeightsFromGovernanceParams()
	core.ActivatedProtocolVersion = snapshot.ActivatedProtocolVersion
	core.ActiveProtocolManifest = snapshot.ProtocolManifest

	if snapshot.ProposalVotesByAddress != nil {
		proposalStore.VotesByAddress = snapshot.ProposalVotesByAddress
	}
	if snapshot.Announcements != nil {
		announcementStore.Restore(snapshot.Announcements)
	}
	if snapshot.Ratings != nil {
		ratingStore.Restore(snapshot.Ratings)
	}

	if snapshot.BridgeValidators != nil {
		bridgeValidatorStore.Restore(snapshot.BridgeValidators)
	}
	if snapshot.NodeAdmissions != nil {
		nodeAdmissionStore.Restore(snapshot.NodeAdmissions)
	}
	if snapshot.VaultLockAttestations != nil {
		vaultLockAttestationStore.Restore(snapshot.VaultLockAttestations)
	}
	if snapshot.VaultReleaseApprovals != nil {
		vaultReleaseApprovalStore.Restore(snapshot.VaultReleaseApprovals)
	}

	if snapshot.EthCcc1Balances != nil {
		ethCcc1Store.Restore(snapshot.EthCcc1Balances, snapshot.EthCcc1Supply)
	}
	if snapshot.UsdcCcc1Balances != nil {
		usdcCcc1Store.Restore(snapshot.UsdcCcc1Balances, snapshot.UsdcCcc1Supply)
	}
	if snapshot.BtcCcc1Balances != nil {
		btcCcc1Store.Balances = snapshot.BtcCcc1Balances
	}
	btcCcc1Store.Token.TotalSupply = snapshot.BtcCcc1Supply
	vaultEthLastScannedBlock = snapshot.VaultEthLastScannedBlock
}
