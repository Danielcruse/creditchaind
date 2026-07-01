package core

type Validator struct {
	Wallet         Wallet
	Stake          uint64
	Reputation     uint64
	Active         bool
	Rewards        uint64
	BlocksProduced uint64
	VotingPower    uint64
}

func NewValidator(
	stake uint64,
) *Validator {

	return &Validator{
		Wallet:         NewWallet(),
		Stake:          stake,
		Reputation:     100,
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

func (v *Validator) AddReward(
	amount uint64,
) {
	v.Rewards += amount
}

func (v *Validator) AddStake(
	amount uint64,
) {
	v.Stake += amount
}