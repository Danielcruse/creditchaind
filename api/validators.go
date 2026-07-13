package api

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"

	"github.com/creditchain/creditchaind/core"
)

var validatorStore = core.NewValidatorStore()
var delegationStore = core.NewDelegationStore()

// MinValidatorReputation matches the default reputation a fresh
// wallet starts with (core.NewWallet), so any wallet that has staked
// something is immediately eligible.
const MinValidatorReputation = 50

// ProduceBlockReward is the flat CCT reward minted per block produced
// by a registered validator via /validators/produce-block.
const ProduceBlockReward = 10

// ValidatorRegisterFeeCCT is a CCT anti-spam cost for registering as a
// validator - same pattern as HubApplyFeeCCT/ModeratorRegisterFeeCCT,
// paid to registrationFeeAddress (api/apply.go). Highest of the three
// registration fees: a validator produces blocks and earns
// ProduceBlockReward indefinitely, the most consequential and highest-
// trust of the three roles - this is on top of, not instead of, the
// existing core.MinValidatorStake ETH stake requirement.
const ValidatorRegisterFeeCCT = 50

type ValidatorInfo struct {
	Address          string  `json:"address"`
	Stake            uint64  `json:"stake"`
	Reputation       uint64  `json:"reputation"`
	Rewards          uint64  `json:"rewards"`
	BlocksProduced   uint64  `json:"blocksProduced"`
	MissedBlocks     uint64  `json:"missedBlocks"`
	SlashCount       uint64  `json:"slashCount"`
	Uptime           float64 `json:"uptime"`
	VotingPower      uint64  `json:"votingPower"`
	Active           bool    `json:"active"`
	IsCurrentLeader  bool    `json:"isCurrentLeader"`
	AuthorizedNodeID string  `json:"authorizedNodeId"`

	// Pool fields - see core.DelegationStore and
	// core.ValidatorStatus.CommissionBps's doc comments.
	CommissionBps uint16 `json:"commissionBps"`
	// TotalDelegated is CCT delegated by third parties, separate from
	// Stake (this validator's own bonded ETHTEST).
	TotalDelegated uint64 `json:"totalDelegated"`
	DelegatorCount int    `json:"delegatorCount"`
	// EffectiveStake is what actually feeds PoSR selection weight (see
	// api/consensus_loop.go's hydrateValidator) - Stake + TotalDelegated.
	EffectiveStake uint64 `json:"effectiveStake"`
	// EstimatedAPY is a labeled APPROXIMATION for a delegator
	// considering this validator - see estimateDelegatorAPY's doc
	// comment for exactly what it does and doesn't account for. Zero
	// for a validator with no delegators (nothing to estimate) or an
	// inactive one.
	EstimatedAPY float64 `json:"estimatedApy"`
}

func toValidatorInfo(v *core.ValidatorStatus, totalEffectiveStakeAcrossAll uint64) ValidatorInfo {
	wallet := walletStore.Get(v.Address)
	stakingStore.SyncWallet(wallet, v.Address)

	leader := currentLeader()
	isLeader := leader != nil && leader.Wallet.Address == v.Address

	totalDelegated := delegationStore.TotalDelegatedTo(v.Address)
	effectiveStake := wallet.StakedETH + totalDelegated

	return ValidatorInfo{
		Address:          v.Address,
		Stake:            wallet.StakedETH,
		Reputation:       wallet.Reputation,
		Rewards:          wallet.Rewards,
		BlocksProduced:   v.BlocksProduced,
		MissedBlocks:     v.MissedBlocks,
		SlashCount:       v.SlashCount,
		Uptime:           v.Uptime(),
		VotingPower:      wallet.VotingPower,
		Active:           v.Active,
		IsCurrentLeader:  isLeader,
		AuthorizedNodeID: v.AuthorizedNodeID,
		CommissionBps:    v.CommissionBps,
		TotalDelegated:   totalDelegated,
		DelegatorCount:   delegationStore.DelegatorCount(v.Address),
		EffectiveStake:   effectiveStake,
		EstimatedAPY:     estimateDelegatorAPY(v, effectiveStake, totalDelegated, totalEffectiveStakeAcrossAll),
	}
}

