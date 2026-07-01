package core

func Stake(
	v *Validator,
	amount uint64,
) {
	v.Stake += amount
}

func Unstake(
	v *Validator,
	amount uint64,
) {
	if amount > v.Stake {
		return
	}

	v.Stake -= amount
}

func StakeETH(
	w *Wallet,
	amount uint64,
) {
	w.StakedETH += amount

	w.OETHBalance += amount

	w.Reputation +=
		amount / 10

	w.VotingPower +=
		amount / 10
}

func RequestUnstake(
	w *Wallet,
	amount uint64,
) {

	if amount >
		w.StakedETH {
		return
	}

	w.StakedETH -= amount

	if amount >
		w.OETHBalance {
		w.OETHBalance = 0
	} else {
		w.OETHBalance -= amount
	}
}
func RewardForStake(
	amount uint64,
) uint64 {

	return amount / 10
}