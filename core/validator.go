package core

// Validator wraps a Wallet with consensus-specific state. Reputation
// is deliberately NOT a field here - it is always read as
// v.Wallet.Reputation, since Wallet is the single canonical owner of
// reputation for every address (validator or not).
//
// Stake is a synchronized read-model of StakingStore (the canonical
// stake ledger), kept in sync via StakingStore.SyncValidator - it is
// never incremented directly outside of that sync call.
type Validator struct {
	Wallet Wallet

	Stake uint64 // synced from StakingStore - do not write directly

	Uptime         float64 // 0.0-1.0, rolling reliability over trailing window
	SlashCount     uint64  // cumulative strikes, decays selection weight
	CommunityTrust uint64  // scaffolded for future PoSR weighting (delegations, disputes, endorsements)

	Active         bool
	Rewards        uint64
	BlocksProduced uint64
	VotingPower    uint64
}

func NewValidator(stake uint64) *Validator {
	return &Validator{
		Wallet:         NewWallet(),
		Stake:          stake,
		Uptime:         1.0,
		SlashCount:     0,
		CommunityTrust: 0,
		Active:         true,
		Rewards:        0,
		BlocksProduced: 0,
		VotingPower:    1,
	}
}

func (v *Validator) Activate() {
	v.Active = true
}

func (v *Validator) Deactivate() {
	v.Active = false
}

func (v *Validator) AddReward(amount uint64) {
	v.Rewards += amount
}
