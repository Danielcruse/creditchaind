package core

type State struct {

	Balances map[string]uint64

	OETHBalances map[string]uint64

	StakedETH map[string]uint64

	Rewards map[string]uint64

	Reputation map[string]uint64

	VotingPower map[string]uint64
}

func NewState() *State {

	return &State{

		Balances:
			make(
				map[string]uint64,
			),

		OETHBalances:
			make(
				map[string]uint64,
			),

		StakedETH:
			make(
				map[string]uint64,
			),

		Rewards:
			make(
				map[string]uint64,
			),

		Reputation:
			make(
				map[string]uint64,
			),

		VotingPower:
			make(
				map[string]uint64,
			),
	}
}