// estimateDelegatorAPY is a clearly-labeled APPROXIMATION of what a
// delegator to this validator might earn annually, not a guarantee -
// real PoSR selection also weighs reputation (55%) and uptime (20%),
// not just stake (25%, see core/consensus.go's "Locked... Approved"
// weights), so an active/high-reputation validator with modest stake
// will actually get selected MORE often than this stake-only
// approximation predicts, and vice versa. Deliberately a simple
// proportional-to-effective-stake estimate rather than reimplementing
// core's unexported validatorWeight here - transparently labeled as
// an estimate in both this field's name and the frontend copy that
// displays it.
func estimateDelegatorAPY(v *core.ValidatorStatus, effectiveStake, totalDelegated, totalEffectiveStakeAcrossAll uint64) float64 {
	if !v.Active || effectiveStake == 0 || totalDelegated == 0 || totalEffectiveStakeAcrossAll == 0 {
		return 0
	}
	share := float64(effectiveStake) / float64(totalEffectiveStakeAcrossAll)
	blocksPerYear := float64(365*24*3600) / consensusTickInterval.Seconds()
	annualPoolReward := blocksPerYear * share * float64(ProduceBlockReward)
	delegatorAnnualReward := annualPoolReward * float64(10000-v.CommissionBps) / 10000
	return delegatorAnnualReward / float64(totalDelegated) * 100
}

// totalEffectiveStakeAcrossValidators sums every registered
// validator's effective stake (own + delegated) - the denominator
// estimateDelegatorAPY needs to turn "this validator's share of the
// whole network" into a per-validator estimate.
func totalEffectiveStakeAcrossValidators() uint64 {
	var total uint64
	for _, v := range validatorStore.List() {
		wallet := walletStore.Get(v.Address)
		stakingStore.SyncWallet(wallet, v.Address)
		total += wallet.StakedETH + delegationStore.TotalDelegatedTo(v.Address)
	}
	return total
}

// HandleValidators lists every registered validator from the shared
// ValidatorStore, with stake/reputation synced live from the
// canonical StakingStore/WalletStore - a real leaderboard, sortable
// via ?sort=stake|uptime|blocks|reputation|delegated (defaults to
// stake, meaning EffectiveStake, the PoSR-relevant figure).
func HandleValidators(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	statuses := validatorStore.List()
	totalEffectiveStake := totalEffectiveStakeAcrossValidators()

	list := []ValidatorInfo{}
	for _, v := range statuses {
		list = append(list, toValidatorInfo(v, totalEffectiveStake))
	}

	switch r.URL.Query().Get("sort") {
	case "uptime":
		sort.Slice(list, func(i, j int) bool { return list[i].Uptime > list[j].Uptime })
	case "blocks":
		sort.Slice(list, func(i, j int) bool { return list[i].BlocksProduced > list[j].BlocksProduced })
	case "reputation":
		sort.Slice(list, func(i, j int) bool { return list[i].Reputation > list[j].Reputation })
	case "delegated":
		sort.Slice(list, func(i, j int) bool { return list[i].TotalDelegated > list[j].TotalDelegated })
	default:
		sort.Slice(list, func(i, j int) bool { return list[i].EffectiveStake > list[j].EffectiveStake })
	}

	json.NewEncoder(w).Encode(list)
}

