package legacy

// ARCHIVED: not currently referenced by any live code path.
// Preserved per architecture decision (2026-07) pending confirmation
// of its role in the long-term Layer-1 state-management design.
// Do not delete without explicit sign-off.
// Moved from core/state.go during the reputation/staking consolidation pass.

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