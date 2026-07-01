package core

func InterestRate(
	score uint64,
) uint64 {

	switch {
	case score >= 1000:
		return 3

	case score >= 800:
		return 5

	case score >= 700:
		return 8

	case score >= 600:
		return 12

	default:
		return 20
	}
}