func HandleRegisterValidator(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req validatorRegisterData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	wallet := walletStore.Get(address)
	stakingStore.SyncWallet(wallet, address)

	if wallet.StakedETH < core.MinValidatorStake {
		http.Error(w, fmt.Sprintf("must stake at least %d ETH before becoming a validator", core.MinValidatorStake), http.StatusBadRequest)
		return
	}

	if wallet.Reputation < MinValidatorReputation {
		http.Error(w, "reputation too low to become a validator", http.StatusBadRequest)
		return
	}

	if tokenStore.BalanceOf(address) < ValidatorRegisterFeeCCT {
		http.Error(w, fmt.Sprintf("you need at least %d CCT to cover the validator registration fee", ValidatorRegisterFeeCCT), http.StatusBadRequest)
		return
	}

	// The client must explicitly SIGN which node it's delegating
	// block-production to (fetched from that node's own GET /node
	// first) rather than this node unilaterally asserting itself into
	// the transaction's Data unsigned - otherwise a malicious producer
	// could rewrite Data on someone else's real registration
	// transaction in transit to redirect their delegation to an
	// attacker-controlled node, and nothing before this fix would have
	// caught it (verifyTxAuth in api/apply.go only bound the actor's
	// identity, not which node they chose). Requiring it to match
	// THIS node's own identity here is a sanity/UX check, not the
	// security boundary - the actual boundary is verifyTxAuth
	// confirming tx.Data matches what was cryptographically signed.
	if req.NodeID != config.NodeID || req.NodePublicKey != hex.EncodeToString(nodeIdentity.PublicKey) {
		http.Error(w, "signed request must delegate to this node's own identity (see GET /node)", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)

	tx := recordTx(auth, address, "", 0, core.TxValidatorRegister, string(data))

	json.NewEncoder(w).Encode(
		map[string]string{
			"status": "pending",
			"txId":   tx.ID,
		},
	)
}

func HandleProduceBlock(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	_, address, err := requireSignedRequest(r, nil)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	v, ok := validatorStore.Get(address)
	if !ok || !v.Active {
		http.Error(w, "not a registered validator", http.StatusBadRequest)
		return
	}

	if v.AuthorizedNodeID != config.NodeID {
		http.Error(w, "this validator delegated block production to a different node", http.StatusForbidden)
		return
	}

	// Block production is normally fully automatic (see
	// consensus_loop.go) - this is a manual nudge for the currently
	// PoSR-elected leader only, not a free-for-all button anymore.
	leader := currentLeader()
	if leader == nil || leader.Wallet.Address != address {
		http.Error(w, "not currently the elected leader - wait for your turn", http.StatusBadRequest)
		return
	}

	produceBlockAsLeader(address)

	json.NewEncoder(w).Encode(toValidatorInfo(v, totalEffectiveStakeAcrossValidators()))
}

// HandleConsensusStatus reports who the automatic consensus loop
// would currently elect to produce the next block, and how often it
// ticks, so the frontend can show a "your turn" style status instead
// of a bare produce-block button.
func HandleConsensusStatus(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	leader := currentLeader()

	resp := map[string]any{
		"tickIntervalSeconds": int(consensusTickInterval.Seconds()),
		"blockHeight":         blockchain.LatestBlock().Index,
		// False means no registered validator currently clears the full
		// reputation gate, and selection is leaning on the relaxed
		// fallback tier (see core.SelectValidator's doc comment) just to
		// keep the network alive - surfaced here rather than silently
		// happening.
		"strictEligibilityMet": core.AnyStrictlyEligible(hydrateValidators()),
	}

	if leader != nil {
		resp["currentLeader"] = leader.Wallet.Address
	} else {
		resp["currentLeader"] = nil
	}

	json.NewEncoder(w).Encode(resp)
}

// HandleDelegateToValidator lets any wallet back a registered, active
// validator's PoSR weight with their own CCT, sharing in that
// validator's future block rewards net of commission (see
// api/apply.go's applyBlockReward) - without gaining any block-
// production rights themselves (core.ValidatorStatus.AuthorizedNodeID
// is a completely separate concept: which node produces blocks, never
// whose funds back the validator).
func HandleDelegateToValidator(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req validatorDelegateData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if req.Amount == 0 {
		http.Error(w, "amount must be positive", http.StatusBadRequest)
		return
	}
	if req.Validator == address {
		http.Error(w, "use your own stake to back your own validator, not delegation", http.StatusBadRequest)
		return
	}

	v, ok := validatorStore.Get(req.Validator)
	if !ok || !v.Active {
		http.Error(w, "not a registered, active validator", http.StatusNotFound)
		return
	}

	// Fast-fail on the real balance - the authoritative check is
	// applyValidatorDelegate's Transfer call, same "spare the caller a
	// pending tx that's certain to be dropped" courtesy every other
	// handler in this codebase follows.
	if tokenStore.BalanceOf(address) < req.Amount {
		http.Error(w, "insufficient CCT balance to delegate", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", req.Amount, core.TxValidatorDelegate, string(data))

	json.NewEncoder(w).Encode(
		map[string]string{
			"status": "pending",
			"txId":   tx.ID,
		},
	)
}

// HandleUndelegateFromValidator queues an undelegate request - the
// CCT stays bonded (still counted toward the validator's PoSR weight,
// still sharing in its rewards) until core.UnbondingPeriodSeconds
// elapses, same unbonding-period model as regular staking.
func HandleUndelegateFromValidator(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req validatorUndelegateData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if req.Amount == 0 {
		http.Error(w, "amount must be positive", http.StatusBadRequest)
		return
	}
	if delegationStore.AvailableToUndelegate(req.Validator, address) < req.Amount {
		http.Error(w, "insufficient delegated position to undelegate that much", http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", req.Amount, core.TxValidatorUndelegate, string(data))

	json.NewEncoder(w).Encode(
		map[string]any{
			"status":                 "pending",
			"txId":                   tx.ID,
			"unbondingPeriodSeconds": core.UnbondingPeriodSeconds,
		},
	)
}

// HandleSetValidatorCommission lets a registered validator update
// their own commission rate (basis points, capped at
// core.MaxCommissionBps) - only ever their OWN rate, enforced by
// signing with their own key (verifyTxAuth binds tx.AuthAddress to
// tx.From) and re-checked again at apply time
// (core.ValidatorStore.SetCommission).
func HandleSetValidatorCommission(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req validatorSetCommissionData

	auth, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if _, ok := validatorStore.Get(address); !ok {
		http.Error(w, "not a registered validator", http.StatusBadRequest)
		return
	}
	if req.CommissionBps > core.MaxCommissionBps {
		http.Error(w, fmt.Sprintf("commission cannot exceed %d basis points (%.0f%%)", core.MaxCommissionBps, float64(core.MaxCommissionBps)/100), http.StatusBadRequest)
		return
	}

	data, _ := json.Marshal(req)
	tx := recordTx(auth, address, "", 0, core.TxValidatorSetCommission, string(data))

	json.NewEncoder(w).Encode(
		map[string]string{
			"status": "pending",
			"txId":   tx.ID,
		},
	)
}

// delegationView is one delegator's position with a validator - used
// both for a validator's own "who's backing me" list and a
// delegator's own "where have I delegated" list.
type delegationView struct {
	Validator string `json:"validator"`
	Delegator string `json:"delegator"`
	Amount    uint64 `json:"amount"`
}

// HandleListDelegations lists delegation positions, filtered by
// exactly one of ?validator=X (every delegator currently backing that
// validator) or ?delegator=Y (everywhere that wallet has delegated).
func HandleListDelegations(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	filterValidator := r.URL.Query().Get("validator")
	filterDelegator := r.URL.Query().Get("delegator")

	list := []delegationView{}

	if filterValidator != "" {
		for delegator, amount := range delegationStore.Delegations[filterValidator] {
			if amount == 0 {
				continue
			}
			list = append(list, delegationView{Validator: filterValidator, Delegator: delegator, Amount: amount})
		}
	} else if filterDelegator != "" {
		for validator, delegators := range delegationStore.Delegations {
			if amount, ok := delegators[filterDelegator]; ok && amount > 0 {
				list = append(list, delegationView{Validator: validator, Delegator: filterDelegator, Amount: amount})
			}
		}
	}

	sort.Slice(list, func(i, j int) bool { return list[i].Amount > list[j].Amount })

	json.NewEncoder(w).Encode(list)
}
