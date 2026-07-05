package core

func RewardValidator(
	v *Validator,
	amount uint64,
) {
	v.Rewards += amount
}

func RewardWallet(
	w *Wallet,
	amount uint64,
) {
	w.Rewards += amount
	w.CCTBalance += amount
}

func CalculateStakeReward(
	staked uint64,
) uint64 {
	return staked / 20
}