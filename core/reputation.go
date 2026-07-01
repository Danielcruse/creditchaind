package core

func IncreaseReputation(
	v *Validator,
	amount uint64,
) {
	v.Reputation += amount
}

func DecreaseReputation(
	v *Validator,
	amount uint64,
) {

	if amount >=
		v.Reputation {

		v.Reputation = 0
		return
	}

	v.Reputation -= amount
}

func IncreaseWalletReputation(
	w *Wallet,
	amount uint64,
) {
	w.Reputation += amount

	w.CreditScore +=
		amount / 2

	w.VotingPower +=
		amount / 2
}

func DecreaseWalletReputation(
	w *Wallet,
	amount uint64,
) {

	if amount >=
		w.Reputation {

		w.Reputation = 0
		return
	}

	w.Reputation -= amount
}