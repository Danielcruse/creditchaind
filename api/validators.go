package api

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/creditchain/creditchaind/core"
)

var validatorStore = core.NewValidatorStore()

// MinValidatorReputation matches the default reputation a fresh
// wallet starts with (core.NewWallet), so any wallet that has staked
// something is immediately eligible.
const MinValidatorReputation = 50

// ProduceBlockReward is the flat CCT reward minted per block produced
// by a registered validator via /validators/produce-block.
const ProduceBlockReward = 10

type ValidatorInfo struct {
	Address         string  `json:"address"`
	Stake           uint64  `json:"stake"`
	Reputation      uint64  `json:"reputation"`
	Rewards         uint64  `json:"rewards"`
	BlocksProduced  uint64  `json:"blocksProduced"`
	MissedBlocks    uint64  `json:"missedBlocks"`
	SlashCount      uint64  `json:"slashCount"`
	Uptime          float64 `json:"uptime"`
	VotingPower     uint64  `json:"votingPower"`
	Active          bool    `json:"active"`
	IsCurrentLeader bool    `json:"isCurrentLeader"`
	AuthorizedNodeID string `json:"authorizedNodeId"`
}

func toValidatorInfo(v *core.ValidatorStatus) ValidatorInfo {
	wallet := walletStore.Get(v.Address)
	stakingStore.SyncWallet(wallet, v.Address)

	leader := currentLeader()
	isLeader := leader != nil && leader.Wallet.Address == v.Address

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
	}
}

// HandleValidators lists every registered validator from the shared
// ValidatorStore, with stake/reputation synced live from the
// canonical StakingStore/WalletStore.
func HandleValidators(
	w http.ResponseWriter,
	r *http.Request,
) {
	enableCors(&w)

	list := []ValidatorInfo{}

	for _, v := range validatorStore.List() {
		list = append(list, toValidatorInfo(v))
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

	json.NewEncoder(w).Encode(toValidatorInfo(v))
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
